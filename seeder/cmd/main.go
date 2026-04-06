// Seeder provisions a default admin employee and test client on first startup.
//
// Flow (admin):
//  1. Try Login — if it succeeds, the admin already exists and is active → skip to client.
//  2. CreateEmployee via user-service gRPC (idempotent, ignores AlreadyExists).
//  3. auth-service Kafka consumer picks up user.employee-created → publishes ACTIVATION token.
//  4. Read the activation token from the notification.send-email Kafka topic.
//  5. ActivateAccount via auth-service gRPC with the token + desired password.
//
// Flow (client):
//  1. Derive client email from admin email (insert +testclient before @).
//  2. Try Login — if it succeeds, the client already exists and is active → done.
//  3. CreateClient via client-service gRPC (idempotent, ignores AlreadyExists).
//  4. Read the activation token from the notification.send-email Kafka topic.
//  5. ActivateAccount via auth-service gRPC with the token + desired password.
//
// Environment variables:
//
//	USER_GRPC_ADDR   — user-service gRPC address    (default: user-service:50052)
//	AUTH_GRPC_ADDR   — auth-service gRPC address    (default: auth-service:50051)
//	CLIENT_GRPC_ADDR — client-service gRPC address  (default: client-service:50054)
//	KAFKA_BROKERS    — comma-separated broker list  (default: kafka:9092)
//	ADMIN_EMAIL      — admin email                  (default: admin@admin.com)
//	ADMIN_PASSWORD   — admin password               (default: Admin1234!)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	kafkalib "github.com/segmentio/kafka-go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	authpb "github.com/exbanka/contract/authpb"
	clientpb "github.com/exbanka/contract/clientpb"
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
	clientAddr := getenv("CLIENT_GRPC_ADDR", "client-service:50054")
	kafka := getenv("KAFKA_BROKERS", "kafka:9092")
	email := getenv("ADMIN_EMAIL", "admin@admin.com")
	password := getenv("ADMIN_PASSWORD", "Admin1234!")

	log.Printf("seeder: admin email=%s", email)

	// ── 1. Connect to services ────────────────────────────────────────────────

	userConn := dial(userAddr)
	defer userConn.Close()
	authConn := dial(authAddr)
	defer authConn.Close()
	clientConn := dial(clientAddr)
	defer clientConn.Close()

	userClient := userpb.NewUserServiceClient(userConn)
	authClient := authpb.NewAuthServiceClient(authConn)
	clientSvcClient := clientpb.NewClientServiceClient(clientConn)

	ctx := context.Background()

	// ── 2. Try Login — already bootstrapped? ──────────────────────────────────

	loginCtx, loginCancel := context.WithTimeout(ctx, 10*time.Second)
	defer loginCancel()
	_, loginErr := authClient.Login(loginCtx, &authpb.LoginRequest{
		Email:    email,
		Password: password,
	})
	if loginErr == nil {
		log.Println("seeder: admin already active — skipping admin bootstrap")
	} else {
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

	// ── Client bootstrapping ──────────────────────────────────────────────────
	clientEmail := deriveClientEmail(email)
	seedClient(ctx, authClient, clientSvcClient, kafka, clientEmail, password)
	log.Println("seeder: all bootstrapping complete")
}

// deriveClientEmail inserts "+testclient" before the "@" in the admin email.
func deriveClientEmail(adminEmail string) string {
	parts := strings.SplitN(adminEmail, "@", 2)
	if len(parts) != 2 {
		return "testclient@admin.com"
	}
	return parts[0] + "+testclient@" + parts[1]
}

// seedClient provisions a default test client account, mirroring the admin flow:
// login check → create → Kafka activation token → activate.
func seedClient(
	ctx context.Context,
	authClient authpb.AuthServiceClient,
	clientSvcClient clientpb.ClientServiceClient,
	kafkaBrokers string,
	clientEmail string,
	password string,
) {
	log.Printf("seeder: client email=%s", clientEmail)

	// 1. Try Login — already bootstrapped?
	loginCtx, loginCancel := context.WithTimeout(ctx, 10*time.Second)
	defer loginCancel()
	_, loginErr := authClient.Login(loginCtx, &authpb.LoginRequest{
		Email:    clientEmail,
		Password: password,
	})
	if loginErr == nil {
		log.Println("seeder: test client already active — skipping")
		return
	}

	// 2. Check if client already exists by email
	getCtx, getCancel := context.WithTimeout(ctx, 10*time.Second)
	defer getCancel()
	existing, getErr := clientSvcClient.GetClientByEmail(getCtx, &clientpb.GetClientByEmailRequest{
		Email: clientEmail,
	})
	clientExists := getErr == nil && existing != nil && existing.Id > 0

	// 3. Create client if not found
	if !clientExists {
		createCtx, createCancel := context.WithTimeout(ctx, 15*time.Second)
		defer createCancel()
		_, createErr := clientSvcClient.CreateClient(createCtx, &clientpb.CreateClientRequest{
			FirstName:   "Test",
			LastName:    "Client",
			DateOfBirth: time.Date(1995, 6, 15, 0, 0, 0, 0, time.UTC).Unix(),
			Gender:      "other",
			Email:       clientEmail,
			Phone:       "+381000000001",
			Address:     "Test Client Address",
			Jmbg:        "1506995000001",
		})
		if createErr != nil {
			st, _ := status.FromError(createErr)
			if st.Code() != codes.AlreadyExists {
				log.Fatalf("seeder: CreateClient failed: %v", createErr)
			}
			log.Println("seeder: client already exists (AlreadyExists)")
		} else {
			log.Println("seeder: client created")
		}
	} else {
		log.Println("seeder: client already exists, continuing to activation")
	}

	// 4. Wait for activation token on Kafka
	log.Println("seeder: waiting for client activation token on Kafka…")
	token := readActivationToken(kafkaBrokers, clientEmail)
	log.Printf("seeder: got client activation token (len=%d)", len(token))

	// 5. Activate account
	actCtx, actCancel := context.WithTimeout(ctx, 15*time.Second)
	defer actCancel()
	_, err := authClient.ActivateAccount(actCtx, &authpb.ActivateAccountRequest{
		Token:           token,
		Password:        password,
		ConfirmPassword: password,
	})
	if err != nil {
		log.Fatalf("seeder: client ActivateAccount failed: %v", err)
	}

	log.Printf("seeder: test client account active — email=%s", clientEmail)
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
