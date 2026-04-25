package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// ClientFundPosition is one (fund, owner) pair's accumulated contribution.
// owner is identified by (UserID, SystemType). The bank's own stake uses
// UserID=1_000_000_000 + SystemType="employee".
type ClientFundPosition struct {
	ID                  uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	FundID              uint64          `gorm:"not null;uniqueIndex:ux_pos_fund_user,priority:1" json:"fund_id"`
	UserID              uint64          `gorm:"not null;uniqueIndex:ux_pos_fund_user,priority:2;index:ix_pos_user" json:"user_id"`
	SystemType          string          `gorm:"size:10;not null;uniqueIndex:ux_pos_fund_user,priority:3;index:ix_pos_user" json:"system_type"`
	TotalContributedRSD decimal.Decimal `gorm:"type:numeric(20,4);not null;default:0" json:"total_contributed_rsd"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
	Version             int64           `gorm:"not null;default:0" json:"-"`
}

func (p *ClientFundPosition) BeforeUpdate(tx *gorm.DB) error {
	if tx != nil {
		tx.Statement.Where("version = ?", p.Version)
	}
	p.Version++
	return nil
}
