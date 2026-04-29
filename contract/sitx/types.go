// Package sitx defines the SI-TX cohort wire-protocol types for cross-bank
// communication. Used by api-gateway (decoding inbound `Message<Type>`
// envelopes on POST /api/v3/interbank), transaction-service (executing
// posting-based TXs), and stock-service (cross-bank OTC negotiations,
// Phase 4). Shape is verbatim from https://arsen.srht.site/si-tx-proto/.
package sitx

import (
	"github.com/shopspring/decimal"
)

// MessageType discriminator values carried in a Message envelope.
const (
	MessageTypeNewTx      = "NEW_TX"
	MessageTypeCommitTx   = "COMMIT_TX"
	MessageTypeRollbackTx = "ROLLBACK_TX"
)

// Posting direction values.
const (
	DirectionDebit  = "DEBIT"
	DirectionCredit = "CREDIT"
)

// TransactionVote.Type values.
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

// Posting is one side of a double-entry TX leg. SI-TX requires that the
// sum of debits equals the sum of credits per assetId across the postings
// of a Transaction (UNBALANCED_TX is rejected with a NoVote).
type Posting struct {
	RoutingNumber int64           `json:"routingNumber"`
	AccountID     string          `json:"accountId"`
	AssetID       string          `json:"assetId"`
	Amount        decimal.Decimal `json:"amount"`
	Direction     string          `json:"direction"`
}

// Transaction is the body of a NEW_TX message.
type Transaction struct {
	Postings []Posting `json:"postings"`
}

// CommitTransaction is the body of a COMMIT_TX message.
type CommitTransaction struct {
	TransactionID string `json:"transactionId"`
}

// RollbackTransaction is the body of a ROLLBACK_TX message.
type RollbackTransaction struct {
	TransactionID string `json:"transactionId"`
}

// NoVote describes a single rejection reason. When applicable, Posting is
// the index into Transaction.Postings (0-based) that triggered the reason.
type NoVote struct {
	Reason  string `json:"reason"`
	Posting *int   `json:"posting,omitempty"`
}

// TransactionVote is the receiver's response to a NEW_TX message. When
// Type=YES, NoVotes is empty/omitted. When Type=NO, NoVotes lists every
// failing reason.
type TransactionVote struct {
	Type    string   `json:"type"`
	NoVotes []NoVote `json:"noVotes,omitempty"`
}
