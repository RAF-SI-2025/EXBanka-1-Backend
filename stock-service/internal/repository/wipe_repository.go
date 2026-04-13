package repository

import "gorm.io/gorm"

// WipeRepository clears all stock-service tables and user-trading state.
// Used ONLY by the admin source-switch flow. DESTRUCTIVE.
type WipeRepository struct {
	db *gorm.DB
}

// NewWipeRepository creates a new WipeRepository backed by the given DB.
func NewWipeRepository(db *gorm.DB) *WipeRepository {
	return &WipeRepository{db: db}
}

// WipeAll deletes every row from the tables owned by stock-service, in a
// single transaction, honoring FK order (children first). Called only by
// the admin source-switch endpoint.
func (r *WipeRepository) WipeAll() error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		tables := []string{
			"tax_collections",
			"capital_gains",
			"order_transactions",
			"orders",
			"holdings",
			"options",
			"listings",
			"forex_pairs",
			"futures_contracts",
			"stocks",
			"stock_exchanges",
		}
		for _, t := range tables {
			if err := tx.Exec("DELETE FROM " + t).Error; err != nil { //nolint:gosec // table names are hardcoded, no user input
				return err
			}
		}
		return nil
	})
}
