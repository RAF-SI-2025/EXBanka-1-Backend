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

// OtcOffer is the INTERNAL flat mirror of an OTC offer (stored in
// RemoteOfferJSON, carried over gRPC). It is NOT the SI-TX wire shape: the spec
// OtcOffer (§8) nests stock/pricePerUnit/premium and carries buyerId + sellerId.
// The wire↔internal translation lives at the api-gateway boundary
// (peerOtcOfferReq/offerReqToProto inbound, protoOfferToJSON outbound) and in
// stock-service's outbound offer map. Do NOT marshal this struct straight to a
// peer — that would emit the non-conformant flat dialect.
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

// (Removed) The former `OtcNegotiation` struct was a non-conformant, unused
// mirror shape ({id,buyerId,sellerId,offer,status,updatedAt}). The SI-TX wire
// GET /negotiations/{rn}/{id} response is `OtcOffer & { isOngoing }` (§8.4),
// which the api-gateway composes inline (protoOfferToJSON + isOngoing); the
// reconciler reads only {isOngoing}. Keeping a divergent typed struct invited
// emitting the wrong shape, so it was deleted (2026-06-06 spec-purity pass).

// OptionDescription is the §2.7.2 option asset payload (asset Type "OPTION").
// Spec shape: nested stock + pricePerUnit, no internal "intent" field — the
// transaction SHAPE (OPTION asset = accept; OPTION pseudo-account = exercise)
// encodes the operation, per the design doc.
type OptionDescription struct {
	NegotiationID  ForeignBankId    `json:"negotiationId"`
	Stock          StockDescription `json:"stock"`
	PricePerUnit   MonetaryValue    `json:"pricePerUnit"`
	SettlementDate string           `json:"settlementDate"`
	Amount         int64            `json:"amount"`
}

// Option intents are INTERNAL ONLY — never serialized to the wire. The
// receiver derives accept vs exercise from transaction shape (OPTION asset
// vs OPTION pseudo-account) and passes the right intent to RecordOptionContract.
const (
	OptionIntentAccept   = "accept"
	OptionIntentExercise = "exercise"
)

// UserInformation is the response shape of GET /user/{rid}/{id} (SI-TX §3.7).
type UserInformation struct {
	BankDisplayName string `json:"bankDisplayName"`
	DisplayName     string `json:"displayName"`
}

// PublicSeller is one seller of a public stock (§3.1).
type PublicSeller struct {
	Seller ForeignBankId `json:"seller"`
	Amount int64         `json:"amount"`
}

// PublicStock groups all sellers of one ticker (§3.1).
type PublicStock struct {
	Stock   StockDescription `json:"stock"`
	Sellers []PublicSeller   `json:"sellers"`
}

// PublicStocksResponse is the §3.1 response: a BARE array.
type PublicStocksResponse []PublicStock

// PublicOptionOffersResponse is the response shape of
// GET /api/v3/public-option-offers — Phase 6 cross-bank option
// discovery (parallel to /public-stock).
type PublicOptionOffersResponse struct {
	Offers []PublicOptionOffer `json:"offers"`
}

// PublicOptionOffer is one OPEN OTC option listing on a peer bank.
// Discovering banks then drive negotiation via the unified
// POST /api/v3/otc/options/{id}/bid; stock-service dispatches the
// cross-bank negotiation using offerId.routingNumber as the seller
// bank and sellerId.id as the seller (SP-2b folded the retired
// /me/peer-otc/negotiations client route into the unified surface).
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
