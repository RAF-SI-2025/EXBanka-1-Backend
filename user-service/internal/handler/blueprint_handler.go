package handler

import (
	"context"
	"encoding/json"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/datatypes"

	"github.com/exbanka/contract/changelog"
	pb "github.com/exbanka/contract/userpb"
	"github.com/exbanka/user-service/internal/model"
	"github.com/exbanka/user-service/internal/service"
)

type BlueprintGRPCHandler struct {
	pb.UnimplementedBlueprintServiceServer
	svc *service.BlueprintService
}

func NewBlueprintGRPCHandler(svc *service.BlueprintService) *BlueprintGRPCHandler {
	return &BlueprintGRPCHandler{svc: svc}
}

func (h *BlueprintGRPCHandler) CreateBlueprint(ctx context.Context, req *pb.CreateBlueprintRequest) (*pb.BlueprintResponse, error) {
	bp := model.LimitBlueprint{
		Name:        req.Name,
		Description: req.Description,
		Type:        req.Type,
		Values:      datatypes.JSON(req.ValuesJson),
	}
	result, err := h.svc.CreateBlueprint(ctx, bp)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to create blueprint: %v", err)
	}
	return toBlueprintResponse(result), nil
}

func (h *BlueprintGRPCHandler) GetBlueprint(ctx context.Context, req *pb.GetBlueprintRequest) (*pb.BlueprintResponse, error) {
	result, err := h.svc.GetBlueprint(req.Id)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "blueprint not found")
	}
	return toBlueprintResponse(result), nil
}

func (h *BlueprintGRPCHandler) ListBlueprints(ctx context.Context, req *pb.ListBlueprintsRequest) (*pb.ListBlueprintsResponse, error) {
	blueprints, err := h.svc.ListBlueprints(req.Type)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to list blueprints: %v", err)
	}
	resp := &pb.ListBlueprintsResponse{
		Blueprints: make([]*pb.BlueprintResponse, 0, len(blueprints)),
	}
	for _, bp := range blueprints {
		bp := bp
		resp.Blueprints = append(resp.Blueprints, toBlueprintResponse(&bp))
	}
	return resp, nil
}

func (h *BlueprintGRPCHandler) UpdateBlueprint(ctx context.Context, req *pb.UpdateBlueprintRequest) (*pb.BlueprintResponse, error) {
	var values json.RawMessage
	if req.ValuesJson != "" {
		values = json.RawMessage(req.ValuesJson)
	}
	result, err := h.svc.UpdateBlueprint(ctx, req.Id, req.Name, req.Description, values)
	if err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to update blueprint: %v", err)
	}
	return toBlueprintResponse(result), nil
}

func (h *BlueprintGRPCHandler) DeleteBlueprint(ctx context.Context, req *pb.DeleteBlueprintRequest) (*pb.DeleteBlueprintResponse, error) {
	if err := h.svc.DeleteBlueprint(ctx, req.Id); err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to delete blueprint: %v", err)
	}
	return &pb.DeleteBlueprintResponse{}, nil
}

func (h *BlueprintGRPCHandler) ApplyBlueprint(ctx context.Context, req *pb.ApplyBlueprintRequest) (*pb.ApplyBlueprintResponse, error) {
	appliedBy := changelog.ExtractChangedBy(ctx)
	if err := h.svc.ApplyBlueprint(ctx, req.BlueprintId, req.TargetId, appliedBy); err != nil {
		return nil, status.Errorf(mapServiceError(err), "failed to apply blueprint: %v", err)
	}
	return &pb.ApplyBlueprintResponse{}, nil
}

func toBlueprintResponse(bp *model.LimitBlueprint) *pb.BlueprintResponse {
	return &pb.BlueprintResponse{
		Id:          bp.ID,
		Name:        bp.Name,
		Description: bp.Description,
		Type:        bp.Type,
		ValuesJson:  string(bp.Values),
		CreatedAt:   bp.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:   bp.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

// Suppress unused import warning for codes package.
var _ = codes.OK
