// Package orderkind holds the string discriminator values used in
// account-service's account_reservations.order_kind column. The column
// disambiguates which caller-namespace `order_id` belongs to so two
// different callers (e.g. stock-service Order.ID vs OptionContract.ID,
// both starting at 1) cannot collide on the (order_id, order_kind)
// unique index.
//
// Account-service mirrors these values in its own model package as
// `model.OrderKind*` constants. Keep both in sync.
package orderkind

const (
	// StockOrder — stock placement / forex fill / portfolio fill paths
	// (Order.ID).
	StockOrder = "stock_order"

	// OTCPremium — OTC option accept saga, premium reservation
	// (OptionContract.ID).
	OTCPremium = "otc_premium"

	// OTCStrike — OTC option exercise saga, strike reservation
	// (OptionContract.ID).
	OTCStrike = "otc_strike"

	// OTCStockBuy — OTC stock buy-offer cash reservation
	// (OTCStockBuyOffer.AccountReservationOrderID).
	OTCStockBuy = "otc_stock_buy"
)
