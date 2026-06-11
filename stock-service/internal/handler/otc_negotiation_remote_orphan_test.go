package handler

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	contractsitx "github.com/exbanka/contract/sitx"
	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestAcceptRemoteNegotiation_OrphanCancelledParent_Rejected guards the
// orphan-accept loophole found in the live two-stack adversarial sweep
// (2026-06-05): a cross-bank child chain of a CANCELLED listing could still be
// ACCEPTED, forming a contract and settling money on a listing the poster had
// already withdrawn. The LOCAL accept path checks ErrOTCParentNotOpen, but the
// REMOTE accept path skipped any parent-status check.
//
// Setup: WE (routing 111) host the SELLER side and also host the LISTING (a
// LOCAL offer that has been cancelled). The remote child chain references the
// parent by (remote_parent_routing=111, remote_parent_native_id=<offer id>). The
// seller accepting it must be rejected with FailedPrecondition and NO accept
// dispatched to the peer.
func TestAcceptRemoteNegotiation_OrphanCancelledParent_Rejected(t *testing.T) {
	dispatcher := &fakePeerDispatcher{proxyStatus: 200, proxyResp: []byte(`{}`)}
	accounts := &fakeOTCAccountClient{acct: usdAccount(9)}
	h, db := newRemoteBidFixture(t, dispatcher, accounts)

	// Seed a LOCAL listing owned by client-9 and CANCEL it.
	parent := &model.OTCOffer{
		InitiatorOwnerType:          model.OwnerClient,
		InitiatorOwnerID:            u64ptr(9),
		Direction:                   model.OTCDirectionSellInitiated,
		Ticker:                      "AAPL",
		Quantity:                    decimal.NewFromInt(10),
		Status:                      model.OTCOfferStatusCancelled, // withdrawn
		LastModifiedByPrincipalType: "client",
		LastModifiedByPrincipalID:   9,
	}
	if err := db.Create(parent).Error; err != nil {
		t.Fatalf("seed parent: %v", err)
	}

	// Seed a REMOTE child chain: WE host the seller (client-9@111); the buyer is
	// on peer 222. lastModifiedBy = the buyer (222) so the self-accept guard does
	// NOT trigger — the ONLY thing that should block the accept is the cancelled
	// parent. Parent linkage points at our local offer id.
	parentNative := strconv.FormatUint(parent.ID, 10)
	offer := contractsitx.OtcOffer{
		Ticker:          "AAPL",
		Amount:          10,
		PricePerStock:   decimal.RequireFromString("150"),
		Currency:        "USD",
		Premium:         decimal.RequireFromString("20"),
		PremiumCurrency: "USD",
		SettlementDate:  "2026-07-01",
		LastModifiedBy:  contractsitx.ForeignBankId{RoutingNumber: 222, ID: "client-3"},
	}
	offerJSON, _ := json.Marshal(offer)
	pr := int64(111)
	row := buildRemoteNeg(
		222, "neg-orphan", offer, string(offerJSON),
		222, "client-3", // buyer on peer
		111, "client-9", // seller hosted by us
		&pr, &parentNative, "ongoing",
	)
	repo := repository.NewOTCNegotiationRepository(db)
	if err := repo.UpsertRemoteNeg(row); err != nil {
		t.Fatalf("seed child: %v", err)
	}
	seeded, err := repo.GetRemoteNegByRoutingAndNative(222, "neg-orphan")
	if err != nil {
		t.Fatalf("read child: %v", err)
	}

	_, err = h.AcceptNegotiationChain(context.Background(), &stockpb.OTCAcceptNegotiationRequest{
		NegotiationId:       seeded.ID,
		CallerOwnerType:     "client",
		CallerOwnerId:       9, // the seller (opposite to last proposer) — auth OK, but parent is dead
		ActingPrincipalType: "client",
		ActingPrincipalId:   9,
		AcceptorAccountId:   5001,
	})
	if err == nil {
		t.Fatal("expected accept on a cancelled-parent chain to be rejected, got nil (orphan-accept loophole)")
	}
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("expected FailedPrecondition, got %v", err)
	}
	for _, pc := range dispatcher.proxyCalls {
		if pc.method == "GET" && pc.subpath == "/accept" {
			t.Errorf("orphan accept must NOT dispatch GET /accept; calls: %+v", dispatcher.proxyCalls)
		}
	}
	after, _ := repo.GetRemoteNegByRoutingAndNative(222, "neg-orphan")
	if after.Status != "ongoing" {
		t.Errorf("status after rejected orphan accept: got %q, want ongoing", after.Status)
	}
}

func u64ptr(v uint64) *uint64 { return &v }
