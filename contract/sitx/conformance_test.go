package sitx

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/shopspring/decimal"
)

// dn is a small helper to build a DecimalNumber wire amount from a string.
func dn(s string) DecimalNumber {
	return DecimalNumber{Decimal: decimal.RequireFromString(s)}
}

// coffeePosting2 is the §2.8 coffee transaction's credit leg (the receiving
// account). It is reused by the vote_no fixture's NoVoteReason.
func coffeePosting2() Posting {
	return Posting{
		Account: TxAccount{Type: AccountTypeAccount, Num: "111000141215476411"},
		Amount:  dn("260"),
		Asset:   Asset{Type: AssetTypeMonas, Asset: MonetaryAsset{Currency: "RSD"}},
	}
}

// TestConformance asserts that marshalling each canonical Go value produces
// bytes that are JSON-equal (after json.Compact) to the hand-authored spec
// fixture under testdata/. These fixtures are the shared cohort-interop target:
// any team can diff their encoder output against them. The Go encoder is the
// source of truth for field ORDER; fixtures are authored in struct-field order.
func TestConformance(t *testing.T) {
	cases := []struct {
		name    string
		fixture string
		value   any
	}{
		{
			name:    "newtx_coffee",
			fixture: "newtx_coffee.json",
			value: Message[Transaction]{
				IdempotenceKey: IdempotenceKey{RoutingNumber: 111, LocallyGeneratedKey: "k-coffee-1"},
				MessageType:    MessageTypeNewTx,
				Message: Transaction{
					Postings: []Posting{
						{
							Account: TxAccount{Type: AccountTypeAccount, Num: "444000100182503611"},
							Amount:  dn("-260"),
							Asset:   Asset{Type: AssetTypeMonas, Asset: MonetaryAsset{Currency: "RSD"}},
						},
						coffeePosting2(),
					},
					TransactionID:  ForeignBankId{RoutingNumber: 111, ID: "k-coffee-1"},
					Message:        "coffee",
					PaymentCode:    "289",
					PaymentPurpose: "debt",
				},
			},
		},
		{
			name:    "vote_no",
			fixture: "vote_no.json",
			value: func() TransactionVote {
				p := coffeePosting2()
				return TransactionVote{
					Vote: VoteNo,
					Reasons: []NoVoteReason{
						{Reason: NoVoteReasonInsufficientAsset, Posting: &p},
					},
				}
			}(),
		},
		{
			name:    "public_stock",
			fixture: "public_stock.json",
			value: PublicStocksResponse{
				{
					Stock: StockDescription{Ticker: "AAPL"},
					Sellers: []PublicSeller{
						{Seller: ForeignBankId{RoutingNumber: 111, ID: "client-3"}, Amount: 50},
						{Seller: ForeignBankId{RoutingNumber: 111, ID: "client-9"}, Amount: 20},
					},
				},
			},
		},
		{
			// UserInformation is the spec §3.7 shape {bankDisplayName, displayName};
			// the gateway /user handler emits the same shape.
			name:    "user",
			fixture: "user.json",
			value: UserInformation{
				BankDisplayName: "EXBanka",
				DisplayName:     "Marko Marković",
			},
		},
		{
			name:    "newtx_otc_accept",
			fixture: "newtx_otc_accept.json",
			value: func() Message[Transaction] {
				od := OptionDescription{
					NegotiationID:  ForeignBankId{RoutingNumber: 111, ID: "neg-1"},
					Stock:          StockDescription{Ticker: "WMT"},
					PricePerUnit:   MonetaryValue{Amount: dn("50"), Currency: "RSD"},
					SettlementDate: "2026-12-31T00:00:00+02:00",
					Amount:         10,
				}
				optAsset := Asset{Type: AssetTypeOption, Asset: od}
				rsd := Asset{Type: AssetTypeMonas, Asset: MonetaryAsset{Currency: "RSD"}}
				return Message[Transaction]{
					IdempotenceKey: IdempotenceKey{RoutingNumber: 111, LocallyGeneratedKey: "k-otc-accept-1"},
					MessageType:    MessageTypeNewTx,
					Message: Transaction{
						Postings: []Posting{
							{Account: TxAccount{Type: AccountTypeAccount, Num: "111000117810858011"}, Amount: dn("-1000"), Asset: rsd},
							{Account: TxAccount{Type: AccountTypePerson, ID: &ForeignBankId{RoutingNumber: 222, ID: "client-1"}}, Amount: dn("1000"), Asset: rsd},
							{Account: TxAccount{Type: AccountTypePerson, ID: &ForeignBankId{RoutingNumber: 222, ID: "client-1"}}, Amount: dn("-1"), Asset: optAsset},
							{Account: TxAccount{Type: AccountTypePerson, ID: &ForeignBankId{RoutingNumber: 111, ID: "client-1"}}, Amount: dn("1"), Asset: optAsset},
						},
						TransactionID:  ForeignBankId{RoutingNumber: 111, ID: "k-otc-accept-1"},
						Message:        "Cross-bank OTC otc-accept",
						PaymentCode:    "",
						PaymentPurpose: "",
					},
				}
			}(),
		},
		{
			name:    "newtx_otc_exercise",
			fixture: "newtx_otc_exercise.json",
			value: func() Message[Transaction] {
				rsd := Asset{Type: AssetTypeMonas, Asset: MonetaryAsset{Currency: "RSD"}}
				wmt := Asset{Type: AssetTypeStock, Asset: StockDescription{Ticker: "WMT"}}
				pseudo := TxAccount{Type: AccountTypeOption, ID: &ForeignBankId{RoutingNumber: 111, ID: "neg-1"}}
				return Message[Transaction]{
					IdempotenceKey: IdempotenceKey{RoutingNumber: 111, LocallyGeneratedKey: "k-otc-exercise-1"},
					MessageType:    MessageTypeNewTx,
					Message: Transaction{
						Postings: []Posting{
							{Account: TxAccount{Type: AccountTypeAccount, Num: "111000117810858011"}, Amount: dn("-500"), Asset: rsd},
							{Account: pseudo, Amount: dn("500"), Asset: rsd},
							{Account: pseudo, Amount: dn("-10"), Asset: wmt},
							{Account: TxAccount{Type: AccountTypePerson, ID: &ForeignBankId{RoutingNumber: 111, ID: "client-1"}}, Amount: dn("10"), Asset: wmt},
						},
						TransactionID:  ForeignBankId{RoutingNumber: 111, ID: "k-otc-exercise-1"},
						Message:        "Cross-bank OTC otc-exercise",
						PaymentCode:    "",
						PaymentPurpose: "",
					},
				}
			}(),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatalf("marshal %s: %v", tc.name, err)
			}
			gotCompact := compact(t, got)

			want, err := os.ReadFile(filepath.Join("testdata", tc.fixture))
			if err != nil {
				t.Fatalf("read fixture %s: %v", tc.fixture, err)
			}
			wantCompact := compact(t, want)

			if !bytes.Equal(gotCompact, wantCompact) {
				t.Errorf("fixture %s mismatch:\n got: %s\nwant: %s", tc.fixture, gotCompact, wantCompact)
			}
		})
	}
}

func compact(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := json.Compact(&buf, b); err != nil {
		t.Fatalf("compact: %v\ninput: %s", err, b)
	}
	return buf.Bytes()
}
