package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	authpb "github.com/exbanka/contract/authpb"
	kafkamsg "github.com/exbanka/contract/kafka"
	"github.com/exbanka/verification-service/internal/model"
	"github.com/exbanka/verification-service/internal/repository"
)

// ---------------------------------------------------------------------------
// Hand-written stubs (no codegen, no gomock)
// ---------------------------------------------------------------------------

type stubProducer struct {
	mu sync.Mutex

	createdCalls  []kafkamsg.VerificationChallengeCreatedMessage
	verifiedCalls []kafkamsg.VerificationChallengeVerifiedMessage
	failedCalls   []kafkamsg.VerificationChallengeFailedMessage

	// Errors to return on the next call (per method).
	createdErr  error
	verifiedErr error
	failedErr   error
}

func (s *stubProducer) PublishChallengeCreated(_ context.Context, msg kafkamsg.VerificationChallengeCreatedMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createdCalls = append(s.createdCalls, msg)
	return s.createdErr
}

func (s *stubProducer) PublishChallengeVerified(_ context.Context, msg kafkamsg.VerificationChallengeVerifiedMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.verifiedCalls = append(s.verifiedCalls, msg)
	return s.verifiedErr
}

func (s *stubProducer) PublishChallengeFailed(_ context.Context, msg kafkamsg.VerificationChallengeFailedMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failedCalls = append(s.failedCalls, msg)
	return s.failedErr
}

type stubAuthClient struct {
	enabled    bool
	err        error
	lastReq    *authpb.CheckBiometricsRequest
	callCount  int
}

func (s *stubAuthClient) CheckBiometricsEnabled(_ context.Context, in *authpb.CheckBiometricsRequest, _ ...grpc.CallOption) (*authpb.CheckBiometricsResponse, error) {
	s.callCount++
	s.lastReq = in
	if s.err != nil {
		return nil, s.err
	}
	return &authpb.CheckBiometricsResponse{Enabled: s.enabled}, nil
}

// newServiceWithStubs builds a VerificationService backed by an in-memory SQLite DB,
// with the supplied stubs wired in. Both `producer` and `authClient` may be nil — the
// service tolerates a nil authClient only inside VerifyByBiometric; production code
// always supplies non-nil values.
func newServiceWithStubs(t *testing.T, producer verificationProducer, auth biometricsChecker) (*VerificationService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&model.VerificationChallenge{}))

	repo := repository.NewVerificationChallengeRepository(db)
	svc := &VerificationService{
		repo:            repo,
		producer:        producer,
		db:              db,
		authClient:      auth,
		challengeExpiry: 5 * time.Minute,
		maxAttempts:     3,
	}
	return svc, db
}

// ---------------------------------------------------------------------------
// CreateChallenge
// ---------------------------------------------------------------------------

func TestCreateChallenge_HappyPath_PublishesEvent(t *testing.T) {
	prod := &stubProducer{}
	svc, db := newServiceWithStubs(t, prod, nil)

	vc, err := svc.CreateChallenge(context.Background(), 7, "transaction", 99, "code_pull", "device-A")
	require.NoError(t, err)
	require.NotNil(t, vc)

	assert.Equal(t, uint64(7), vc.UserID)
	assert.Equal(t, "transaction", vc.SourceService)
	assert.Equal(t, uint64(99), vc.SourceID)
	assert.Equal(t, "code_pull", vc.Method)
	assert.Equal(t, "pending", vc.Status)
	assert.Equal(t, "device-A", vc.DeviceID)
	assert.Len(t, vc.Code, 6)
	assert.True(t, vc.ExpiresAt.After(time.Now()))

	// Persisted
	var loaded model.VerificationChallenge
	require.NoError(t, db.First(&loaded, vc.ID).Error)
	assert.Equal(t, "code_pull", loaded.Method)

	// Kafka event published
	require.Len(t, prod.createdCalls, 1)
	assert.Equal(t, vc.ID, prod.createdCalls[0].ChallengeID)
	assert.Equal(t, uint64(7), prod.createdCalls[0].UserID)
	assert.Equal(t, "code_pull", prod.createdCalls[0].Method)
	assert.Equal(t, "mobile", prod.createdCalls[0].DeliveryChannel)
}

func TestCreateChallenge_InvalidMethod(t *testing.T) {
	svc, _ := newServiceWithStubs(t, &stubProducer{}, nil)
	_, err := svc.CreateChallenge(context.Background(), 1, "transaction", 1, "magic_method", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid verification method")
}

func TestCreateChallenge_InvalidSourceService(t *testing.T) {
	svc, _ := newServiceWithStubs(t, &stubProducer{}, nil)
	_, err := svc.CreateChallenge(context.Background(), 1, "stocks", 1, "code_pull", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid source_service")
}

func TestCreateChallenge_ZeroSourceID(t *testing.T) {
	svc, _ := newServiceWithStubs(t, &stubProducer{}, nil)
	_, err := svc.CreateChallenge(context.Background(), 1, "transaction", 0, "code_pull", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "source_id")
}

func TestCreateChallenge_ZeroUserID(t *testing.T) {
	svc, _ := newServiceWithStubs(t, &stubProducer{}, nil)
	_, err := svc.CreateChallenge(context.Background(), 0, "transaction", 1, "code_pull", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "user_id")
}

func TestCreateChallenge_PublisherErrorIsNonFatal(t *testing.T) {
	prod := &stubProducer{createdErr: errors.New("kafka boom")}
	svc, db := newServiceWithStubs(t, prod, nil)

	vc, err := svc.CreateChallenge(context.Background(), 8, "payment", 50, "code_pull", "device-B")
	require.NoError(t, err, "publisher errors should be logged, not returned")
	require.NotNil(t, vc)

	// Challenge still persisted even though publish failed.
	var loaded model.VerificationChallenge
	require.NoError(t, db.First(&loaded, vc.ID).Error)
	assert.Equal(t, "pending", loaded.Status)
}

// ---------------------------------------------------------------------------
// SubmitCode (code_pull, browser-side)
// ---------------------------------------------------------------------------

func TestSubmitCode_CorrectCode_PublishesVerified(t *testing.T) {
	prod := &stubProducer{}
	svc, db := newServiceWithStubs(t, prod, nil)

	vc := seedChallenge(t, db, &model.VerificationChallenge{
		UserID:        42,
		SourceService: "transaction",
		SourceID:      999,
		Method:        "code_pull",
		Code:          "424242",
		ChallengeData: datatypes.JSON([]byte("{}")),
		Status:        "pending",
		ExpiresAt:     time.Now().Add(5 * time.Minute),
		Version:       1,
	})

	success, remaining, err := svc.SubmitCode(context.Background(), vc.ID, "424242")
	require.NoError(t, err)
	assert.True(t, success)
	assert.Equal(t, 2, remaining)

	var loaded model.VerificationChallenge
	require.NoError(t, db.First(&loaded, vc.ID).Error)
	assert.Equal(t, "verified", loaded.Status)
	require.NotNil(t, loaded.VerifiedAt)

	require.Len(t, prod.verifiedCalls, 1)
	assert.Equal(t, vc.ID, prod.verifiedCalls[0].ChallengeID)
	assert.Equal(t, "code_pull", prod.verifiedCalls[0].Method)
}

func TestSubmitCode_BypassCode_Verifies(t *testing.T) {
	prod := &stubProducer{}
	svc, db := newServiceWithStubs(t, prod, nil)

	vc := seedChallenge(t, db, &model.VerificationChallenge{
		UserID:        1,
		SourceService: "transaction",
		SourceID:      1,
		Method:        "code_pull",
		Code:          "999999",
		ChallengeData: datatypes.JSON([]byte("{}")),
		Status:        "pending",
		ExpiresAt:     time.Now().Add(5 * time.Minute),
		Version:       1,
	})

	success, remaining, err := svc.SubmitCode(context.Background(), vc.ID, defaultBypassCode)
	require.NoError(t, err)
	assert.True(t, success)
	assert.Equal(t, 2, remaining)
	require.Len(t, prod.verifiedCalls, 1)
}

func TestSubmitCode_WrongCode_DecrementsRemaining(t *testing.T) {
	prod := &stubProducer{}
	svc, db := newServiceWithStubs(t, prod, nil)

	vc := seedChallenge(t, db, &model.VerificationChallenge{
		UserID:        1,
		SourceService: "transaction",
		SourceID:      1,
		Method:        "code_pull",
		Code:          "111000",
		ChallengeData: datatypes.JSON([]byte("{}")),
		Status:        "pending",
		ExpiresAt:     time.Now().Add(5 * time.Minute),
		Version:       1,
	})

	success, remaining, err := svc.SubmitCode(context.Background(), vc.ID, "000000")
	require.NoError(t, err)
	assert.False(t, success)
	assert.Equal(t, 2, remaining)

	var loaded model.VerificationChallenge
	require.NoError(t, db.First(&loaded, vc.ID).Error)
	assert.Equal(t, "pending", loaded.Status, "still pending until max attempts hit")
	assert.Equal(t, 1, loaded.Attempts)

	assert.Len(t, prod.verifiedCalls, 0)
	assert.Len(t, prod.failedCalls, 0)
}

func TestSubmitCode_MaxAttemptsReached_PublishesFailed(t *testing.T) {
	prod := &stubProducer{}
	svc, db := newServiceWithStubs(t, prod, nil)

	// Pre-seed at attempts = maxAttempts - 1 so this is the final wrong attempt.
	vc := seedChallenge(t, db, &model.VerificationChallenge{
		UserID:        1,
		SourceService: "transaction",
		SourceID:      1,
		Method:        "code_pull",
		Code:          "111000",
		ChallengeData: datatypes.JSON([]byte("{}")),
		Status:        "pending",
		Attempts:      2,
		ExpiresAt:     time.Now().Add(5 * time.Minute),
		Version:       1,
	})

	success, remaining, err := svc.SubmitCode(context.Background(), vc.ID, "000000")
	require.NoError(t, err)
	assert.False(t, success)
	assert.Equal(t, 0, remaining)

	var loaded model.VerificationChallenge
	require.NoError(t, db.First(&loaded, vc.ID).Error)
	assert.Equal(t, "failed", loaded.Status)
	assert.Equal(t, 3, loaded.Attempts)

	require.Len(t, prod.failedCalls, 1)
	assert.Equal(t, vc.ID, prod.failedCalls[0].ChallengeID)
	assert.Equal(t, "max_attempts_exceeded", prod.failedCalls[0].Reason)
}

func TestSubmitCode_NonCodePullMethod_Rejected(t *testing.T) {
	svc, db := newServiceWithStubs(t, &stubProducer{}, nil)

	vc := seedChallenge(t, db, &model.VerificationChallenge{
		UserID:        1,
		SourceService: "transaction",
		SourceID:      1,
		Method:        "qr_scan",
		Code:          "ignored",
		ChallengeData: datatypes.JSON([]byte(`{"token":"x"}`)),
		Status:        "pending",
		ExpiresAt:     time.Now().Add(5 * time.Minute),
		Version:       1,
	})

	success, _, err := svc.SubmitCode(context.Background(), vc.ID, "anything")
	require.Error(t, err)
	assert.False(t, success)
	assert.Contains(t, err.Error(), "code_pull")
}

func TestSubmitCode_ChallengeNotFound(t *testing.T) {
	svc, _ := newServiceWithStubs(t, &stubProducer{}, nil)

	_, _, err := svc.SubmitCode(context.Background(), 9999, "111111")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestSubmitCode_AlreadyVerified(t *testing.T) {
	svc, db := newServiceWithStubs(t, &stubProducer{}, nil)
	vc := seedChallenge(t, db, &model.VerificationChallenge{
		UserID:        1,
		SourceService: "transaction",
		SourceID:      1,
		Method:        "code_pull",
		Code:          "111111",
		ChallengeData: datatypes.JSON([]byte("{}")),
		Status:        "verified",
		ExpiresAt:     time.Now().Add(5 * time.Minute),
		Version:       1,
	})

	_, _, err := svc.SubmitCode(context.Background(), vc.ID, "111111")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "verified")
}

// ---------------------------------------------------------------------------
// SubmitVerification (mobile-side)
// ---------------------------------------------------------------------------

func TestSubmitVerification_CodePull_Correct(t *testing.T) {
	prod := &stubProducer{}
	svc, db := newServiceWithStubs(t, prod, nil)

	vc := seedChallenge(t, db, &model.VerificationChallenge{
		UserID:        1,
		SourceService: "transaction",
		SourceID:      1,
		Method:        "code_pull",
		Code:          "777888",
		ChallengeData: datatypes.JSON([]byte("{}")),
		Status:        "pending",
		ExpiresAt:     time.Now().Add(5 * time.Minute),
		Version:       1,
	})

	success, remaining, newData, err := svc.SubmitVerification(context.Background(), vc.ID, "device-1", "777888")
	require.NoError(t, err)
	assert.True(t, success)
	assert.Equal(t, 2, remaining)
	assert.Empty(t, newData)

	var loaded model.VerificationChallenge
	require.NoError(t, db.First(&loaded, vc.ID).Error)
	assert.Equal(t, "verified", loaded.Status)
	assert.Equal(t, "device-1", loaded.DeviceID)

	require.Len(t, prod.verifiedCalls, 1)
}

func TestSubmitVerification_BindsDeviceWhenEmpty(t *testing.T) {
	svc, db := newServiceWithStubs(t, &stubProducer{}, nil)

	vc := seedChallenge(t, db, &model.VerificationChallenge{
		UserID:        1,
		SourceService: "transaction",
		SourceID:      1,
		Method:        "code_pull",
		Code:          "555555",
		ChallengeData: datatypes.JSON([]byte("{}")),
		Status:        "pending",
		ExpiresAt:     time.Now().Add(5 * time.Minute),
		Version:       1,
	})

	_, _, _, err := svc.SubmitVerification(context.Background(), vc.ID, "shiny-device", "wrong")
	require.NoError(t, err)

	var loaded model.VerificationChallenge
	require.NoError(t, db.First(&loaded, vc.ID).Error)
	assert.Equal(t, "shiny-device", loaded.DeviceID)
}

func TestSubmitVerification_DeviceMismatch_Rejected(t *testing.T) {
	svc, db := newServiceWithStubs(t, &stubProducer{}, nil)

	vc := seedChallenge(t, db, &model.VerificationChallenge{
		UserID:        1,
		SourceService: "transaction",
		SourceID:      1,
		Method:        "code_pull",
		Code:          "555555",
		ChallengeData: datatypes.JSON([]byte("{}")),
		Status:        "pending",
		DeviceID:      "device-A",
		ExpiresAt:     time.Now().Add(5 * time.Minute),
		Version:       1,
	})

	_, _, _, err := svc.SubmitVerification(context.Background(), vc.ID, "device-B", "555555")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "different device")
}

func TestSubmitVerification_WrongResponse_PublishesFailedOnLastAttempt(t *testing.T) {
	prod := &stubProducer{}
	svc, db := newServiceWithStubs(t, prod, nil)

	vc := seedChallenge(t, db, &model.VerificationChallenge{
		UserID:        1,
		SourceService: "transaction",
		SourceID:      1,
		Method:        "code_pull",
		Code:          "111000",
		ChallengeData: datatypes.JSON([]byte("{}")),
		Status:        "pending",
		Attempts:      2,
		ExpiresAt:     time.Now().Add(5 * time.Minute),
		Version:       1,
	})

	success, remaining, _, err := svc.SubmitVerification(context.Background(), vc.ID, "device-X", "wrong")
	require.NoError(t, err)
	assert.False(t, success)
	assert.Equal(t, 0, remaining)

	require.Len(t, prod.failedCalls, 1)
}

func TestSubmitVerification_ChallengeNotFound(t *testing.T) {
	svc, _ := newServiceWithStubs(t, &stubProducer{}, nil)
	_, _, _, err := svc.SubmitVerification(context.Background(), 999, "device", "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestSubmitVerification_NumberMatchWrong_RegeneratesChallengeData(t *testing.T) {
	prod := &stubProducer{}
	svc, db := newServiceWithStubs(t, prod, nil)

	original, _ := json.Marshal(map[string]interface{}{
		"target":  42,
		"options": []int{42, 1, 2, 3, 4},
	})
	vc := seedChallenge(t, db, &model.VerificationChallenge{
		UserID:        1,
		SourceService: "transaction",
		SourceID:      1,
		Method:        "number_match",
		Code:          "ignored",
		ChallengeData: datatypes.JSON(original),
		Status:        "pending",
		ExpiresAt:     time.Now().Add(5 * time.Minute),
		Version:       1,
	})

	success, remaining, newData, err := svc.SubmitVerification(context.Background(), vc.ID, "device-1", "1")
	require.NoError(t, err)
	assert.False(t, success)
	assert.Equal(t, 2, remaining)
	assert.NotEmpty(t, newData, "number_match must regenerate challenge data on miss")

	// Sanity-check shape: parses to JSON with options array.
	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(newData), &parsed))
	_, hasOpts := parsed["options"]
	assert.True(t, hasOpts)
	_, hasTarget := parsed["target"]
	assert.False(t, hasTarget, "display data must NOT leak target")
}

// ---------------------------------------------------------------------------
// VerifyByBiometric
// ---------------------------------------------------------------------------

func TestVerifyByBiometric_HappyPath(t *testing.T) {
	prod := &stubProducer{}
	auth := &stubAuthClient{enabled: true}
	svc, db := newServiceWithStubs(t, prod, auth)

	vc := seedChallenge(t, db, &model.VerificationChallenge{
		UserID:        77,
		SourceService: "transaction",
		SourceID:      33,
		Method:        "code_pull",
		Code:          "111222",
		ChallengeData: datatypes.JSON([]byte("{}")),
		Status:        "pending",
		ExpiresAt:     time.Now().Add(5 * time.Minute),
		Version:       1,
	})

	err := svc.VerifyByBiometric(context.Background(), vc.ID, 77, "device-Z")
	require.NoError(t, err)

	var loaded model.VerificationChallenge
	require.NoError(t, db.First(&loaded, vc.ID).Error)
	assert.Equal(t, "verified", loaded.Status)
	assert.Equal(t, "device-Z", loaded.DeviceID)
	require.NotNil(t, loaded.VerifiedAt)

	// Biometric audit field set in challenge data.
	var data map[string]interface{}
	require.NoError(t, json.Unmarshal(loaded.ChallengeData, &data))
	assert.Equal(t, "biometric", data["verified_by"])

	require.Len(t, prod.verifiedCalls, 1)
	assert.Equal(t, 1, auth.callCount)
	assert.Equal(t, "device-Z", auth.lastReq.DeviceId)
}

func TestVerifyByBiometric_OwnershipMismatch(t *testing.T) {
	auth := &stubAuthClient{enabled: true}
	svc, db := newServiceWithStubs(t, &stubProducer{}, auth)

	vc := seedChallenge(t, db, &model.VerificationChallenge{
		UserID:        100,
		SourceService: "transaction",
		SourceID:      1,
		Method:        "code_pull",
		Code:          "x",
		ChallengeData: datatypes.JSON([]byte("{}")),
		Status:        "pending",
		ExpiresAt:     time.Now().Add(5 * time.Minute),
		Version:       1,
	})

	err := svc.VerifyByBiometric(context.Background(), vc.ID, 999, "device-Z")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not belong")
	assert.Equal(t, 0, auth.callCount, "auth should not be queried before ownership check")
}

func TestVerifyByBiometric_BiometricsDisabled(t *testing.T) {
	auth := &stubAuthClient{enabled: false}
	svc, db := newServiceWithStubs(t, &stubProducer{}, auth)

	vc := seedChallenge(t, db, &model.VerificationChallenge{
		UserID:        1,
		SourceService: "transaction",
		SourceID:      1,
		Method:        "code_pull",
		Code:          "x",
		ChallengeData: datatypes.JSON([]byte("{}")),
		Status:        "pending",
		ExpiresAt:     time.Now().Add(5 * time.Minute),
		Version:       1,
	})

	err := svc.VerifyByBiometric(context.Background(), vc.ID, 1, "device-Z")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "biometrics not enabled")
}

func TestVerifyByBiometric_AuthClientError(t *testing.T) {
	auth := &stubAuthClient{err: errors.New("auth-service down")}
	svc, db := newServiceWithStubs(t, &stubProducer{}, auth)

	vc := seedChallenge(t, db, &model.VerificationChallenge{
		UserID:        1,
		SourceService: "transaction",
		SourceID:      1,
		Method:        "code_pull",
		Code:          "x",
		ChallengeData: datatypes.JSON([]byte("{}")),
		Status:        "pending",
		ExpiresAt:     time.Now().Add(5 * time.Minute),
		Version:       1,
	})

	err := svc.VerifyByBiometric(context.Background(), vc.ID, 1, "device-Z")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "biometrics status")
}

func TestVerifyByBiometric_ChallengeNotFound(t *testing.T) {
	svc, _ := newServiceWithStubs(t, &stubProducer{}, &stubAuthClient{enabled: true})
	err := svc.VerifyByBiometric(context.Background(), 999, 1, "device-Z")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestVerifyByBiometric_AlreadyVerifiedRejected(t *testing.T) {
	svc, db := newServiceWithStubs(t, &stubProducer{}, &stubAuthClient{enabled: true})
	vc := seedChallenge(t, db, &model.VerificationChallenge{
		UserID:        1,
		SourceService: "transaction",
		SourceID:      1,
		Method:        "code_pull",
		Code:          "x",
		ChallengeData: datatypes.JSON([]byte("{}")),
		Status:        "verified",
		ExpiresAt:     time.Now().Add(5 * time.Minute),
		Version:       1,
	})

	err := svc.VerifyByBiometric(context.Background(), vc.ID, 1, "device-Z")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "verified")
}

// ---------------------------------------------------------------------------
// Pure helpers
// ---------------------------------------------------------------------------

func TestGenerateCode_SixDigits(t *testing.T) {
	for i := 0; i < 50; i++ {
		c := generateCode()
		assert.Len(t, c, 6, "generateCode must return exactly six characters")
		for _, r := range c {
			assert.True(t, r >= '0' && r <= '9', "generateCode must be digits only, got %q", c)
		}
	}
}

func TestGenerateQRToken_HexLength64(t *testing.T) {
	tok := generateQRToken()
	assert.Len(t, tok, 64)
	// Validate hex chars only.
	for _, r := range tok {
		isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')
		assert.True(t, isHex, "non-hex char %q in token %q", r, tok)
	}
}

func TestGenerateNumberMatchData_FiveOptionsIncludingTarget(t *testing.T) {
	for i := 0; i < 20; i++ {
		target, options := generateNumberMatchData()
		assert.True(t, target >= 1 && target <= 99, "target out of range: %d", target)
		assert.Len(t, options, 5)

		seen := map[int]bool{}
		hasTarget := false
		for _, o := range options {
			assert.True(t, o >= 1 && o <= 99, "option %d out of range", o)
			assert.False(t, seen[o], "duplicate option %d", o)
			seen[o] = true
			if o == target {
				hasTarget = true
			}
		}
		assert.True(t, hasTarget, "options must include target %d, got %v", target, options)
	}
}

func TestBuildChallengeData_CodePull_EmptyMap(t *testing.T) {
	d, err := buildChallengeData("code_pull")
	require.NoError(t, err)
	assert.Empty(t, d)
}

func TestBuildChallengeData_QRScan_HasToken(t *testing.T) {
	d, err := buildChallengeData("qr_scan")
	require.NoError(t, err)
	tok, ok := d["token"].(string)
	assert.True(t, ok)
	assert.Len(t, tok, 64)
}

func TestBuildChallengeData_NumberMatch_HasTargetAndOptions(t *testing.T) {
	d, err := buildChallengeData("number_match")
	require.NoError(t, err)
	target, ok := d["target"].(int)
	assert.True(t, ok)
	assert.True(t, target >= 1 && target <= 99)
	options, ok := d["options"].([]int)
	assert.True(t, ok)
	assert.Len(t, options, 5)
}

func TestBuildChallengeData_UnknownMethod(t *testing.T) {
	_, err := buildChallengeData("voodoo")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown")
}

func TestBuildDisplayData_CodePull_IncludesCode(t *testing.T) {
	d, err := buildDisplayData("code_pull", "424242", map[string]interface{}{})
	require.NoError(t, err)
	assert.Equal(t, "424242", d["code"])
}

func TestBuildDisplayData_QRScan_Empty(t *testing.T) {
	d, err := buildDisplayData("qr_scan", "", map[string]interface{}{"token": "x"})
	require.NoError(t, err)
	assert.Empty(t, d)
}

func TestBuildDisplayData_NumberMatch_OptionsOnly(t *testing.T) {
	d, err := buildDisplayData("number_match", "", map[string]interface{}{
		"target":  42,
		"options": []int{1, 42, 7, 8, 9},
	})
	require.NoError(t, err)
	_, hasTarget := d["target"]
	assert.False(t, hasTarget, "must not leak target into display data")
	opts, ok := d["options"].([]int)
	assert.True(t, ok)
	assert.Len(t, opts, 5)
}

func TestBuildDisplayData_NumberMatch_MissingOptions(t *testing.T) {
	_, err := buildDisplayData("number_match", "", map[string]interface{}{})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "options") ||
		strings.Contains(err.Error(), "missing"))
}

func TestBuildDisplayData_UnknownMethod(t *testing.T) {
	_, err := buildDisplayData("voodoo", "code", nil)
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// Constructor wiring (light smoke check)
// ---------------------------------------------------------------------------

func TestNewVerificationService_ReturnsConfiguredInstance(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	repo := repository.NewVerificationChallengeRepository(db)

	svc := NewVerificationService(repo, nil, db, nil, 7*time.Minute, 5)
	require.NotNil(t, svc)
	assert.Equal(t, 7*time.Minute, svc.challengeExpiry)
	assert.Equal(t, 5, svc.maxAttempts)
}

// checkResponse: cover the "default" branch (unsupported method) and the qr_scan/number_match
// JSON-decode error paths to lift checkResponse to 100%.
func TestCheckResponse_UnknownMethod_ReturnsFalse(t *testing.T) {
	svc, _ := newServiceWithStubs(t, &stubProducer{}, nil)
	vc := &model.VerificationChallenge{Method: "alien"}
	assert.False(t, svc.checkResponse(vc, "anything"))
}

func TestCheckResponse_QRScan_BadJSON(t *testing.T) {
	svc, _ := newServiceWithStubs(t, &stubProducer{}, nil)
	vc := &model.VerificationChallenge{
		Method:        "qr_scan",
		ChallengeData: datatypes.JSON([]byte("not-json")),
	}
	assert.False(t, svc.checkResponse(vc, "x"))
}

func TestCheckResponse_QRScan_TokenWrongType(t *testing.T) {
	svc, _ := newServiceWithStubs(t, &stubProducer{}, nil)
	data, _ := json.Marshal(map[string]interface{}{"token": 123})
	vc := &model.VerificationChallenge{
		Method:        "qr_scan",
		ChallengeData: datatypes.JSON(data),
	}
	assert.False(t, svc.checkResponse(vc, "anything"))
}

func TestCheckResponse_NumberMatch_BadJSON(t *testing.T) {
	svc, _ := newServiceWithStubs(t, &stubProducer{}, nil)
	vc := &model.VerificationChallenge{
		Method:        "number_match",
		ChallengeData: datatypes.JSON([]byte("garbage")),
	}
	assert.False(t, svc.checkResponse(vc, "5"))
}

func TestCheckResponse_NumberMatch_TargetWrongType(t *testing.T) {
	svc, _ := newServiceWithStubs(t, &stubProducer{}, nil)
	data, _ := json.Marshal(map[string]interface{}{"target": "not-a-number"})
	vc := &model.VerificationChallenge{
		Method:        "number_match",
		ChallengeData: datatypes.JSON(data),
	}
	assert.False(t, svc.checkResponse(vc, "5"))
}
