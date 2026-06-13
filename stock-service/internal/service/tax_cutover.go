package service

import (
	"github.com/exbanka/stock-service/internal/model"
	"gorm.io/gorm"
)

// CleanupLegacyBuyerPremiumRows removes accept-time buyer option-premium COST
// rows for option contracts that are still ACTIVE, so the resolution-month
// model can re-book the premium at exercise/expiry without double-counting.
//
// Under the old model the buyer's premium was booked at accept as a negative
// capital-gain row keyed "<contract.saga_id>:buyer-premium-cg". The new model
// books the premium only when the option resolves. For a contract that was
// accepted under the old model but has NOT yet resolved, that accept-time row
// would otherwise be counted in addition to the new resolution-time row.
//
// Targeting is exact: only rows whose idempotency_key equals an ACTIVE
// contract's "<saga_id>:buyer-premium-cg" are deleted, and only when still
// uncollected (tax_collection_id IS NULL). Already-collected rows and rows for
// already-resolved contracts (where the accept-time premium is the legitimate,
// final accounting) are never touched. Idempotent and safe on every startup.
// Spec docs/superpowers/specs/2026-06-04-options-premium-tax-design.md §6.
func CleanupLegacyBuyerPremiumRows(db *gorm.DB) (int64, error) {
	activeKeys := db.Model(&model.OptionContract{}).
		Select("saga_id || ':buyer-premium-cg'").
		Where("status = ? AND saga_id <> ''", model.OptionContractStatusActive)
	res := db.Where("tax_collection_id IS NULL").
		Where("idempotency_key IN (?)", activeKeys).
		Delete(&model.CapitalGain{})
	return res.RowsAffected, res.Error
}
