package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	pb "github.com/exbanka/contract/verificationpb"
	"github.com/exbanka/verification-service/internal/model"
	"github.com/exbanka/verification-service/internal/service"
)

// verificationService is the minimal subset of *service.VerificationService that
// this handler depends on. Defined to allow hand-written stubs in tests; the
// concrete service satisfies it.
type verificationService interface {
	CreateChallenge(ctx context.Context, userID uint64, sourceService string, sourceID uint64, method string, deviceID string) (*model.VerificationChallenge, error)
	GetChallengeStatus(challengeID uint64) (*model.VerificationChallenge, error)
	GetPendingChallenge(userID uint64) (*model.VerificationChallenge, error)
	SubmitVerification(ctx context.Context, challengeID uint64, deviceID string, response string) (bool, int, string, error)
	SubmitCode(ctx context.Context, challengeID uint64, code string) (bool, int, error)
	VerifyByBiometric(ctx context.Context, challengeID uint64, userID uint64, deviceID string) error
}

type VerificationGRPCHandler struct {
	pb.UnimplementedVerificationGRPCServiceServer
	svc verificationService
}

func NewVerificationGRPCHandler(svc *service.VerificationService) *VerificationGRPCHandler {
	return &VerificationGRPCHandler{svc: svc}
}

func (h *VerificationGRPCHandler) CreateChallenge(ctx context.Context, req *pb.CreateChallengeRequest) (*pb.CreateChallengeResponse, error) {
	vc, err := h.svc.CreateChallenge(ctx, req.GetUserId(), req.GetSourceService(), req.GetSourceId(), req.GetMethod(), req.GetDeviceId())
	if err != nil {
		return nil, err
	}

	// Build response challenge_data based on method.
	// For the browser: code_pull returns {}, qr_scan returns {"token":..., "verify_url":...},
	// number_match returns {"target":...}.
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
		return nil, err
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
	vc, err := h.svc.GetPendingChallenge(req.GetUserId())
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
		return nil, err
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
		return nil, err
	}

	return &pb.SubmitCodeResponse{
		Success:           success,
		RemainingAttempts: int32(remaining),
	}, nil
}

func (h *VerificationGRPCHandler) VerifyByBiometric(ctx context.Context, req *pb.VerifyByBiometricRequest) (*pb.VerifyByBiometricResponse, error) {
	err := h.svc.VerifyByBiometric(ctx, req.GetChallengeId(), req.GetUserId(), req.GetDeviceId())
	if err != nil {
		return nil, err
	}
	return &pb.VerifyByBiometricResponse{Success: true}, nil
}
