package handler

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/otccache"
)

// fakeMyNegLister is an in-memory MyNegotiationLister. localRows are returned
// by ListByBidder filtered to the (ownerType, ownerID) bidder; remoteRows by
// ListRemoteNegByClient filtered to the principal.
type fakeMyNegLister struct {
	localRows  []model.OTCNegotiation
	remoteRows []model.OTCNegotiation
	localErr   error
	remoteErr  error
	// bankRemoteRows feeds ListRemoteNegByBankParty (SP-3 Task 5b): the bank's
	// REMOTE bidder chains (party id "employee-<N>"). Role-filtered on the
	// employee-side; kept separate from remoteRows (the client path).
	bankRemoteRows []model.OTCNegotiation
	bankErr        error
}

func (f *fakeMyNegLister) ListByBidder(
	ownerType model.OwnerType, ownerID *uint64, _ []string, _, _ int,
) ([]model.OTCNegotiation, int64, error) {
	if f.localErr != nil {
		return nil, 0, f.localErr
	}
	out := make([]model.OTCNegotiation, 0, len(f.localRows))
	for _, r := range f.localRows {
		if r.BidderOwnerType != ownerType {
			continue
		}
		switch {
		case ownerType == model.OwnerClient:
			if r.BidderOwnerID == nil || ownerID == nil || *r.BidderOwnerID != *ownerID {
				continue
			}
		default: // bank
			if r.BidderOwnerID != nil {
				continue
			}
		}
		out = append(out, r)
	}
	return out, int64(len(out)), nil
}

func (f *fakeMyNegLister) ListRemoteNegByClient(_ int64, clientPrincipal, _ string) ([]model.OTCNegotiation, error) {
	if f.remoteErr != nil {
		return nil, f.remoteErr
	}
	out := make([]model.OTCNegotiation, 0, len(f.remoteRows))
	for _, r := range f.remoteRows {
		if (r.RemoteBuyerID != nil && *r.RemoteBuyerID == clientPrincipal) ||
			(r.RemoteSellerID != nil && *r.RemoteSellerID == clientPrincipal) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeMyNegLister) ListRemoteNegByBankParty(_ int64, role string) ([]model.OTCNegotiation, error) {
	if f.bankErr != nil {
		return nil, f.bankErr
	}
	out := make([]model.OTCNegotiation, 0, len(f.bankRemoteRows))
	for _, r := range f.bankRemoteRows {
		switch role {
		case "buyer":
			if r.RemoteBuyerID != nil && hasEmployeePrefix(*r.RemoteBuyerID) {
				out = append(out, r)
			}
		case "seller":
			if r.RemoteSellerID != nil && hasEmployeePrefix(*r.RemoteSellerID) {
				out = append(out, r)
			}
		default:
			out = append(out, r)
		}
	}
	return out, nil
}

func u64(v uint64) *uint64 { return &v }
func i64(v int64) *int64   { return &v }
func str(v string) *string { return &v }

// ---------------- pure helpers ----------------

func TestNegStatusRank(t *testing.T) {
	require.Equal(t, 0, negStatusRank(model.OTCNegotiationStatusAccepted))
	require.Equal(t, 1, negStatusRank(model.OTCNegotiationStatusOpen))
	require.Equal(t, 1, negStatusRank(model.OTCNegotiationStatusCountered))
	require.Equal(t, 1, negStatusRank("ongoing"))
	require.Equal(t, 2, negStatusRank(model.OTCNegotiationStatusRejected))
	require.Equal(t, 2, negStatusRank(model.OTCNegotiationStatusCancelled))
	require.Equal(t, 2, negStatusRank(model.OTCNegotiationStatusExpired))
}

func TestPickActiveChain_NonTerminalWins(t *testing.T) {
	old := time.Now().Add(-2 * time.Hour)
	newer := time.Now().Add(-1 * time.Hour)
	chains := []*model.OTCNegotiation{
		{ID: 1, Status: model.OTCNegotiationStatusCancelled, CreatedAt: newer}, // terminal, newest
		{ID: 2, Status: model.OTCNegotiationStatusCountered, CreatedAt: old},   // live, older
	}
	got := pickActiveChain(chains, nil)
	require.Equal(t, uint64(2), got.id, "non-terminal chain must beat a newer terminal one")
	require.Equal(t, model.OTCNegotiationStatusCountered, got.status)
}

func TestPickActiveChain_AcceptedBeatsOpen(t *testing.T) {
	chains := []*model.OTCNegotiation{
		{ID: 1, Status: model.OTCNegotiationStatusOpen, CreatedAt: time.Now()},
		{ID: 2, Status: model.OTCNegotiationStatusAccepted, CreatedAt: time.Now().Add(-time.Hour)},
	}
	got := pickActiveChain(chains, nil)
	require.Equal(t, uint64(2), got.id, "accepted (contract) outranks open")
}

func TestPickActiveChain_AllTerminalMostRecent(t *testing.T) {
	old := time.Now().Add(-2 * time.Hour)
	newer := time.Now().Add(-1 * time.Hour)
	chains := []*model.OTCNegotiation{
		{ID: 1, Status: model.OTCNegotiationStatusRejected, CreatedAt: old},
		{ID: 2, Status: model.OTCNegotiationStatusCancelled, CreatedAt: newer},
	}
	got := pickActiveChain(chains, nil)
	require.Equal(t, uint64(2), got.id, "all terminal → most recently created wins")
}

// ---------------- ListUnifiedOptionOffers stamping ----------------

func TestListUnifiedOptionOffers_StampsMyNegotiation_LocalAndRemote(t *testing.T) {
	model.SetOwnRouting("111")
	cache := otccache.NewOptionCache()
	otccache.SetOptionForTest(cache, otccache.OptionSnapshot{
		Offers: []otccache.OptionOffer{
			// Local offer the caller bid on (LocalID 42).
			{Kind: "local", BankCode: "111", RoutingNumber: 111, OfferID: "42", LocalID: 42, Ticker: "AAPL", Direction: "sell_initiated"},
			// Local offer the caller did NOT bid on.
			{Kind: "local", BankCode: "111", RoutingNumber: 111, OfferID: "43", LocalID: 43, Ticker: "MSFT", Direction: "sell_initiated"},
			// Remote offer the caller bid on cross-bank (peer 333, native "xyz").
			{Kind: "remote", BankCode: "333", RoutingNumber: 333, OfferID: "xyz", LocalID: 900, Ticker: "JNJ", Direction: "sell_initiated"},
		},
	})

	lister := &fakeMyNegLister{
		localRows: []model.OTCNegotiation{
			{ID: 7, ParentOfferID: 42, BidderOwnerType: model.OwnerClient, BidderOwnerID: u64(5),
				Status: model.OTCNegotiationStatusOpen, RoutingNumber: 111, Quantity: decimal.NewFromInt(1)},
		},
		remoteRows: []model.OTCNegotiation{
			{ID: 88, RoutingNumber: 333, Status: "ongoing",
				RemoteBuyerRouting: i64(111), RemoteBuyerID: str("client-5"),
				RemoteParentRouting: i64(333), RemoteParentNativeID: str("xyz")},
		},
	}

	h := NewOTCHandler().WithOptionCache(cache).WithMyNegotiations(lister, 111)
	resp, err := h.ListUnifiedOptionOffers(context.Background(), &stockpb.ListUnifiedOptionOffersRequest{
		Page: 1, PageSize: 10, ActingOwnerType: "client", ActingOwnerId: 5,
	})
	require.NoError(t, err)
	require.Len(t, resp.GetOffers(), 3)

	byTicker := map[string]*stockpb.UnifiedOptionOffer{}
	for _, o := range resp.GetOffers() {
		byTicker[o.GetTicker()] = o
	}

	// Local offer the caller bid on → stamped.
	require.Equal(t, uint64(7), byTicker["AAPL"].GetMyNegotiationId())
	require.Equal(t, model.OTCNegotiationStatusOpen, byTicker["AAPL"].GetMyNegotiationStatus())

	// Local offer the caller did NOT bid on → absent.
	require.Equal(t, uint64(0), byTicker["MSFT"].GetMyNegotiationId())
	require.Equal(t, "", byTicker["MSFT"].GetMyNegotiationStatus())

	// Remote offer the caller bid on cross-bank → stamped.
	require.Equal(t, uint64(88), byTicker["JNJ"].GetMyNegotiationId())
	require.Equal(t, "ongoing", byTicker["JNJ"].GetMyNegotiationStatus())
}

// TestListUnifiedOptionOffers_BankBidder_StampsRemote: a bank that bid on a
// remote offer (party id "employee-<N>") sees its my_negotiation_id stamped on
// that offer in discovery. The bank's remote bidder chain comes from
// ListRemoteNegByBankParty (prefix-matched), keyed to the offer by its
// (RemoteParentRouting, RemoteParentNativeID) lot key. SP-3 Task 5b.
func TestListUnifiedOptionOffers_BankBidder_StampsRemote(t *testing.T) {
	model.SetOwnRouting("111")
	cache := otccache.NewOptionCache()
	otccache.SetOptionForTest(cache, otccache.OptionSnapshot{
		Offers: []otccache.OptionOffer{
			// Remote offer the BANK bid on cross-bank (peer 333, native "xyz").
			{Kind: "remote", BankCode: "333", RoutingNumber: 333, OfferID: "xyz", LocalID: 900, Ticker: "JNJ", Direction: "sell_initiated"},
			// A remote offer the bank did NOT bid on.
			{Kind: "remote", BankCode: "333", RoutingNumber: 333, OfferID: "zzz", LocalID: 901, Ticker: "MSFT", Direction: "sell_initiated"},
		},
	})

	lister := &fakeMyNegLister{
		bankRemoteRows: []model.OTCNegotiation{
			{ID: 88, RoutingNumber: 333, Status: "ongoing",
				RemoteBuyerRouting: i64(111), RemoteBuyerID: str("employee-5"),
				RemoteParentRouting: i64(333), RemoteParentNativeID: str("xyz")},
		},
	}

	h := NewOTCHandler().WithOptionCache(cache).WithMyNegotiations(lister, 111)
	resp, err := h.ListUnifiedOptionOffers(context.Background(), &stockpb.ListUnifiedOptionOffersRequest{
		Page: 1, PageSize: 10, ActingOwnerType: "bank", ActingOwnerId: 0,
	})
	require.NoError(t, err)
	require.Len(t, resp.GetOffers(), 2)

	byTicker := map[string]*stockpb.UnifiedOptionOffer{}
	for _, o := range resp.GetOffers() {
		byTicker[o.GetTicker()] = o
	}
	// Remote offer the bank bid on → stamped with the bank's chain.
	require.Equal(t, uint64(88), byTicker["JNJ"].GetMyNegotiationId())
	require.Equal(t, "ongoing", byTicker["JNJ"].GetMyNegotiationStatus())
	// Remote offer the bank did NOT bid on → absent.
	require.Equal(t, uint64(0), byTicker["MSFT"].GetMyNegotiationId())
}

func TestListUnifiedOptionOffers_MultipleChains_ActiveWins(t *testing.T) {
	model.SetOwnRouting("111")
	cache := otccache.NewOptionCache()
	otccache.SetOptionForTest(cache, otccache.OptionSnapshot{
		Offers: []otccache.OptionOffer{
			{Kind: "local", BankCode: "111", RoutingNumber: 111, OfferID: "42", LocalID: 42, Ticker: "AAPL", Direction: "sell_initiated"},
		},
	})
	// Two chains on the same offer: a newer terminal one and an older live one.
	lister := &fakeMyNegLister{
		localRows: []model.OTCNegotiation{
			{ID: 10, ParentOfferID: 42, BidderOwnerType: model.OwnerClient, BidderOwnerID: u64(5),
				Status: model.OTCNegotiationStatusCancelled, RoutingNumber: 111,
				CreatedAt: time.Now(), Quantity: decimal.NewFromInt(1)},
			{ID: 11, ParentOfferID: 42, BidderOwnerType: model.OwnerClient, BidderOwnerID: u64(5),
				Status: model.OTCNegotiationStatusCountered, RoutingNumber: 111,
				CreatedAt: time.Now().Add(-time.Hour), Quantity: decimal.NewFromInt(1)},
		},
	}
	h := NewOTCHandler().WithOptionCache(cache).WithMyNegotiations(lister, 111)
	resp, err := h.ListUnifiedOptionOffers(context.Background(), &stockpb.ListUnifiedOptionOffersRequest{
		Page: 1, PageSize: 10, ActingOwnerType: "client", ActingOwnerId: 5,
	})
	require.NoError(t, err)
	require.Len(t, resp.GetOffers(), 1)
	require.Equal(t, uint64(11), resp.GetOffers()[0].GetMyNegotiationId(), "live chain beats newer terminal one")
	require.Equal(t, model.OTCNegotiationStatusCountered, resp.GetOffers()[0].GetMyNegotiationStatus())
}

// ---------------- D2: per-viewer term projection on the offer DTO ----------------

// A BIDDER (me_owner==false) with a chain on the offer sees that chain's
// CURRENT terms re-sourced onto strike_price/premium/settlement_date.
func TestListUnifiedOptionOffers_BidderSeesChainTerms(t *testing.T) {
	model.SetOwnRouting("111")
	cache := otccache.NewOptionCache()
	otccache.SetOptionForTest(cache, otccache.OptionSnapshot{
		Offers: []otccache.OptionOffer{
			// Termless local listing (poster client 7); the viewer is a bidder.
			{Kind: "local", BankCode: "111", RoutingNumber: 111, OfferID: "42", LocalID: 42,
				SellerID: "client-7", Ticker: "AAPL", Direction: "sell_initiated"},
		},
	})
	lister := &fakeMyNegLister{
		localRows: []model.OTCNegotiation{
			{ID: 7, ParentOfferID: 42, BidderOwnerType: model.OwnerClient, BidderOwnerID: u64(5),
				Status: model.OTCNegotiationStatusCountered, RoutingNumber: 111,
				Quantity:       decimal.NewFromInt(3),
				StrikePrice:    decimal.NewFromInt(100),
				Premium:        decimal.NewFromInt(8),
				SettlementDate: time.Date(2030, 12, 31, 0, 0, 0, 0, time.UTC)},
		},
	}
	h := NewOTCHandler().WithOptionCache(cache).WithMyNegotiations(lister, 111)
	resp, err := h.ListUnifiedOptionOffers(context.Background(), &stockpb.ListUnifiedOptionOffersRequest{
		Page: 1, PageSize: 10, ActingOwnerType: "client", ActingOwnerId: 5,
		ActorUserId: 5, ActorSystemType: "client",
	})
	require.NoError(t, err)
	require.Len(t, resp.GetOffers(), 1)
	item := resp.GetOffers()[0]
	require.False(t, item.GetMeOwner(), "viewer is a bidder, not the owner")
	require.Equal(t, "100.00", item.GetStrikePrice())
	require.Equal(t, "8.00", item.GetPremium())
	require.Equal(t, "2030-12-31T00:00:00Z", item.GetSettlementDate())
}

// The OWNER (me_owner==true) of a LOCAL offer sees their most recent counter
// terms, fetched via the wired owner-latest-counter source.
func TestListUnifiedOptionOffers_OwnerSeesLatestCounter(t *testing.T) {
	model.SetOwnRouting("111")
	cache := otccache.NewOptionCache()
	otccache.SetOptionForTest(cache, otccache.OptionSnapshot{
		Offers: []otccache.OptionOffer{
			{Kind: "local", BankCode: "111", RoutingNumber: 111, OfferID: "42", LocalID: 42,
				SellerID: "client-5", Ticker: "AAPL", Direction: "sell_initiated"},
		},
	})
	var gotOffer uint64
	var gotType string
	var gotID uint64
	fn := func(offerID uint64, principalType string, principalID uint64) (*OfferTerms, error) {
		gotOffer, gotType, gotID = offerID, principalType, principalID
		return &OfferTerms{StrikePrice: "120.00", Premium: "9.00", SettlementDate: "2031-01-31T00:00:00Z"}, nil
	}
	h := NewOTCHandler().WithOptionCache(cache).WithOwnerLatestCounter(fn)
	resp, err := h.ListUnifiedOptionOffers(context.Background(), &stockpb.ListUnifiedOptionOffersRequest{
		Page: 1, PageSize: 10, ActingOwnerType: "client", ActingOwnerId: 5,
		ActorUserId: 5, ActorSystemType: "client",
	})
	require.NoError(t, err)
	require.Len(t, resp.GetOffers(), 1)
	item := resp.GetOffers()[0]
	require.True(t, item.GetMeOwner(), "viewer owns this listing")
	require.Equal(t, "120.00", item.GetStrikePrice())
	require.Equal(t, "9.00", item.GetPremium())
	require.Equal(t, "2031-01-31T00:00:00Z", item.GetSettlementDate())
	// Resolved against the LOCAL offer id + the acting PRINCIPAL (not "bank").
	require.Equal(t, uint64(42), gotOffer)
	require.Equal(t, "client", gotType)
	require.Equal(t, uint64(5), gotID)
}

// An OWNER whose latest-counter source returns nil (never countered) sees
// empty terms.
func TestListUnifiedOptionOffers_OwnerNoCounter_Empty(t *testing.T) {
	model.SetOwnRouting("111")
	cache := otccache.NewOptionCache()
	otccache.SetOptionForTest(cache, otccache.OptionSnapshot{
		Offers: []otccache.OptionOffer{
			{Kind: "local", BankCode: "111", RoutingNumber: 111, OfferID: "42", LocalID: 42,
				SellerID: "client-5", Ticker: "AAPL", Direction: "sell_initiated"},
		},
	})
	fn := func(uint64, string, uint64) (*OfferTerms, error) { return nil, nil }
	h := NewOTCHandler().WithOptionCache(cache).WithOwnerLatestCounter(fn)
	resp, err := h.ListUnifiedOptionOffers(context.Background(), &stockpb.ListUnifiedOptionOffersRequest{
		Page: 1, PageSize: 10, ActingOwnerType: "client", ActingOwnerId: 5,
		ActorUserId: 5, ActorSystemType: "client",
	})
	require.NoError(t, err)
	require.Len(t, resp.GetOffers(), 1)
	item := resp.GetOffers()[0]
	require.True(t, item.GetMeOwner())
	require.Equal(t, "", item.GetStrikePrice())
	require.Equal(t, "", item.GetPremium())
	require.Equal(t, "", item.GetSettlementDate())
}

// A NON-PARTICIPANT (not the owner, no chain) sees empty terms.
func TestListUnifiedOptionOffers_NonParticipant_Empty(t *testing.T) {
	model.SetOwnRouting("111")
	cache := otccache.NewOptionCache()
	otccache.SetOptionForTest(cache, otccache.OptionSnapshot{
		Offers: []otccache.OptionOffer{
			{Kind: "local", BankCode: "111", RoutingNumber: 111, OfferID: "42", LocalID: 42,
				SellerID: "client-7", Ticker: "AAPL", Direction: "sell_initiated"},
		},
	})
	// Lister has no chain for viewer client 5; owner-latest-counter wired but the
	// viewer is not the owner so it is never consulted.
	fn := func(uint64, string, uint64) (*OfferTerms, error) {
		t.Fatalf("owner-latest-counter must not be called for a non-owner")
		return nil, nil
	}
	h := NewOTCHandler().WithOptionCache(cache).
		WithMyNegotiations(&fakeMyNegLister{}, 111).WithOwnerLatestCounter(fn)
	resp, err := h.ListUnifiedOptionOffers(context.Background(), &stockpb.ListUnifiedOptionOffersRequest{
		Page: 1, PageSize: 10, ActingOwnerType: "client", ActingOwnerId: 5,
		ActorUserId: 5, ActorSystemType: "client",
	})
	require.NoError(t, err)
	require.Len(t, resp.GetOffers(), 1)
	item := resp.GetOffers()[0]
	require.False(t, item.GetMeOwner())
	require.Equal(t, uint64(0), item.GetMyNegotiationId())
	require.Equal(t, "", item.GetStrikePrice())
	require.Equal(t, "", item.GetPremium())
	require.Equal(t, "", item.GetSettlementDate())
}

func TestListUnifiedOptionOffers_NoLister_NoStamp(t *testing.T) {
	model.SetOwnRouting("111")
	cache := otccache.NewOptionCache()
	otccache.SetOptionForTest(cache, otccache.OptionSnapshot{
		Offers: []otccache.OptionOffer{
			{Kind: "local", BankCode: "111", RoutingNumber: 111, OfferID: "42", LocalID: 42, Ticker: "AAPL", Direction: "sell_initiated"},
		},
	})
	h := NewOTCHandler().WithOptionCache(cache) // no WithMyNegotiations
	resp, err := h.ListUnifiedOptionOffers(context.Background(), &stockpb.ListUnifiedOptionOffersRequest{
		Page: 1, PageSize: 10, ActingOwnerType: "client", ActingOwnerId: 5,
	})
	require.NoError(t, err)
	require.Len(t, resp.GetOffers(), 1)
	require.Equal(t, uint64(0), resp.GetOffers()[0].GetMyNegotiationId())
}

// ---------------- GetOffer stamping ----------------

func newGetOfferDBFixture(t *testing.T) (*OTCOptionsHandler, *gorm.DB, uint64) {
	t.Helper()
	model.SetOwnRouting("111")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.Holding{}, &model.OTCOffer{}, &model.OTCOfferRevision{},
		&model.OptionContract{}, &model.OTCOfferReadReceipt{}, &model.Listing{}, &model.Stock{},
		&model.OTCNegotiation{},
	))
	fx := newOTCOptionsHandlerFixtureFromDB(t, db)
	fx.seedSellerHolding(t, 7, 42, 100)
	id := fx.createOffer(t, 7, 42)
	return fx.h, db, id
}

func TestGetOffer_StampsMyNegotiation_Local(t *testing.T) {
	h, db, offerID := newGetOfferDBFixture(t)

	// Make client 5 the offer's counterparty (a directed offer) so they pass
	// the GetOffer participant gate while also being the bidder. Load → modify →
	// save the struct so the versioned BeforeUpdate hook is satisfied.
	var off model.OTCOffer
	require.NoError(t, db.First(&off, offerID).Error)
	cpType := model.OwnerClient
	off.CounterpartyOwnerType = &cpType
	off.CounterpartyOwnerID = u64(5)
	require.NoError(t, db.Save(&off).Error)

	// Bidder client 5 has a live chain on this local offer.
	require.NoError(t, db.Create(&model.OTCNegotiation{
		ParentOfferID: offerID, BidderOwnerType: model.OwnerClient, BidderOwnerID: u64(5),
		Status: model.OTCNegotiationStatusOpen, RoutingNumber: 111,
		Quantity: decimal.NewFromInt(1), StrikePrice: decimal.NewFromInt(150), Premium: decimal.NewFromInt(20),
		SettlementDate:            time.Now().AddDate(0, 0, 30),
		LastActionByPrincipalType: "client", LastActionByPrincipalID: 5,
		LastActionByOwnerType: "client", LastActionAt: time.Now(),
	}).Error)

	negRepoLister := &fakeMyNegLister{localRows: mustLoadNegs(t, db)}
	h2 := h.WithMyNegotiations(negRepoLister).WithRemoteOffers(&fakeRemoteOffers{}, "111").WithPeerContracts(nil, 111)

	// The bidder sees my_negotiation_id stamped.
	resp, err := h2.GetOffer(context.Background(), &stockpb.GetOTCOfferRequest{
		OfferId: offerID, ActorUserId: 5, ActorSystemType: "client",
		ActingOwnerType: "client", ActingOwnerId: 5,
	})
	require.NoError(t, err)
	require.NotZero(t, resp.GetOffer().GetMyNegotiationId())
	require.Equal(t, model.OTCNegotiationStatusOpen, resp.GetOffer().GetMyNegotiationStatus())
}

func TestGetOffer_PosterNotBidder_NoStamp(t *testing.T) {
	h, db, offerID := newGetOfferDBFixture(t)
	// Client 5 bid on the offer, but we fetch AS the poster (client 7).
	require.NoError(t, db.Create(&model.OTCNegotiation{
		ParentOfferID: offerID, BidderOwnerType: model.OwnerClient, BidderOwnerID: u64(5),
		Status: model.OTCNegotiationStatusOpen, RoutingNumber: 111,
		Quantity: decimal.NewFromInt(1), StrikePrice: decimal.NewFromInt(150), Premium: decimal.NewFromInt(20),
		SettlementDate:            time.Now().AddDate(0, 0, 30),
		LastActionByPrincipalType: "client", LastActionByPrincipalID: 5,
		LastActionByOwnerType: "client", LastActionAt: time.Now(),
	}).Error)

	h2 := h.WithMyNegotiations(&fakeMyNegLister{localRows: mustLoadNegs(t, db)}).
		WithRemoteOffers(&fakeRemoteOffers{}, "111").WithPeerContracts(nil, 111)

	// Poster (client 7) is me_owner but has no bidder chain → my_negotiation_id absent.
	resp, err := h2.GetOffer(context.Background(), &stockpb.GetOTCOfferRequest{
		OfferId: offerID, ActorUserId: 7, ActorSystemType: "client",
		ActingOwnerType: "client", ActingOwnerId: 7,
	})
	require.NoError(t, err)
	require.True(t, resp.GetOffer().GetMeOwner(), "poster is the owner")
	require.Equal(t, uint64(0), resp.GetOffer().GetMyNegotiationId(), "poster has no bidder chain")
}

func TestGetOffer_StampsMyNegotiation_Remote(t *testing.T) {
	model.SetOwnRouting("111")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.Holding{}, &model.OTCOffer{}, &model.OTCOfferRevision{},
		&model.OptionContract{}, &model.OTCOfferReadReceipt{}, &model.Listing{}, &model.Stock{}, &model.OTCNegotiation{},
	))
	fx := newOTCOptionsHandlerFixtureFromDB(t, db)

	foreignID := "xyz"
	bankCode := "333"
	sellerID := "client-9"
	remote := &model.OTCOffer{
		ID: 555, RoutingNumber: 333, NativeID: &foreignID,
		InitiatorBankCode: &bankCode, RemoteSellerID: &sellerID,
		InitiatorOwnerType: model.OwnerBank, Direction: model.OTCDirectionSellInitiated,
		Ticker: "JNJ", Quantity: decimal.NewFromInt(10), Status: "open",
	}
	lister := &fakeMyNegLister{remoteRows: []model.OTCNegotiation{
		{ID: 88, RoutingNumber: 333, Status: "ongoing",
			RemoteBuyerRouting: i64(111), RemoteBuyerID: str("client-5"),
			RemoteParentRouting: i64(333), RemoteParentNativeID: str("xyz")},
	}}

	h := fx.h.WithRemoteOffers(&fakeRemoteOffers{row: remote}, "111").
		WithPeerContracts(nil, 111).WithMyNegotiations(lister)

	resp, err := h.GetOffer(context.Background(), &stockpb.GetOTCOfferRequest{
		OfferId: 555, ActorUserId: 5, ActorSystemType: "client",
		ActingOwnerType: "client", ActingOwnerId: 5,
	})
	require.NoError(t, err)
	require.Equal(t, "remote", resp.GetOffer().GetKind())
	require.Equal(t, uint64(88), resp.GetOffer().GetMyNegotiationId())
	require.Equal(t, "ongoing", resp.GetOffer().GetMyNegotiationStatus())
}

// D2 — GetOffer projects the BIDDER's own chain terms onto the detail response.
func TestGetOffer_BidderSeesChainTerms(t *testing.T) {
	h, db, offerID := newGetOfferDBFixture(t)

	// Direct the offer at client 5 so they pass the participant gate as bidder.
	var off model.OTCOffer
	require.NoError(t, db.First(&off, offerID).Error)
	cpType := model.OwnerClient
	off.CounterpartyOwnerType = &cpType
	off.CounterpartyOwnerID = u64(5)
	require.NoError(t, db.Save(&off).Error)

	require.NoError(t, db.Create(&model.OTCNegotiation{
		ParentOfferID: offerID, BidderOwnerType: model.OwnerClient, BidderOwnerID: u64(5),
		Status: model.OTCNegotiationStatusCountered, RoutingNumber: 111,
		Quantity: decimal.NewFromInt(2), StrikePrice: decimal.NewFromInt(100), Premium: decimal.NewFromInt(8),
		SettlementDate:            time.Date(2030, 12, 31, 0, 0, 0, 0, time.UTC),
		LastActionByPrincipalType: "client", LastActionByPrincipalID: 5,
		LastActionByOwnerType: "client", LastActionAt: time.Now(),
	}).Error)

	h2 := h.WithMyNegotiations(&fakeMyNegLister{localRows: mustLoadNegs(t, db)}).
		WithRemoteOffers(&fakeRemoteOffers{}, "111").WithPeerContracts(nil, 111)

	resp, err := h2.GetOffer(context.Background(), &stockpb.GetOTCOfferRequest{
		OfferId: offerID, ActorUserId: 5, ActorSystemType: "client",
		ActingOwnerType: "client", ActingOwnerId: 5,
	})
	require.NoError(t, err)
	require.False(t, resp.GetOffer().GetMeOwner())
	require.Equal(t, "100.00", resp.GetOffer().GetStrikePrice())
	require.Equal(t, "8.00", resp.GetOffer().GetPremium())
	require.Equal(t, "2030-12-31T00:00:00Z", resp.GetOffer().GetSettlementDate())
}

// D2 — GetOffer projects the OWNER's most recent counter terms (via the wired
// owner-latest-counter source) onto the detail response.
func TestGetOffer_OwnerSeesLatestCounter(t *testing.T) {
	h, _, offerID := newGetOfferDBFixture(t)

	var gotOffer uint64
	var gotType string
	var gotID uint64
	fn := func(oid uint64, ptype string, pid uint64) (*OfferTerms, error) {
		gotOffer, gotType, gotID = oid, ptype, pid
		return &OfferTerms{StrikePrice: "120.00", Premium: "11.00", SettlementDate: "2031-01-31T00:00:00Z"}, nil
	}
	h2 := h.WithMyNegotiations(&fakeMyNegLister{}).
		WithRemoteOffers(&fakeRemoteOffers{}, "111").WithPeerContracts(nil, 111).
		WithOwnerLatestCounter(fn)

	// Poster client 7 views their own offer → me_owner, owner branch.
	resp, err := h2.GetOffer(context.Background(), &stockpb.GetOTCOfferRequest{
		OfferId: offerID, ActorUserId: 7, ActorSystemType: "client",
		ActingOwnerType: "client", ActingOwnerId: 7,
	})
	require.NoError(t, err)
	require.True(t, resp.GetOffer().GetMeOwner())
	require.Equal(t, "120.00", resp.GetOffer().GetStrikePrice())
	require.Equal(t, "11.00", resp.GetOffer().GetPremium())
	require.Equal(t, "2031-01-31T00:00:00Z", resp.GetOffer().GetSettlementDate())
	require.Equal(t, offerID, gotOffer)
	require.Equal(t, "client", gotType)
	require.Equal(t, uint64(7), gotID)
}

func mustLoadNegs(t *testing.T, db *gorm.DB) []model.OTCNegotiation {
	t.Helper()
	var rows []model.OTCNegotiation
	require.NoError(t, db.Find(&rows).Error)
	return rows
}

// TestBuildMyNegotiationIndex_ExcludesTerminalRemoteChains is the regression for
// the re-list independence bug: after a buyer's negotiation is ACCEPTED on a
// (seller, ticker) listing, a later listing for the SAME (seller, ticker) — which
// shares the composite parent key — must NOT show "you already bid". Only ONGOING
// remote chains are indexed; terminal (accepted/cancelled/…) chains are excluded.
func TestBuildMyNegotiationIndex_ExcludesTerminalRemoteChains(t *testing.T) {
	const ownRouting int64 = 222
	const sellerRouting int64 = 111
	buyer := "client-7"
	acceptedNative := "ps:111:client-9:AAPL"
	ongoingNative := "ps:111:client-9:MSFT"
	mk := func(status, native string, id uint64) model.OTCNegotiation {
		pr := sellerRouting
		pn := native
		b := buyer
		return model.OTCNegotiation{
			ID: id, Local: false, Status: status,
			RemoteBuyerID:        &b,
			RemoteParentRouting:  &pr,
			RemoteParentNativeID: &pn,
		}
	}
	lister := &fakeMyNegLister{
		remoteRows: []model.OTCNegotiation{
			mk("accepted", acceptedNative, 1),
			mk("ongoing", ongoingNative, 2),
		},
	}
	idx, err := buildMyNegotiationIndex(lister, "client", 7, ownRouting)
	if err != nil {
		t.Fatalf("build index: %v", err)
	}
	if _, ok := idx.remote[remoteParentKey(sellerRouting, acceptedNative)]; ok {
		t.Errorf("accepted (terminal) chain must NOT be indexed — it would show 'already bid' on a re-listed same-(seller,ticker) offer")
	}
	if _, ok := idx.remote[remoteParentKey(sellerRouting, ongoingNative)]; !ok {
		t.Errorf("ongoing chain SHOULD be indexed (an active bid still marks its listing)")
	}
}
