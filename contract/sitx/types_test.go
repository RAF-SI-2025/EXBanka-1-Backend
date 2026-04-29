package sitx_test

import (
	"encoding/json"
	"testing"

	"github.com/exbanka/contract/sitx"
	"github.com/shopspring/decimal"
)

func TestMessage_RoundTripNewTx(t *testing.T) {
	in := sitx.Message[sitx.Transaction]{
		IdempotenceKey: sitx.IdempotenceKey{
			RoutingNumber:       111,
			LocallyGeneratedKey: "abc-123",
		},
		MessageType: sitx.MessageTypeNewTx,
		Message: sitx.Transaction{
			Postings: []sitx.Posting{
				{RoutingNumber: 111, AccountID: "111000001", AssetID: "RSD", Amount: decimal.NewFromInt(100), Direction: sitx.DirectionDebit},
				{RoutingNumber: 222, AccountID: "222000002", AssetID: "RSD", Amount: decimal.NewFromInt(100), Direction: sitx.DirectionCredit},
			},
		},
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out sitx.Message[sitx.Transaction]
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.MessageType != sitx.MessageTypeNewTx {
		t.Errorf("MessageType: got %q, want %q", out.MessageType, sitx.MessageTypeNewTx)
	}
	if out.IdempotenceKey.RoutingNumber != 111 {
		t.Errorf("routing: got %d", out.IdempotenceKey.RoutingNumber)
	}
	if len(out.Message.Postings) != 2 {
		t.Errorf("postings: got %d", len(out.Message.Postings))
	}
	if !out.Message.Postings[0].Amount.Equal(decimal.NewFromInt(100)) {
		t.Errorf("amount: got %s", out.Message.Postings[0].Amount)
	}
}

func TestTransactionVote_NoVoteShape(t *testing.T) {
	v := sitx.TransactionVote{
		Type: sitx.VoteNo,
		NoVotes: []sitx.NoVote{
			{Reason: sitx.NoVoteReasonInsufficientAsset, Posting: ptr(0)},
			{Reason: sitx.NoVoteReasonNoSuchAccount, Posting: ptr(1)},
		},
	}
	raw, _ := json.Marshal(v)
	var got sitx.TransactionVote
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Type != sitx.VoteNo {
		t.Errorf("type: got %q", got.Type)
	}
	if len(got.NoVotes) != 2 || got.NoVotes[0].Reason != sitx.NoVoteReasonInsufficientAsset {
		t.Errorf("noVotes: got %+v", got.NoVotes)
	}
}

func ptr(i int) *int { return &i }
