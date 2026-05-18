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

	// Phase 10 — cross-bank cascade-cancel grouping. When the bidder
	// discovered this listing via /public-option-offers, they capture
	// the listing's offerId here and pass it through; both banks' local
	// peer_otc_negotiations rows store it. Cascade on accept matches on
	// this exact key (NOT on ticker+date, which would cause false
	// positives for two distinct same-ticker listings).
	//
	// omitempty so free-form negotiations (no discovery) leave it
	// unset — backwards-compatible with peer banks that don't yet emit
	// this field. A zero RoutingNumber + empty ID means "no parent".
	ParentOfferID ForeignBankId `json:"parentOfferId,omitempty"`

	// Fix #1 (2026-05-16) — the buyer's pre-bound bank account number on
	// THEIR bank. When set, the seller's bank uses this exact account
	// number for the buyer-debit posting on accept, instead of resolving
	// the "client-<id>" participant id to "first active account in the
	// requested currency". omitempty so peer banks that haven't upgraded
	// can still bid (their requests fall back to the legacy participant-
	// id resolution path in transaction-service/sitx/posting_executor).
	// Format is the bank's 18-digit account number (e.g. "111000164448338821").
	BuyerAccountNumber string `json:"buyerAccountNumber,omitempty"`
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
//
// Intent is a local extension (cohort partners ignore unknown fields)
// that differentiates an accept TX (intent="" or "accept") from an
// exercise TX (intent="exercise"). On exercise, the same 4-posting
// envelope reuses the option's terms but tells each bank's executor
// "transition the existing contract to exercised + run holding ops"
// rather than "form a new contract + lock seller holdings".
type OptionDescription struct {
	Ticker         string          `json:"ticker"`
	Amount         int64           `json:"amount"`
	StrikePrice    decimal.Decimal `json:"strikePrice"`
	Currency       string          `json:"currency"`
	SettlementDate string          `json:"settlementDate"`
	NegotiationID  ForeignBankId   `json:"negotiationId"`
	Intent         string          `json:"intent,omitempty"`
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

// PublicOptionOffersResponse is the response shape of
// GET /api/v3/public-option-offers — Phase 6 cross-bank option
// discovery (parallel to /public-stock).
type PublicOptionOffersResponse struct {
	Offers []PublicOptionOffer `json:"offers"`
}

// PublicOptionOffer is one OPEN OTC option listing on a peer bank.
// Discovering banks then drive negotiation via the existing
// POST /api/v3/me/peer-otc/negotiations using offerId.routingNumber
// as seller_bank_code and sellerId.id as seller_id.
//
// Money values use the canonical {amount, currency} pair so the
// discovering UI can render strike + premium without inferring
// currency from the listing's underlying.
type PublicOptionOffer struct {
	OfferID         ForeignBankId   `json:"offerId"`
	Ticker          string          `json:"ticker"`
	Amount          int64           `json:"amount"`
	StrikePrice     decimal.Decimal `json:"strikePrice"`
	StrikeCurrency  string          `json:"strikeCurrency"`
	Premium         decimal.Decimal `json:"premium"`
	PremiumCurrency string          `json:"premiumCurrency"`
	SettlementDate  string          `json:"settlementDate"`
	SellerID        ForeignBankId   `json:"sellerId"`
	Direction       string          `json:"direction"` // "sell_initiated" | "buy_initiated"
	CreatedAt       string          `json:"createdAt"`
	LastModifiedBy  ForeignBankId   `json:"lastModifiedBy"`

	// Best-bid / best-ask surface (Part A 2026-05-16). STRICTLY
	// OPTIONAL — peers that don't upgrade omit these fields and the
	// JSON unmarshaller leaves them as zero values, which we treat
	// as "not reported" in the cache projection. Publishers populate
	// from their local OTCNegotiationRepository.AggregateActiveBidsByOffer.
	BestBid           string `json:"bestBid,omitempty"`
	BestAsk           string `json:"bestAsk,omitempty"`
	ActiveChainsCount int32  `json:"activeChainsCount,omitempty"`
}
