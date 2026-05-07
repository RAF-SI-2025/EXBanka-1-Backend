package handler

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	"github.com/exbanka/stock-service/internal/model"
)

func TestOTCOptionsHandler_OwnerIDEqual(t *testing.T) {
	if !ownerIDEqual(nil, nil) {
		t.Error("nil/nil should be equal")
	}
	a, b := uint64(1), uint64(1)
	if !ownerIDEqual(&a, &b) {
		t.Error("1/1 should be equal")
	}
	c := uint64(2)
	if ownerIDEqual(&a, &c) {
		t.Error("1/2 should not be equal")
	}
	if ownerIDEqual(nil, &a) {
		t.Error("nil/1 should not be equal")
	}
	if ownerIDEqual(&a, nil) {
		t.Error("1/nil should not be equal")
	}
}

func TestMapOTCErr_Nil(t *testing.T) {
	if err := mapOTCErr(nil); err != nil {
		t.Errorf("got %v want nil", err)
	}
}

func TestMapOTCErr_GormNotFound(t *testing.T) {
	err := mapOTCErr(gorm.ErrRecordNotFound)
	if status.Code(err) != codes.NotFound {
		t.Errorf("expected NotFound, got %v", err)
	}
}

func TestMapOTCErr_Passthrough(t *testing.T) {
	sentinel := errors.New("custom")
	got := mapOTCErr(sentinel)
	if !errors.Is(got, sentinel) {
		t.Errorf("expected sentinel, got %v", got)
	}
}

func TestToContractProto_NoTimestamps(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	uid := uint64(7)
	c := &model.OptionContract{
		ID:              42,
		OfferID:         5,
		StockID:         99,
		Quantity:        decimal.NewFromInt(100),
		StrikePrice:     decimal.NewFromInt(150),
		PremiumPaid:     decimal.NewFromInt(500),
		PremiumCurrency: "USD",
		StrikeCurrency:  "USD",
		SettlementDate:  now,
		Status:          model.OptionContractStatusActive,
		BuyerOwnerType:  model.OwnerClient,
		BuyerOwnerID:    &uid,
		SellerOwnerType: model.OwnerBank,
		SellerOwnerID:   nil,
		PremiumPaidAt:   now,
		CreatedAt:       now,
		UpdatedAt:       now,
		Version:         3,
	}
	r := toContractProto(c)
	if r.Id != 42 || r.OfferId != 5 || r.StockId != 99 {
		t.Errorf("ids: %+v", r)
	}
	if r.Quantity != "100" || r.StrikePrice != "150" || r.PremiumPaid != "500" {
		t.Errorf("amounts: %+v", r)
	}
	if r.Buyer.UserId != 7 || r.Buyer.SystemType != "client" {
		t.Errorf("buyer: %+v", r.Buyer)
	}
	if r.Seller.UserId != 0 || r.Seller.SystemType != "bank" {
		t.Errorf("seller: %+v", r.Seller)
	}
	if r.ExercisedAt != "" || r.ExpiredAt != "" {
		t.Errorf("expected empty exercised/expired, got %q/%q", r.ExercisedAt, r.ExpiredAt)
	}
	if r.Version != 3 {
		t.Errorf("version: %d", r.Version)
	}
}

func TestToContractProto_WithExercisedAndExpired(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	exer := now.Add(time.Hour)
	exp := now.Add(2 * time.Hour)
	c := &model.OptionContract{
		BuyerOwnerType:  model.OwnerBank,
		SellerOwnerType: model.OwnerBank,
		ExercisedAt:     &exer,
		ExpiredAt:       &exp,
		SettlementDate:  now,
		PremiumPaidAt:   now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	r := toContractProto(c)
	if r.ExercisedAt == "" {
		t.Error("expected exercised_at set")
	}
	if r.ExpiredAt == "" {
		t.Error("expected expired_at set")
	}
}

func TestToOTCOfferProto_Minimal(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	uid := uint64(99)
	o := &model.OTCOffer{
		ID:                          1,
		Direction:                   model.OTCDirectionSellInitiated,
		StockID:                     7,
		Quantity:                    decimal.NewFromInt(50),
		StrikePrice:                 decimal.NewFromInt(120),
		Premium:                     decimal.NewFromInt(300),
		SettlementDate:              now,
		Status:                      model.OTCOfferStatusPending,
		InitiatorOwnerType:          model.OwnerClient,
		InitiatorOwnerID:            &uid,
		LastModifiedByPrincipalID:   99,
		LastModifiedByPrincipalType: "client",
		CreatedAt:                   now,
		UpdatedAt:                   now,
		Version:                     1,
	}
	r := toOTCOfferProto(o, true)
	if r.Id != 1 || r.StockId != 7 || r.Direction != model.OTCDirectionSellInitiated {
		t.Errorf("basic: %+v", r)
	}
	if r.Initiator == nil || r.Initiator.UserId != 99 {
		t.Errorf("initiator: %+v", r.Initiator)
	}
	if r.Counterparty != nil {
		t.Errorf("expected no counterparty")
	}
	if !r.Unread {
		t.Error("unread flag not propagated")
	}
}

func TestToOTCOfferProto_WithCounterparty(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	cpt := model.OwnerBank
	uid := uint64(7)
	o := &model.OTCOffer{
		ID:                    1,
		InitiatorOwnerType:    model.OwnerClient,
		InitiatorOwnerID:      &uid,
		CounterpartyOwnerType: &cpt,
		CounterpartyOwnerID:   nil,
		SettlementDate:        now,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	r := toOTCOfferProto(o, false)
	if r.Counterparty == nil {
		t.Fatal("expected counterparty")
	}
	if r.Counterparty.SystemType != "bank" || r.Counterparty.UserId != 0 {
		t.Errorf("got %+v", r.Counterparty)
	}
}

func TestPeerContractToProto(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	p := &model.PeerOptionContract{
		ID:                       1,
		CrossbankTxID:            "tx-123",
		PostingIndex:             2,
		NegotiationRoutingNumber: 222,
		NegotiationID:            "neg-1",
		BuyerRoutingNumber:       111,
		BuyerID:                  "client-7",
		SellerRoutingNumber:      222,
		SellerID:                 "client-9",
		Ticker:                   "AAPL",
		Quantity:                 100,
		StrikePrice:              decimal.NewFromInt(150),
		Currency:                 "USD",
		SettlementDate:           "2026-12-31",
		Direction:                "DEBIT",
		Status:                   "active",
		CreatedAt:                now,
	}
	r := peerContractToProto(p)
	if r.Id != 1 || r.CrossbankTxId != "tx-123" || r.Ticker != "AAPL" || r.Quantity != 100 {
		t.Errorf("basic: %+v", r)
	}
	if r.BuyerId == nil || r.BuyerId.RoutingNumber != 111 || r.BuyerId.Id != "client-7" {
		t.Errorf("buyer: %+v", r.BuyerId)
	}
	if r.SellerId == nil || r.SellerId.RoutingNumber != 222 {
		t.Errorf("seller: %+v", r.SellerId)
	}
	if r.StrikePrice != "150" {
		t.Errorf("strike: %s", r.StrikePrice)
	}
	if r.CreatedAtUnix != now.Unix() {
		t.Errorf("created_at: %d", r.CreatedAtUnix)
	}
}
