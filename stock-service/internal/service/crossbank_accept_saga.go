package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	accountpb "github.com/exbanka/contract/accountpb"
	"github.com/exbanka/stock-service/internal/model"
)

// CrossbankAcceptSaga drives the 5-phase distributed accept saga (§5.1
// of the design). Initiator-side. Each phase persists an
// InterBankSagaLog row keyed on (TxID, Phase, Role=initiator) so the
// driver can re-enter on restart and skip completed phases.
//
// Phases:
//  1. reserve_buyer_funds   — local: reserve buyer's premium via account-service
//  2. reserve_seller_shares — peer call: ask seller's bank to reserve shares
//  3. transfer_funds        — Spec 3 inter-bank transfer of premium
//  4. transfer_ownership    — peer call: ask seller's bank to mark ownership transferred
//  5. finalize              — local: mark OptionContract ACTIVE; tell peer to mark ACTIVE
//
// Compensations on each post-phase-1 failure roll back the prior phases.
type CrossbankAcceptSaga struct {
	exec       *CrossbankSagaExecutor
	peers      CrossbankPeerRouter
	accounts   OTCAccountClient
	contracts  OptionContractWriter
	offers     OTCOfferWriter
	transfers  InterBankTransferer
	ownBankCode string
}

// OptionContractWriter is the narrow surface needed to create / update
// OptionContract rows during the saga.
type OptionContractWriter interface {
	Create(c *model.OptionContract) error
	Save(c *model.OptionContract) error
	GetByID(id uint64) (*model.OptionContract, error)
	Delete(id uint64) error
}

// OTCOfferWriter is the narrow surface for marking offer status.
type OTCOfferWriter interface {
	GetByID(id uint64) (*model.OTCOffer, error)
	Save(o *model.OTCOffer) error
}

// InterBankTransferer is the surface needed to drive the Phase-3 funds
// movement via Spec 3's transaction-service. Implemented by an adapter
// over transactionpb.InterBankServiceClient.
type InterBankTransferer interface {
	Initiate(ctx context.Context, sender, receiver, amount, currency, memo string) (string /* tx_id */, bool /* committed */, string /* failReason */, error)
	Reverse(ctx context.Context, originalTxID, memo string) error
}

// CrossbankAcceptInput captures the parameters of a cross-bank accept call.
// Mirrors AcceptInput but with explicit bank codes and the buyer's external
// client id (used by the peer to identify the buyer).
type CrossbankAcceptInput struct {
	OfferID                uint64
	BuyerUserID            int64
	BuyerSystemType        string
	BuyerBankCode          string
	BuyerClientIDExternal  string
	BuyerAccountID         uint64
	BuyerAccountNumber     string
	SellerUserID           int64
	SellerSystemType       string
	SellerBankCode         string
	SellerClientIDExternal string
	SellerAccountNumber    string // canonical account number on seller's bank for fund transfer
	Premium                decimal.Decimal
	Currency               string
	Quantity               decimal.Decimal
	StrikePrice            decimal.Decimal
	SettlementDate         time.Time
	AssetListingID         uint64 // shared listing id across cohort (faculty seed)
}

func NewCrossbankAcceptSaga(
	exec *CrossbankSagaExecutor,
	peers CrossbankPeerRouter,
	accounts OTCAccountClient,
	contracts OptionContractWriter,
	offers OTCOfferWriter,
	transfers InterBankTransferer,
	ownBankCode string,
) *CrossbankAcceptSaga {
	return &CrossbankAcceptSaga{
		exec: exec, peers: peers, accounts: accounts,
		contracts: contracts, offers: offers, transfers: transfers,
		ownBankCode: ownBankCode,
	}
}

// Run executes the 5-phase saga end to end. Returns the new OptionContract
// on success. On failure runs the appropriate compensation chain and
// returns an error describing the failing phase.
func (s *CrossbankAcceptSaga) Run(ctx context.Context, in CrossbankAcceptInput) (*model.OptionContract, error) {
	if s.peers == nil || s.accounts == nil || s.transfers == nil {
		return nil, errors.New("cross-bank accept saga deps not wired")
	}

	txID := uuid.NewString()
	role := model.SagaRoleInitiator
	sagaKind := model.SagaKindAccept
	remoteCode := in.SellerBankCode

	s.exec.PublishStarted(ctx, sagaKind, txID, in.BuyerBankCode, remoteCode, in.OfferID, 0)

	// ---------- Phase 1: reserve_buyer_funds (local) ----------
	if err := s.exec.BeginPhase(ctx, txID, model.PhaseReserveBuyerFunds, role, sagaKind, remoteCode, &in.OfferID, nil, in); err != nil {
		return nil, fmt.Errorf("phase1 begin: %w", err)
	}
	if _, err := s.accounts.ReserveFunds(ctx, in.BuyerAccountID, txIDtoUint64(txID), in.Premium, in.Currency); err != nil {
		_ = s.exec.FailPhase(ctx, txID, model.PhaseReserveBuyerFunds, role, "reserve_funds: "+err.Error())
		s.exec.PublishRolledBack(ctx, sagaKind, txID, model.PhaseReserveBuyerFunds, err.Error(), nil)
		return nil, fmt.Errorf("phase1 reserve_funds: %w", err)
	}
	_ = s.exec.CompletePhase(ctx, txID, model.PhaseReserveBuyerFunds, role)

	// Create the OptionContract row up-front so phase-4/5 have a stable id.
	contract := &model.OptionContract{
		OfferID: in.OfferID,
		BuyerUserID: in.BuyerUserID, BuyerSystemType: in.BuyerSystemType,
		BuyerBankCode: ptrStr(in.BuyerBankCode),
		SellerUserID: in.SellerUserID, SellerSystemType: in.SellerSystemType,
		SellerBankCode: ptrStr(in.SellerBankCode),
		Quantity: in.Quantity, StrikePrice: in.StrikePrice,
		PremiumPaid: in.Premium, PremiumCurrency: in.Currency, StrikeCurrency: in.Currency,
		SettlementDate: in.SettlementDate, Status: model.OptionContractStatusActive,
		SagaID: txID, PremiumPaidAt: time.Now().UTC(),
		CrossbankTxID: &txID,
	}
	if err := s.contracts.Create(contract); err != nil {
		s.compensateBuyerFunds(ctx, txID, in)
		_ = s.exec.FailPhase(ctx, txID, model.PhaseReserveBuyerFunds, role, "create_contract: "+err.Error())
		return nil, fmt.Errorf("create contract: %w", err)
	}

	// ---------- Phase 2: reserve_seller_shares (peer) ----------
	cid := contract.ID
	if err := s.exec.BeginPhase(ctx, txID, model.PhaseReserveSellerShares, role, sagaKind, remoteCode, &in.OfferID, &cid, in); err != nil {
		return nil, fmt.Errorf("phase2 begin: %w", err)
	}
	peer, err := s.peers.ClientFor(in.SellerBankCode)
	if err != nil {
		s.failAndCompensate(ctx, txID, role, model.PhaseReserveSellerShares, err.Error(), in, contract, []string{model.PhaseReserveBuyerFunds})
		return nil, err
	}
	resp, err := peer.ReserveShares(ctx, PeerReserveSharesRequest{
		TxID: txID, SagaKind: sagaKind, OfferID: in.OfferID, ContractID: contract.ID,
		AssetListingID: in.AssetListingID, Quantity: in.Quantity.String(),
		BuyerBankCode: in.BuyerBankCode, SellerBankCode: in.SellerBankCode,
	})
	if err != nil || !resp.Confirmed {
		reason := "phase2 reserve_shares failed"
		if err != nil {
			reason = err.Error()
		} else if resp.FailReason != "" {
			reason = resp.FailReason
		}
		s.failAndCompensate(ctx, txID, role, model.PhaseReserveSellerShares, reason, in, contract, []string{model.PhaseReserveBuyerFunds})
		return nil, errors.New(reason)
	}
	_ = s.exec.CompletePhase(ctx, txID, model.PhaseReserveSellerShares, role)

	// ---------- Phase 3: transfer_funds via Spec 3 ----------
	if err := s.exec.BeginPhase(ctx, txID, model.PhaseTransferFunds, role, sagaKind, remoteCode, &in.OfferID, &cid, in); err != nil {
		return nil, fmt.Errorf("phase3 begin: %w", err)
	}
	transferTxID, committed, failReason, err := s.transfers.Initiate(ctx,
		in.BuyerAccountNumber, in.SellerAccountNumber,
		in.Premium.String(), in.Currency,
		"OTC accept "+txID,
	)
	if err != nil || !committed {
		reason := failReason
		if err != nil {
			reason = err.Error()
		}
		s.failAndCompensate(ctx, txID, role, model.PhaseTransferFunds, reason, in, contract,
			[]string{model.PhaseReserveBuyerFunds, model.PhaseReserveSellerShares})
		return nil, fmt.Errorf("phase3: %s", reason)
	}
	_ = s.exec.CompletePhase(ctx, txID, model.PhaseTransferFunds, role)
	log.Printf("OTC accept saga=%s phase3 transfer_funds committed via inter-bank tx=%s", txID, transferTxID)

	// ---------- Phase 4: transfer_ownership (peer) ----------
	if err := s.exec.BeginPhase(ctx, txID, model.PhaseTransferOwnership, role, sagaKind, remoteCode, &in.OfferID, &cid, in); err != nil {
		return nil, fmt.Errorf("phase4 begin: %w", err)
	}
	tResp, err := peer.TransferOwnership(ctx, PeerTransferOwnershipRequest{
		TxID: txID, ContractID: contract.ID,
		AssetListingID: in.AssetListingID, Quantity: in.Quantity.String(),
		FromBankCode: in.SellerBankCode, FromClientIDExternal: in.SellerClientIDExternal,
		ToBankCode: in.BuyerBankCode, ToClientIDExternal: in.BuyerClientIDExternal,
	})
	if err != nil || !tResp.Confirmed {
		reason := "phase4 transfer_ownership failed"
		if err != nil {
			reason = err.Error()
		} else if tResp.FailReason != "" {
			reason = tResp.FailReason
		}
		// Phase 3 was committed → use REVERSE_TRANSFER to undo.
		_ = s.transfers.Reverse(ctx, transferTxID, "OTC accept compensation "+txID)
		s.failAndCompensate(ctx, txID, role, model.PhaseTransferOwnership, reason, in, contract,
			[]string{model.PhaseReserveBuyerFunds, model.PhaseReserveSellerShares, model.PhaseTransferFunds})
		return nil, errors.New(reason)
	}
	_ = s.exec.CompletePhase(ctx, txID, model.PhaseTransferOwnership, role)

	// ---------- Phase 5: finalize ----------
	if err := s.exec.BeginPhase(ctx, txID, model.PhaseFinalize, role, sagaKind, remoteCode, &in.OfferID, &cid, in); err != nil {
		return nil, fmt.Errorf("phase5 begin: %w", err)
	}
	_, _ = peer.Finalize(ctx, PeerFinalizeRequest{
		TxID: txID, ContractID: contract.ID, OfferID: in.OfferID,
		BuyerBankCode: in.BuyerBankCode, SellerBankCode: in.SellerBankCode,
		BuyerClientIDExternal: in.BuyerClientIDExternal, SellerClientIDExternal: in.SellerClientIDExternal,
		StrikePrice: in.StrikePrice.String(), Quantity: in.Quantity.String(),
		Premium: in.Premium.String(), Currency: in.Currency,
		SettlementDate: in.SettlementDate.Format("2006-01-02"),
		SagaKind: sagaKind,
	})
	// Mark the offer ACCEPTED locally.
	if o, err := s.offers.GetByID(in.OfferID); err == nil && o != nil {
		o.Status = model.OTCOfferStatusAccepted
		_ = s.offers.Save(o)
	}
	_ = s.exec.CompletePhase(ctx, txID, model.PhaseFinalize, role)
	s.exec.PublishCommitted(ctx, sagaKind, txID, contract.ID)
	return contract, nil
}

// failAndCompensate runs the standard compensation for every completed
// phase and emits the rolled-back kafka event.
func (s *CrossbankAcceptSaga) failAndCompensate(ctx context.Context, txID, role, failingPhase, reason string, in CrossbankAcceptInput, contract *model.OptionContract, completedPhases []string) {
	_ = s.exec.FailPhase(ctx, txID, failingPhase, role, reason)

	// Walk completed phases in reverse and compensate each.
	compensated := make([]string, 0, len(completedPhases))
	for i := len(completedPhases) - 1; i >= 0; i-- {
		switch completedPhases[i] {
		case model.PhaseReserveBuyerFunds:
			s.compensateBuyerFunds(ctx, txID, in)
			compensated = append(compensated, model.PhaseReserveBuyerFunds)
		case model.PhaseReserveSellerShares:
			if peer, err := s.peers.ClientFor(in.SellerBankCode); err == nil {
				cid := uint64(0)
				if contract != nil {
					cid = contract.ID
				}
				_, _ = peer.RollbackShares(ctx, PeerRollbackSharesRequest{TxID: txID, ContractID: cid})
			}
			compensated = append(compensated, model.PhaseReserveSellerShares)
		case model.PhaseTransferFunds:
			// Already reversed by the caller before invoking this method.
			compensated = append(compensated, model.PhaseTransferFunds)
		}
	}
	// Drop the contract row — it was never finalized.
	if contract != nil && contract.ID != 0 {
		_ = s.contracts.Delete(contract.ID)
	}
	s.exec.PublishRolledBack(ctx, model.SagaKindAccept, txID, failingPhase, reason, compensated)
}

// compensateBuyerFunds releases the phase-1 reservation on the buyer's
// account so the funds become available again.
func (s *CrossbankAcceptSaga) compensateBuyerFunds(ctx context.Context, txID string, in CrossbankAcceptInput) {
	if _, err := s.accounts.ReleaseReservation(ctx, txIDtoUint64(txID)); err != nil {
		log.Printf("WARN: cross-bank accept saga=%s: release_reservation compensation: %v", txID, err)
	}
	_ = in // silence unused
	_ = accountpb.AccountResponse{}
}

// txIDtoUint64 hashes the saga UUID into a uint64 idempotency key for the
// account-service's reservation API which still uses uint64 keys.
//
// Trivial fold over the UUID's hex digits — we just need a stable
// non-colliding mapping. Same UUID always produces the same uint64.
func txIDtoUint64(txID string) uint64 {
	// Strip dashes and parse the first 16 hex chars as a uint64.
	clean := ""
	for _, r := range txID {
		if r != '-' {
			clean += string(r)
		}
		if len(clean) >= 16 {
			break
		}
	}
	if len(clean) < 16 {
		clean = (clean + "0000000000000000")[:16]
	}
	v, err := strconv.ParseUint(clean, 16, 64)
	if err != nil {
		return 0
	}
	return v
}

func ptrStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
