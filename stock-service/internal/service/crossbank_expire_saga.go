package service

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/exbanka/stock-service/internal/model"
)

// CrossbankExpireSaga drives the 3-phase expire flow for cross-bank
// option contracts. The seller-side bank releases its reservation and the
// buyer-side bank marks the contract EXPIRED locally; both publish
// otc.contract-expired-crossbank.
//
// Phases:
//  1. expire_notify — peer call: ContractExpire
//  2. expire_apply  — local: release reservation (if seller-side) or
//                     mark contract EXPIRED (if buyer-side)
//  3. finalize      — kafka publish + bookkeeping
type CrossbankExpireSaga struct {
	exec       *CrossbankSagaExecutor
	peers      CrossbankPeerRouter
	contracts  OptionContractWriter
	holdingRes *HoldingReservationService
	ownBank    string
}

func NewCrossbankExpireSaga(
	exec *CrossbankSagaExecutor,
	peers CrossbankPeerRouter,
	contracts OptionContractWriter,
	holdingRes *HoldingReservationService,
	ownBank string,
) *CrossbankExpireSaga {
	return &CrossbankExpireSaga{exec: exec, peers: peers, contracts: contracts, holdingRes: holdingRes, ownBank: ownBank}
}

// ExpireContract drives the 3-phase expire saga for a cross-bank contract.
// Caller is the bank that holds the EXPIRED side (typically the seller —
// the bank that issued the option). Idempotent: re-running on an already
// EXPIRED contract is a no-op.
func (s *CrossbankExpireSaga) ExpireContract(ctx context.Context, contractID uint64) error {
	c, err := s.contracts.GetByID(contractID)
	if err != nil {
		return err
	}
	if c.Status == model.OptionContractStatusExpired {
		return nil
	}
	if c.Status != model.OptionContractStatusActive {
		return errors.New("contract is not active")
	}

	buyerCode := derefOr(c.BuyerBankCode, "")
	sellerCode := derefOr(c.SellerBankCode, "")
	if buyerCode == "" || sellerCode == "" || buyerCode == sellerCode {
		return errors.New("contract is not cross-bank")
	}

	// Determine the peer (the OTHER bank) — we send the notify to them.
	var peerCode string
	switch s.ownBank {
	case sellerCode:
		peerCode = buyerCode
	case buyerCode:
		peerCode = sellerCode
	default:
		return errors.New("own bank is neither buyer nor seller for this contract")
	}

	txID := uuid.NewString()
	role := model.SagaRoleInitiator
	sagaKind := model.SagaKindExpire
	cid := c.ID

	s.exec.PublishStarted(ctx, sagaKind, txID, s.ownBank, peerCode, c.OfferID, c.ID)

	// Phase 1: notify the peer.
	if err := s.exec.BeginPhase(ctx, txID, model.PhaseExpireNotify, role, sagaKind, peerCode, &c.OfferID, &cid, contractID); err != nil {
		return err
	}
	peer, err := s.peers.ClientFor(peerCode)
	if err != nil {
		_ = s.exec.FailPhase(ctx, txID, model.PhaseExpireNotify, role, err.Error())
		return err
	}
	if _, err := peer.ContractExpire(ctx, PeerContractExpireRequest{TxID: txID, ContractID: c.ID}); err != nil {
		// Best-effort — even if peer notify fails we continue with local
		// expire. Cron will retry the peer.
		_ = s.exec.FailPhase(ctx, txID, model.PhaseExpireNotify, role, err.Error())
	} else {
		_ = s.exec.CompletePhase(ctx, txID, model.PhaseExpireNotify, role)
	}

	// Phase 2: local expire. Seller side releases the reservation; buyer
	// side just flips the status row.
	if err := s.exec.BeginPhase(ctx, txID, model.PhaseExpireApply, role, sagaKind, peerCode, &c.OfferID, &cid, contractID); err != nil {
		return err
	}
	if s.ownBank == sellerCode && s.holdingRes != nil {
		if _, err := s.holdingRes.ReleaseForOTCContract(ctx, c.ID); err != nil {
			_ = s.exec.FailPhase(ctx, txID, model.PhaseExpireApply, role, err.Error())
			return err
		}
	}
	now := time.Now().UTC()
	c.Status = model.OptionContractStatusExpired
	c.ExpiredAt = &now
	if err := s.contracts.Save(c); err != nil {
		_ = s.exec.FailPhase(ctx, txID, model.PhaseExpireApply, role, err.Error())
		return err
	}
	_ = s.exec.CompletePhase(ctx, txID, model.PhaseExpireApply, role)
	s.exec.PublishCommitted(ctx, sagaKind, txID, c.ID)
	return nil
}
