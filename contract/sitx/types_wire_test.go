package sitx

import (
	"encoding/json"
	"testing"

	"github.com/shopspring/decimal"
)

func TestTransaction_SpecCoffeeShape(t *testing.T) {
	tx := Transaction{
		Postings: []Posting{
			{
				Account: TxAccount{Type: "ACCOUNT", Num: "444000100182503611"},
				Amount:  DecimalNumber{decimal.RequireFromString("-260")},
				Asset:   Asset{Type: "MONAS", Asset: MonetaryAsset{Currency: "RSD"}},
			},
			{
				Account: TxAccount{Type: "ACCOUNT", Num: "111000141215476411"},
				Amount:  DecimalNumber{decimal.RequireFromString("260")},
				Asset:   Asset{Type: "MONAS", Asset: MonetaryAsset{Currency: "RSD"}},
			},
		},
		TransactionID:  ForeignBankId{RoutingNumber: 111, ID: "tx-1"},
		Message:        "coffee",
		PaymentCode:    "289",
		PaymentPurpose: "debt",
	}
	b, err := json.Marshal(tx)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"postings":[{"account":{"type":"ACCOUNT","num":"444000100182503611"},"amount":-260,"asset":{"type":"MONAS","asset":{"currency":"RSD"}}},{"account":{"type":"ACCOUNT","num":"111000141215476411"},"amount":260,"asset":{"type":"MONAS","asset":{"currency":"RSD"}}}],"transactionId":{"routingNumber":111,"id":"tx-1"},"message":"coffee","paymentCode":"289","paymentPurpose":"debt"}`
	if string(b) != want {
		t.Fatalf("wire shape mismatch:\n got: %s\nwant: %s", string(b), want)
	}
}

func TestTransactionVote_YesAndNo(t *testing.T) {
	yes, err := json.Marshal(TransactionVote{Vote: VoteYes})
	if err != nil {
		t.Fatalf("marshal yes vote: %v", err)
	}
	if string(yes) != `{"vote":"YES"}` {
		t.Fatalf("yes vote: %s", yes)
	}
	p := Posting{Account: TxAccount{Type: "ACCOUNT", Num: "1"}, Amount: DecimalNumber{decimal.RequireFromString("5")}, Asset: Asset{Type: "MONAS", Asset: MonetaryAsset{Currency: "RSD"}}}
	no, err := json.Marshal(TransactionVote{Vote: VoteNo, Reasons: []NoVoteReason{{Reason: NoVoteReasonInsufficientAsset, Posting: &p}}})
	if err != nil {
		t.Fatalf("marshal no vote: %v", err)
	}
	want := `{"vote":"NO","reasons":[{"reason":"INSUFFICIENT_ASSET","posting":{"account":{"type":"ACCOUNT","num":"1"},"amount":5,"asset":{"type":"MONAS","asset":{"currency":"RSD"}}}}]}`
	if string(no) != want {
		t.Fatalf("no vote:\n got: %s\nwant: %s", string(no), want)
	}
}

func TestCommitRollback_ForeignBankId(t *testing.T) {
	c, err := json.Marshal(CommitTransaction{TransactionID: ForeignBankId{RoutingNumber: 111, ID: "tx-1"}})
	if err != nil {
		t.Fatalf("marshal commit: %v", err)
	}
	if string(c) != `{"transactionId":{"routingNumber":111,"id":"tx-1"}}` {
		t.Fatalf("commit: %s", c)
	}
	r, err := json.Marshal(RollbackTransaction{TransactionID: ForeignBankId{RoutingNumber: 111, ID: "tx-1"}})
	if err != nil {
		t.Fatalf("marshal rollback: %v", err)
	}
	if string(r) != `{"transactionId":{"routingNumber":111,"id":"tx-1"}}` {
		t.Fatalf("rollback: %s", r)
	}
}
