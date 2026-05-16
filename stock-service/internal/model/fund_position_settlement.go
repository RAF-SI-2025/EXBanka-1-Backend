package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// Direction constants for FundPositionSettlement.
const (
	FundSettlementDirectionInvest = "invest"
	FundSettlementDirectionRedeem = "redeem"
)

// FundPositionSettlement is the idempotency ledger for
// ClientFundPosition mutations. Every Increment / Decrement against a
// position MUST go through this row: the UNIQUE on FundContributionID
// is what makes the running total safe to replay (the fund_invest /
// fund_redeem sagas may retry their UpsertPosition / DecrementPosition
// step after a crash; without this ledger, IncrementContribution would
// add `delta` twice and silently inflate the user's stake).
//
// See repository.ClientFundPositionRepository.IncrementContribution /
// DecrementContribution for the insert-then-update pattern. (Fix R2,
// 2026-05-16: previously the increment ran a bare GORM expression
// `total_contributed_rsd + ?` with no dedup — money-double-count on
// any retry.)
type FundPositionSettlement struct {
	ID                   uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	ClientFundPositionID uint64          `gorm:"not null;index:ix_fps_position" json:"client_fund_position_id"`
	FundContributionID   uint64          `gorm:"not null;uniqueIndex" json:"fund_contribution_id"`
	Delta                decimal.Decimal `gorm:"type:numeric(20,4);not null" json:"delta"`
	Direction            string          `gorm:"size:8;not null;check:direction IN ('invest','redeem')" json:"direction"`
	CreatedAt            time.Time       `json:"created_at"`
}

func (FundPositionSettlement) TableName() string { return "fund_position_settlements" }
