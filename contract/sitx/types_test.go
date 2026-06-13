package sitx_test

import (
	"encoding/json"
	"testing"

	"github.com/exbanka/contract/sitx"
	"github.com/shopspring/decimal"
)

func TestMessage_RoundTripNewTx(t *testing.T) {
	posting := func(num, currency string, amount int64) sitx.Posting {
		return sitx.Posting{
			Account: sitx.TxAccount{Type: "ACCOUNT", Num: num},
			Amount:  sitx.DecimalNumber{Decimal: decimal.NewFromInt(amount)},
			Asset:   sitx.Asset{Type: "MONAS", Asset: sitx.MonetaryAsset{Currency: currency}},
		}
	}
	in := sitx.Message[sitx.Transaction]{
		IdempotenceKey: sitx.IdempotenceKey{
			RoutingNumber:       111,
			LocallyGeneratedKey: "abc-123",
		},
		MessageType: sitx.MessageTypeNewTx,
		Message: sitx.Transaction{
			Postings: []sitx.Posting{
				posting("111000001", "RSD", -100),
				posting("222000002", "RSD", 100),
			},
			TransactionID:  sitx.ForeignBankId{RoutingNumber: 111, ID: "tx-abc"},
			Message:        "test transfer",
			PaymentCode:    "289",
			PaymentPurpose: "test",
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
	// Amount round-trips as a JSON number; sign is preserved.
	gotAmt := out.Message.Postings[0].Amount.Decimal
	if !gotAmt.Equal(decimal.NewFromInt(-100)) {
		t.Errorf("amount: got %s, want -100", gotAmt)
	}
	if out.Message.Postings[0].Account.Num != "111000001" {
		t.Errorf("account num: got %s", out.Message.Postings[0].Account.Num)
	}
}

func TestTransactionVote_NoVoteShape(t *testing.T) {
	offendingPosting := sitx.Posting{
		Account: sitx.TxAccount{Type: "ACCOUNT", Num: "111000001"},
		Amount:  sitx.DecimalNumber{Decimal: decimal.NewFromInt(-100)},
		Asset:   sitx.Asset{Type: "MONAS", Asset: sitx.MonetaryAsset{Currency: "RSD"}},
	}
	v := sitx.TransactionVote{
		Vote: sitx.VoteNo,
		Reasons: []sitx.NoVoteReason{
			{Reason: sitx.NoVoteReasonInsufficientAsset, Posting: &offendingPosting},
			{Reason: sitx.NoVoteReasonNoSuchAccount},
		},
	}
	raw, _ := json.Marshal(v)
	var got sitx.TransactionVote
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Vote != sitx.VoteNo {
		t.Errorf("vote: got %q", got.Vote)
	}
	if len(got.Reasons) != 2 || got.Reasons[0].Reason != sitx.NoVoteReasonInsufficientAsset {
		t.Errorf("reasons: got %+v", got.Reasons)
	}
	if got.Reasons[0].Posting == nil || got.Reasons[0].Posting.Account.Num != "111000001" {
		t.Errorf("posting in reason: got %+v", got.Reasons[0].Posting)
	}
	if got.Reasons[1].Posting != nil {
		t.Errorf("second reason posting should be nil: got %+v", got.Reasons[1].Posting)
	}
}
