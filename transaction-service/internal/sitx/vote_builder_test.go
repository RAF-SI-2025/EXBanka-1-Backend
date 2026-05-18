package sitx_test

import (
	"testing"

	contractsitx "github.com/exbanka/contract/sitx"
	"github.com/exbanka/transaction-service/internal/sitx"
	"github.com/shopspring/decimal"
)

func mkPosting(rn int64, acct, asset, amount, dir string) contractsitx.Posting {
	return contractsitx.Posting{
		RoutingNumber: rn,
		AccountID:     acct,
		AssetID:       asset,
		Amount:        decimal.RequireFromString(amount),
		Direction:     dir,
	}
}

func TestVoteBuilder_BalancedYes(t *testing.T) {
	postings := []contractsitx.Posting{
		mkPosting(222, "acc-A", "RSD", "100.00", contractsitx.DirectionDebit),
		mkPosting(111, "acc-B", "RSD", "100.00", contractsitx.DirectionCredit),
	}
	vote := sitx.BuildPrelimVote(postings)
	if vote.Type != contractsitx.VoteYes || len(vote.NoVotes) != 0 {
		t.Errorf("expected YES with 0 noVotes, got %+v", vote)
	}
}

func TestVoteBuilder_UnbalancedSingleAsset(t *testing.T) {
	postings := []contractsitx.Posting{
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
}

func TestVoteBuilder_UnbalancedMultiAsset(t *testing.T) {
	postings := []contractsitx.Posting{
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
