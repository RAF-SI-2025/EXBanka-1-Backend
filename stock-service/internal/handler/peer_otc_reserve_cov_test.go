package handler_test

import (
	"context"
	"errors"
	"testing"

	stockpb "github.com/exbanka/contract/stockpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestReserveSellerSharesForNewTx(t *testing.T) {
	ctx := context.Background()

	t.Run("invalid args", func(t *testing.T) {
		h, _, _, _ := newPeerOtcHandler(t)
		_, err := h.ReserveSellerSharesForNewTx(ctx, &stockpb.ReserveSellerSharesRequest{})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("want InvalidArgument, got %v", err)
		}
	})

	t.Run("no reserver wired votes no", func(t *testing.T) {
		h, _, _, _ := newPeerOtcHandler(t)
		resp, err := h.ReserveSellerSharesForNewTx(ctx, &stockpb.ReserveSellerSharesRequest{
			SellerId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-5"},
			Ticker:   "AAPL", Quantity: 3, CrossbankTxId: "tx-1",
		})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if resp.GetOk() {
			t.Fatal("want ok=false with no reserver")
		}
	})

	t.Run("routing mismatch votes no", func(t *testing.T) {
		h, _, _, _ := newPeerOtcHandler(t)
		h.SetHoldingReserver(&fakeReserver{})
		resp, err := h.ReserveSellerSharesForNewTx(ctx, &stockpb.ReserveSellerSharesRequest{
			SellerId: &stockpb.PeerForeignBankId{RoutingNumber: 999, Id: "client-5"},
			Ticker:   "AAPL", Quantity: 3, CrossbankTxId: "tx-1",
		})
		if err != nil || resp.GetOk() {
			t.Fatalf("want ok=false on routing mismatch, got resp=%v err=%v", resp, err)
		}
	})

	t.Run("unparseable seller id votes no", func(t *testing.T) {
		h, _, _, _ := newPeerOtcHandler(t)
		h.SetHoldingReserver(&fakeReserver{})
		resp, err := h.ReserveSellerSharesForNewTx(ctx, &stockpb.ReserveSellerSharesRequest{
			SellerId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "garbage"},
			Ticker:   "AAPL", Quantity: 3, CrossbankTxId: "tx-1",
		})
		if err != nil || resp.GetOk() {
			t.Fatalf("want ok=false on bad seller id, got resp=%v err=%v", resp, err)
		}
	})

	t.Run("success", func(t *testing.T) {
		h, _, _, _ := newPeerOtcHandler(t)
		h.SetHoldingReserver(&fakeReserver{})
		resp, err := h.ReserveSellerSharesForNewTx(ctx, &stockpb.ReserveSellerSharesRequest{
			SellerId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-5"},
			Ticker:   "AAPL", Quantity: 3, CrossbankTxId: "tx-1",
		})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !resp.GetOk() || resp.GetReservedQuantity() != 3 {
			t.Fatalf("unexpected success resp: %+v", resp)
		}
	})

	t.Run("failed precondition votes no", func(t *testing.T) {
		h, _, _, _ := newPeerOtcHandler(t)
		h.SetHoldingReserver(&fakeReserver{newTxReserveErr: status.Error(codes.FailedPrecondition, "insufficient")})
		resp, err := h.ReserveSellerSharesForNewTx(ctx, &stockpb.ReserveSellerSharesRequest{
			SellerId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-5"},
			Ticker:   "AAPL", Quantity: 3, CrossbankTxId: "tx-1",
		})
		if err != nil || resp.GetOk() {
			t.Fatalf("want ok=false on FailedPrecondition, got resp=%v err=%v", resp, err)
		}
	})

	t.Run("other error propagates as internal", func(t *testing.T) {
		h, _, _, _ := newPeerOtcHandler(t)
		h.SetHoldingReserver(&fakeReserver{newTxReserveErr: errors.New("db down")})
		_, err := h.ReserveSellerSharesForNewTx(ctx, &stockpb.ReserveSellerSharesRequest{
			SellerId: &stockpb.PeerForeignBankId{RoutingNumber: 111, Id: "client-5"},
			Ticker:   "AAPL", Quantity: 3, CrossbankTxId: "tx-1",
		})
		if status.Code(err) != codes.Internal {
			t.Fatalf("want Internal, got %v", err)
		}
	})
}

func TestReleaseSellerSharesForNewTx(t *testing.T) {
	ctx := context.Background()

	t.Run("missing crossbank id", func(t *testing.T) {
		h, _, _, _ := newPeerOtcHandler(t)
		_, err := h.ReleaseSellerSharesForNewTx(ctx, &stockpb.ReleaseSellerSharesRequest{})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("want InvalidArgument, got %v", err)
		}
	})

	t.Run("no reserver releases zero", func(t *testing.T) {
		h, _, _, _ := newPeerOtcHandler(t)
		resp, err := h.ReleaseSellerSharesForNewTx(ctx, &stockpb.ReleaseSellerSharesRequest{CrossbankTxId: "tx-1"})
		if err != nil || resp.GetReleasedQuantity() != 0 {
			t.Fatalf("want zero release, got resp=%v err=%v", resp, err)
		}
	})

	t.Run("success", func(t *testing.T) {
		h, _, _, _ := newPeerOtcHandler(t)
		h.SetHoldingReserver(&fakeReserver{})
		resp, err := h.ReleaseSellerSharesForNewTx(ctx, &stockpb.ReleaseSellerSharesRequest{CrossbankTxId: "tx-1"})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if resp == nil {
			t.Fatal("nil response")
		}
	})

	t.Run("error propagates as internal", func(t *testing.T) {
		h, _, _, _ := newPeerOtcHandler(t)
		h.SetHoldingReserver(&fakeReserver{releaseTxErr: errors.New("boom")})
		_, err := h.ReleaseSellerSharesForNewTx(ctx, &stockpb.ReleaseSellerSharesRequest{CrossbankTxId: "tx-1"})
		if status.Code(err) != codes.Internal {
			t.Fatalf("want Internal, got %v", err)
		}
	})
}

func TestPeerOTCHandler_WithCapitalGain(t *testing.T) {
	h, _, _, _ := newPeerOtcHandler(t)
	if got := h.WithCapitalGain(nil); got == nil {
		t.Fatal("WithCapitalGain returned nil")
	}
}
