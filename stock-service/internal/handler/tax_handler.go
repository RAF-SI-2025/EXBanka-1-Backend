package handler

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/service"
)

type TaxHandler struct {
	pb.UnimplementedTaxGRPCServiceServer
	taxSvc *service.TaxService
}

func NewTaxHandler(taxSvc *service.TaxService) *TaxHandler {
	return &TaxHandler{taxSvc: taxSvc}
}

func (h *TaxHandler) ListTaxRecords(ctx context.Context, req *pb.ListTaxRecordsRequest) (*pb.ListTaxRecordsResponse, error) {
	now := time.Now()
	filter := service.TaxFilter{
		UserType: req.UserType,
		Search:   req.Search,
		Page:     int(req.Page),
		PageSize: int(req.PageSize),
	}

	summaries, total, err := h.taxSvc.ListTaxRecords(now.Year(), int(now.Month()), filter)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	records := make([]*pb.TaxRecord, len(summaries))
	for i, s := range summaries {
		lastCollection := ""
		if s.LastCollection != nil {
			lastCollection = s.LastCollection.Format("2006-01-02T15:04:05Z")
		}
		records[i] = &pb.TaxRecord{
			UserId:         s.UserID,
			UserType:       s.SystemType,
			FirstName:      s.UserFirstName,
			LastName:       s.UserLastName,
			TotalDebtRsd:   s.TotalDebtRSD.StringFixed(2),
			LastCollection: lastCollection,
		}
	}

	return &pb.ListTaxRecordsResponse{
		TaxRecords: records,
		TotalCount: total,
	}, nil
}

func (h *TaxHandler) ListUserTaxRecords(ctx context.Context, req *pb.ListUserTaxRecordsRequest) (*pb.ListUserTaxRecordsResponse, error) {
	page := int(req.Page)
	pageSize := int(req.PageSize)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}

	gains, total, err := h.taxSvc.ListUserTaxRecords(req.UserId, req.SystemType, page, pageSize)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	records := make([]*pb.UserTaxRecord, len(gains))
	for i, g := range gains {
		records[i] = &pb.UserTaxRecord{
			Id:               g.ID,
			SecurityType:     g.SecurityType,
			Ticker:           g.Ticker,
			Quantity:         g.Quantity,
			BuyPricePerUnit:  g.BuyPricePerUnit.StringFixed(4),
			SellPricePerUnit: g.SellPricePerUnit.StringFixed(4),
			TotalGain:        g.TotalGain.StringFixed(4),
			Currency:         g.Currency,
			TaxYear:          int32(g.TaxYear),
			TaxMonth:         int32(g.TaxMonth),
			CreatedAt:        g.CreatedAt.Format("2006-01-02T15:04:05Z"),
		}
	}

	// Fetch balance summary
	taxPaid, taxUnpaid, balErr := h.taxSvc.GetUserTaxSummary(req.UserId, req.SystemType)
	if balErr != nil {
		return nil, status.Error(codes.Internal, balErr.Error())
	}

	return &pb.ListUserTaxRecordsResponse{
		Records:            records,
		TotalCount:         total,
		TaxPaidThisYear:    taxPaid.StringFixed(2),
		TaxUnpaidThisMonth: taxUnpaid.StringFixed(2),
	}, nil
}

func (h *TaxHandler) CollectTax(ctx context.Context, req *pb.CollectTaxRequest) (*pb.CollectTaxResponse, error) {
	now := time.Now()
	collected, totalRSD, failed, err := h.taxSvc.CollectTax(now.Year(), int(now.Month()))
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &pb.CollectTaxResponse{
		CollectedCount:    collected,
		TotalCollectedRsd: totalRSD.StringFixed(2),
		FailedCount:       failed,
	}, nil
}
