package handler

import (
	"context"
	"errors"

	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/transaction-service/internal/model"
	"github.com/exbanka/transaction-service/internal/repository"
)

// PeerBankAdminGRPCHandler implements transactionpb.PeerBankAdminServiceServer.
// Backs the api-gateway /api/v3/peer-banks admin routes (gated upstream by
// the peer_banks.manage.any permission).
type PeerBankAdminGRPCHandler struct {
	transactionpb.UnimplementedPeerBankAdminServiceServer
	repo *repository.PeerBankRepository
}

func NewPeerBankAdminGRPCHandler(repo *repository.PeerBankRepository) *PeerBankAdminGRPCHandler {
	return &PeerBankAdminGRPCHandler{repo: repo}
}

func (h *PeerBankAdminGRPCHandler) ListPeerBanks(ctx context.Context, req *transactionpb.ListPeerBanksRequest) (*transactionpb.ListPeerBanksResponse, error) {
	rows, err := h.repo.List(req.GetActiveOnly())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list peer banks: %v", err)
	}
	out := make([]*transactionpb.PeerBank, 0, len(rows))
	for i := range rows {
		out = append(out, peerBankToProto(&rows[i]))
	}
	return &transactionpb.ListPeerBanksResponse{PeerBanks: out}, nil
}

func (h *PeerBankAdminGRPCHandler) GetPeerBank(ctx context.Context, req *transactionpb.GetPeerBankRequest) (*transactionpb.PeerBank, error) {
	row, err := h.repo.GetByID(req.GetId())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "peer bank not found")
		}
		return nil, status.Errorf(codes.Internal, "get peer bank: %v", err)
	}
	return peerBankToProto(row), nil
}

func (h *PeerBankAdminGRPCHandler) CreatePeerBank(ctx context.Context, req *transactionpb.CreatePeerBankRequest) (*transactionpb.PeerBank, error) {
	if req.GetBankCode() == "" || req.GetRoutingNumber() == 0 || req.GetBaseUrl() == "" || req.GetApiToken() == "" {
		return nil, status.Error(codes.InvalidArgument, "bank_code, routing_number, base_url, api_token are required")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.GetApiToken()), bcrypt.DefaultCost)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "bcrypt: %v", err)
	}
	row := &model.PeerBank{
		BankCode:          req.GetBankCode(),
		RoutingNumber:     req.GetRoutingNumber(),
		BaseURL:           req.GetBaseUrl(),
		APITokenBcrypt:    string(hash),
		APITokenPlaintext: req.GetApiToken(),
		HMACInboundKey:    req.GetHmacInboundKey(),
		HMACOutboundKey:   req.GetHmacOutboundKey(),
		Active:            req.GetActive(),
	}
	if err := h.repo.Create(row); err != nil {
		return nil, status.Errorf(codes.Internal, "create peer bank: %v", err)
	}
	return peerBankToProto(row), nil
}

func (h *PeerBankAdminGRPCHandler) UpdatePeerBank(ctx context.Context, req *transactionpb.UpdatePeerBankRequest) (*transactionpb.PeerBank, error) {
	row, err := h.repo.GetByID(req.GetId())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "peer bank not found")
		}
		return nil, status.Errorf(codes.Internal, "get: %v", err)
	}
	if req.GetBaseUrlSet() {
		row.BaseURL = req.GetBaseUrl()
	}
	if req.GetApiTokenSet() && req.GetApiToken() != "" {
		hash, hErr := bcrypt.GenerateFromPassword([]byte(req.GetApiToken()), bcrypt.DefaultCost)
		if hErr != nil {
			return nil, status.Errorf(codes.Internal, "bcrypt: %v", hErr)
		}
		row.APITokenBcrypt = string(hash)
		row.APITokenPlaintext = req.GetApiToken()
	}
	if req.GetHmacInboundKeySet() {
		row.HMACInboundKey = req.GetHmacInboundKey()
	}
	if req.GetHmacOutboundKeySet() {
		row.HMACOutboundKey = req.GetHmacOutboundKey()
	}
	if req.GetActiveSet() {
		row.Active = req.GetActive()
	}
	if err := h.repo.Update(row); err != nil {
		return nil, status.Errorf(codes.Internal, "update peer bank: %v", err)
	}
	return peerBankToProto(row), nil
}

func (h *PeerBankAdminGRPCHandler) DeletePeerBank(ctx context.Context, req *transactionpb.DeletePeerBankRequest) (*transactionpb.DeletePeerBankResponse, error) {
	if err := h.repo.Delete(req.GetId()); err != nil {
		return nil, status.Errorf(codes.Internal, "delete peer bank: %v", err)
	}
	return &transactionpb.DeletePeerBankResponse{}, nil
}

func peerBankToProto(row *model.PeerBank) *transactionpb.PeerBank {
	return &transactionpb.PeerBank{
		Id:              row.ID,
		BankCode:        row.BankCode,
		RoutingNumber:   row.RoutingNumber,
		BaseUrl:         row.BaseURL,
		ApiTokenPreview: tokenPreview(row.APITokenPlaintext),
		HmacEnabled:     row.HMACInboundKey != "" && row.HMACOutboundKey != "",
		Active:          row.Active,
		CreatedAt:       row.CreatedAt.Unix(),
		UpdatedAt:       row.UpdatedAt.Unix(),
	}
}

// tokenPreview returns the last 4 chars (or fewer if the token is short).
// The full token is never returned over the wire.
func tokenPreview(tok string) string {
	if len(tok) <= 4 {
		return tok
	}
	return "…" + tok[len(tok)-4:]
}
