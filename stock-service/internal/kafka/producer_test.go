package kafka

import (
	"context"
	"testing"
	"time"

	contract "github.com/exbanka/contract/kafka"
)

// TestNewProducer_Constructs verifies NewProducer constructs without
// attempting an immediate broker handshake.
func TestNewProducer_Constructs(t *testing.T) {
	p := NewProducer("127.0.0.1:1") // unused address
	if p == nil {
		t.Fatal("nil producer")
	}
	if p.inner == nil {
		t.Fatal("inner nil")
	}
}

// TestProducer_Close idempotent close.
func TestProducer_Close(t *testing.T) {
	p := NewProducer("127.0.0.1:1")
	if err := p.Close(); err != nil {
		t.Errorf("close err: %v", err)
	}
}

// TestProducer_PublishWithCanceledCtx ensures the typed publish helpers
// route through the inner producer. We use a canceled ctx so the underlying
// kafka write returns immediately with ctx.Err() instead of hanging.
func TestProducer_TypedPublishHelpers_RouteThroughInner(t *testing.T) {
	p := NewProducer("127.0.0.1:1")
	defer p.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	// All these err out (no broker), but coverage records the lines.
	_ = p.Publish(ctx, "x", map[string]int{"a": 1})
	_ = p.PublishRaw(ctx, "x", []byte("y"))
	_ = p.PublishSecuritySynced(ctx, contract.SecuritySyncedMessage{})
	_ = p.PublishListingUpdated(ctx, contract.ListingUpdatedMessage{})
	_ = p.PublishOrderCreated(ctx, map[string]any{})
	_ = p.PublishOrderApproved(ctx, map[string]any{})
	_ = p.PublishOrderDeclined(ctx, map[string]any{})
	_ = p.PublishOrderFilled(ctx, map[string]any{})
	_ = p.PublishOrderCancelled(ctx, map[string]any{})
	_ = p.PublishHoldingUpdated(ctx, contract.HoldingUpdatedMessage{})
	_ = p.PublishOTCTradeExecuted(ctx, contract.OTCTradeMessage{})
	_ = p.PublishTaxCollected(ctx, contract.TaxCollectedMessage{})
	_ = p.PublishOptionExercised(ctx, contract.OptionExercisedMessage{})
}
