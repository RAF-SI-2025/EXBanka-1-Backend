package sitx

// Vote is the transaction-service-internal result of evaluating an inbound
// NEW_TX. It carries the YES/NO verdict plus, on NO, one or more NoVote
// reasons. This is DISTINCT from the spec wire type contractsitx.TransactionVote
// (which serialises {vote, reasons[].posting}) — the internal form references
// the offending posting by 0-based INDEX (NoVote.Posting), which the gateway
// re-expands into the full wire posting before sending the vote on the wire.
//
// Vote values reuse the spec constants contractsitx.VoteYes / VoteNo.
type Vote struct {
	Type    string
	NoVotes []NoVote
}

// NoVote is one NO reason. Posting is the 0-based index of the offending
// posting within the NEW_TX's posting list, or nil for whole-transaction
// reasons (e.g. UNBALANCED_TX).
type NoVote struct {
	Reason  string
	Posting *int
}
