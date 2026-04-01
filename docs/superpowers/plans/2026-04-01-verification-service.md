# Verification Service Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create the verification-service that owns all verification challenge logic (code_pull, qr_scan, number_match, email fallback).

**Architecture:** New gRPC microservice with PostgreSQL for challenge state, Kafka for event publishing. Other services call CreateChallenge/GetChallengeStatus gRPC. Mobile app calls SubmitVerification. Browser calls SubmitCode.

**Tech Stack:** Go 1.26, gRPC/Protobuf, GORM/PostgreSQL, Kafka (segmentio/kafka-go), shopspring/decimal

**Depends on:** Plan 1 (proto definitions need verification.proto added to contract/)

---

## Task 1: Proto definition

**Files:**
- Create: `contract/proto/verification/verification.proto`
- Modify: `Makefile` (proto target)

- [ ] **Step 1: Create proto directory**

```bash
mkdir -p contract/proto/verification
```

- [ ] **Step 2: Create contract/proto/verification/verification.proto**

```protobuf
syntax = "proto3";
package verification;
option go_package = "github.com/exbanka/contract/verificationpb;verificationpb";

service VerificationGRPCService {
  // CreateChallenge creates a new verification challenge for a transaction.
  rpc CreateChallenge(CreateChallengeRequest) returns (CreateChallengeResponse);
  // GetChallengeStatus returns the current status of a verification challenge.
  rpc GetChallengeStatus(GetChallengeStatusRequest) returns (GetChallengeStatusResponse);
  // GetPendingChallenge returns the pending challenge for a user+device (mobile polling).
  rpc GetPendingChallenge(GetPendingChallengeRequest) returns (GetPendingChallengeResponse);
  // SubmitVerification validates a mobile-submitted response (qr_scan, number_match, code_pull with biometric).
  rpc SubmitVerification(SubmitVerificationRequest) returns (SubmitVerificationResponse);
  // SubmitCode validates a browser-submitted 6-digit code (code_pull, email fallback).
  rpc SubmitCode(SubmitCodeRequest) returns (SubmitCodeResponse);
}

message CreateChallengeRequest {
  uint64 user_id        = 1;
  string source_service = 2; // "transaction", "payment", "transfer"
  uint64 source_id      = 3;
  string method         = 4; // "code_pull", "qr_scan", "number_match", "email"
  string device_id      = 5; // empty if email fallback
}

message CreateChallengeResponse {
  uint64 challenge_id    = 1;
  string method          = 2;
  string challenge_data  = 3; // JSON string with method-specific data
  string expires_at      = 4; // RFC3339
}

message GetChallengeStatusRequest {
  uint64 challenge_id = 1;
}

message GetChallengeStatusResponse {
  uint64 challenge_id = 1;
  string status       = 2; // "pending", "verified", "expired", "failed"
  string method       = 3;
  string expires_at   = 4; // RFC3339
  string verified_at  = 5; // RFC3339, empty if not verified
}

message GetPendingChallengeRequest {
  uint64 user_id   = 1;
  string device_id = 2;
}

message GetPendingChallengeResponse {
  bool   found          = 1;
  uint64 challenge_id   = 2;
  string method         = 3;
  string challenge_data = 4; // JSON string
  string expires_at     = 5; // RFC3339
}

message SubmitVerificationRequest {
  uint64 challenge_id = 1;
  string device_id    = 2;
  string response     = 3; // the user's answer: selected number, QR token, or code
}

message SubmitVerificationResponse {
  bool   success            = 1;
  int32  remaining_attempts = 2;
  string new_challenge_data = 3; // regenerated challenge data on wrong answer (number_match)
}

message SubmitCodeRequest {
  uint64 challenge_id = 1;
  string code         = 2; // 6-digit code
}

message SubmitCodeResponse {
  bool  success            = 1;
  int32 remaining_attempts = 2;
}
```

- [ ] **Step 3: Create verificationpb output directory**

```bash
mkdir -p contract/verificationpb
```

- [ ] **Step 4: Update Makefile proto target**

In the `proto:` target, add `verification/verification.proto` to the protoc command and add the mv/rmdir lines for verification:

Add `verification/verification.proto` to the end of the protoc input list (after `exchange/exchange.proto`).

Add after the `mkdir -p` line:
```
contract/verificationpb
```

Add after the exchange mv line:
```
mv contract/verification/*.pb.go contract/verificationpb/ 2>/dev/null || true
```

Add `contract/verification` to the rmdir line.

The full updated proto target becomes:

```makefile
proto:
	protoc -I contract/proto \
		--go_out=contract --go_opt=paths=source_relative \
		--go-grpc_out=contract --go-grpc_opt=paths=source_relative \
		auth/auth.proto user/user.proto notification/notification.proto \
		client/client.proto account/account.proto card/card.proto \
		transaction/transaction.proto credit/credit.proto \
		exchange/exchange.proto verification/verification.proto
	mkdir -p contract/authpb contract/userpb contract/notificationpb \
		contract/clientpb contract/accountpb contract/cardpb \
		contract/transactionpb contract/creditpb contract/exchangepb \
		contract/verificationpb
	mv contract/auth/*.pb.go contract/authpb/ 2>/dev/null || true
	mv contract/user/*.pb.go contract/userpb/ 2>/dev/null || true
	mv contract/notification/*.pb.go contract/notificationpb/ 2>/dev/null || true
	mv contract/client/*.pb.go contract/clientpb/ 2>/dev/null || true
	mv contract/account/*.pb.go contract/accountpb/ 2>/dev/null || true
	mv contract/card/*.pb.go contract/cardpb/ 2>/dev/null || true
	mv contract/transaction/*.pb.go contract/transactionpb/ 2>/dev/null || true
	mv contract/credit/*.pb.go contract/creditpb/ 2>/dev/null || true
	mv contract/exchange/*.pb.go contract/exchangepb/ 2>/dev/null || true
	mv contract/verification/*.pb.go contract/verificationpb/ 2>/dev/null || true
	rmdir contract/auth contract/user contract/notification 2>/dev/null || true
	rmdir contract/client contract/account contract/card \
		contract/transaction contract/credit contract/exchange \
		contract/verification 2>/dev/null || true
```

- [ ] **Step 5: Generate proto files**

```bash
make proto
```

- [ ] **Step 6: Verify generated files exist**

```bash
ls contract/verificationpb/
```

Expected: `verification.pb.go` and `verification_grpc.pb.go`.

- [ ] **Step 7: Commit**

```bash
git add contract/proto/verification/ contract/verificationpb/ Makefile
git commit -m "feat(contract): add verification.proto with VerificationGRPCService definition"
```

---

## Task 2: Kafka messages

**Files:**
- Modify: `contract/kafka/messages.go`

- [ ] **Step 1: Add verification topic constants**

Append the following block to `contract/kafka/messages.go`:

```go
// Verification service topic constants
const (
	TopicVerificationChallengeCreated  = "verification.challenge-created"
	TopicVerificationChallengeVerified = "verification.challenge-verified"
	TopicVerificationChallengeFailed   = "verification.challenge-failed"
	TopicMobilePush                    = "notification.mobile-push"
)
```

- [ ] **Step 2: Add verification message structs**

Append the following structs to `contract/kafka/messages.go`:

```go
// VerificationChallengeCreatedMessage is published when a new verification challenge is created.
// notification-service consumes this to store a mobile inbox item for the user's device.
type VerificationChallengeCreatedMessage struct {
	ChallengeID     uint64 `json:"challenge_id"`
	UserID          uint64 `json:"user_id"`
	DeviceID        string `json:"device_id"`
	Method          string `json:"method"`           // "code_pull", "qr_scan", "number_match"
	DisplayData     string `json:"display_data"`     // JSON string — what the mobile app needs to show
	DeliveryChannel string `json:"delivery_channel"` // "mobile" or "email"
	ExpiresAt       string `json:"expires_at"`       // RFC3339
}

// VerificationChallengeVerifiedMessage is published when a challenge is successfully verified.
// transaction-service consumes this to unblock the pending transaction.
type VerificationChallengeVerifiedMessage struct {
	ChallengeID   uint64 `json:"challenge_id"`
	UserID        uint64 `json:"user_id"`
	SourceService string `json:"source_service"` // "transaction", "payment", "transfer"
	SourceID      uint64 `json:"source_id"`
	Method        string `json:"method"`
	VerifiedAt    string `json:"verified_at"` // RFC3339
}

// VerificationChallengeFailedMessage is published when a challenge fails (max attempts or expired).
// transaction-service consumes this to cancel the pending transaction.
type VerificationChallengeFailedMessage struct {
	ChallengeID   uint64 `json:"challenge_id"`
	UserID        uint64 `json:"user_id"`
	SourceService string `json:"source_service"`
	SourceID      uint64 `json:"source_id"`
	Reason        string `json:"reason"` // "max_attempts_exceeded", "expired"
}

// MobilePushMessage is published by notification-service when a mobile inbox item is stored.
// api-gateway consumes this to push via WebSocket to connected mobile devices.
type MobilePushMessage struct {
	UserID   uint64 `json:"user_id"`
	DeviceID string `json:"device_id"`
	Type     string `json:"type"` // "verification_challenge"
	Payload  string `json:"payload"` // JSON string
}
```

- [ ] **Step 3: Add email type constant for verification code email**

Append to the email type constants section:

```go
const (
	EmailTypeVerificationCode = EmailType("VERIFICATION_CODE")
)
```

- [ ] **Step 4: Verify contract builds**

```bash
cd contract && go build ./...
```

- [ ] **Step 5: Commit**

```bash
git add contract/kafka/messages.go
git commit -m "feat(contract): add verification Kafka topics and message structs"
```

---

## Task 3: Service scaffold and config

**Files:**
- Create: `verification-service/go.mod`
- Create: `verification-service/internal/config/config.go`
- Modify: `go.work` — add `./verification-service`

- [ ] **Step 1: Create directory structure**

```bash
mkdir -p verification-service/cmd \
  verification-service/internal/{config,model,repository,service,handler,kafka}
```

- [ ] **Step 2: Create verification-service/go.mod**

```go
module github.com/exbanka/verification-service

go 1.26.1

require (
	github.com/exbanka/contract v0.0.0
	github.com/segmentio/kafka-go v0.4.50
	github.com/shopspring/decimal v1.4.0
	google.golang.org/grpc v1.79.2
	gorm.io/driver/postgres v1.6.0
	gorm.io/gorm v1.31.1
)

replace github.com/exbanka/contract => ../contract
```

- [ ] **Step 3: Create verification-service/internal/config/config.go**

```go
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	DBHost       string
	DBPort       string
	DBUser       string
	DBPassword   string
	DBName       string
	DBSslmode    string
	GRPCAddr     string
	KafkaBrokers string

	ChallengeExpiry time.Duration
	MaxAttempts     int
}

func Load() *Config {
	expiry := 5 * time.Minute
	if v := os.Getenv("VERIFICATION_CHALLENGE_EXPIRY"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			expiry = d
		}
	}

	maxAttempts := 3
	if v := os.Getenv("VERIFICATION_MAX_ATTEMPTS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxAttempts = n
		}
	}

	return &Config{
		DBHost:          getEnv("VERIFICATION_DB_HOST", "localhost"),
		DBPort:          getEnv("VERIFICATION_DB_PORT", "5440"),
		DBUser:          getEnv("VERIFICATION_DB_USER", "postgres"),
		DBPassword:      getEnv("VERIFICATION_DB_PASSWORD", "postgres"),
		DBName:          getEnv("VERIFICATION_DB_NAME", "verificationdb"),
		DBSslmode:       getEnv("VERIFICATION_DB_SSLMODE", "disable"),
		GRPCAddr:        getEnv("VERIFICATION_GRPC_ADDR", ":50060"),
		KafkaBrokers:    getEnv("KAFKA_BROKERS", "localhost:9092"),
		ChallengeExpiry: expiry,
		MaxAttempts:     maxAttempts,
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func (c *Config) DSN() string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName, c.DBSslmode)
}
```

- [ ] **Step 4: Add verification-service to go.work**

Add `./verification-service` to the `use` block in `go.work`.

- [ ] **Step 5: Run go mod tidy**

```bash
cd verification-service && go mod tidy
```

- [ ] **Step 6: Verify config compiles**

```bash
cd verification-service && go build ./internal/config/
```

- [ ] **Step 7: Commit**

```bash
git add verification-service/go.mod verification-service/go.sum \
  verification-service/internal/config/ go.work
git commit -m "feat(verification-service): scaffold module with config"
```

---

## Task 4: Model

**Files:**
- Create: `verification-service/internal/model/verification_challenge.go`

- [ ] **Step 1: Create verification-service/internal/model/verification_challenge.go**

```go
package model

import (
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// VerificationChallenge stores the state of a single verification challenge
// (code_pull, qr_scan, number_match, or email fallback).
type VerificationChallenge struct {
	ID            uint64         `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID        uint64         `gorm:"not null;index" json:"user_id"`
	SourceService string         `gorm:"size:30;not null" json:"source_service"` // "transaction", "payment", "transfer"
	SourceID      uint64         `gorm:"not null" json:"source_id"`
	Method        string         `gorm:"size:20;not null" json:"method"` // "code_pull", "qr_scan", "number_match", "email"
	Code          string         `gorm:"size:6;not null" json:"-"`       // 6-digit code (used for code_pull and email; internal for qr/number)
	ChallengeData datatypes.JSON `gorm:"type:jsonb" json:"challenge_data"`
	Status        string         `gorm:"size:20;not null;default:'pending';index" json:"status"` // "pending", "verified", "expired", "failed"
	Attempts      int            `gorm:"not null;default:0" json:"attempts"`
	ExpiresAt     time.Time      `gorm:"not null;index" json:"expires_at"`
	VerifiedAt    *time.Time     `json:"verified_at,omitempty"`
	DeviceID      string         `gorm:"size:100" json:"device_id,omitempty"` // filled when mobile app claims the challenge
	Version       int64          `gorm:"not null;default:1" json:"-"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

// BeforeUpdate enforces optimistic locking via the Version field.
// The WHERE clause filters on the current version; if another transaction
// already incremented it, RowsAffected will be 0.
func (vc *VerificationChallenge) BeforeUpdate(tx *gorm.DB) error {
	tx.Statement.Where("version = ?", vc.Version)
	vc.Version++
	return nil
}
```

- [ ] **Step 2: Add datatypes dependency**

```bash
cd verification-service && go get gorm.io/datatypes && go mod tidy
```

- [ ] **Step 3: Verify model compiles**

```bash
cd verification-service && go build ./internal/model/
```

- [ ] **Step 4: Commit**

```bash
git add verification-service/internal/model/ verification-service/go.mod verification-service/go.sum
git commit -m "feat(verification-service): add VerificationChallenge model with optimistic locking"
```

---

## Task 5: Repository

**Files:**
- Create: `verification-service/internal/repository/verification_challenge_repository.go`

- [ ] **Step 1: Create verification-service/internal/repository/verification_challenge_repository.go**

```go
package repository

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/exbanka/verification-service/internal/model"
)

type VerificationChallengeRepository struct {
	db *gorm.DB
}

func NewVerificationChallengeRepository(db *gorm.DB) *VerificationChallengeRepository {
	return &VerificationChallengeRepository{db: db}
}

// Create inserts a new VerificationChallenge into the database.
func (r *VerificationChallengeRepository) Create(vc *model.VerificationChallenge) error {
	return r.db.Create(vc).Error
}

// GetByID retrieves a challenge by primary key.
func (r *VerificationChallengeRepository) GetByID(id uint64) (*model.VerificationChallenge, error) {
	var vc model.VerificationChallenge
	err := r.db.First(&vc, id).Error
	if err != nil {
		return nil, err
	}
	return &vc, nil
}

// GetByIDForUpdate retrieves a challenge by primary key with a SELECT FOR UPDATE lock.
// Must be called within a transaction.
func (r *VerificationChallengeRepository) GetByIDForUpdate(tx *gorm.DB, id uint64) (*model.VerificationChallenge, error) {
	var vc model.VerificationChallenge
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&vc, id).Error
	if err != nil {
		return nil, err
	}
	return &vc, nil
}

// Save persists changes to an existing VerificationChallenge.
// The BeforeUpdate hook enforces optimistic locking.
func (r *VerificationChallengeRepository) Save(vc *model.VerificationChallenge) error {
	return r.db.Save(vc).Error
}

// SaveInTx persists changes within an existing transaction.
func (r *VerificationChallengeRepository) SaveInTx(tx *gorm.DB, vc *model.VerificationChallenge) error {
	return tx.Save(vc).Error
}

// GetPendingByUser returns the most recent pending challenge for a user+device pair.
// Used by mobile app polling to discover challenges that need attention.
func (r *VerificationChallengeRepository) GetPendingByUser(userID uint64, deviceID string) (*model.VerificationChallenge, error) {
	var vc model.VerificationChallenge
	err := r.db.Where("user_id = ? AND device_id = ? AND status = ? AND expires_at > ?",
		userID, deviceID, "pending", time.Now()).
		Order("created_at DESC").
		First(&vc).Error
	if err != nil {
		return nil, err
	}
	return &vc, nil
}

// ExpireOld transitions all pending challenges past their expiry time to "expired".
// Uses SkipHooks to bypass the optimistic locking BeforeUpdate hook since this is a
// bulk administrative operation, not a concurrent user action.
func (r *VerificationChallengeRepository) ExpireOld() (int64, error) {
	result := r.db.Session(&gorm.Session{SkipHooks: true}).
		Model(&model.VerificationChallenge{}).
		Where("status = ? AND expires_at < ?", "pending", time.Now()).
		Update("status", "expired")
	return result.RowsAffected, result.Error
}
```

- [ ] **Step 2: Verify repository compiles**

```bash
cd verification-service && go build ./internal/repository/
```

- [ ] **Step 3: Commit**

```bash
git add verification-service/internal/repository/
git commit -m "feat(verification-service): add VerificationChallengeRepository with FOR UPDATE and bulk expiry"
```

---

## Task 6: Kafka producer

**Files:**
- Create: `verification-service/internal/kafka/producer.go`
- Create: `verification-service/internal/kafka/topics.go`

- [ ] **Step 1: Create verification-service/internal/kafka/producer.go**

```go
package kafka

import (
	"context"
	"encoding/json"

	kafkamsg "github.com/exbanka/contract/kafka"
	kafkago "github.com/segmentio/kafka-go"
)

type Producer struct {
	writer *kafkago.Writer
}

func NewProducer(brokers string) *Producer {
	return &Producer{
		writer: &kafkago.Writer{
			Addr:     kafkago.TCP(brokers),
			Balancer: &kafkago.LeastBytes{},
		},
	}
}

func (p *Producer) Close() error {
	return p.writer.Close()
}

func (p *Producer) publish(ctx context.Context, topic string, v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return p.writer.WriteMessages(ctx, kafkago.Message{
		Topic: topic,
		Value: data,
	})
}

// PublishChallengeCreated publishes when a new challenge is created for mobile delivery.
func (p *Producer) PublishChallengeCreated(ctx context.Context, msg kafkamsg.VerificationChallengeCreatedMessage) error {
	return p.publish(ctx, kafkamsg.TopicVerificationChallengeCreated, msg)
}

// PublishChallengeVerified publishes when a challenge is successfully verified.
func (p *Producer) PublishChallengeVerified(ctx context.Context, msg kafkamsg.VerificationChallengeVerifiedMessage) error {
	return p.publish(ctx, kafkamsg.TopicVerificationChallengeVerified, msg)
}

// PublishChallengeFailed publishes when a challenge fails (max attempts or expired).
func (p *Producer) PublishChallengeFailed(ctx context.Context, msg kafkamsg.VerificationChallengeFailedMessage) error {
	return p.publish(ctx, kafkamsg.TopicVerificationChallengeFailed, msg)
}

// PublishSendEmail publishes an email via the notification service's existing topic.
// Used for the email fallback method.
func (p *Producer) PublishSendEmail(ctx context.Context, msg kafkamsg.SendEmailMessage) error {
	return p.publish(ctx, kafkamsg.TopicSendEmail, msg)
}
```

- [ ] **Step 2: Create verification-service/internal/kafka/topics.go**

```go
package kafka

import (
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

// EnsureTopics creates Kafka topics if they don't already exist.
// This prevents the consumer group partition assignment race condition
// where consumers join before topics are created by producers.
func EnsureTopics(broker string, topics ...string) {
	addr := strings.Split(broker, ",")[0]

	var conn *kafkago.Conn
	var err error

	for i := 0; i < 10; i++ {
		conn, err = kafkago.Dial("tcp", addr)
		if err == nil {
			break
		}
		log.Printf("waiting for kafka (%d/10): %v", i+1, err)
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		log.Printf("warn: could not connect to kafka to ensure topics: %v", err)
		return
	}
	defer conn.Close()

	controller, err := conn.Controller()
	if err != nil {
		log.Printf("warn: could not get kafka controller: %v", err)
		return
	}

	controllerConn, err := kafkago.Dial("tcp", net.JoinHostPort(controller.Host, strconv.Itoa(controller.Port)))
	if err != nil {
		log.Printf("warn: could not connect to kafka controller: %v", err)
		return
	}
	defer controllerConn.Close()

	topicConfigs := make([]kafkago.TopicConfig, len(topics))
	for i, t := range topics {
		topicConfigs[i] = kafkago.TopicConfig{
			Topic:             t,
			NumPartitions:     1,
			ReplicationFactor: 1,
		}
	}

	if err := controllerConn.CreateTopics(topicConfigs...); err != nil {
		log.Printf("warn: topic creation error (topics may already exist): %v", err)
	} else {
		log.Printf("kafka topics ensured: %v", topics)
	}
}
```

- [ ] **Step 3: Verify kafka package compiles**

```bash
cd verification-service && go mod tidy && go build ./internal/kafka/
```

- [ ] **Step 4: Commit**

```bash
git add verification-service/internal/kafka/ verification-service/go.mod verification-service/go.sum
git commit -m "feat(verification-service): add Kafka producer and topic pre-creation"
```

---

## Task 7: Service layer

**Files:**
- Create: `verification-service/internal/service/verification_service.go`

- [ ] **Step 1: Create verification-service/internal/service/verification_service.go**

```go
package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"time"

	kafkamsg "github.com/exbanka/contract/kafka"
	shared "github.com/exbanka/contract/shared"
	kafkaprod "github.com/exbanka/verification-service/internal/kafka"
	"github.com/exbanka/verification-service/internal/model"
	"github.com/exbanka/verification-service/internal/repository"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

var validMethods = map[string]bool{
	"code_pull":    true,
	"qr_scan":     true,
	"number_match": true,
	"email":        true,
}

var validSourceServices = map[string]bool{
	"transaction": true,
	"payment":     true,
	"transfer":    true,
}

type VerificationService struct {
	repo            *repository.VerificationChallengeRepository
	producer        *kafkaprod.Producer
	db              *gorm.DB
	challengeExpiry time.Duration
	maxAttempts     int
}

func NewVerificationService(
	repo *repository.VerificationChallengeRepository,
	producer *kafkaprod.Producer,
	db *gorm.DB,
	challengeExpiry time.Duration,
	maxAttempts int,
) *VerificationService {
	return &VerificationService{
		repo:            repo,
		producer:        producer,
		db:              db,
		challengeExpiry: challengeExpiry,
		maxAttempts:     maxAttempts,
	}
}

// CreateChallenge creates a new verification challenge and publishes the appropriate Kafka event.
// For email fallback, it publishes to notification.send-email instead of verification.challenge-created.
func (s *VerificationService) CreateChallenge(ctx context.Context, userID uint64, sourceService string, sourceID uint64, method string, deviceID string) (*model.VerificationChallenge, error) {
	if !validMethods[method] {
		return nil, fmt.Errorf("invalid verification method: %s; must be one of: code_pull, qr_scan, number_match, email", method)
	}
	if !validSourceServices[sourceService] {
		return nil, fmt.Errorf("invalid source_service: %s; must be one of: transaction, payment, transfer", sourceService)
	}
	if sourceID == 0 {
		return nil, fmt.Errorf("source_id must be greater than 0")
	}
	if userID == 0 {
		return nil, fmt.Errorf("user_id must be greater than 0")
	}

	code := generateCode()
	challengeData, err := buildChallengeData(method)
	if err != nil {
		return nil, fmt.Errorf("failed to build challenge data: %w", err)
	}

	challengeDataJSON, err := json.Marshal(challengeData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal challenge data: %w", err)
	}

	vc := &model.VerificationChallenge{
		UserID:        userID,
		SourceService: sourceService,
		SourceID:      sourceID,
		Method:        method,
		Code:          code,
		ChallengeData: datatypes.JSON(challengeDataJSON),
		Status:        "pending",
		Attempts:      0,
		ExpiresAt:     time.Now().Add(s.challengeExpiry),
		DeviceID:      deviceID,
		Version:       1,
	}

	if err := s.repo.Create(vc); err != nil {
		return nil, fmt.Errorf("failed to create verification challenge: %w", err)
	}

	// Publish Kafka events AFTER the DB transaction commits.
	if method == "email" {
		// Email fallback: send code via existing notification.send-email topic.
		if err := s.producer.PublishSendEmail(ctx, kafkamsg.SendEmailMessage{
			To:        "", // Caller (gateway/transaction-service) must resolve email before calling; or use user_id lookup
			EmailType: kafkamsg.EmailTypeVerificationCode,
			Data: map[string]string{
				"code":         code,
				"challenge_id": fmt.Sprintf("%d", vc.ID),
			},
		}); err != nil {
			log.Printf("warn: failed to publish send-email for verification %d: %v", vc.ID, err)
		}
	} else {
		// Mobile delivery: publish to verification.challenge-created for notification-service.
		displayData, err := buildDisplayData(method, code, challengeData)
		if err != nil {
			log.Printf("warn: failed to build display data for verification %d: %v", vc.ID, err)
		} else {
			displayDataJSON, _ := json.Marshal(displayData)
			if err := s.producer.PublishChallengeCreated(ctx, kafkamsg.VerificationChallengeCreatedMessage{
				ChallengeID:     vc.ID,
				UserID:          userID,
				DeviceID:        deviceID,
				Method:          method,
				DisplayData:     string(displayDataJSON),
				DeliveryChannel: "mobile",
				ExpiresAt:       vc.ExpiresAt.UTC().Format(time.RFC3339),
			}); err != nil {
				log.Printf("warn: failed to publish challenge-created for verification %d: %v", vc.ID, err)
			}
		}
	}

	return vc, nil
}

// GetChallengeStatus returns the current status of a challenge by ID.
func (s *VerificationService) GetChallengeStatus(challengeID uint64) (*model.VerificationChallenge, error) {
	vc, err := s.repo.GetByID(challengeID)
	if err != nil {
		return nil, fmt.Errorf("challenge not found: %w", err)
	}
	return vc, nil
}

// GetPendingChallenge returns the most recent pending challenge for a user+device pair.
func (s *VerificationService) GetPendingChallenge(userID uint64, deviceID string) (*model.VerificationChallenge, error) {
	return s.repo.GetPendingByUser(userID, deviceID)
}

// SubmitVerification handles mobile-submitted responses (qr_scan, number_match, code_pull with biometric).
// It validates the response inside a SELECT FOR UPDATE transaction for concurrency safety.
func (s *VerificationService) SubmitVerification(ctx context.Context, challengeID uint64, deviceID string, response string) (bool, int, string, error) {
	var success bool
	var remaining int
	var newChallengeData string

	err := s.db.Transaction(func(tx *gorm.DB) error {
		vc, err := s.repo.GetByIDForUpdate(tx, challengeID)
		if err != nil {
			return fmt.Errorf("challenge not found: %w", err)
		}

		if err := s.validateChallengeState(vc); err != nil {
			return err
		}

		// Bind device if not already bound
		if vc.DeviceID == "" {
			vc.DeviceID = deviceID
		} else if vc.DeviceID != deviceID {
			return fmt.Errorf("challenge already bound to a different device")
		}

		correct := s.checkResponse(vc, response)
		vc.Attempts++

		if correct {
			now := time.Now()
			vc.Status = "verified"
			vc.VerifiedAt = &now
			success = true
			remaining = s.maxAttempts - vc.Attempts
		} else {
			remaining = s.maxAttempts - vc.Attempts
			if remaining <= 0 {
				vc.Status = "failed"
			} else if vc.Method == "number_match" {
				// Regenerate challenge data on wrong number_match answer
				newData, err := buildChallengeData("number_match")
				if err == nil {
					newDataJSON, _ := json.Marshal(newData)
					vc.ChallengeData = datatypes.JSON(newDataJSON)
					// Return new options (without target) for the mobile app
					displayData := map[string]interface{}{
						"options": newData["options"],
					}
					displayDataJSON, _ := json.Marshal(displayData)
					newChallengeData = string(displayDataJSON)
				}
			}
		}

		result := tx.Save(vc)
		if err := shared.CheckRowsAffected(result); err != nil {
			return err
		}

		// Publish events after save within the transaction closure
		// (the actual Kafka write happens here but the DB commit is guaranteed by returning nil)
		if vc.Status == "verified" {
			if pubErr := s.producer.PublishChallengeVerified(ctx, kafkamsg.VerificationChallengeVerifiedMessage{
				ChallengeID:   vc.ID,
				UserID:        vc.UserID,
				SourceService: vc.SourceService,
				SourceID:      vc.SourceID,
				Method:        vc.Method,
				VerifiedAt:    vc.VerifiedAt.UTC().Format(time.RFC3339),
			}); pubErr != nil {
				log.Printf("warn: failed to publish challenge-verified for %d: %v", vc.ID, pubErr)
			}
		} else if vc.Status == "failed" {
			if pubErr := s.producer.PublishChallengeFailed(ctx, kafkamsg.VerificationChallengeFailedMessage{
				ChallengeID:   vc.ID,
				UserID:        vc.UserID,
				SourceService: vc.SourceService,
				SourceID:      vc.SourceID,
				Reason:        "max_attempts_exceeded",
			}); pubErr != nil {
				log.Printf("warn: failed to publish challenge-failed for %d: %v", vc.ID, pubErr)
			}
		}

		return nil
	})

	return success, remaining, newChallengeData, err
}

// SubmitCode handles browser-submitted 6-digit codes (code_pull and email fallback methods).
func (s *VerificationService) SubmitCode(ctx context.Context, challengeID uint64, code string) (bool, int, error) {
	var success bool
	var remaining int

	err := s.db.Transaction(func(tx *gorm.DB) error {
		vc, err := s.repo.GetByIDForUpdate(tx, challengeID)
		if err != nil {
			return fmt.Errorf("challenge not found: %w", err)
		}

		if err := s.validateChallengeState(vc); err != nil {
			return err
		}

		// code_pull and email methods accept direct code submission
		if vc.Method != "code_pull" && vc.Method != "email" {
			return fmt.Errorf("code submission is only allowed for code_pull and email methods; this challenge uses %s", vc.Method)
		}

		vc.Attempts++

		if vc.Code == code {
			now := time.Now()
			vc.Status = "verified"
			vc.VerifiedAt = &now
			success = true
			remaining = s.maxAttempts - vc.Attempts
		} else {
			remaining = s.maxAttempts - vc.Attempts
			if remaining <= 0 {
				vc.Status = "failed"
			}
		}

		result := tx.Save(vc)
		if err := shared.CheckRowsAffected(result); err != nil {
			return err
		}

		if vc.Status == "verified" {
			if pubErr := s.producer.PublishChallengeVerified(ctx, kafkamsg.VerificationChallengeVerifiedMessage{
				ChallengeID:   vc.ID,
				UserID:        vc.UserID,
				SourceService: vc.SourceService,
				SourceID:      vc.SourceID,
				Method:        vc.Method,
				VerifiedAt:    vc.VerifiedAt.UTC().Format(time.RFC3339),
			}); pubErr != nil {
				log.Printf("warn: failed to publish challenge-verified for %d: %v", vc.ID, pubErr)
			}
		} else if vc.Status == "failed" {
			if pubErr := s.producer.PublishChallengeFailed(ctx, kafkamsg.VerificationChallengeFailedMessage{
				ChallengeID:   vc.ID,
				UserID:        vc.UserID,
				SourceService: vc.SourceService,
				SourceID:      vc.SourceID,
				Reason:        "max_attempts_exceeded",
			}); pubErr != nil {
				log.Printf("warn: failed to publish challenge-failed for %d: %v", vc.ID, pubErr)
			}
		}

		return nil
	})

	return success, remaining, err
}

// ExpireOldChallenges marks all pending challenges past their expiry as "expired"
// and publishes failure events for each. Called by the background goroutine.
func (s *VerificationService) ExpireOldChallenges(ctx context.Context) {
	count, err := s.repo.ExpireOld()
	if err != nil {
		log.Printf("warn: failed to expire old challenges: %v", err)
		return
	}
	if count > 0 {
		log.Printf("verification-service: expired %d old challenges", count)
	}
}

// validateChallengeState checks that the challenge is in a valid state for submission.
func (s *VerificationService) validateChallengeState(vc *model.VerificationChallenge) error {
	if vc.Status != "pending" {
		return fmt.Errorf("challenge is already %s", vc.Status)
	}
	if time.Now().After(vc.ExpiresAt) {
		return fmt.Errorf("challenge has expired")
	}
	if vc.Attempts >= s.maxAttempts {
		return fmt.Errorf("max attempts exceeded for this challenge")
	}
	return nil
}

// checkResponse validates the user's response against the challenge data.
func (s *VerificationService) checkResponse(vc *model.VerificationChallenge, response string) bool {
	switch vc.Method {
	case "code_pull":
		return vc.Code == response
	case "qr_scan":
		var data map[string]interface{}
		if err := json.Unmarshal(vc.ChallengeData, &data); err != nil {
			return false
		}
		token, ok := data["token"].(string)
		if !ok {
			return false
		}
		return token == response
	case "number_match":
		var data map[string]interface{}
		if err := json.Unmarshal(vc.ChallengeData, &data); err != nil {
			return false
		}
		target, ok := data["target"].(float64) // JSON numbers are float64
		if !ok {
			return false
		}
		return response == fmt.Sprintf("%d", int(target))
	default:
		return false
	}
}

// generateCode returns a cryptographically random 6-digit zero-padded string.
func generateCode() string {
	max := big.NewInt(1000000)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		// Fallback to a fixed code should never happen in practice
		return "000000"
	}
	return fmt.Sprintf("%06d", n.Int64())
}

// generateQRToken returns a cryptographically random 64-character hex string.
func generateQRToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// generateNumberMatchData returns a target number (1-99) and a shuffled slice of
// 5 options that includes the target plus 4 random decoys.
func generateNumberMatchData() (int, []int) {
	// Generate target (1-99)
	targetBig, _ := rand.Int(rand.Reader, big.NewInt(99))
	target := int(targetBig.Int64()) + 1

	// Generate 4 unique decoys different from the target
	decoys := make(map[int]bool)
	decoys[target] = true
	for len(decoys) < 5 {
		dBig, _ := rand.Int(rand.Reader, big.NewInt(99))
		d := int(dBig.Int64()) + 1
		decoys[d] = true
	}

	options := make([]int, 0, 5)
	for d := range decoys {
		options = append(options, d)
	}

	// Shuffle options using Fisher-Yates with crypto/rand
	for i := len(options) - 1; i > 0; i-- {
		jBig, _ := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		j := int(jBig.Int64())
		options[i], options[j] = options[j], options[i]
	}

	return target, options
}

// buildChallengeData constructs the method-specific challenge data map.
func buildChallengeData(method string) (map[string]interface{}, error) {
	switch method {
	case "code_pull":
		return map[string]interface{}{}, nil
	case "qr_scan":
		token := generateQRToken()
		if token == "" {
			return nil, fmt.Errorf("failed to generate QR token")
		}
		return map[string]interface{}{
			"token": token,
		}, nil
	case "number_match":
		target, options := generateNumberMatchData()
		return map[string]interface{}{
			"target":  target,
			"options": options,
		}, nil
	case "email":
		return map[string]interface{}{}, nil
	default:
		return nil, fmt.Errorf("unknown method: %s", method)
	}
}

// buildDisplayData constructs the data that the mobile app should display.
// For code_pull: includes the code. For number_match: only includes options (NOT the target).
// For qr_scan: no display data needed (browser shows the QR code, phone scans it).
func buildDisplayData(method string, code string, challengeData map[string]interface{}) (map[string]interface{}, error) {
	switch method {
	case "code_pull":
		return map[string]interface{}{
			"code": code,
		}, nil
	case "qr_scan":
		// No display data needed for the mobile app — the phone scans the QR from the browser
		return map[string]interface{}{}, nil
	case "number_match":
		options, ok := challengeData["options"]
		if !ok {
			return nil, fmt.Errorf("number_match challenge data missing options")
		}
		return map[string]interface{}{
			"options": options,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported method for display data: %s", method)
	}
}
```

- [ ] **Step 2: Verify service compiles**

```bash
cd verification-service && go mod tidy && go build ./internal/service/
```

- [ ] **Step 3: Commit**

```bash
git add verification-service/internal/service/ verification-service/go.mod verification-service/go.sum
git commit -m "feat(verification-service): add VerificationService with challenge lifecycle and crypto helpers"
```

---

## Task 8: gRPC handler

**Files:**
- Create: `verification-service/internal/handler/grpc_handler.go`

- [ ] **Step 1: Create verification-service/internal/handler/grpc_handler.go**

```go
package handler

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/exbanka/contract/verificationpb"
	"github.com/exbanka/verification-service/internal/service"
)

// mapServiceError maps service-layer error messages to appropriate gRPC status codes.
func mapServiceError(err error) codes.Code {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "not found"):
		return codes.NotFound
	case strings.Contains(msg, "must be"), strings.Contains(msg, "invalid"):
		return codes.InvalidArgument
	case strings.Contains(msg, "already exists"), strings.Contains(msg, "duplicate"):
		return codes.AlreadyExists
	case strings.Contains(msg, "already "), strings.Contains(msg, "expired"),
		strings.Contains(msg, "max attempts"), strings.Contains(msg, "only allowed"):
		return codes.FailedPrecondition
	case strings.Contains(msg, "bound to a different device"):
		return codes.PermissionDenied
	case strings.Contains(msg, "optimistic lock"):
		return codes.Aborted
	default:
		return codes.Internal
	}
}

type VerificationGRPCHandler struct {
	pb.UnimplementedVerificationGRPCServiceServer
	svc *service.VerificationService
}

func NewVerificationGRPCHandler(svc *service.VerificationService) *VerificationGRPCHandler {
	return &VerificationGRPCHandler{svc: svc}
}

func (h *VerificationGRPCHandler) CreateChallenge(ctx context.Context, req *pb.CreateChallengeRequest) (*pb.CreateChallengeResponse, error) {
	vc, err := h.svc.CreateChallenge(ctx, req.GetUserId(), req.GetSourceService(), req.GetSourceId(), req.GetMethod(), req.GetDeviceId())
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}

	// Build response challenge_data based on method.
	// For the browser: code_pull returns {}, qr_scan returns {"token":..., "verify_url":...},
	// number_match returns {"target":...}, email returns {}.
	var responseData map[string]interface{}
	if err := json.Unmarshal(vc.ChallengeData, &responseData); err != nil {
		responseData = map[string]interface{}{}
	}

	// For qr_scan, add the verify_url to the response
	if vc.Method == "qr_scan" {
		if token, ok := responseData["token"].(string); ok {
			responseData["verify_url"] = "/api/verify/" + uintToStr(vc.ID) + "?token=" + token
		}
	}

	// For number_match, only return target to browser (options go to mobile via Kafka)
	if vc.Method == "number_match" {
		target := responseData["target"]
		responseData = map[string]interface{}{
			"target": target,
		}
	}

	challengeDataJSON, _ := json.Marshal(responseData)

	return &pb.CreateChallengeResponse{
		ChallengeId:   vc.ID,
		Method:        vc.Method,
		ChallengeData: string(challengeDataJSON),
		ExpiresAt:     vc.ExpiresAt.UTC().Format(time.RFC3339),
	}, nil
}

func (h *VerificationGRPCHandler) GetChallengeStatus(ctx context.Context, req *pb.GetChallengeStatusRequest) (*pb.GetChallengeStatusResponse, error) {
	vc, err := h.svc.GetChallengeStatus(req.GetChallengeId())
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}

	resp := &pb.GetChallengeStatusResponse{
		ChallengeId: vc.ID,
		Status:      vc.Status,
		Method:      vc.Method,
		ExpiresAt:   vc.ExpiresAt.UTC().Format(time.RFC3339),
	}
	if vc.VerifiedAt != nil {
		resp.VerifiedAt = vc.VerifiedAt.UTC().Format(time.RFC3339)
	}

	return resp, nil
}

func (h *VerificationGRPCHandler) GetPendingChallenge(ctx context.Context, req *pb.GetPendingChallengeRequest) (*pb.GetPendingChallengeResponse, error) {
	vc, err := h.svc.GetPendingChallenge(req.GetUserId(), req.GetDeviceId())
	if err != nil {
		// Not found is a valid state for polling — return found=false
		return &pb.GetPendingChallengeResponse{Found: false}, nil
	}

	// Build display data for mobile app (never include secrets)
	var challengeData map[string]interface{}
	if err := json.Unmarshal(vc.ChallengeData, &challengeData); err != nil {
		challengeData = map[string]interface{}{}
	}

	// For number_match: only return options, NOT target
	displayData := map[string]interface{}{}
	switch vc.Method {
	case "code_pull":
		displayData["code"] = vc.Code
	case "number_match":
		if options, ok := challengeData["options"]; ok {
			displayData["options"] = options
		}
	case "qr_scan":
		// No display data — phone scans QR from browser
	}

	displayDataJSON, _ := json.Marshal(displayData)

	return &pb.GetPendingChallengeResponse{
		Found:         true,
		ChallengeId:   vc.ID,
		Method:        vc.Method,
		ChallengeData: string(displayDataJSON),
		ExpiresAt:     vc.ExpiresAt.UTC().Format(time.RFC3339),
	}, nil
}

func (h *VerificationGRPCHandler) SubmitVerification(ctx context.Context, req *pb.SubmitVerificationRequest) (*pb.SubmitVerificationResponse, error) {
	success, remaining, newChallengeData, err := h.svc.SubmitVerification(ctx, req.GetChallengeId(), req.GetDeviceId(), req.GetResponse())
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}

	return &pb.SubmitVerificationResponse{
		Success:           success,
		RemainingAttempts: int32(remaining),
		NewChallengeData:  newChallengeData,
	}, nil
}

func (h *VerificationGRPCHandler) SubmitCode(ctx context.Context, req *pb.SubmitCodeRequest) (*pb.SubmitCodeResponse, error) {
	success, remaining, err := h.svc.SubmitCode(ctx, req.GetChallengeId(), req.GetCode())
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}

	return &pb.SubmitCodeResponse{
		Success:           success,
		RemainingAttempts: int32(remaining),
	}, nil
}

// uintToStr converts a uint64 to its decimal string representation.
func uintToStr(n uint64) string {
	return strings.TrimRight(strings.TrimRight(
		strings.Replace(
			strings.Replace(
				json.Number(json.Number(string(rune('0'+n%10)))).String(),
				"", "", 0),
			"", "", 0),
		"0"), "")
}
```

Wait -- that `uintToStr` helper is unnecessarily complex. Replace the entire handler with a clean version using `strconv`:

```go
package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/exbanka/contract/verificationpb"
	"github.com/exbanka/verification-service/internal/service"
)

// mapServiceError maps service-layer error messages to appropriate gRPC status codes.
func mapServiceError(err error) codes.Code {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "not found"):
		return codes.NotFound
	case strings.Contains(msg, "must be"), strings.Contains(msg, "invalid"):
		return codes.InvalidArgument
	case strings.Contains(msg, "already exists"), strings.Contains(msg, "duplicate"):
		return codes.AlreadyExists
	case strings.Contains(msg, "already "), strings.Contains(msg, "expired"),
		strings.Contains(msg, "max attempts"), strings.Contains(msg, "only allowed"):
		return codes.FailedPrecondition
	case strings.Contains(msg, "bound to a different device"):
		return codes.PermissionDenied
	case strings.Contains(msg, "optimistic lock"):
		return codes.Aborted
	default:
		return codes.Internal
	}
}

type VerificationGRPCHandler struct {
	pb.UnimplementedVerificationGRPCServiceServer
	svc *service.VerificationService
}

func NewVerificationGRPCHandler(svc *service.VerificationService) *VerificationGRPCHandler {
	return &VerificationGRPCHandler{svc: svc}
}

func (h *VerificationGRPCHandler) CreateChallenge(ctx context.Context, req *pb.CreateChallengeRequest) (*pb.CreateChallengeResponse, error) {
	vc, err := h.svc.CreateChallenge(ctx, req.GetUserId(), req.GetSourceService(), req.GetSourceId(), req.GetMethod(), req.GetDeviceId())
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}

	// Build response challenge_data based on method.
	// For the browser: code_pull returns {}, qr_scan returns {"token":..., "verify_url":...},
	// number_match returns {"target":...}, email returns {}.
	var responseData map[string]interface{}
	if err := json.Unmarshal(vc.ChallengeData, &responseData); err != nil {
		responseData = map[string]interface{}{}
	}

	// For qr_scan, add the verify_url to the response
	if vc.Method == "qr_scan" {
		if token, ok := responseData["token"].(string); ok {
			responseData["verify_url"] = fmt.Sprintf("/api/verify/%d?token=%s", vc.ID, token)
		}
	}

	// For number_match, only return target to browser (options go to mobile via Kafka)
	if vc.Method == "number_match" {
		target := responseData["target"]
		responseData = map[string]interface{}{
			"target": target,
		}
	}

	challengeDataJSON, _ := json.Marshal(responseData)

	return &pb.CreateChallengeResponse{
		ChallengeId:   vc.ID,
		Method:        vc.Method,
		ChallengeData: string(challengeDataJSON),
		ExpiresAt:     vc.ExpiresAt.UTC().Format(time.RFC3339),
	}, nil
}

func (h *VerificationGRPCHandler) GetChallengeStatus(ctx context.Context, req *pb.GetChallengeStatusRequest) (*pb.GetChallengeStatusResponse, error) {
	vc, err := h.svc.GetChallengeStatus(req.GetChallengeId())
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}

	resp := &pb.GetChallengeStatusResponse{
		ChallengeId: vc.ID,
		Status:      vc.Status,
		Method:      vc.Method,
		ExpiresAt:   vc.ExpiresAt.UTC().Format(time.RFC3339),
	}
	if vc.VerifiedAt != nil {
		resp.VerifiedAt = vc.VerifiedAt.UTC().Format(time.RFC3339)
	}

	return resp, nil
}

func (h *VerificationGRPCHandler) GetPendingChallenge(ctx context.Context, req *pb.GetPendingChallengeRequest) (*pb.GetPendingChallengeResponse, error) {
	vc, err := h.svc.GetPendingChallenge(req.GetUserId(), req.GetDeviceId())
	if err != nil {
		// Not found is a valid state for polling — return found=false
		return &pb.GetPendingChallengeResponse{Found: false}, nil
	}

	// Build display data for mobile app (never include secrets)
	var challengeData map[string]interface{}
	if err := json.Unmarshal(vc.ChallengeData, &challengeData); err != nil {
		challengeData = map[string]interface{}{}
	}

	// For number_match: only return options, NOT target
	displayData := map[string]interface{}{}
	switch vc.Method {
	case "code_pull":
		displayData["code"] = vc.Code
	case "number_match":
		if options, ok := challengeData["options"]; ok {
			displayData["options"] = options
		}
	case "qr_scan":
		// No display data — phone scans QR from browser
	}

	displayDataJSON, _ := json.Marshal(displayData)

	return &pb.GetPendingChallengeResponse{
		Found:         true,
		ChallengeId:   vc.ID,
		Method:        vc.Method,
		ChallengeData: string(displayDataJSON),
		ExpiresAt:     vc.ExpiresAt.UTC().Format(time.RFC3339),
	}, nil
}

func (h *VerificationGRPCHandler) SubmitVerification(ctx context.Context, req *pb.SubmitVerificationRequest) (*pb.SubmitVerificationResponse, error) {
	success, remaining, newChallengeData, err := h.svc.SubmitVerification(ctx, req.GetChallengeId(), req.GetDeviceId(), req.GetResponse())
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}

	return &pb.SubmitVerificationResponse{
		Success:           success,
		RemainingAttempts: int32(remaining),
		NewChallengeData:  newChallengeData,
	}, nil
}

func (h *VerificationGRPCHandler) SubmitCode(ctx context.Context, req *pb.SubmitCodeRequest) (*pb.SubmitCodeResponse, error) {
	success, remaining, err := h.svc.SubmitCode(ctx, req.GetChallengeId(), req.GetCode())
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}

	return &pb.SubmitCodeResponse{
		Success:           success,
		RemainingAttempts: int32(remaining),
	}, nil
}
```

- [ ] **Step 2: Verify handler compiles**

```bash
cd verification-service && go mod tidy && go build ./internal/handler/
```

- [ ] **Step 3: Commit**

```bash
git add verification-service/internal/handler/ verification-service/go.mod verification-service/go.sum
git commit -m "feat(verification-service): add gRPC handler with service error mapping"
```

---

## Task 9: main.go wiring

**Files:**
- Create: `verification-service/cmd/main.go`

- [ ] **Step 1: Create verification-service/cmd/main.go**

```go
package main

import (
	"context"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	kafkamsg "github.com/exbanka/contract/kafka"
	shared "github.com/exbanka/contract/shared"
	pb "github.com/exbanka/contract/verificationpb"
	"github.com/exbanka/verification-service/internal/config"
	"github.com/exbanka/verification-service/internal/handler"
	kafkaprod "github.com/exbanka/verification-service/internal/kafka"
	"github.com/exbanka/verification-service/internal/model"
	"github.com/exbanka/verification-service/internal/repository"
	"github.com/exbanka/verification-service/internal/service"
)

func main() {
	cfg := config.Load()

	// 1. Connect to PostgreSQL
	db, err := gorm.Open(postgres.Open(cfg.DSN()), &gorm.Config{})
	if err != nil {
		log.Fatalf("failed to connect to database: %v", err)
	}

	// 2. Auto-migrate models
	if err := db.AutoMigrate(&model.VerificationChallenge{}); err != nil {
		log.Fatalf("failed to migrate: %v", err)
	}

	// 3. Create Kafka producer
	producer := kafkaprod.NewProducer(cfg.KafkaBrokers)
	defer producer.Close()

	// 4. Create repository
	repo := repository.NewVerificationChallengeRepository(db)

	// 5. Create service
	svc := service.NewVerificationService(repo, producer, db, cfg.ChallengeExpiry, cfg.MaxAttempts)

	// 6. Create gRPC handler
	grpcHandler := handler.NewVerificationGRPCHandler(svc)

	// 7. Create TCP listener
	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	// 8. Create gRPC server and register services
	s := grpc.NewServer()
	pb.RegisterVerificationGRPCServiceServer(s, grpcHandler)
	shared.RegisterHealthCheck(s, "verification-service")

	// 9. Ensure Kafka topics exist (produces to + consumed from)
	kafkaprod.EnsureTopics(cfg.KafkaBrokers,
		kafkamsg.TopicVerificationChallengeCreated,
		kafkamsg.TopicVerificationChallengeVerified,
		kafkamsg.TopicVerificationChallengeFailed,
		kafkamsg.TopicSendEmail,
	)

	// 10. Start background expiry goroutine
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runExpiryLoop(ctx, svc)

	// 11. Start gRPC server
	go func() {
		log.Printf("verification-service listening on %s", cfg.GRPCAddr)
		if err := s.Serve(lis); err != nil {
			log.Fatalf("failed to serve: %v", err)
		}
	}()

	// 12. Wait for interrupt signal and shut down gracefully
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down verification-service gracefully...")
	cancel()
	s.GracefulStop()
	log.Println("Server stopped")
}

// runExpiryLoop runs every 60 seconds to expire old pending challenges.
// It respects context cancellation for graceful shutdown.
func runExpiryLoop(ctx context.Context, svc *service.VerificationService) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			svc.ExpireOldChallenges(ctx)
		case <-ctx.Done():
			log.Println("verification-service: expiry loop stopped")
			return
		}
	}
}
```

- [ ] **Step 2: Verify full service compiles**

```bash
cd verification-service && go mod tidy && go build ./cmd
```

- [ ] **Step 3: Commit**

```bash
git add verification-service/cmd/ verification-service/go.mod verification-service/go.sum
git commit -m "feat(verification-service): add main.go with full wiring, health check, and expiry loop"
```

---

## Task 10: Dockerfile

**Files:**
- Create: `verification-service/Dockerfile`

- [ ] **Step 1: Create verification-service/Dockerfile**

```dockerfile
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY contract/ contract/
COPY verification-service/ verification-service/
ENV GOWORK=off
RUN cd verification-service && \
    go mod edit -replace github.com/exbanka/contract=../contract && \
    go mod download && go build -o /verification-service ./cmd

FROM alpine:3.19
RUN apk --no-cache add ca-certificates netcat-openbsd
COPY --from=builder /verification-service /verification-service
CMD ["/verification-service"]
```

- [ ] **Step 2: Commit**

```bash
git add verification-service/Dockerfile
git commit -m "feat(verification-service): add Dockerfile"
```

---

## Task 11: Docker Compose

**Files:**
- Modify: `docker-compose.yml`

- [ ] **Step 1: Add verification-db to the Databases section**

Add after the `exchange-db` block (before the `# --- Services ---` comment):

```yaml
  verification-db:
    image: postgres:16-alpine
    environment:
      POSTGRES_DB: verificationdb
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: postgres
    ports:
      - "5440:5432"
    volumes:
      - verification-db-data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
      interval: 5s
      timeout: 5s
      retries: 5
```

- [ ] **Step 2: Add verification-service to the Services section**

Add after the `exchange-service` block (before the `# --- API Gateway ---` comment):

```yaml
  verification-service:
    build:
      context: .
      dockerfile: verification-service/Dockerfile
    environment:
      VERIFICATION_DB_HOST: verification-db
      VERIFICATION_DB_PORT: "5432"
      VERIFICATION_DB_USER: postgres
      VERIFICATION_DB_PASSWORD: postgres
      VERIFICATION_DB_NAME: verificationdb
      VERIFICATION_GRPC_ADDR: ":50060"
      KAFKA_BROKERS: kafka:9092
      VERIFICATION_DB_SSLMODE: disable
      VERIFICATION_CHALLENGE_EXPIRY: "5m"
      VERIFICATION_MAX_ATTEMPTS: "3"
    ports:
      - "50060:50060"
    depends_on:
      verification-db:
        condition: service_healthy
      kafka:
        condition: service_healthy
```

- [ ] **Step 3: Add VERIFICATION_GRPC_ADDR to api-gateway environment**

In the `api-gateway` service's `environment:` block, add:

```yaml
      VERIFICATION_GRPC_ADDR: "verification-service:50060"
```

- [ ] **Step 4: Add verification-service to api-gateway depends_on**

In the `api-gateway` service's `depends_on:` block, add:

```yaml
      verification-service:
        condition: service_started
```

- [ ] **Step 5: Add volume**

In the `volumes:` section at the bottom, add:

```yaml
  verification-db-data:
```

- [ ] **Step 6: Commit**

```bash
git add docker-compose.yml
git commit -m "feat(docker): add verification-service and verification-db to docker-compose"
```

---

## Task 12: Makefile updates

**Files:**
- Modify: `Makefile`

- [ ] **Step 1: Add verification-service to the build target**

Add to the `build:` target, after the exchange-service build line:

```makefile
	cd verification-service && go build -o bin/verification-service ./cmd
```

- [ ] **Step 2: Add verification-service to the tidy target**

Add to the `tidy:` target:

```makefile
	cd verification-service && go mod tidy
```

- [ ] **Step 3: Add verification-service to the clean target**

Add to the `clean:` target:

```makefile
	rm -f contract/verificationpb/*.go
	rm -f verification-service/bin/*
```

- [ ] **Step 4: Add verification-service to the test target**

Add to the `test:` target:

```makefile
	cd verification-service && go test ./... -v
```

- [ ] **Step 5: Verify make build works**

```bash
make build
```

- [ ] **Step 6: Commit**

```bash
git add Makefile
git commit -m "feat(makefile): add verification-service to build, tidy, clean, and test targets"
```

---

## Task 13: Final verification and go.work sync

**Files:**
- Verify: `go.work` includes `./verification-service`
- Verify: all generated proto files in `contract/verificationpb/`
- Verify: full build passes

- [ ] **Step 1: Verify go.work includes verification-service**

```bash
grep verification-service go.work
```

Expected: `./verification-service`

- [ ] **Step 2: Verify proto generated files**

```bash
ls contract/verificationpb/
```

Expected: `verification.pb.go` and `verification_grpc.pb.go`

- [ ] **Step 3: Verify contract builds**

```bash
cd contract && go build ./...
```

- [ ] **Step 4: Verify verification-service builds**

```bash
cd verification-service && go build ./cmd
```

- [ ] **Step 5: Run verification-service tests**

```bash
cd verification-service && go test ./... -v
```

- [ ] **Step 6: Verify make tidy works across workspace**

```bash
make tidy
```

- [ ] **Step 7: Verify full workspace build**

```bash
make build
```

- [ ] **Step 8: Final commit (if any tidy changes)**

```bash
git add -A
git diff --cached --stat
# Only commit if there are changes
git commit -m "chore(verification-service): final tidy and workspace sync"
```

---

## Summary of deliverables

| Deliverable | Path |
|---|---|
| Proto definition | `contract/proto/verification/verification.proto` |
| Generated proto code | `contract/verificationpb/` |
| Kafka messages | `contract/kafka/messages.go` (modified) |
| Config | `verification-service/internal/config/config.go` |
| Model | `verification-service/internal/model/verification_challenge.go` |
| Repository | `verification-service/internal/repository/verification_challenge_repository.go` |
| Service | `verification-service/internal/service/verification_service.go` |
| gRPC Handler | `verification-service/internal/handler/grpc_handler.go` |
| Kafka Producer | `verification-service/internal/kafka/producer.go` |
| Kafka Topics | `verification-service/internal/kafka/topics.go` |
| Main entrypoint | `verification-service/cmd/main.go` |
| Dockerfile | `verification-service/Dockerfile` |
| go.mod | `verification-service/go.mod` |
| go.work | `go.work` (modified) |
| Makefile | `Makefile` (modified) |
| Docker Compose | `docker-compose.yml` (modified) |

## What this plan does NOT cover (handled by other plans)

- API Gateway routes and handlers for verification endpoints (separate gateway plan)
- auth-service MobileDevice model and activation flow (separate auth-service plan)
- notification-service MobileInboxItem and Kafka consumer (separate notification-service plan)
- transaction-service migration to use verification-service (separate migration plan)
- WebSocket endpoint in api-gateway (separate gateway plan)
- MobileAuthMiddleware and RequireDeviceSignature (separate gateway plan)
- Integration tests (separate test plan)
- Specification.md and CLAUDE.md updates (done after all plans complete)
