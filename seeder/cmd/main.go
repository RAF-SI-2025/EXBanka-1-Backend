// Seeder provisions a default admin employee on first startup.
//
// Flow:
//  1. Try Login — if it succeeds, the admin already exists and is active → exit 0.
//  2. CreateEmployee via user-service gRPC (idempotent, ignores AlreadyExists).
//  3. auth-service Kafka consumer picks up user.employee-created → publishes ACTIVATION token.
//  4. Read the activation token from the notification.send-email Kafka topic.
//  5. ActivateAccount via auth-service gRPC with the token + desired password.
//
// Environment variables:
//
//	USER_GRPC_ADDR   — user-service gRPC address  (default: user-service:50052)
//	AUTH_GRPC_ADDR   — auth-service gRPC address  (default: auth-service:50051)
//	KAFKA_BROKERS    — comma-separated broker list (default: kafka:9092)
//	ADMIN_EMAIL      — admin email                 (default: admin@admin.com)
//	ADMIN_PASSWORD   — admin password              (default: Admin1234!)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	kafkalib "github.com/segmentio/kafka-go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	authpb "github.com/exbanka/contract/authpb"
	userpb "github.com/exbanka/contract/userpb"
)

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func dial(addr string) *grpc.ClientConn {
	for i := 0; i < 20; i++ {
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err == nil {
			return conn
		}
		log.Printf("seeder: waiting for %s (%v)…", addr, err)
		time.Sleep(3 * time.Second)
	}
	log.Fatalf("seeder: cannot connect to %s", addr)
	return nil
}

func main() {
	userAddr := getenv("USER_GRPC_ADDR", "user-service:50052")
	authAddr := getenv("AUTH_GRPC_ADDR", "auth-service:50051")
	kafka := getenv("KAFKA_BROKERS", "kafka:9092")
	email := getenv("ADMIN_EMAIL", "admin@admin.com")
	password := getenv("ADMIN_PASSWORD", "Admin1234!")

	log.Printf("seeder: admin email=%s", email)

	// ── 1. Connect to services ────────────────────────────────────────────────

	userConn := dial(userAddr)
	defer userConn.Close()
	authConn := dial(authAddr)
	defer authConn.Close()

	userClient := userpb.NewUserServiceClient(userConn)
	authClient := authpb.NewAuthServiceClient(authConn)

	ctx := context.Background()

	// ── 2. Try Login — already bootstrapped? ──────────────────────────────────

	loginCtx, loginCancel := context.WithTimeout(ctx, 10*time.Second)
	defer loginCancel()
	_, loginErr := authClient.Login(loginCtx, &authpb.LoginRequest{
		Email:    email,
		Password: password,
	})
	if loginErr == nil {
		log.Println("seeder: admin already active — nothing to do")
		return
	}

	// ── 3. Look up existing employee by email (may already exist) ────────────

	principalID := findEmployeeByEmail(ctx, userClient, email)

	// ── 4. Create employee if not found ───────────────────────────────────────

	if principalID == 0 {
		createCtx, createCancel := context.WithTimeout(ctx, 15*time.Second)
		defer createCancel()
		_, createErr := userClient.CreateEmployee(createCtx, &userpb.CreateEmployeeRequest{
			FirstName:   "System",
			LastName:    "Admin",
			DateOfBirth: time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC).Unix(),
			Gender:      "other",
			Email:       email,
			Phone:       "+381000000000",
			Address:     "System Account",
			Jmbg:        "0101990000000",
			Username:    "admin",
			Position:    "System Administrator",
			Department:  "IT",
			Role:        "EmployeeAdmin",
		})
		if createErr != nil {
			st, _ := status.FromError(createErr)
			if st.Code() != codes.AlreadyExists {
				log.Fatalf("seeder: CreateEmployee failed: %v", createErr)
			}
		}
		log.Println("seeder: employee created")
		principalID = findEmployeeByEmail(ctx, userClient, email)
		if principalID == 0 {
			log.Fatalf("seeder: admin employee not found after create (email=%s)", email)
		}
	} else {
		log.Println("seeder: employee already exists, continuing")
	}
	log.Printf("seeder: principal_id=%d", principalID)

	// ── 5. Wait for auth-service to pick up user.employee-created and publish activation token ──
	// auth-service's EmployeeConsumer does this automatically via Kafka.

	log.Println("seeder: employee created, waiting for activation token on Kafka…")

	// ── 6. Read activation token from Kafka ───────────────────────────────────

	token := readActivationToken(kafka, email)
	log.Printf("seeder: got activation token (len=%d)", len(token))

	// ── 7. Activate account with desired password ─────────────────────────────

	actCtx, actCancel := context.WithTimeout(ctx, 15*time.Second)
	defer actCancel()
	_, err := authClient.ActivateAccount(actCtx, &authpb.ActivateAccountRequest{
		Token:           token,
		Password:        password,
		ConfirmPassword: password,
	})
	if err != nil {
		log.Fatalf("seeder: ActivateAccount failed: %v", err)
	}

	log.Printf("seeder: admin account active — email=%s", email)
}

// readActivationToken scans the notification.send-email Kafka topic for an
// ACTIVATION message addressed to email and returns the token.
// Retries for up to 60 seconds to tolerate startup ordering.
func readActivationToken(brokers, email string) string {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		r := kafkalib.NewReader(kafkalib.ReaderConfig{
			Brokers:     []string{brokers},
			Topic:       "notification.send-email",
			Partition:   0,
			StartOffset: kafkalib.FirstOffset,
			MaxWait:     500 * time.Millisecond,
		})

		scanCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		token := scanOnce(r, scanCtx, email)
		cancel()
		r.Close()

		if token != "" {
			return token
		}
		log.Println("seeder: activation token not yet in Kafka, retrying…")
		time.Sleep(2 * time.Second)
	}
	log.Fatalf("seeder: timed out waiting for activation token for %s", email)
	return ""
}

func scanOnce(r *kafkalib.Reader, ctx context.Context, email string) string {
	var latest string
	for {
		msg, err := r.ReadMessage(ctx)
		if err != nil {
			break
		}
		var body struct {
			To        string            `json:"to"`
			EmailType string            `json:"email_type"`
			Data      map[string]string `json:"data"`
		}
		if json.Unmarshal(msg.Value, &body) != nil {
			continue
		}
		if body.To == email && body.EmailType == "ACTIVATION" {
			if t := body.Data["token"]; t != "" {
				latest = t
			}
		}
	}
	return latest
}

// findEmployeeByEmail does a partial-match list and returns the first exact match.
// Returns 0 if not found.
func findEmployeeByEmail(ctx context.Context, c userpb.UserServiceClient, email string) int64 {
	listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := c.ListEmployees(listCtx, &userpb.ListEmployeesRequest{
		EmailFilter: email,
		Page:        1,
		PageSize:    50,
	})
	if err != nil {
		return 0
	}
	for _, emp := range resp.Employees {
		if emp.Email == email {
			return emp.Id
		}
	}
	return 0
}

func init() {
	log.SetFlags(log.Ltime | log.Lshortfile)
	_ = fmt.Sprintf // avoid unused import
}
