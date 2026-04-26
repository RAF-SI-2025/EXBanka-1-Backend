package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/exbanka/stock-service/internal/model"
)

// CrossbankExerciseSaga drives the cross-bank exercise flow. Same 5 phases
// as accept, but the moneys/payloads carry strike instead of premium and
// the contract was already created on accept. Initiator: contract buyer.
type CrossbankExerciseSaga struct {
	exec        *CrossbankSagaExecutor
	peers       CrossbankPeerRouter
	accounts    OTCAccountClient
	contracts   OptionContractWriter
	transfers   InterBankTransferer
	ownBankCode string
}

func NewCrossbankExerciseSaga(
	exec *CrossbankSagaExecutor,
	peers CrossbankPeerRouter,
	accounts OTCAccountClient,
	contracts OptionContractWriter,
	transfers InterBankTransferer,
	ownBankCode string,
) *CrossbankExerciseSaga {
	return &CrossbankExerciseSaga{
		exec: exec, peers: peers, accounts: accounts,
		contracts: contracts, transfers: transfers, ownBankCode: ownBankCode,
	}
}

// CrossbankExerciseInput carries the accounts on each side plus the saga's
// canonical asset listing id so the peer can locate the security row.
type CrossbankExerciseInput struct {
	ContractID             uint64
	BuyerAccountID         uint64
	BuyerAccountNumber     string
	BuyerClientIDExternal  string
	SellerAccountNumber    string
	SellerClientIDExternal string
	AssetListingID         uint64
}

// Run executes the 5-phase exercise saga. Strike amount = qty * strike_price.
func (s *CrossbankExerciseSaga) Run(ctx context.Context, in CrossbankExerciseInput) (*model.OptionContract, error) {
	if s.peers == nil || s.accounts == nil || s.transfers == nil {
		return nil, errors.New("cross-bank exercise saga deps not wired")
	}
	c, err := s.contracts.GetByID(in.ContractID)
	if err != nil {
		return nil, err
	}
	if c.Status != model.OptionContractStatusActive {
		return nil, errors.New("contract is not active")
	}
	if !c.SettlementDate.After(time.Now().UTC().Truncate(24 * time.Hour)) {
		return nil, errors.New("contract has expired")
	}
	buyerCode := derefOr(c.BuyerBankCode, "")
	sellerCode := derefOr(c.SellerBankCode, "")
	if buyerCode == "" || sellerCode == "" || buyerCode == sellerCode {
		return nil, errors.New("contract is not cross-bank")
	}

	txID := uuid.NewString()
	role := model.SagaRoleInitiator
	sagaKind := model.SagaKindExercise
	cid := c.ID
	strikeAmt := c.Quantity.Mul(c.StrikePrice)
	currency := c.StrikeCurrency

	s.exec.PublishStarted(ctx, sagaKind, txID, buyerCode, sellerCode, c.OfferID, c.ID)

	// Phase 1: reserve buyer's strike funds.
	if err := s.exec.BeginPhase(ctx, txID, model.PhaseReserveBuyerFunds, role, sagaKind, sellerCode, &c.OfferID, &cid, in); err != nil {
		return nil, err
	}
	if _, err := s.accounts.ReserveFunds(ctx, in.BuyerAccountID, txIDtoUint64(txID), strikeAmt, currency); err != nil {
		_ = s.exec.FailPhase(ctx, txID, model.PhaseReserveBuyerFunds, role, err.Error())
		s.exec.PublishRolledBack(ctx, sagaKind, txID, model.PhaseReserveBuyerFunds, err.Error(), nil)
		return nil, err
	}
	_ = s.exec.CompletePhase(ctx, txID, model.PhaseReserveBuyerFunds, role)

	peer, err := s.peers.ClientFor(sellerCode)
	if err != nil {
		s.releaseStrike(ctx, txID)
		return nil, err
	}

	// Phase 2: ask seller's bank to reserve the shares we're about to take.
	if err := s.exec.BeginPhase(ctx, txID, model.PhaseReserveSellerShares, role, sagaKind, sellerCode, &c.OfferID, &cid, in); err != nil {
		return nil, err
	}
	rResp, err := peer.ReserveShares(ctx, PeerReserveSharesRequest{
		TxID: txID, SagaKind: sagaKind, OfferID: c.OfferID, ContractID: c.ID,
		AssetListingID: in.AssetListingID, Quantity: c.Quantity.String(),
		BuyerBankCode: buyerCode, SellerBankCode: sellerCode,
	})
	if err != nil || !rResp.Confirmed {
		reason := "phase2 reserve_shares failed"
		if err != nil {
			reason = err.Error()
		} else if rResp.FailReason != "" {
			reason = rResp.FailReason
		}
		s.releaseStrike(ctx, txID)
		_ = s.exec.FailPhase(ctx, txID, model.PhaseReserveSellerShares, role, reason)
		s.exec.PublishRolledBack(ctx, sagaKind, txID, model.PhaseReserveSellerShares, reason, []string{model.PhaseReserveBuyerFunds})
		return nil, errors.New(reason)
	}
	_ = s.exec.CompletePhase(ctx, txID, model.PhaseReserveSellerShares, role)

	// Phase 3: transfer strike funds via Spec 3.
	if err := s.exec.BeginPhase(ctx, txID, model.PhaseTransferFunds, role, sagaKind, sellerCode, &c.OfferID, &cid, in); err != nil {
		return nil, err
	}
	transferTxID, committed, failReason, err := s.transfers.Initiate(ctx,
		in.BuyerAccountNumber, in.SellerAccountNumber,
		strikeAmt.String(), currency, "OTC exercise "+txID,
	)
	if err != nil || !committed {
		reason := failReason
		if err != nil {
			reason = err.Error()
		}
		_, _ = peer.RollbackShares(ctx, PeerRollbackSharesRequest{TxID: txID, ContractID: c.ID})
		s.releaseStrike(ctx, txID)
		_ = s.exec.FailPhase(ctx, txID, model.PhaseTransferFunds, role, reason)
		s.exec.PublishRolledBack(ctx, sagaKind, txID, model.PhaseTransferFunds, reason,
			[]string{model.PhaseReserveBuyerFunds, model.PhaseReserveSellerShares})
		return nil, fmt.Errorf("phase3: %s", reason)
	}
	_ = s.exec.CompletePhase(ctx, txID, model.PhaseTransferFunds, role)

	// Phase 4: transfer ownership.
	if err := s.exec.BeginPhase(ctx, txID, model.PhaseTransferOwnership, role, sagaKind, sellerCode, &c.OfferID, &cid, in); err != nil {
		return nil, err
	}
	tResp, err := peer.TransferOwnership(ctx, PeerTransferOwnershipRequest{
		TxID: txID, ContractID: c.ID,
		AssetListingID: in.AssetListingID, Quantity: c.Quantity.String(),
		FromBankCode: sellerCode, FromClientIDExternal: in.SellerClientIDExternal,
		ToBankCode: buyerCode, ToClientIDExternal: in.BuyerClientIDExternal,
	})
	if err != nil || !tResp.Confirmed {
		reason := "phase4 transfer_ownership failed"
		if err != nil {
			reason = err.Error()
		} else if tResp.FailReason != "" {
			reason = tResp.FailReason
		}
		_ = s.transfers.Reverse(ctx, transferTxID, "OTC exercise compensation "+txID)
		_, _ = peer.RollbackShares(ctx, PeerRollbackSharesRequest{TxID: txID, ContractID: c.ID})
		s.releaseStrike(ctx, txID)
		_ = s.exec.FailPhase(ctx, txID, model.PhaseTransferOwnership, role, reason)
		s.exec.PublishRolledBack(ctx, sagaKind, txID, model.PhaseTransferOwnership, reason,
			[]string{model.PhaseReserveBuyerFunds, model.PhaseReserveSellerShares, model.PhaseTransferFunds})
		return nil, errors.New(reason)
	}
	_ = s.exec.CompletePhase(ctx, txID, model.PhaseTransferOwnership, role)

	// Phase 5: finalize — mark contract EXERCISED locally + tell peer.
	if err := s.exec.BeginPhase(ctx, txID, model.PhaseFinalize, role, sagaKind, sellerCode, &c.OfferID, &cid, in); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	c.Status = model.OptionContractStatusExercised
	c.ExercisedAt = &now
	c.CrossbankExerciseTxID = &txID
	_ = s.contracts.Save(c)
	_, _ = peer.Finalize(ctx, PeerFinalizeRequest{
		TxID: txID, ContractID: c.ID, OfferID: c.OfferID,
		BuyerBankCode: buyerCode, SellerBankCode: sellerCode,
		BuyerClientIDExternal: in.BuyerClientIDExternal, SellerClientIDExternal: in.SellerClientIDExternal,
		StrikePrice: c.StrikePrice.String(), Quantity: c.Quantity.String(),
		Premium: c.PremiumPaid.String(), Currency: currency,
		SettlementDate: c.SettlementDate.Format("2006-01-02"),
		SagaKind: sagaKind,
	})
	_ = s.exec.CompletePhase(ctx, txID, model.PhaseFinalize, role)
	s.exec.PublishCommitted(ctx, sagaKind, txID, c.ID)
	_ = strikeAmt
	_ = decimal.Zero
	return c, nil
}

func (s *CrossbankExerciseSaga) releaseStrike(ctx context.Context, txID string) {
	if _, err := s.accounts.ReleaseReservation(ctx, txIDtoUint64(txID)); err != nil {
		// Best-effort
		_ = err
	}
}

func derefOr(s *string, fallback string) string {
	if s == nil {
		return fallback
	}
	return *s
}
