package repository

import (
	"time"

	"github.com/shopspring/decimal"
)

type StockFilter struct {
	Search          string
	ExchangeAcronym string
	MinPrice        *decimal.Decimal
	MaxPrice        *decimal.Decimal
	MinVolume       *int64
	MaxVolume       *int64
	SortBy          string // "price", "volume", "change", "margin"
	SortOrder       string // "asc", "desc"
	Page            int
	PageSize        int
}

type FuturesFilter struct {
	Search             string
	ExchangeAcronym    string
	MinPrice           *decimal.Decimal
	MaxPrice           *decimal.Decimal
	MinVolume          *int64
	MaxVolume          *int64
	SettlementDateFrom *time.Time
	SettlementDateTo   *time.Time
	SortBy             string
	SortOrder          string
	Page               int
	PageSize           int
}

type ForexFilter struct {
	Search        string
	BaseCurrency  string
	QuoteCurrency string
	Liquidity     string
	SortBy        string
	SortOrder     string
	Page          int
	PageSize      int
}

type OptionFilter struct {
	StockID        *uint64
	OptionType     string // "call", "put", "" (both)
	SettlementDate *time.Time
	MinStrike      *decimal.Decimal
	MaxStrike      *decimal.Decimal
	Page           int
	PageSize       int
}

type OrderFilter struct {
	Status     string // "pending", "approved", "declined", "done"
	Direction  string // "buy", "sell"
	OrderType  string // "market", "limit", "stop", "stop_limit"
	AgentEmail string // for supervisor view
	Page       int
	PageSize   int
}

type HoldingFilter struct {
	SecurityType string
	Page         int
	PageSize     int
}

type OTCFilter struct {
	SecurityType string
	Ticker       string
	Page         int
	PageSize     int
}

type TaxFilter struct {
	UserType string // "client", "actuary"
	Search   string
	Page     int
	PageSize int
}

type AccountGainSummary struct {
	AccountID uint64
	Currency  string
	TotalGain decimal.Decimal
}

type TaxUserSummary struct {
	UserID         uint64
	SystemType     string
	UserFirstName  string
	UserLastName   string
	TotalDebtRSD   decimal.Decimal
	LastCollection *time.Time
}
