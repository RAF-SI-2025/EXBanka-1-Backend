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
// rejects empty postings, rejects postings that don't balance per asset.
// More expensive checks (account existence, asset acceptability, sufficient
// funds) require account-service calls and are executed by posting_executor
// inside the same DB transaction as the resource reservation.
//
// Postings are the executor's InternalPosting form: Direction is the internal
// effect (DEBIT = asset leaves, CREDIT = asset arrives) and Amount is a
// non-negative magnitude string. To balance per the spec we reconstruct the
// spec-signed amount (DEBIT → negative, CREDIT → positive) and sum per
// asset key (AssetType + ":" + AssetID). UNBALANCED_TX is a whole-transaction
// reason, so it carries no posting index.
func BuildPrelimVote(postings []contractsitx.InternalPosting) Vote {
	if len(postings) == 0 {
		return Vote{
			Type:    contractsitx.VoteNo,
			NoVotes: []NoVote{{Reason: contractsitx.NoVoteReasonUnbalancedTx}},
		}
	}
	netByAsset := map[string]decimal.Decimal{}
	for _, p := range postings {
		amt, err := decimal.NewFromString(p.Amount)
		if err != nil {
			return Vote{
				Type:    contractsitx.VoteNo,
				NoVotes: []NoVote{{Reason: contractsitx.NoVoteReasonUnbalancedTx}},
			}
		}
		signed := amt.Abs()
		if p.Direction == contractsitx.DirectionDebit {
			signed = signed.Neg() // internal DEBIT ↔ spec negative (asset leaves)
		}
		key := p.AssetType + ":" + p.AssetID
		netByAsset[key] = netByAsset[key].Add(signed)
	}
	for _, n := range netByAsset {
		if !n.IsZero() {
			return Vote{
				Type:    contractsitx.VoteNo,
				NoVotes: []NoVote{{Reason: contractsitx.NoVoteReasonUnbalancedTx}},
			}
		}
	}
	return Vote{Type: contractsitx.VoteYes}
}
