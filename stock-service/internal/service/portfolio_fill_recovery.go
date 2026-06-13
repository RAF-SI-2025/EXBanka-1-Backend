package service

import (
	"context"
	"fmt"

	"github.com/exbanka/stock-service/internal/model"
)

// RecoverFillSaga auto-resolves a crash-stranded fill saga (stock buy, stock
// sell, or forex buy) with no human intervention. The order + transaction are
// the recovery anchor (both already persisted by the engine before the saga);
// SagaRecovery loads them and passes them in along with the rollback direction.
//
// The saga is rebuilt under the persisted sagaID and forward-resumed (Execute)
// to complete the fill, or rolled back (Compensate) when it was already
// aborting. Every fill step is idempotent — keyed money legs, audit-only
// record/convert, marker-guarded holding upsert, settlement-keyed holding
// decrement — so the replay is safe. The best-effort commission leg is
// recovered separately via the credit_commission reconciler.
func (s *PortfolioService) RecoverFillSaga(ctx context.Context, sagaID string, order *model.Order, txn *model.OrderTransaction, rollingBack bool) error {
	if order == nil || txn == nil {
		return fmt.Errorf("recover fill saga %s: nil order or txn", sagaID)
	}

	if order.SecurityType == "forex" {
		if s.forexFillSvc == nil {
			return fmt.Errorf("recover fill saga %s: forex fill service not wired", sagaID)
		}
		return s.forexFillSvc.RecoverForexBuySaga(ctx, sagaID, order, txn, rollingBack)
	}

	build := s.buildBuyFillSaga
	if order.Direction == "sell" {
		build = s.buildSellFillSaga
	}
	sg, state, _, err := build(ctx, sagaID, order, txn)
	if err != nil {
		return fmt.Errorf("recover fill saga %s: rebuild: %w", sagaID, err)
	}
	if rollingBack {
		return sg.Compensate(ctx, state)
	}
	return sg.Execute(ctx, state)
}
