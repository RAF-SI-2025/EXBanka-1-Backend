package cronreg

import (
	"context"
	"errors"
	"time"

	adminpb "github.com/exbanka/contract/adminpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GRPCServer implements the AdminCron gRPC service using a Registry.
// Register it alongside the service's own gRPC server in cmd/main.go:
//
//	adminpb.RegisterAdminCronServer(s, cronreg.NewGRPCServer(cronRegistry))
type GRPCServer struct {
	adminpb.UnimplementedAdminCronServer
	r *Registry
}

// NewGRPCServer creates a GRPCServer backed by the given Registry.
func NewGRPCServer(r *Registry) *GRPCServer { return &GRPCServer{r: r} }

// ListCrons returns a snapshot of all registered crons in this service.
func (s *GRPCServer) ListCrons(_ context.Context, _ *adminpb.ListCronsRequest) (*adminpb.ListCronsResponse, error) {
	infos := s.r.List()
	resp := &adminpb.ListCronsResponse{Crons: make([]*adminpb.CronInfoMsg, 0, len(infos))}
	for _, i := range infos {
		resp.Crons = append(resp.Crons, toMsg(i))
	}
	return resp, nil
}

// GetCron returns the current snapshot for a single named cron.
func (s *GRPCServer) GetCron(_ context.Context, req *adminpb.GetCronRequest) (*adminpb.CronInfoMsg, error) {
	info, err := s.r.Get(req.Name)
	if err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return toMsg(info), nil
}

// TriggerCron enqueues a one-shot run. Returns FailedPrecondition if the cron
// is paused and force is false.
func (s *GRPCServer) TriggerCron(_ context.Context, req *adminpb.TriggerRequest) (*adminpb.CronCtrlResponse, error) {
	if err := s.r.Trigger(req.Name, req.Force, req.TriggeredBy); err != nil {
		if errors.Is(err, ErrCronNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		if errors.Is(err, ErrCronPaused) {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &adminpb.CronCtrlResponse{Status: "triggered"}, nil
}

// PauseCron marks the cron as paused.
func (s *GRPCServer) PauseCron(_ context.Context, req *adminpb.PauseRequest) (*adminpb.CronCtrlResponse, error) {
	if err := s.r.Pause(req.Name, req.PausedBy); err != nil {
		if errors.Is(err, ErrCronNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &adminpb.CronCtrlResponse{Status: "paused"}, nil
}

// ResumeCron marks the cron as active again.
func (s *GRPCServer) ResumeCron(_ context.Context, req *adminpb.ResumeRequest) (*adminpb.CronCtrlResponse, error) {
	if err := s.r.Resume(req.Name); err != nil {
		if errors.Is(err, ErrCronNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &adminpb.CronCtrlResponse{Status: "resumed"}, nil
}

func toMsg(i CronInfo) *adminpb.CronInfoMsg {
	return &adminpb.CronInfoMsg{
		Name:             i.Name,
		Service:          i.Service,
		Description:      i.Description,
		Interval:         i.Interval.String(),
		CronExpression:   i.CronExpression,
		LastStartedAt:    tFmt(i.LastStartedAt),
		LastFinishedAt:   tFmt(i.LastFinishedAt),
		LastError:        i.LastError,
		NextScheduledAt:  i.NextScheduledAt.UTC().Format(time.RFC3339),
		IsPaused:         i.IsPaused,
		PausedByEmployee: i.PausedByEmployee,
		PausedAt:         tFmt(i.PausedAt),
		RunCount:         i.RunCount,
		ErrorCount:       i.ErrorCount,
	}
}

func tFmt(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
