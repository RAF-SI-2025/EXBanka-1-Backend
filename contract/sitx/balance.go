package sitx

import "github.com/shopspring/decimal"

// IsBalanced reports whether, for every asset, the signed amounts of all
// postings sum to zero (SI-TX §2.8). Assets are keyed by type+id so MONAS:RSD
// and STOCK:AAPL are checked independently. A transaction that is not balanced
// must be rejected with the UNBALANCED_TX NoVote reason.
func IsBalanced(postings []Posting) bool {
	sums := map[string]decimal.Decimal{}
	for _, p := range postings {
		id, err := assetToID(p.Asset)
		if err != nil {
			return false
		}
		key := p.Asset.Type + ":" + id
		sums[key] = sums[key].Add(p.Amount.Decimal)
	}
	for _, s := range sums {
		if !s.IsZero() {
			return false
		}
	}
	return true
}
