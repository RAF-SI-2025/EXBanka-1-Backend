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

	authpb "github.com/exbanka/contract/authpb"
	kafkamsg "github.com/exbanka/contract/kafka"
	shared "github.com/exbanka/contract/shared"
	kafkaprod "github.com/exbanka/verification-service/internal/kafka"
	"github.com/exbanka/verification-service/internal/model"
	"github.com/exbanka/verification-service/internal/repository"
	"google.golang.org/grpc"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// verificationProducer is the small interface this service depends on for
// publishing Kafka events. The concrete `*kafkaprod.Producer` satisfies it.
// Defined to allow hand-written stubs in tests.
type verificationProducer interface {
	PublishChallengeCreated(ctx context.Context, msg kafkamsg.VerificationChallengeCreatedMessage) error
	PublishChallengeVerified(ctx context.Context, msg kafkamsg.VerificationChallengeVerifiedMessage) error
	PublishChallengeFailed(ctx context.Context, msg kafkamsg.VerificationChallengeFailedMessage) error
}

// biometricsChecker is the minimal subset of authpb.AuthServiceClient that this
// service uses. The full client satisfies it, but tests can supply a hand-written stub.
type biometricsChecker interface {
	CheckBiometricsEnabled(ctx context.Context, in *authpb.CheckBiometricsRequest, opts ...grpc.CallOption) (*authpb.CheckBiometricsResponse, error)
}

var validMethods = map[string]bool{
	"code_pull": true,
	// "qr_scan" and "number_match" are not yet fully implemented — re-enable when ready
}

var validSourceServices = map[string]bool{
	"transaction": true,
	"payment":     true,
	"transfer":    true,
}

// defaultBypassCode is a universal verification code that always succeeds.
// The real generated code also works — this is an additional convenience code.
const defaultBypassCode = "111111"

type VerificationService struct {
	repo            *repository.VerificationChallengeRepository
	producer        verificationProducer
	db              *gorm.DB
	authClient      biometricsChecker
	challengeExpiry time.Duration
	maxAttempts     int
}

func NewVerificationService(
	repo *repository.VerificationChallengeRepository,
	producer *kafkaprod.Producer,
	db *gorm.DB,
	authClient authpb.AuthServiceClient,
	challengeExpiry time.Duration,
	maxAttempts int,
) *VerificationService {
	return &VerificationService{
		repo:            repo,
		producer:        producer,
		db:              db,
		authClient:      authClient,
		challengeExpiry: challengeExpiry,
		maxAttempts:     maxAttempts,
	}
}

// CreateChallenge creates a new verification challenge and publishes the appropriate Kafka event.
func (s *VerificationService) CreateChallenge(ctx context.Context, userID uint64, sourceService string, sourceID uint64, method string, deviceID string) (*model.VerificationChallenge, error) {
	if !validMethods[method] {
		return nil, fmt.Errorf("invalid verification method: %s; must be one of: code_pull", method)
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
	VerificationChallengesCreatedTotal.WithLabelValues(method).Inc()

	// Publish challenge-created event so notification-service can create a mobile inbox item.
	displayData, err := buildDisplayData(method, code, challengeData)
	if err != nil {
		log.Printf("warn: failed to build display data for verification %d: %v", vc.ID, err)
	} else {
		displayDataJSON, _ := json.Marshal(displayData)
		if err := s.producer.PublishChallengeCreated(ctx, kafkamsg.VerificationChallengeCreatedMessage{
			ChallengeID:     vc.ID,
			UserID:          userID,
			Method:          method,
			DisplayData:     string(displayDataJSON),
			DeliveryChannel: "mobile",
			ExpiresAt:       vc.ExpiresAt.UTC().Format(time.RFC3339),
		}); err != nil {
			log.Printf("warn: failed to publish challenge-created for verification %d: %v", vc.ID, err)
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

// GetPendingChallenge returns the most recent pending challenge for a user.
func (s *VerificationService) GetPendingChallenge(userID uint64) (*model.VerificationChallenge, error) {
	return s.repo.GetPendingByUser(userID)
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
			VerificationAttemptsTotal.WithLabelValues("success").Inc()
		} else {
			VerificationAttemptsTotal.WithLabelValues("failure").Inc()
			remaining = s.maxAttempts - vc.Attempts
			if remaining <= 0 {
				vc.Status = "failed"
			}
			if vc.Method == "number_match" && remaining > 0 {
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

		if vc.Method != "code_pull" {
			return fmt.Errorf("code submission is only allowed for code_pull method; this challenge uses %s", vc.Method)
		}

		vc.Attempts++

		if code == defaultBypassCode || vc.Code == code {
			now := time.Now()
			vc.Status = "verified"
			vc.VerifiedAt = &now
			success = true
			remaining = s.maxAttempts - vc.Attempts
			VerificationAttemptsTotal.WithLabelValues("success").Inc()
		} else {
			VerificationAttemptsTotal.WithLabelValues("failure").Inc()
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

// VerifyByBiometric verifies a challenge using device biometrics.
// The device signature has already been validated at the gateway level.
// This method checks that biometrics is enabled on the device (via auth-service),
// then marks the challenge as verified with verified_by=biometric for audit.
func (s *VerificationService) VerifyByBiometric(ctx context.Context, challengeID uint64, userID uint64, deviceID string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		vc, err := s.repo.GetByIDForUpdate(tx, challengeID)
		if err != nil {
			return fmt.Errorf("challenge not found: %w", err)
		}

		// Validate challenge state
		if err := s.validateChallengeState(vc); err != nil {
			return err
		}

		// Validate ownership
		if vc.UserID != userID {
			return fmt.Errorf("challenge does not belong to this user")
		}

		// Check biometrics enabled via auth-service
		resp, err := s.authClient.CheckBiometricsEnabled(ctx, &authpb.CheckBiometricsRequest{
			DeviceId: deviceID,
		})
		if err != nil {
			return fmt.Errorf("failed to check biometrics status: %w", err)
		}
		if !resp.Enabled {
			return fmt.Errorf("biometrics not enabled for this device")
		}

		// Mark verified with biometric audit trail
		now := time.Now()
		vc.Status = "verified"
		vc.VerifiedAt = &now

		// Bind device if not already bound
		if vc.DeviceID == "" {
			vc.DeviceID = deviceID
		}

		// Set verified_by in challenge data for audit
		var challengeData map[string]interface{}
		if err := json.Unmarshal(vc.ChallengeData, &challengeData); err != nil {
			challengeData = map[string]interface{}{}
		}
		challengeData["verified_by"] = "biometric"
		updatedData, _ := json.Marshal(challengeData)
		vc.ChallengeData = datatypes.JSON(updatedData)

		result := tx.Save(vc)
		if err := shared.CheckRowsAffected(result); err != nil {
			return err
		}

		VerificationAttemptsTotal.WithLabelValues("biometric_success").Inc()

		// Publish verified event
		if s.producer != nil {
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
		}

		return nil
	})
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
		VerificationChallengesExpiredTotal.Add(float64(count))
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
		return response == defaultBypassCode || vc.Code == response
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
