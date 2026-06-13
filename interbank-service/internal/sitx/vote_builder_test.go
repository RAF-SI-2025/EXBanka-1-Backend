package sitx_test

import (
	"testing"

	contractsitx "github.com/exbanka/contract/sitx"
	"github.com/exbanka/interbank-service/internal/sitx"
)

// mkPosting builds a MONAS InternalPosting (the executor/vote-builder working
// form): magnitude-string Amount + explicit account/asset type tags.
func mkPosting(rn int64, acct, asset, amount, dir string) contractsitx.InternalPosting {
	return contractsitx.InternalPosting{
		RoutingNumber: rn,
		AccountType:   "ACCOUNT",
		AccountID:     acct,
		AssetType:     "MONAS",
		AssetID:       asset,
		Amount:        amount,
		Direction:     dir,
	}
}

func TestVoteBuilder_BalancedYes(t *testing.T) {
	postings := []contractsitx.InternalPosting{
		mkPosting(222, "acc-A", "RSD", "100.00", contractsitx.DirectionDebit),
		mkPosting(111, "acc-B", "RSD", "100.00", contractsitx.DirectionCredit),
	}
	vote := sitx.BuildPrelimVote(postings)
	if vote.Type != contractsitx.VoteYes || len(vote.NoVotes) != 0 {
		t.Errorf("expected YES with 0 noVotes, got %+v", vote)
	}
}

func TestVoteBuilder_UnbalancedSingleAsset(t *testing.T) {
	postings := []contractsitx.InternalPosting{
		mkPosting(222, "acc-A", "RSD", "100.00", contractsitx.DirectionDebit),
		mkPosting(111, "acc-B", "RSD", "90.00", contractsitx.DirectionCredit),
	}
	vote := sitx.BuildPrelimVote(postings)
	if vote.Type != contractsitx.VoteNo {
		t.Fatalf("expected NO, got %+v", vote)
	}
	if len(vote.NoVotes) != 1 || vote.NoVotes[0].Reason != contractsitx.NoVoteReasonUnbalancedTx {
		t.Errorf("expected single UNBALANCED_TX, got %+v", vote.NoVotes)
	}
	// UNBALANCED_TX is a whole-transaction reason → no posting index.
	if vote.NoVotes[0].Posting != nil {
		t.Errorf("UNBALANCED_TX must not carry a posting index, got %v", *vote.NoVotes[0].Posting)
	}
}

func TestVoteBuilder_UnbalancedMultiAsset(t *testing.T) {
	postings := []contractsitx.InternalPosting{
		mkPosting(222, "acc-A", "RSD", "100.00", contractsitx.DirectionDebit),
		mkPosting(111, "acc-B", "RSD", "100.00", contractsitx.DirectionCredit),
		mkPosting(222, "acc-C", "EUR", "50.00", contractsitx.DirectionDebit),
	}
	vote := sitx.BuildPrelimVote(postings)
	if vote.Type != contractsitx.VoteNo {
		t.Fatalf("expected NO, got %+v", vote)
	}
}

func TestVoteBuilder_EmptyPostings(t *testing.T) {
	vote := sitx.BuildPrelimVote(nil)
	if vote.Type != contractsitx.VoteNo {
		t.Errorf("empty postings should be NO, got %+v", vote)
	}
}
