package service

import (
	"github.com/shopspring/decimal"
)

// DerivedListingData holds computed fields for a listing.
// These are NOT stored in the DB — calculated on-the-fly from listing + security data.
type DerivedListingData struct {
	ContractSize      int64           `json:"contract_size"`
	MaintenanceMargin decimal.Decimal `json:"maintenance_margin"`
	InitialMarginCost decimal.Decimal `json:"initial_margin_cost"`
	ChangePercent     decimal.Decimal `json:"change_percent"`
	DollarVolume      decimal.Decimal `json:"dollar_volume"`
	NominalValue      decimal.Decimal `json:"nominal_value"`
	MarketCap         decimal.Decimal `json:"market_cap,omitempty"` // stocks only
}

// CalculateDerivedData computes all derived fields for a listing.
// securityType: "stock", "futures", "forex"
// price: listing current price
// change: listing change value
// volume: listing volume
// contractSizeOverride: used for futures (from FuturesContract.ContractSize). 0 = use default.
// outstandingShares: stocks only (for MarketCap). 0 = not applicable.
// stockPrice: options only (underlying stock price for margin). Zero = not applicable.
func CalculateDerivedData(
	securityType string,
	price decimal.Decimal,
	change decimal.Decimal,
	volume int64,
	contractSizeOverride int64,
	outstandingShares int64,
	stockPrice decimal.Decimal,
) DerivedListingData {
	d := DerivedListingData{}

	// Determine ContractSize
	switch securityType {
	case "stock":
		d.ContractSize = 1
	case "futures":
		if contractSizeOverride > 0 {
			d.ContractSize = contractSizeOverride
		} else {
			d.ContractSize = 1
		}
	case "forex":
		d.ContractSize = 1000
	default:
		d.ContractSize = 1
	}
	cs := decimal.NewFromInt(d.ContractSize)

	// MaintenanceMargin (per security type)
	switch securityType {
	case "stock":
		// 50% × Price
		d.MaintenanceMargin = price.Mul(decimal.NewFromFloat(0.50))
	case "futures":
		// ContractSize × Price × 10%
		d.MaintenanceMargin = cs.Mul(price).Mul(decimal.NewFromFloat(0.10))
	case "forex":
		// ContractSize × Price × 10%
		d.MaintenanceMargin = cs.Mul(price).Mul(decimal.NewFromFloat(0.10))
	default:
		d.MaintenanceMargin = decimal.Zero
	}

	// InitialMarginCost = MaintenanceMargin × 1.1
	d.InitialMarginCost = d.MaintenanceMargin.Mul(decimal.NewFromFloat(1.1))

	// ChangePercent = 100 × Change / (Price − Change)
	denominator := price.Sub(change)
	if !denominator.IsZero() {
		d.ChangePercent = change.Mul(decimal.NewFromInt(100)).Div(denominator).Round(4)
	}

	// DollarVolume = Volume × Price
	d.DollarVolume = decimal.NewFromInt(volume).Mul(price)

	// NominalValue = ContractSize × Price
	d.NominalValue = cs.Mul(price)

	// MarketCap = OutstandingShares × Price (stocks only)
	if securityType == "stock" && outstandingShares > 0 {
		d.MarketCap = decimal.NewFromInt(outstandingShares).Mul(price)
	}

	return d
}

// CalculateOptionDerivedData computes derived fields for an option.
// Options don't have listings, but the data is needed for the option detail view.
func CalculateOptionDerivedData(
	premium decimal.Decimal,
	stockPrice decimal.Decimal,
) DerivedListingData {
	contractSize := int64(100) // standardized for stock options
	cs := decimal.NewFromInt(contractSize)

	// MaintenanceMargin = ContractSize × 50% × StockPrice
	maintenanceMargin := cs.Mul(stockPrice).Mul(decimal.NewFromFloat(0.50))

	return DerivedListingData{
		ContractSize:      contractSize,
		MaintenanceMargin: maintenanceMargin,
		InitialMarginCost: maintenanceMargin.Mul(decimal.NewFromFloat(1.1)),
		NominalValue:      cs.Mul(premium),
	}
}
