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
	case strings.Contains(msg, "bound to a different device"),
		strings.Contains(msg, "biometrics not enabled"),
		strings.Contains(msg, "does not belong to this user"):
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

func (h *VerificationGRPCHandler) VerifyByBiometric(ctx context.Context, req *pb.VerifyByBiometricRequest) (*pb.VerifyByBiometricResponse, error) {
	err := h.svc.VerifyByBiometric(ctx, req.GetChallengeId(), req.GetUserId(), req.GetDeviceId())
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "%v", err)
	}
	return &pb.VerifyByBiometricResponse{Success: true}, nil
}
