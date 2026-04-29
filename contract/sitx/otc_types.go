package sitx

import "github.com/shopspring/decimal"

// ForeignBankId is the (routingNumber, id) tuple used by SI-TX wherever a
// resource needs to be identified across banks (negotiations, users,
// public-stock entries). The routingNumber locates the owning bank; the
// id is the bank-local identifier (string for flexibility).
type ForeignBankId struct {
	RoutingNumber int64  `json:"routingNumber"`
	ID            string `json:"id"`
}

// OtcOffer is the in-flight SI-TX offer body. Sent on POST /negotiations
// (initial offer) and PUT /negotiations/{rid}/{id} (counter-offers).
type OtcOffer struct {
	Ticker          string          `json:"ticker"`
	Amount          int64           `json:"amount"`
	PricePerStock   decimal.Decimal `json:"pricePerStock"`
	Currency        string          `json:"currency"`
	Premium         decimal.Decimal `json:"premium"`
	PremiumCurrency string          `json:"premiumCurrency"`
	SettlementDate  string          `json:"settlementDate"`
	LastModifiedBy  ForeignBankId   `json:"lastModifiedBy"`
}

// OtcNegotiation is the full negotiation record (offer + meta).
type OtcNegotiation struct {
	ID        ForeignBankId `json:"id"`
	BuyerID   ForeignBankId `json:"buyerId"`
	SellerID  ForeignBankId `json:"sellerId"`
	Offer     OtcOffer      `json:"offer"`
	Status    string        `json:"status"` // ongoing | accepted | cancelled | expired
	UpdatedAt string        `json:"updatedAt"`
}

// OptionDescription is the SI-TX `assetId` shape for option-contract
// postings inside a NEW_TX. When acceptance triggers TX formation, the
// 4 postings reference the option's terms via this struct (encoded as
// JSON in the assetId field per cohort convention).
type OptionDescription struct {
	Ticker         string          `json:"ticker"`
	Amount         int64           `json:"amount"`
	StrikePrice    decimal.Decimal `json:"strikePrice"`
	Currency       string          `json:"currency"`
	SettlementDate string          `json:"settlementDate"`
	NegotiationID  ForeignBankId   `json:"negotiationId"`
}

// UserInformation is the response shape of GET /user/{rid}/{id}.
type UserInformation struct {
	ID        ForeignBankId `json:"id"`
	FirstName string        `json:"firstName"`
	LastName  string        `json:"lastName"`
}

// PublicStocksResponse is the response shape of GET /public-stock.
type PublicStocksResponse struct {
	Stocks []PublicStock `json:"stocks"`
}

// PublicStock is one entry in PublicStocksResponse — a stock holding the
// owner has flagged as public on this bank, available for OTC offers
// from peer banks.
type PublicStock struct {
	OwnerID       ForeignBankId   `json:"ownerId"`
	Ticker        string          `json:"ticker"`
	Amount        int64           `json:"amount"`
	PricePerStock decimal.Decimal `json:"pricePerStock"`
	Currency      string          `json:"currency"`
}
