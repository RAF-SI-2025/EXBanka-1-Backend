package sitx_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	accountpb "github.com/exbanka/contract/accountpb"
	contractsitx "github.com/exbanka/contract/sitx"
	"github.com/exbanka/interbank-service/internal/sitx"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestSettleLocal_SettlesOwnDebitMoneyLegs verifies SettleLocal calls
// SettleOutgoing for each money DEBIT leg on OUR routing — using the same
// per-posting tags Reserve placed — and skips CREDIT legs, option legs, and
// legs on the peer's routing.
func TestSettleLocal_SettlesOwnDebitMoneyLegs(t *testing.T) {
	var settledKeys []string
	stub := &stubAccountClient{
		settleOutFn: func(_ context.Context, in *accountpb.SettleOutgoingRequest, _ ...grpc.CallOption) (*accountpb.SettleOutgoingResponse, error) {
			settledKeys = append(settledKeys, in.ReservationKey)
			if in.IdempotencyKey == "" {
				t.Errorf("settle missing idempotency key")
			}
			return &accountpb.SettleOutgoingResponse{}, nil
		},
	}
	exec := sitx.NewPostingExecutor(stub, 111)

	optDesc := `{"negotiationId":{"routingNumber":222,"id":"neg-1"}}`
	postings := []contractsitx.InternalPosting{
		money(111, "111-A", "RSD", 100, contractsitx.DirectionDebit),     // idx 0 → settle
		money(111, "111-C", "RSD", 50, contractsitx.DirectionCredit),     // idx 1 → skip (credit)
		option(111, "client-7", optDesc, 4, contractsitx.DirectionDebit), // idx 2 → skip (option leg)
		money(222, "222-B", "RSD", 100, contractsitx.DirectionDebit),     // idx 3 → skip (peer routing)
	}
	if err := exec.SettleLocal(context.Background(), postings, "222", "idem-S"); err != nil {
		t.Fatalf("SettleLocal: %v", err)
	}
	if len(settledKeys) != 1 {
		t.Fatalf("expected exactly 1 settle, got %d (%v)", len(settledKeys), settledKeys)
	}
	if settledKeys[0] != "222:idem-S:0" {
		t.Errorf("settle key = %q, want 222:idem-S:0", settledKeys[0])
	}
}

// TestSettleLocal_NotFoundIsBenign verifies a NotFound from SettleOutgoing (no
// hold landed for that leg) is swallowed, while any other error propagates.
func TestSettleLocal_NotFoundIsBenign(t *testing.T) {
	notFound := &stubAccountClient{
		settleOutFn: func(_ context.Context, _ *accountpb.SettleOutgoingRequest, _ ...grpc.CallOption) (*accountpb.SettleOutgoingResponse, error) {
			return nil, status.Error(codes.NotFound, "no reservation")
		},
	}
	exec := sitx.NewPostingExecutor(notFound, 111)
	postings := []contractsitx.InternalPosting{
		money(111, "111-A", "RSD", 100, contractsitx.DirectionDebit),
	}
	if err := exec.SettleLocal(context.Background(), postings, "222", "idem-NF"); err != nil {
		t.Errorf("NotFound should be benign, got %v", err)
	}

	boom := errors.New("downstream unavailable")
	failing := &stubAccountClient{
		settleOutFn: func(_ context.Context, _ *accountpb.SettleOutgoingRequest, _ ...grpc.CallOption) (*accountpb.SettleOutgoingResponse, error) {
			return nil, status.Error(codes.Unavailable, boom.Error())
		},
	}
	exec2 := sitx.NewPostingExecutor(failing, 111)
	if err := exec2.SettleLocal(context.Background(), postings, "222", "idem-ERR"); err == nil {
		t.Errorf("expected non-NotFound settle error to propagate")
	}
}

// TestExtractOwnOptionItems_Accept verifies the sender-side accept mirror emits
// one accept OptionItem per OPTION-asset leg on our routing, with buyer/seller
// resolved from the CREDIT/DEBIT pairing by OptionDescription JSON.
func TestExtractOwnOptionItems_Accept(t *testing.T) {
	exec := sitx.NewPostingExecutor(&stubAccountClient{}, 111)
	optDesc := `{"negotiationId":{"routingNumber":222,"id":"neg-9"}}`
	postings := []contractsitx.InternalPosting{
		option(111, "client-7", optDesc, 4, contractsitx.DirectionDebit),  // our seller leg
		option(222, "client-8", optDesc, 4, contractsitx.DirectionCredit), // buyer leg (peer)
	}
	items := exec.ExtractOwnOptionItems(postings)
	if len(items) != 1 {
		t.Fatalf("expected 1 own option item, got %d (%+v)", len(items), items)
	}
	it := items[0]
	if it.Kind != sitx.OptionKindAccept {
		t.Errorf("kind = %q, want accept", it.Kind)
	}
	if it.PostingIndex != 0 || it.Direction != contractsitx.DirectionDebit {
		t.Errorf("posting index/direction = %d/%s", it.PostingIndex, it.Direction)
	}
	if it.OptionDescriptionJSON != optDesc {
		t.Errorf("desc = %q", it.OptionDescriptionJSON)
	}
	// Buyer comes from the CREDIT leg (peer/client-8); seller from the DEBIT
	// leg (us/client-7).
	if it.Buyer.RoutingNumber != 222 || it.Buyer.ID != "client-8" {
		t.Errorf("buyer = %+v, want {222, client-8}", it.Buyer)
	}
	if it.Seller.RoutingNumber != 111 || it.Seller.ID != "client-7" {
		t.Errorf("seller = %+v, want {111, client-7}", it.Seller)
	}
}

// TestExtractOwnOptionItems_ExerciseBuyer verifies an exercise TX (an OPTION
// pseudo-account leg present) emits an exercise_buyer item for the STOCK CREDIT
// leg arriving on our routing, carrying the reconstructed negotiationId.
func TestExtractOwnOptionItems_ExerciseBuyer(t *testing.T) {
	exec := sitx.NewPostingExecutor(&stubAccountClient{}, 111)
	postings := []contractsitx.InternalPosting{
		// OPTION pseudo-account leg → carries the negotiationId for the exercise.
		{RoutingNumber: 222, AccountType: contractsitx.AccountTypeOption, AccountID: "neg-EX", AssetType: "OPTION", AssetID: "{}", Amount: "1", Direction: contractsitx.DirectionDebit},
		// Buyer's underlying stock arrival on our routing.
		{RoutingNumber: 111, AccountType: contractsitx.AccountTypePerson, AccountID: "client-7", AssetType: contractsitx.AssetTypeStock, AssetID: "AAPL", Amount: "4", Direction: contractsitx.DirectionCredit},
	}
	items := exec.ExtractOwnOptionItems(postings)
	if len(items) != 1 {
		t.Fatalf("expected 1 exercise item, got %d (%+v)", len(items), items)
	}
	it := items[0]
	if it.Kind != sitx.OptionKindExerciseBuyer {
		t.Errorf("kind = %q, want exercise_buyer", it.Kind)
	}
	if it.Direction != contractsitx.DirectionCredit {
		t.Errorf("direction = %q, want CREDIT", it.Direction)
	}
	// The reconstructed option description must carry the negotiationId from the
	// pseudo-account leg so recordOptionExercise can extract it.
	if it.OptionDescriptionJSON == "" {
		t.Fatalf("expected reconstructed option description JSON")
	}
	var od contractsitx.OptionDescription
	if err := json.Unmarshal([]byte(it.OptionDescriptionJSON), &od); err != nil {
		t.Fatalf("desc not valid JSON: %v", err)
	}
	if od.NegotiationID.RoutingNumber != 222 || od.NegotiationID.ID != "neg-EX" {
		t.Errorf("reconstructed negotiationId = %+v, want {222, neg-EX}", od.NegotiationID)
	}
}

// TestExtractOwnOptionItems_NoOptionLegs verifies a plain money TX yields no
// option items (the common transfer case).
func TestExtractOwnOptionItems_NoOptionLegs(t *testing.T) {
	exec := sitx.NewPostingExecutor(&stubAccountClient{}, 111)
	postings := []contractsitx.InternalPosting{
		money(111, "111-A", "RSD", 100, contractsitx.DirectionCredit),
		money(222, "222-B", "RSD", 100, contractsitx.DirectionDebit),
	}
	if items := exec.ExtractOwnOptionItems(postings); len(items) != 0 {
		t.Errorf("expected no option items on a money TX, got %+v", items)
	}
}
