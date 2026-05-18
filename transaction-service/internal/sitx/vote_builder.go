// Package sitx contains transaction-service-side helpers for the SI-TX
// peer protocol: vote building, posting execution, outbound HTTP calls.
// Wire types live in `contract/sitx` and are imported under the alias
// `contractsitx` to avoid a name collision.
package sitx

import (
	contractsitx "github.com/exbanka/contract/sitx"
	"github.com/shopspring/decimal"
)

// BuildPrelimVote runs cheap, in-process validation on an inbound NEW_TX:
// rejects empty postings, rejects postings that don't balance per assetId.
// More expensive checks (account existence, asset acceptability, sufficient
// funds) require account-service calls and are executed by posting_executor
// inside the same DB transaction as the resource reservation.
func BuildPrelimVote(postings []contractsitx.Posting) contractsitx.TransactionVote {
	if len(postings) == 0 {
		return contractsitx.TransactionVote{
			Type:    contractsitx.VoteNo,
			NoVotes: []contractsitx.NoVote{{Reason: contractsitx.NoVoteReasonUnbalancedTx}},
		}
	}
	netByAsset := map[string]decimal.Decimal{}
	for _, p := range postings {
		net := netByAsset[p.AssetID]
		switch p.Direction {
		case contractsitx.DirectionDebit:
			net = net.Sub(p.Amount)
		case contractsitx.DirectionCredit:
			net = net.Add(p.Amount)
		}
		netByAsset[p.AssetID] = net
	}
	for _, n := range netByAsset {
		if !n.IsZero() {
			return contractsitx.TransactionVote{
				Type:    contractsitx.VoteNo,
				NoVotes: []contractsitx.NoVote{{Reason: contractsitx.NoVoteReasonUnbalancedTx}},
			}
		}
	}
	return contractsitx.TransactionVote{Type: contractsitx.VoteYes}
}
