package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/exbanka/verification-service/internal/model"
	"github.com/exbanka/verification-service/internal/repository"
)

// setupTestVerificationService creates an in-memory SQLite database, auto-migrates
// the VerificationChallenge table, and returns a VerificationService with nil producer.
func setupTestVerificationService(t *testing.T) (*VerificationService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&model.VerificationChallenge{}))

	repo := repository.NewVerificationChallengeRepository(db)
	svc := &VerificationService{
		repo:            repo,
		producer:        nil,
		db:              db,
		challengeExpiry: 5 * time.Minute,
		maxAttempts:     3,
	}
	return svc, db
}

// seedChallenge inserts a VerificationChallenge directly into the DB and returns it.
func seedChallenge(t *testing.T, db *gorm.DB, vc *model.VerificationChallenge) *model.VerificationChallenge {
	t.Helper()
	require.NoError(t, db.Create(vc).Error)
	return vc
}

// ---------------------------------------------------------------------------
// validateChallengeState tests
// ---------------------------------------------------------------------------

func TestValidateChallengeState_PendingNotExpiredZeroAttempts(t *testing.T) {
	svc, _ := setupTestVerificationService(t)
	vc := &model.VerificationChallenge{
		Status:    "pending",
		ExpiresAt: time.Now().Add(5 * time.Minute),
		Attempts:  0,
	}
	err := svc.validateChallengeState(vc)
	assert.NoError(t, err)
}

func TestValidateChallengeState_Expired(t *testing.T) {
	svc, _ := setupTestVerificationService(t)
	vc := &model.VerificationChallenge{
		Status:    "pending",
		ExpiresAt: time.Now().Add(-1 * time.Minute),
		Attempts:  0,
	}
	err := svc.validateChallengeState(vc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expired")
}

func TestValidateChallengeState_MaxAttemptsReached(t *testing.T) {
	svc, _ := setupTestVerificationService(t)
	vc := &model.VerificationChallenge{
		Status:    "pending",
		ExpiresAt: time.Now().Add(5 * time.Minute),
		Attempts:  3,
	}
	err := svc.validateChallengeState(vc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "attempts")
}

func TestValidateChallengeState_AlreadyVerified(t *testing.T) {
	svc, _ := setupTestVerificationService(t)
	vc := &model.VerificationChallenge{
		Status:    "verified",
		ExpiresAt: time.Now().Add(5 * time.Minute),
		Attempts:  1,
	}
	err := svc.validateChallengeState(vc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "verified")
}

func TestValidateChallengeState_StatusFailed(t *testing.T) {
	svc, _ := setupTestVerificationService(t)
	vc := &model.VerificationChallenge{
		Status:    "failed",
		ExpiresAt: time.Now().Add(5 * time.Minute),
		Attempts:  3,
	}
	err := svc.validateChallengeState(vc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed")
}

// ---------------------------------------------------------------------------
// checkResponse tests
// ---------------------------------------------------------------------------

func TestCheckResponse_CodePull_CorrectCode(t *testing.T) {
	svc, _ := setupTestVerificationService(t)
	vc := &model.VerificationChallenge{
		Method: "code_pull",
		Code:   "482957",
	}
	assert.True(t, svc.checkResponse(vc, "482957"))
}

func TestCheckResponse_CodePull_WrongCode(t *testing.T) {
	svc, _ := setupTestVerificationService(t)
	vc := &model.VerificationChallenge{
		Method: "code_pull",
		Code:   "482957",
	}
	assert.False(t, svc.checkResponse(vc, "999999"))
}

func TestCheckResponse_CodePull_BypassCode(t *testing.T) {
	svc, _ := setupTestVerificationService(t)
	vc := &model.VerificationChallenge{
		Method: "code_pull",
		Code:   "482957",
	}
	assert.True(t, svc.checkResponse(vc, "111111"))
}

func TestCheckResponse_QRScan_CorrectToken(t *testing.T) {
	svc, _ := setupTestVerificationService(t)
	token := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	data, _ := json.Marshal(map[string]interface{}{"token": token})
	vc := &model.VerificationChallenge{
		Method:        "qr_scan",
		ChallengeData: datatypes.JSON(data),
	}
	assert.True(t, svc.checkResponse(vc, token))
}

func TestCheckResponse_QRScan_WrongToken(t *testing.T) {
	svc, _ := setupTestVerificationService(t)
	token := "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	data, _ := json.Marshal(map[string]interface{}{"token": token})
	vc := &model.VerificationChallenge{
		Method:        "qr_scan",
		ChallengeData: datatypes.JSON(data),
	}
	assert.False(t, svc.checkResponse(vc, "wrong_token"))
}

func TestCheckResponse_NumberMatch_CorrectTarget(t *testing.T) {
	svc, _ := setupTestVerificationService(t)
	data, _ := json.Marshal(map[string]interface{}{
		"target":  42,
		"options": []int{12, 42, 67, 88, 3},
	})
	vc := &model.VerificationChallenge{
		Method:        "number_match",
		ChallengeData: datatypes.JSON(data),
	}
	assert.True(t, svc.checkResponse(vc, "42"))
}

func TestCheckResponse_NumberMatch_WrongTarget(t *testing.T) {
	svc, _ := setupTestVerificationService(t)
	data, _ := json.Marshal(map[string]interface{}{
		"target":  42,
		"options": []int{12, 42, 67, 88, 3},
	})
	vc := &model.VerificationChallenge{
		Method:        "number_match",
		ChallengeData: datatypes.JSON(data),
	}
	assert.False(t, svc.checkResponse(vc, "67"))
}

// ---------------------------------------------------------------------------
// validMethods / validSourceServices tests
// ---------------------------------------------------------------------------

func TestValidMethods_CodePullEnabled(t *testing.T) {
	assert.True(t, validMethods["code_pull"])
}

func TestValidMethods_EmailDisabled(t *testing.T) {
	assert.False(t, validMethods["email"])
}

func TestValidMethods_QRScanDisabled(t *testing.T) {
	assert.False(t, validMethods["qr_scan"])
}

func TestValidMethods_NumberMatchDisabled(t *testing.T) {
	assert.False(t, validMethods["number_match"])
}

func TestValidSourceServices(t *testing.T) {
	assert.True(t, validSourceServices["transaction"])
	assert.True(t, validSourceServices["payment"])
	assert.True(t, validSourceServices["transfer"])
	assert.False(t, validSourceServices["unknown"])
}

// ---------------------------------------------------------------------------
// DB-backed tests: ExpireOldChallenges
// ---------------------------------------------------------------------------

func TestExpireOldChallenges_MarksOnlyExpired(t *testing.T) {
	svc, db := setupTestVerificationService(t)

	// Challenge that should be expired (past expiry)
	expired := seedChallenge(t, db, &model.VerificationChallenge{
		UserID:        1,
		SourceService: "transaction",
		SourceID:      100,
		Method:        "code_pull",
		Code:          "123456",
		ChallengeData: datatypes.JSON([]byte("{}")),
		Status:        "pending",
		Attempts:      0,
		ExpiresAt:     time.Now().Add(-10 * time.Minute),
		Version:       1,
	})

	// Challenge that should NOT be expired (still valid)
	notExpired := seedChallenge(t, db, &model.VerificationChallenge{
		UserID:        2,
		SourceService: "payment",
		SourceID:      200,
		Method:        "code_pull",
		Code:          "654321",
		ChallengeData: datatypes.JSON([]byte("{}")),
		Status:        "pending",
		Attempts:      0,
		ExpiresAt:     time.Now().Add(5 * time.Minute),
		Version:       1,
	})

	// Already verified — should not be touched
	alreadyVerified := seedChallenge(t, db, &model.VerificationChallenge{
		UserID:        3,
		SourceService: "transfer",
		SourceID:      300,
		Method:        "code_pull",
		Code:          "111222",
		ChallengeData: datatypes.JSON([]byte("{}")),
		Status:        "verified",
		Attempts:      1,
		ExpiresAt:     time.Now().Add(-5 * time.Minute),
		Version:       1,
	})

	svc.ExpireOldChallenges(context.Background())

	// Verify the expired one was updated
	var r1 model.VerificationChallenge
	require.NoError(t, db.Where("id = ?", expired.ID).First(&r1).Error)
	assert.Equal(t, "expired", r1.Status)

	// Verify the not-expired one was untouched
	var r2 model.VerificationChallenge
	require.NoError(t, db.Where("id = ?", notExpired.ID).First(&r2).Error)
	assert.Equal(t, "pending", r2.Status)

	// Verify the already-verified one was untouched
	var r3 model.VerificationChallenge
	require.NoError(t, db.Where("id = ?", alreadyVerified.ID).First(&r3).Error)
	assert.Equal(t, "verified", r3.Status)
}

// ---------------------------------------------------------------------------
// DB-backed tests: GetChallengeStatus
// ---------------------------------------------------------------------------

func TestGetChallengeStatus_Found(t *testing.T) {
	svc, db := setupTestVerificationService(t)

	created := seedChallenge(t, db, &model.VerificationChallenge{
		UserID:        10,
		SourceService: "transaction",
		SourceID:      500,
		Method:        "code_pull",
		Code:          "987654",
		ChallengeData: datatypes.JSON([]byte("{}")),
		Status:        "pending",
		Attempts:      1,
		ExpiresAt:     time.Now().Add(5 * time.Minute),
		Version:       1,
	})

	vc, err := svc.GetChallengeStatus(created.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, vc.ID)
	assert.Equal(t, uint64(10), vc.UserID)
	assert.Equal(t, "transaction", vc.SourceService)
	assert.Equal(t, uint64(500), vc.SourceID)
	assert.Equal(t, "pending", vc.Status)
	assert.Equal(t, 1, vc.Attempts)
}

func TestGetChallengeStatus_NotFound(t *testing.T) {
	svc, _ := setupTestVerificationService(t)

	_, err := svc.GetChallengeStatus(999999)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// ---------------------------------------------------------------------------
// DB-backed tests: GetPendingChallenge
// ---------------------------------------------------------------------------

func TestGetPendingChallenge_ReturnsPendingForUser(t *testing.T) {
	svc, db := setupTestVerificationService(t)

	created := seedChallenge(t, db, &model.VerificationChallenge{
		UserID:        20,
		SourceService: "payment",
		SourceID:      600,
		Method:        "code_pull",
		Code:          "112233",
		ChallengeData: datatypes.JSON([]byte("{}")),
		Status:        "pending",
		Attempts:      0,
		ExpiresAt:     time.Now().Add(5 * time.Minute),
		Version:       1,
	})

	vc, err := svc.GetPendingChallenge(20)
	require.NoError(t, err)
	assert.Equal(t, created.ID, vc.ID)
	assert.Equal(t, "pending", vc.Status)
}

func TestGetPendingChallenge_NotFoundWhenNoPending(t *testing.T) {
	svc, _ := setupTestVerificationService(t)

	_, err := svc.GetPendingChallenge(99999)
	require.Error(t, err)
}
