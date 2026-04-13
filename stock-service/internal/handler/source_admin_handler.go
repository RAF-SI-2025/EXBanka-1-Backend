package handler

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/service"
	"github.com/exbanka/stock-service/internal/source"
)

// SourceFactory builds a Source from its canonical name.
// The main wiring injects a concrete factory that constructs the right
// Source implementation (and for "simulator" handles self-registration).
type SourceFactory func(name string) (source.Source, error)

// SourceAdminHandler implements the SourceAdminService gRPC server.
type SourceAdminHandler struct {
	pb.UnimplementedSourceAdminServiceServer
	svc     service.SwitchableSyncService
	factory SourceFactory
}

// NewSourceAdminHandler constructs a SourceAdminHandler.
func NewSourceAdminHandler(svc service.SwitchableSyncService, factory SourceFactory) *SourceAdminHandler {
	return &SourceAdminHandler{svc: svc, factory: factory}
}

// SwitchSource validates the requested source name, builds the source via the
// factory, delegates to the sync service, and returns the current status.
func (h *SourceAdminHandler) SwitchSource(ctx context.Context, req *pb.SwitchSourceRequest) (*pb.SwitchSourceResponse, error) {
	switch req.Source {
	case "external", "generated", "simulator":
		// valid
	default:
		return nil, status.Errorf(codes.InvalidArgument, "unknown source %q: must be one of external, generated, simulator", req.Source)
	}

	newSrc, err := h.factory(req.Source)
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "build source: %v", err)
	}

	if err := h.svc.SwitchSource(ctx, newSrc); err != nil {
		return nil, status.Errorf(codes.Internal, "switch: %v", err)
	}

	return &pb.SwitchSourceResponse{Status: h.currentStatus()}, nil
}

// GetSourceStatus returns the current source name and switch status.
func (h *SourceAdminHandler) GetSourceStatus(_ context.Context, _ *pb.GetSourceStatusRequest) (*pb.SourceStatus, error) {
	return h.currentStatus(), nil
}

func (h *SourceAdminHandler) currentStatus() *pb.SourceStatus {
	statusStr, lastErr, startedAt, name := h.svc.GetStatus()
	return &pb.SourceStatus{
		Source:    name,
		Status:    statusStr,
		StartedAt: startedAt.UTC().Format("2006-01-02T15:04:05Z"),
		LastError: lastErr,
	}
}
