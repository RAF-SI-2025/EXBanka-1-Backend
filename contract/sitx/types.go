// Package sitx defines the SI-TX cohort wire-protocol types for cross-bank
// communication. Used by api-gateway (decoding inbound `Message<Type>`
// envelopes on POST /api/v3/interbank), transaction-service (executing
// posting-based TXs), and stock-service (cross-bank OTC negotiations,
// Phase 4). Shape is verbatim from https://arsen.srht.site/si-tx-proto/.
package sitx

// MessageType discriminator values carried in a Message envelope.
const (
	MessageTypeNewTx      = "NEW_TX"
	MessageTypeCommitTx   = "COMMIT_TX"
	MessageTypeRollbackTx = "ROLLBACK_TX"
)

// Posting direction values. NOTE: the spec wire Posting has NO direction field
// (sign of Amount encodes it). These constants name the INTERNAL effect used by
// the protobuf SiTxPosting and the executor/vote-builder, not a wire field.
const (
	DirectionDebit  = "DEBIT"
	DirectionCredit = "CREDIT"
)

// SiTxPosting account-type tags (§2.7 account tagged-union discriminator).
// The outbound wire builder uses these to construct the spec `account` union,
// and the executor uses them to resolve the target. PERSON/OPTION resolve to a
// participant id; ACCOUNT resolves to a raw bank account number.
const (
	AccountTypePerson  = "PERSON"
	AccountTypeAccount = "ACCOUNT"
	AccountTypeOption  = "OPTION"
)

// SiTxPosting asset-type tags (§2.7 asset tagged-union discriminator). The
// executor detects an option leg via AssetType=="OPTION" (not by sniffing the
// asset_id), and the wire builder uses these to construct the spec `asset` union.
const (
	AssetTypeMonas  = "MONAS"
	AssetTypeStock  = "STOCK"
	AssetTypeOption = "OPTION"
)

// TransactionVote.Vote values.
const (
	VoteYes = "YES"
	VoteNo  = "NO"
)

// NoVote reason codes from SI-TX. Receivers may emit one or more of these
// inside a TransactionVote when voting NO on a NEW_TX.
const (
	NoVoteReasonUnbalancedTx              = "UNBALANCED_TX"
	NoVoteReasonNoSuchAccount             = "NO_SUCH_ACCOUNT"
	NoVoteReasonNoSuchAsset               = "NO_SUCH_ASSET"
	NoVoteReasonUnacceptableAsset         = "UNACCEPTABLE_ASSET"
	NoVoteReasonInsufficientAsset         = "INSUFFICIENT_ASSET"
	NoVoteReasonOptionAmountIncorrect     = "OPTION_AMOUNT_INCORRECT"
	NoVoteReasonOptionUsedOrExpired       = "OPTION_USED_OR_EXPIRED"
	NoVoteReasonOptionNegotiationNotFound = "OPTION_NEGOTIATION_NOT_FOUND"
)

// IdempotenceKey is the SI-TX-mandated key that pins identity for a
// `Message<Type>`. Sender generates `LocallyGeneratedKey` (max 64 bytes
// per spec); receiver tracks indefinitely so retries return the cached
// vote rather than re-executing the TX.
type IdempotenceKey struct {
	RoutingNumber       int64  `json:"routingNumber"`
	LocallyGeneratedKey string `json:"locallyGeneratedKey"`
}

// Message is the envelope every SI-TX HTTP request carries. T parametrises
// over the body shape (Transaction for NEW_TX, CommitTransaction for
// COMMIT_TX, RollbackTransaction for ROLLBACK_TX, etc.).
type Message[T any] struct {
	IdempotenceKey IdempotenceKey `json:"idempotenceKey"`
	MessageType    string         `json:"messageType"`
	Message        T              `json:"message"`
}

// TxAccount is the SI-TX tagged union (§2.6).
type TxAccount struct {
	Type string         `json:"type"`          // "PERSON" | "ACCOUNT" | "OPTION"
	ID   *ForeignBankId `json:"id,omitempty"`  // PERSON, OPTION
	Num  string         `json:"num,omitempty"` // ACCOUNT
}

// MonetaryAsset is the §2.7 monetary asset payload (asset Type "MONAS").
type MonetaryAsset struct {
	Currency string `json:"currency"`
}

// MonetaryValue is the §2.5 {amount, currency} money value used inside
// OptionDescription.pricePerUnit. Amount is a bare JSON number (DecimalNumber).
type MonetaryValue struct {
	Amount   DecimalNumber `json:"amount"`
	Currency string        `json:"currency"`
}

// StockDescription is the §2.7 stock asset payload (asset Type "STOCK").
type StockDescription struct {
	Ticker string `json:"ticker"`
}

// Asset is the §2.7 tagged union (holds MonetaryAsset, StockDescription, or
// OptionDescription depending on Type). NOTE: on unmarshal, Asset.Asset is
// populated as map[string]interface{}, NOT the concrete type — callers (e.g.
// mapping.go) must switch on Type and re-marshal/unmarshal into the concrete
// type, or read fields out of the map.
type Asset struct {
	Type  string      `json:"type"`
	Asset interface{} `json:"asset"`
}

// Posting is one §2.8.1 double-entry leg. Amount is SIGNED (negative=credit/asset
// leaves, positive=debit/asset arrives) and serializes as a JSON number.
type Posting struct {
	Account TxAccount     `json:"account"`
	Amount  DecimalNumber `json:"amount"`
	Asset   Asset         `json:"asset"`
}

// Transaction is the body of a NEW_TX message (§2.8.2).
type Transaction struct {
	Postings       []Posting     `json:"postings"`
	TransactionID  ForeignBankId `json:"transactionId"`
	Message        string        `json:"message"`
	CallNumber     string        `json:"callNumber,omitempty"`
	PaymentCode    string        `json:"paymentCode"`
	PaymentPurpose string        `json:"paymentPurpose"`
}

// CommitTransaction is the body of a COMMIT_TX message (§2.12.2); it references
// the initiator's transactionId as a ForeignBankId.
type CommitTransaction struct {
	TransactionID ForeignBankId `json:"transactionId"`
}

// RollbackTransaction is the body of a ROLLBACK_TX message (§2.12.3).
type RollbackTransaction struct {
	TransactionID ForeignBankId `json:"transactionId"`
}

// NoVoteReason (§2.12.1). Posting is the FULL offending posting (not an index).
type NoVoteReason struct {
	Reason  string   `json:"reason"`
	Posting *Posting `json:"posting,omitempty"`
}

// TransactionVote is the NEW_TX response (§2.12.1).
type TransactionVote struct {
	Vote    string         `json:"vote"`
	Reasons []NoVoteReason `json:"reasons,omitempty"`
}
