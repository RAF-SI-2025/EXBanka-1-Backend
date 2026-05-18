package model

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// ClientFundPosition is one (fund, owner) pair's accumulated contribution.
// owner is identified by (OwnerType, OwnerID). The bank's own stake uses
// OwnerType="bank" with OwnerID=NULL.
type ClientFundPosition struct {
	ID                  uint64          `gorm:"primaryKey;autoIncrement" json:"id"`
	FundID              uint64          `gorm:"not null;uniqueIndex:ux_pos_fund_owner,priority:1" json:"fund_id"`
	OwnerType           OwnerType       `gorm:"size:8;not null;uniqueIndex:ux_pos_fund_owner,priority:2;index:ix_pos_owner,priority:1;check:owner_type IN ('client','bank')" json:"owner_type"`
	OwnerID             *uint64         `gorm:"uniqueIndex:ux_pos_fund_owner,priority:3;index:ix_pos_owner,priority:2" json:"owner_id"`
	TotalContributedRSD decimal.Decimal `gorm:"type:numeric(20,4);not null;default:0" json:"total_contributed_rsd"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
	Version             int64           `gorm:"not null;default:0" json:"-"`
}

func (p *ClientFundPosition) BeforeSave(tx *gorm.DB) error {
	return ValidateOwner(p.OwnerType, p.OwnerID)
}

func (p *ClientFundPosition) BeforeUpdate(tx *gorm.DB) error {
	if tx != nil {
		tx.Statement.Where("version = ?", p.Version)
	}
	p.Version++
	return nil
}
