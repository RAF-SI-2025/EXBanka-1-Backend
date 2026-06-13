//go:build integration

package workflows

import (
	"fmt"
	"testing"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestSP1_ClientReplica_NotificationUsesResolvedEmail verifies the SP-1 end-to-end path:
// creating a client publishes client.created (card-service builds its ClientReplica from it),
// and blocking that client's card resolves the owner email (via the replica, gRPC fallback
// otherwise) and sends a CARD_STATUS_CHANGED notification to the correct address.
func TestSP1_ClientReplica_NotificationUsesResolvedEmail(t *testing.T) {
	adminC := loginAsAdmin(t)
	_, _, cardID, _, email := setupClientWithCard(t, adminC, "visa")

	resp, err := adminC.POST(fmt.Sprintf("/api/v3/cards/%d/block", cardID), nil)
	if err != nil {
		t.Fatalf("block card: %v", err)
	}
	helpers.RequireStatus(t, resp, 200)

	if !scanKafkaForEmailType(t, email, "CARD_STATUS_CHANGED") {
		t.Fatalf("expected CARD_STATUS_CHANGED notification to %s after blocking card %d", email, cardID)
	}
}
