package service

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	accountpb "github.com/exbanka/contract/accountpb"
	"github.com/exbanka/stock-service/internal/model"
)

// RecoverFundSaga auto-resolves a crash-stranded fund invest/redeem saga with no
// human intervention. Called by the saga-recovery reconciler for any stuck fund
// step; idempotent.
//
// The contribution row (created up-front under sagaID) is the recovery anchor:
// from it + the fund + account snapshots the saga is rebuilt identically and
// re-driven. Forward-resume (Execute) finishes the transfer when it was crashed
// mid-forward; rollback (Compensate) finishes the undo when it was already
// aborting. Every money step is idempotency-keyed and the position step is
// keyed on the contribution id, so replay is safe.
func (s *FundService) RecoverFundSaga(ctx context.Context, sagaID string, contribID uint64) error {
	if sagaID == "" {
		return errors.New("recover fund saga: empty sagaID")
	}
	contrib, err := s.contribs.GetBySagaID(sagaID)
	if errors.Is(err, gorm.ErrRecordNotFound) || contrib == nil {
		// No contribution row — nothing committed to resolve.
		return nil
	}
	if err != nil {
		return fmt.Errorf("recover fund saga %s: load contribution: %w", sagaID, err)
	}

	fund, err := s.repo.GetByID(contrib.FundID)
	if err != nil {
		return fmt.Errorf("recover fund saga %s: load fund %d: %w", sagaID, contrib.FundID, err)
	}
	fundAcct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: fund.RSDAccountID})
	if err != nil {
		return fmt.Errorf("recover fund saga %s: get fund account: %w", sagaID, err)
	}
	counterAcct, err := s.accounts.GetAccount(ctx, &accountpb.GetAccountRequest{Id: contrib.SourceOrTargetAccountID})
	if err != nil {
		return fmt.Errorf("recover fund saga %s: get counterparty account: %w", sagaID, err)
	}

	rollingBack := false
	if chk, ok := s.sagaRepo.(sagaCompensationChecker); ok {
		has, herr := chk.HasCompensations(sagaID)
		if herr != nil {
			return fmt.Errorf("recover fund saga %s: compensation check: %w", sagaID, herr)
		}
		rollingBack = has
	}

	switch contrib.Direction {
	case model.FundDirectionInvest:
		sg, state := s.buildInvestSaga(sagaID, investSagaParams{
			fundID: contrib.FundID, contribID: contrib.ID,
			posOwnerType: contrib.OwnerType, posOwnerID: contrib.OwnerID,
			srcAcctNumber: counterAcct.AccountNumber, fundAcctNumber: fundAcct.AccountNumber,
			amountNative: contrib.AmountNative, nativeCurrency: contrib.NativeCurrency,
			amountRSD: contrib.AmountRSD,
		})
		if rollingBack {
			if cerr := sg.Compensate(ctx, state); cerr != nil {
				return cerr
			}
			return s.contribs.UpdateStatus(contrib.ID, model.FundContributionStatusFailed)
		}
		if eerr := sg.Execute(ctx, state); eerr != nil {
			return eerr
		}
		return s.contribs.UpdateStatus(contrib.ID, model.FundContributionStatusCompleted)

	case model.FundDirectionRedeem:
		// Resolve the bank RSD account only when a fee is owed (matches Redeem).
		var bankRSDAcctNo string
		if !contrib.FeeRSD.IsZero() {
			if s.bankRSDAccountFn == nil {
				return fmt.Errorf("recover fund saga %s: bank RSD resolver not wired (fee credit)", sagaID)
			}
			acctNo, _, berr := s.bankRSDAccountFn(ctx)
			if berr != nil {
				return fmt.Errorf("recover fund saga %s: resolve bank RSD account: %w", sagaID, berr)
			}
			bankRSDAcctNo = acctNo
		}
		sg, state := s.buildRedeemSaga(sagaID, redeemSagaParams{
			fundID: contrib.FundID, contribID: contrib.ID,
			posOwnerType: contrib.OwnerType, posOwnerID: contrib.OwnerID,
			fundAcctNumber: fundAcct.AccountNumber, targetAcctNumber: counterAcct.AccountNumber,
			bankRSDAcctNumber: bankRSDAcctNo,
			amountRSD:         contrib.AmountRSD, feeRSD: contrib.FeeRSD,
		})
		if rollingBack {
			if cerr := sg.Compensate(ctx, state); cerr != nil {
				return cerr
			}
			return s.contribs.UpdateStatus(contrib.ID, model.FundContributionStatusFailed)
		}
		if eerr := sg.Execute(ctx, state); eerr != nil {
			return eerr
		}
		// Best-effort, idempotent position decrement (mirrors Redeem's
		// post-saga step), then mark the contribution completed.
		if derr := s.positions.DecrementContribution(contrib.FundID, contrib.OwnerType, contrib.OwnerID, contrib.AmountRSD, contrib.ID); derr != nil {
			// Non-fatal: money already moved; the position row is recoverable.
			return s.contribs.UpdateStatus(contrib.ID, model.FundContributionStatusCompleted)
		}
		return s.contribs.UpdateStatus(contrib.ID, model.FundContributionStatusCompleted)

	default:
		return fmt.Errorf("recover fund saga %s: unknown contribution direction %q", sagaID, contrib.Direction)
	}
}
