package handler_test

import (
	"context"
	"errors"
	"strconv"
	"testing"

	accountpb "github.com/exbanka/contract/accountpb"
	"github.com/exbanka/stock-service/internal/handler"
	"github.com/exbanka/stock-service/internal/model"
	"google.golang.org/grpc"
)

// fakeOfferReader stubs handler.OfferReaderByID for the resolver tests.
type fakeOfferReader struct {
	offers map[uint64]*model.OTCOffer
}

func (f *fakeOfferReader) GetByID(id uint64) (*model.OTCOffer, error) {
	if o, ok := f.offers[id]; ok {
		return o, nil
	}
	return nil, errors.New("not found")
}

// fakeAcctClient stubs handler.OTCAccountClient for the resolver tests.
type fakeAcctClient struct {
	byID map[uint64]*accountpb.AccountResponse
}

func (f *fakeAcctClient) GetAccount(_ context.Context, in *accountpb.GetAccountRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	if a, ok := f.byID[in.GetId()]; ok {
		return a, nil
	}
	return nil, errors.New("not found")
}

func (f *fakeAcctClient) GetAccountByNumber(_ context.Context, _ *accountpb.GetAccountByNumberRequest, _ ...grpc.CallOption) (*accountpb.AccountResponse, error) {
	return nil, errors.New("not used")
}

func negWithParent(parentRouting int64, parentNativeID, sellerID string) *model.OTCNegotiation {
	pr := parentRouting
	pn := parentNativeID
	sid := sellerID
	return &model.OTCNegotiation{
		RemoteParentRouting:  &pr,
		RemoteParentNativeID: &pn,
		RemoteSellerID:       &sid,
	}
}

// TestSellerAccountResolver_ReturnsNominatedNumber: a sell_initiated parent
// listing with a bound InitiatorAccountID resolves to that account's number when
// the account is active and matches the premium currency.
func TestSellerAccountResolver_ReturnsNominatedNumber(t *testing.T) {
	model.SetOwnRouting("111")
	const accountID uint64 = 42
	const accountNum = "111000000000000777"
	offers := &fakeOfferReader{offers: map[uint64]*model.OTCOffer{
		5: {ID: 5, Direction: model.OTCDirectionSellInitiated, InitiatorAccountID: accountID},
	}}
	accts := &fakeAcctClient{byID: map[uint64]*accountpb.AccountResponse{
		accountID: {Id: accountID, AccountNumber: accountNum, Status: "active", CurrencyCode: "USD"},
	}}
	r := handler.NewSellerAccountResolver(offers, accts, 111)

	got := r.ResolveSellerAccountNumber(context.Background(), negWithParent(111, "5", "client-9"), "USD")
	if got != accountNum {
		t.Errorf("ResolveSellerAccountNumber = %q, want %q", got, accountNum)
	}
}

// TestSellerAccountResolver_EmptyWhenNoParent: a free-form negotiation (no local
// parent listing) yields "" → caller falls back to participant id.
func TestSellerAccountResolver_EmptyWhenNoParent(t *testing.T) {
	model.SetOwnRouting("111")
	r := handler.NewSellerAccountResolver(&fakeOfferReader{}, &fakeAcctClient{}, 111)

	neg := &model.OTCNegotiation{} // no parent fields
	if got := r.ResolveSellerAccountNumber(context.Background(), neg, "USD"); got != "" {
		t.Errorf("expected empty (no parent), got %q", got)
	}
}

// TestSellerAccountResolver_EmptyWhenParentOnPeer: when the parent listing lives
// on a peer bank (parent routing != ownRouting) we cannot read its
// InitiatorAccountID → "" fallback.
func TestSellerAccountResolver_EmptyWhenParentOnPeer(t *testing.T) {
	model.SetOwnRouting("111")
	r := handler.NewSellerAccountResolver(&fakeOfferReader{}, &fakeAcctClient{}, 111)

	if got := r.ResolveSellerAccountNumber(context.Background(), negWithParent(222, "5", "client-9"), "USD"); got != "" {
		t.Errorf("expected empty (parent on peer), got %q", got)
	}
}

// TestSellerAccountResolver_EmptyWhenCurrencyMismatch: the bound account is in a
// different currency than the premium → "" (don't pin a wrong-currency account;
// the executor would reject it — fall back to participant resolution instead).
func TestSellerAccountResolver_EmptyWhenCurrencyMismatch(t *testing.T) {
	model.SetOwnRouting("111")
	const accountID uint64 = 42
	offers := &fakeOfferReader{offers: map[uint64]*model.OTCOffer{
		5: {ID: 5, Direction: model.OTCDirectionSellInitiated, InitiatorAccountID: accountID},
	}}
	accts := &fakeAcctClient{byID: map[uint64]*accountpb.AccountResponse{
		accountID: {Id: accountID, AccountNumber: "111000000000000777", Status: "active", CurrencyCode: "EUR"},
	}}
	r := handler.NewSellerAccountResolver(offers, accts, 111)

	if got := r.ResolveSellerAccountNumber(context.Background(), negWithParent(111, "5", "client-9"), "USD"); got != "" {
		t.Errorf("expected empty (currency mismatch), got %q", got)
	}
}

// TestSellerAccountResolver_EmptyWhenUnbound: a sell_initiated listing whose
// InitiatorAccountID is 0 (unbound) yields "" fallback.
func TestSellerAccountResolver_EmptyWhenUnbound(t *testing.T) {
	model.SetOwnRouting("111")
	offers := &fakeOfferReader{offers: map[uint64]*model.OTCOffer{
		5: {ID: 5, Direction: model.OTCDirectionSellInitiated, InitiatorAccountID: 0},
	}}
	r := handler.NewSellerAccountResolver(offers, &fakeAcctClient{}, 111)

	if got := r.ResolveSellerAccountNumber(context.Background(), negWithParent(111, strconv.Itoa(5), "client-9"), "USD"); got != "" {
		t.Errorf("expected empty (unbound account), got %q", got)
	}
}
