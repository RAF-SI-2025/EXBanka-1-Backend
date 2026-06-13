package service

import (
	"context"
	"errors"
	"testing"

	"github.com/exbanka/stock-service/internal/model"
)

type fakeContractFormer struct {
	contract *model.OptionContract
	err      error
	calls    int
}

func (f *fakeContractFormer) MintContractFromAcceptedNegotiation(_ context.Context, _ MintFromNegotiationInput) (*model.OptionContract, error) {
	f.calls++
	return f.contract, f.err
}

// TestAcceptNegotiation_Former_Success_CascadeAndLink drives the full post-TX
// formation path: a winning chain mints a contract, the sibling chain is
// cascade-cancelled + notified, and the winning negotiation gets its
// minted_contract_id linked.
func TestAcceptNegotiation_Former_Success_CascadeAndLink(t *testing.T) {
	env, notif := newNegTestEnvWithNotifier(t)
	former := &fakeContractFormer{contract: &model.OptionContract{ID: 500}}
	env.svc = env.svc.WithContractFormer(former)

	listing := seedListing(t, env, 1 /*poster*/, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	// Two competing bidders → two chains.
	winning, err := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7))
	if err != nil {
		t.Fatalf("open winning: %v", err)
	}
	if _, err := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 8)); err != nil {
		t.Fatalf("open sibling: %v", err)
	}

	// The poster (last action was the bidder's bid) accepts the winning chain.
	res, err := env.svc.AcceptNegotiation(context.Background(), AcceptNegotiationInput{
		NegotiationID:       winning.ID,
		CallerOwnerType:     model.OwnerClient,
		CallerOwnerID:       u64p(1),
		ActingPrincipalType: "client",
		ActingPrincipalID:   1,
		AcceptorAccountID:   100,
	})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if former.calls != 1 {
		t.Fatalf("former should be called once, got %d", former.calls)
	}
	if res.Contract == nil || res.Contract.ID != 500 {
		t.Fatalf("expected minted contract 500, got %+v", res.Contract)
	}
	if len(res.CancelledSiblings) != 1 {
		t.Fatalf("expected 1 cascade-cancelled sibling, got %d", len(res.CancelledSiblings))
	}
	// minted_contract_id linked on the winning negotiation.
	got, _ := env.negRepo.GetByID(winning.ID)
	if got.MintedContractID == nil || *got.MintedContractID != 500 {
		t.Errorf("minted_contract_id not linked: %v", got.MintedContractID)
	}
	// Notifications: cascade-cancel for sibling + 2 contract-created.
	var cascade, created int
	for _, m := range notif.all() {
		switch m.Type {
		case "OTC_OFFER_CASCADE_CANCELLED":
			cascade++
		case "OTC_CONTRACT_CREATED":
			created++
		}
	}
	if cascade != 1 {
		t.Errorf("expected 1 cascade notification, got %d", cascade)
	}
	if created != 2 {
		t.Errorf("expected 2 contract-created notifications, got %d", created)
	}
}

// TestAcceptNegotiation_Former_MintFails_RestoresListing drives the
// formation-failure path: the listing + sibling are restored and the call errors.
func TestAcceptNegotiation_Former_MintFails_RestoresListing(t *testing.T) {
	env, _ := newNegTestEnvWithNotifier(t)
	former := &fakeContractFormer{err: errors.New("insufficient funds")}
	env.svc = env.svc.WithContractFormer(former)

	listing := seedListing(t, env, 1, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	winning, err := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	_, err = env.svc.AcceptNegotiation(context.Background(), AcceptNegotiationInput{
		NegotiationID:       winning.ID,
		CallerOwnerType:     model.OwnerClient,
		CallerOwnerID:       u64p(1),
		ActingPrincipalType: "client",
		ActingPrincipalID:   1,
		AcceptorAccountID:   100,
	})
	if err == nil {
		t.Fatal("expected error when mint fails")
	}
	// The listing must be restored to OPEN (not left consumed).
	parent, _ := env.offerRepo.GetByID(listing.ID)
	if !parent.IsOpenListing() {
		t.Errorf("listing should be restored to open, status=%s", parent.Status)
	}
}

func TestAcceptNegotiation_Former_NoAcceptorAccount(t *testing.T) {
	env, _ := newNegTestEnvWithNotifier(t)
	former := &fakeContractFormer{contract: &model.OptionContract{ID: 1}}
	env.svc = env.svc.WithContractFormer(former)

	listing := seedListing(t, env, 1, model.OTCDirectionSellInitiated, model.OTCOfferStatusOpen)
	winning, err := env.svc.OpenNegotiation(context.Background(), sampleOpenInput(listing.ID, 7))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, err = env.svc.AcceptNegotiation(context.Background(), AcceptNegotiationInput{
		NegotiationID:       winning.ID,
		CallerOwnerType:     model.OwnerClient,
		CallerOwnerID:       u64p(1),
		ActingPrincipalType: "client",
		ActingPrincipalID:   1,
		// AcceptorAccountID omitted (0) → ErrOTCAcceptorAccountRequired.
	})
	if !errors.Is(err, ErrOTCAcceptorAccountRequired) {
		t.Fatalf("want ErrOTCAcceptorAccountRequired, got %v", err)
	}
	if former.calls != 0 {
		t.Errorf("former must not be called without an acceptor account")
	}
	// Listing restored.
	parent, _ := env.offerRepo.GetByID(listing.ID)
	if !parent.IsOpenListing() {
		t.Errorf("listing should be restored to open, status=%s", parent.Status)
	}
}
