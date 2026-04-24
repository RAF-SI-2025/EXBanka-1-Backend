package repository

import (
	"time"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

type OrderTransactionRepository struct {
	db *gorm.DB
}

func NewOrderTransactionRepository(db *gorm.DB) *OrderTransactionRepository {
	return &OrderTransactionRepository{db: db}
}

func (r *OrderTransactionRepository) Create(tx *model.OrderTransaction) error {
	return r.db.Create(tx).Error
}

// Update persists changes to an existing OrderTransaction. Used by the fill
// saga's convert_amount step to record native/converted amounts and FX rate
// on the transaction row before settlement proceeds.
func (r *OrderTransactionRepository) Update(tx *model.OrderTransaction) error {
	return r.db.Save(tx).Error
}

func (r *OrderTransactionRepository) ListByOrderID(orderID uint64) ([]model.OrderTransaction, error) {
	var txns []model.OrderTransaction
	if err := r.db.Where("order_id = ?", orderID).
		Order("executed_at ASC").Find(&txns).Error; err != nil {
		return nil, err
	}
	return txns, nil
}

// HoldingTransactionRow is a denormalized row the Part-B endpoint returns —
// the OrderTransaction joined with owning-order fields the caller needs
// (direction, account, commission, ticker). Flat struct so the service layer
// can serialize it without re-looking-up every parent order.
type HoldingTransactionRow struct {
	ID              uint64
	OrderID         uint64
	ExecutedAt      time.Time
	Direction       string
	Quantity        int64
	PricePerUnit    decimal.Decimal
	NativeAmount    *decimal.Decimal
	NativeCurrency  string
	ConvertedAmount *decimal.Decimal
	AccountCurrency string
	FxRate          *decimal.Decimal
	Commission      decimal.Decimal
	AccountID       uint64
	Ticker          string
}

// ListByHolding returns all OrderTransactions for orders that match a
// holding's aggregation key (user_id, system_type, security_type, security_id).
// The holding_id itself is not stored on the order — the join is on the
// tuple. ordersByHolding filters orders via a listing join (listing.security_id
// matches the holding's security_id for the same security_type).
func (r *OrderTransactionRepository) ListByHolding(
	userID uint64,
	systemType, securityType string,
	securityID uint64,
	direction string,
	page, pageSize int,
) ([]HoldingTransactionRow, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}

	// Base query: transactions → orders → listings; orders scoped by owner,
	// listings matched on (security_id, security_type) so cross-account
	// orders for the same security flow through.
	base := r.db.
		Table("order_transactions").
		Joins("JOIN orders ON orders.id = order_transactions.order_id").
		Joins("JOIN listings ON listings.id = orders.listing_id").
		Where("orders.user_id = ? AND orders.system_type = ?", userID, systemType).
		Where("listings.security_id = ? AND listings.security_type = ?", securityID, securityType)

	if direction != "" {
		base = base.Where("orders.direction = ?", direction)
	}

	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	type joinRow struct {
		ID              uint64
		OrderID         uint64
		ExecutedAt      time.Time
		Quantity        int64
		PricePerUnit    decimal.Decimal
		NativeAmount    *decimal.Decimal
		NativeCurrency  string
		ConvertedAmount *decimal.Decimal
		AccountCurrency string
		FxRate          *decimal.Decimal
		Direction       string
		Commission      decimal.Decimal
		AccountID       uint64
		Ticker          string
	}
	var rows []joinRow
	if err := base.
		Select(`order_transactions.id            AS id,
		        order_transactions.order_id      AS order_id,
		        order_transactions.executed_at   AS executed_at,
		        order_transactions.quantity      AS quantity,
		        order_transactions.price_per_unit AS price_per_unit,
		        order_transactions.native_amount AS native_amount,
		        order_transactions.native_currency AS native_currency,
		        order_transactions.converted_amount AS converted_amount,
		        order_transactions.account_currency AS account_currency,
		        order_transactions.fx_rate       AS fx_rate,
		        orders.direction                 AS direction,
		        orders.commission                AS commission,
		        orders.account_id                AS account_id,
		        orders.ticker                    AS ticker`).
		Order("order_transactions.executed_at DESC").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Scan(&rows).Error; err != nil {
		return nil, 0, err
	}

	out := make([]HoldingTransactionRow, len(rows))
	for i, row := range rows {
		out[i] = HoldingTransactionRow{
			ID:              row.ID,
			OrderID:         row.OrderID,
			ExecutedAt:      row.ExecutedAt,
			Direction:       row.Direction,
			Quantity:        row.Quantity,
			PricePerUnit:    row.PricePerUnit,
			NativeAmount:    row.NativeAmount,
			NativeCurrency:  row.NativeCurrency,
			ConvertedAmount: row.ConvertedAmount,
			AccountCurrency: row.AccountCurrency,
			FxRate:          row.FxRate,
			Commission:      row.Commission,
			AccountID:       row.AccountID,
			Ticker:          row.Ticker,
		}
	}
	return out, total, nil
}
