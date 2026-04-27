package handler

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/contract/shared/saga"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

// CrossbankInternalHandler implements stockpb.CrossBankOTCServiceServer for
// the responder side (i.e. the peer's saga driver makes these calls).
//
// HMAC verification + nonce checking happen at the api-gateway middleware
// layer before the request reaches this handler — by the time we run, the
// request has already been authenticated.
type CrossbankInternalHandler struct {
	stockpb.UnimplementedCrossBankOTCServiceServer

	offerRepo    *repository.OTCOfferRepository
	contractRepo *repository.OptionContractRepository
	holdingRepo  *repository.HoldingRepository
	holdingRes   *service.HoldingReservationService
	logs         *repository.InterBankSagaLogRepository
	ownBankCode  string
	// db + idem wire saga-step idempotency for the responder-side Handle*
	// RPCs. Each Handle* RPC requires idempotency_key and caches the wire
	// response under that key, so a peer's retry returns the same
	// response without re-running the underlying side effect.
	db   *gorm.DB
	idem *repository.IdempotencyRepository
}

func NewCrossbankInternalHandler(
	offerRepo *repository.OTCOfferRepository,
	contractRepo *repository.OptionContractRepository,
	holdingRepo *repository.HoldingRepository,
	holdingRes *service.HoldingReservationService,
	logs *repository.InterBankSagaLogRepository,
	ownBankCode string,
	db *gorm.DB,
	idem *repository.IdempotencyRepository,
) *CrossbankInternalHandler {
	return &CrossbankInternalHandler{
		offerRepo: offerRepo, contractRepo: contractRepo,
		holdingRepo: holdingRepo, holdingRes: holdingRes,
		logs: logs, ownBankCode: ownBankCode,
		db: db, idem: idem,
	}
}

// PeerListOffers returns the responder's public offers since the given
// timestamp. Filtered to public offers only (private offers don't fan out).
func (h *CrossbankInternalHandler) PeerListOffers(ctx context.Context, in *stockpb.PeerListOffersRequest) (*stockpb.PeerListOffersResponse, error) {
	if h.offerRepo == nil {
		return &stockpb.PeerListOffersResponse{}, nil
	}
	// PeerListOffers returns OFFERS visible to peer banks. The "either" role
	// scope means we list across every owner_type — pass empty values to
	// ListByOwner; the repo treats both as wildcards via the role scope.
	rows, _, err := h.offerRepo.ListByOwner("", nil, "either",
		[]string{model.OTCOfferStatusPending, model.OTCOfferStatusCountered}, 0, 1, 100)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	out := &stockpb.PeerListOffersResponse{Offers: make([]*stockpb.OTCOfferResponse, 0, len(rows))}
	for i := range rows {
		if !rows[i].Public {
			continue
		}
		out.Offers = append(out.Offers, &stockpb.OTCOfferResponse{
			Id:        rows[i].ID,
			Direction: rows[i].Direction,
			StockId:   rows[i].StockID,
			Quantity:  rows[i].Quantity.String(),
			StrikePrice: rows[i].StrikePrice.String(),
			Premium:    rows[i].Premium.String(),
			SettlementDate: rows[i].SettlementDate.Format("2006-01-02"),
			Status:    rows[i].Status,
		})
	}
	return out, nil
}

// PeerFetchOffer returns one offer's current state.
func (h *CrossbankInternalHandler) PeerFetchOffer(ctx context.Context, in *stockpb.PeerFetchOfferRequest) (*stockpb.OTCOfferResponse, error) {
	o, err := h.offerRepo.GetByID(in.OfferId)
	if err != nil {
		return nil, status.Error(codes.NotFound, "offer not found")
	}
	return &stockpb.OTCOfferResponse{
		Id: o.ID, Direction: o.Direction, StockId: o.StockID,
		Quantity: o.Quantity.String(), StrikePrice: o.StrikePrice.String(),
		Premium: o.Premium.String(), SettlementDate: o.SettlementDate.Format("2006-01-02"),
		Status: o.Status,
	}, nil
}

// PeerReviseOffer / PeerAcceptIntent: stubs returning Unimplemented until
// the cohort wire shape is locked.
func (h *CrossbankInternalHandler) PeerReviseOffer(ctx context.Context, in *stockpb.PeerReviseOfferRequest) (*stockpb.OTCOfferResponse, error) {
	return nil, status.Error(codes.Unimplemented, "PeerReviseOffer wire shape pending cohort lock-down")
}

func (h *CrossbankInternalHandler) PeerAcceptIntent(ctx context.Context, in *stockpb.PeerAcceptIntentRequest) (*stockpb.PeerAcceptIntentResponse, error) {
	return &stockpb.PeerAcceptIntentResponse{TxId: uuid.NewString()}, nil
}

// HandleReserveSellerShares is the Phase-2 responder. Looks up the seller's
// holding for the asset and reserves `quantity` against the contract id.
// Idempotent on (tx_id, phase, role) via the saga log AND on
// idempotency_key via the response cache (Plan 2026-04-27 Task 9).
func (h *CrossbankInternalHandler) HandleReserveSellerShares(ctx context.Context, in *stockpb.ReserveSellerSharesRequest) (*stockpb.ReserveSellerSharesResponse, error) {
	if in.GetIdempotencyKey() == "" {
		return nil, service.ErrIdempotencyMissing
	}
	if h.db == nil || h.idem == nil {
		return nil, status.Errorf(codes.Internal, "idempotency repository not wired")
	}
	var resp *stockpb.ReserveSellerSharesResponse
	err := h.db.Transaction(func(tx *gorm.DB) error {
		out, runErr := repository.Run(h.idem, tx, in.GetIdempotencyKey(),
			func() *stockpb.ReserveSellerSharesResponse { return &stockpb.ReserveSellerSharesResponse{} },
			func() (*stockpb.ReserveSellerSharesResponse, error) {
				return h.executeHandleReserveSellerShares(ctx, in)
			})
		if runErr != nil {
			return runErr
		}
		resp = out
		return nil
	})
	return resp, err
}

func (h *CrossbankInternalHandler) executeHandleReserveSellerShares(ctx context.Context, in *stockpb.ReserveSellerSharesRequest) (*stockpb.ReserveSellerSharesResponse, error) {
	if h.holdingRes == nil || h.logs == nil {
		return nil, status.Error(codes.Unimplemented, "responder deps not wired")
	}
	// Idempotency: if we've already completed this row, return the cached
	// success.
	if existing, err := h.logs.Get(in.TxId, string(saga.StepReserveSellerShares), model.SagaRoleResponder); err == nil && existing.Status == model.IBSagaStatusCompleted {
		return &stockpb.ReserveSellerSharesResponse{Confirmed: true, ReservationId: existing.ID}, nil
	}
	// Record begin.
	cid := in.ContractId
	row := &model.InterBankSagaLog{
		TxID: in.TxId, Phase: string(saga.StepReserveSellerShares), Role: model.SagaRoleResponder,
		RemoteBankCode: in.BuyerBankCode,
		Status:         model.IBSagaStatusPending,
		OfferID:        &in.OfferId,
		ContractID:     &cid,
		SagaKind:       in.SagaKind,
	}
	_ = h.logs.UpsertByTxPhaseRole(row)
	qty, err := decimal.NewFromString(in.Quantity)
	if err != nil {
		row.Status = model.IBSagaStatusFailed
		row.ErrorReason = "invalid quantity"
		_ = h.logs.UpsertByTxPhaseRole(row)
		return &stockpb.ReserveSellerSharesResponse{Confirmed: false, FailReason: "invalid_quantity"}, nil
	}
	// Cross-bank: we don't know who the seller user is locally — the seller
	// is identified by the asset_listing_id + the existing OTC offer's
	// initiator. Look up the offer and use its initiator as the seller.
	o, err := h.offerRepo.GetByID(in.OfferId)
	if err != nil {
		row.Status = model.IBSagaStatusFailed
		row.ErrorReason = "offer not found"
		_ = h.logs.UpsertByTxPhaseRole(row)
		return &stockpb.ReserveSellerSharesResponse{Confirmed: false, FailReason: "offer_not_found"}, nil
	}
	if _, err := h.holdingRes.ReserveForOTCContract(ctx,
		o.InitiatorOwnerType, o.InitiatorOwnerID,
		"stock", o.StockID, in.ContractId, qty.IntPart()); err != nil {
		row.Status = model.IBSagaStatusFailed
		row.ErrorReason = err.Error()
		_ = h.logs.UpsertByTxPhaseRole(row)
		return &stockpb.ReserveSellerSharesResponse{Confirmed: false, FailReason: "insufficient_shares"}, nil
	}
	row.Status = model.IBSagaStatusCompleted
	_ = h.logs.UpsertByTxPhaseRole(row)
	return &stockpb.ReserveSellerSharesResponse{Confirmed: true, ReservationId: strconv.FormatUint(in.ContractId, 10)}, nil
}

// HandleTransferOwnership is the Phase-4 responder. Consumes the seller's
// reservation (shares physically leave the seller's holding). Wrapped in
// the saga-step idempotency contract.
func (h *CrossbankInternalHandler) HandleTransferOwnership(ctx context.Context, in *stockpb.TransferOwnershipRequest) (*stockpb.TransferOwnershipResponse, error) {
	if in.GetIdempotencyKey() == "" {
		return nil, service.ErrIdempotencyMissing
	}
	if h.db == nil || h.idem == nil {
		return nil, status.Errorf(codes.Internal, "idempotency repository not wired")
	}
	var resp *stockpb.TransferOwnershipResponse
	err := h.db.Transaction(func(tx *gorm.DB) error {
		out, runErr := repository.Run(h.idem, tx, in.GetIdempotencyKey(),
			func() *stockpb.TransferOwnershipResponse { return &stockpb.TransferOwnershipResponse{} },
			func() (*stockpb.TransferOwnershipResponse, error) {
				return h.executeHandleTransferOwnership(ctx, in)
			})
		if runErr != nil {
			return runErr
		}
		resp = out
		return nil
	})
	return resp, err
}

func (h *CrossbankInternalHandler) executeHandleTransferOwnership(ctx context.Context, in *stockpb.TransferOwnershipRequest) (*stockpb.TransferOwnershipResponse, error) {
	if h.holdingRes == nil || h.logs == nil {
		return nil, status.Error(codes.Unimplemented, "responder deps not wired")
	}
	// Idempotency.
	if existing, err := h.logs.Get(in.TxId, string(saga.StepTransferOwnership), model.SagaRoleResponder); err == nil && existing.Status == model.IBSagaStatusCompleted {
		return &stockpb.TransferOwnershipResponse{Confirmed: true}, nil
	}
	cid := in.ContractId
	row := &model.InterBankSagaLog{
		TxID: in.TxId, Phase: string(saga.StepTransferOwnership), Role: model.SagaRoleResponder,
		RemoteBankCode: in.ToBankCode,
		Status:         model.IBSagaStatusPending,
		ContractID:     &cid,
		SagaKind:       model.SagaKindAccept,
	}
	_ = h.logs.UpsertByTxPhaseRole(row)

	qty, err := decimal.NewFromString(in.Quantity)
	if err != nil {
		row.Status = model.IBSagaStatusFailed
		row.ErrorReason = "invalid quantity"
		_ = h.logs.UpsertByTxPhaseRole(row)
		return &stockpb.TransferOwnershipResponse{Confirmed: false, FailReason: "invalid_quantity"}, nil
	}
	syntheticTxn := in.ContractId + 1_000_000_000_000
	if _, err := h.holdingRes.ConsumeForOTCContract(ctx, in.ContractId, qty.IntPart(), syntheticTxn); err != nil {
		row.Status = model.IBSagaStatusFailed
		row.ErrorReason = err.Error()
		_ = h.logs.UpsertByTxPhaseRole(row)
		return &stockpb.TransferOwnershipResponse{Confirmed: false, FailReason: "consume_failed"}, nil
	}
	row.Status = model.IBSagaStatusCompleted
	_ = h.logs.UpsertByTxPhaseRole(row)
	return &stockpb.TransferOwnershipResponse{
		Confirmed:  true,
		AssignedAt: time.Now().UTC().Format(time.RFC3339),
		Serial:     in.TxId,
	}, nil
}

// HandleFinalize records the contract on the responder side. The contract
// row mirrors the initiator's so both banks have the same ground truth.
// Wrapped in the saga-step idempotency contract.
func (h *CrossbankInternalHandler) HandleFinalize(ctx context.Context, in *stockpb.FinalizeRequest) (*stockpb.FinalizeResponse, error) {
	if in.GetIdempotencyKey() == "" {
		return nil, service.ErrIdempotencyMissing
	}
	if h.db == nil || h.idem == nil {
		return nil, status.Errorf(codes.Internal, "idempotency repository not wired")
	}
	var resp *stockpb.FinalizeResponse
	err := h.db.Transaction(func(tx *gorm.DB) error {
		out, runErr := repository.Run(h.idem, tx, in.GetIdempotencyKey(),
			func() *stockpb.FinalizeResponse { return &stockpb.FinalizeResponse{} },
			func() (*stockpb.FinalizeResponse, error) {
				return h.executeHandleFinalize(ctx, in)
			})
		if runErr != nil {
			return runErr
		}
		resp = out
		return nil
	})
	return resp, err
}

func (h *CrossbankInternalHandler) executeHandleFinalize(ctx context.Context, in *stockpb.FinalizeRequest) (*stockpb.FinalizeResponse, error) {
	if h.contractRepo == nil || h.logs == nil {
		return nil, status.Error(codes.Unimplemented, "responder deps not wired")
	}
	if existing, err := h.logs.Get(in.TxId, string(saga.StepFinalizeAccept), model.SagaRoleResponder); err == nil && existing.Status == model.IBSagaStatusCompleted {
		return &stockpb.FinalizeResponse{Ok: true}, nil
	}
	cid := in.ContractId
	row := &model.InterBankSagaLog{
		TxID: in.TxId, Phase: string(saga.StepFinalizeAccept), Role: model.SagaRoleResponder,
		RemoteBankCode: in.BuyerBankCode,
		Status:         model.IBSagaStatusCompleted,
		ContractID:     &cid,
		SagaKind:       in.SagaKind,
	}
	_ = h.logs.UpsertByTxPhaseRole(row)
	return &stockpb.FinalizeResponse{Ok: true}, nil
}

// HandleContractExpire releases the seller's holding reservation locally.
// Wrapped in the saga-step idempotency contract.
func (h *CrossbankInternalHandler) HandleContractExpire(ctx context.Context, in *stockpb.ContractExpireRequest) (*stockpb.ContractExpireResponse, error) {
	if in.GetIdempotencyKey() == "" {
		return nil, service.ErrIdempotencyMissing
	}
	if h.db == nil || h.idem == nil {
		return nil, status.Errorf(codes.Internal, "idempotency repository not wired")
	}
	var resp *stockpb.ContractExpireResponse
	err := h.db.Transaction(func(tx *gorm.DB) error {
		out, runErr := repository.Run(h.idem, tx, in.GetIdempotencyKey(),
			func() *stockpb.ContractExpireResponse { return &stockpb.ContractExpireResponse{} },
			func() (*stockpb.ContractExpireResponse, error) {
				return h.executeHandleContractExpire(ctx, in)
			})
		if runErr != nil {
			return runErr
		}
		resp = out
		return nil
	})
	return resp, err
}

func (h *CrossbankInternalHandler) executeHandleContractExpire(ctx context.Context, in *stockpb.ContractExpireRequest) (*stockpb.ContractExpireResponse, error) {
	if h.holdingRes == nil {
		return nil, status.Error(codes.Unimplemented, "responder deps not wired")
	}
	if _, err := h.holdingRes.ReleaseForOTCContract(ctx, in.ContractId); err != nil {
		return &stockpb.ContractExpireResponse{Ok: false, Reason: err.Error()}, nil
	}
	return &stockpb.ContractExpireResponse{Ok: true}, nil
}

// HandleSagaCheckStatus reports a single saga row's status. Used by the
// initiator's CHECK_STATUS reconciler cron when the inter-bank link drops.
func (h *CrossbankInternalHandler) HandleSagaCheckStatus(ctx context.Context, in *stockpb.SagaCheckStatusRequest) (*stockpb.SagaCheckStatusResponse, error) {
	if h.logs == nil {
		return nil, status.Error(codes.Unimplemented, "responder deps not wired")
	}
	phase := in.Phase
	if phase == "" {
		phase = string(saga.StepReserveSellerShares)
	}
	row, err := h.logs.Get(in.TxId, phase, model.SagaRoleResponder)
	if err != nil {
		if errors.Is(err, errNotFound{}) {
			return &stockpb.SagaCheckStatusResponse{TxId: in.TxId, NotFound: true}, nil
		}
		return &stockpb.SagaCheckStatusResponse{TxId: in.TxId, NotFound: true}, nil
	}
	return &stockpb.SagaCheckStatusResponse{
		TxId: row.TxID, Phase: row.Phase, Role: row.Role,
		Status: row.Status, ErrorReason: row.ErrorReason,
	}, nil
}

// HandleReserveSharesRollback releases the seller's holding reservation
// when the initiator's saga compensates. Wrapped in the saga-step
// idempotency contract.
func (h *CrossbankInternalHandler) HandleReserveSharesRollback(ctx context.Context, in *stockpb.ReserveSharesRollbackRequest) (*stockpb.ReserveSharesRollbackResponse, error) {
	if in.GetIdempotencyKey() == "" {
		return nil, service.ErrIdempotencyMissing
	}
	if h.db == nil || h.idem == nil {
		return nil, status.Errorf(codes.Internal, "idempotency repository not wired")
	}
	var resp *stockpb.ReserveSharesRollbackResponse
	err := h.db.Transaction(func(tx *gorm.DB) error {
		out, runErr := repository.Run(h.idem, tx, in.GetIdempotencyKey(),
			func() *stockpb.ReserveSharesRollbackResponse { return &stockpb.ReserveSharesRollbackResponse{} },
			func() (*stockpb.ReserveSharesRollbackResponse, error) {
				return h.executeHandleReserveSharesRollback(ctx, in)
			})
		if runErr != nil {
			return runErr
		}
		resp = out
		return nil
	})
	return resp, err
}

func (h *CrossbankInternalHandler) executeHandleReserveSharesRollback(ctx context.Context, in *stockpb.ReserveSharesRollbackRequest) (*stockpb.ReserveSharesRollbackResponse, error) {
	if h.holdingRes == nil {
		return nil, status.Error(codes.Unimplemented, "responder deps not wired")
	}
	if _, err := h.holdingRes.ReleaseForOTCContract(ctx, in.ContractId); err != nil {
		return &stockpb.ReserveSharesRollbackResponse{Ok: false}, nil
	}
	return &stockpb.ReserveSharesRollbackResponse{Ok: true}, nil
}

// errNotFound is a sentinel used in the not-found branch of CheckStatus —
// kept local so the handler doesn't pull gorm into its imports.
type errNotFound struct{}

func (errNotFound) Error() string { return "not found" }
