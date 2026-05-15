package kafka

import (
	"encoding/json"
	"testing"
)

func TestGeneralNotificationMessage_DataRoundTrip(t *testing.T) {
	msg := GeneralNotificationMessage{
		UserID: 42,
		Type:   "ORDER_FILLED",
		Data:   map[string]string{"ticker": "AAPL", "quantity": "10", "direction": "buy"},
		RefType: "order",
		RefID:   7,
	}
	b, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got GeneralNotificationMessage
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Type != "ORDER_FILLED" || got.Data["ticker"] != "AAPL" || got.RefID != 7 {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	// Legacy form (no Data) still round-trips.
	legacy := GeneralNotificationMessage{UserID: 1, Type: "password_changed", Title: "T", Message: "M"}
	lb, _ := json.Marshal(legacy)
	var lgot GeneralNotificationMessage
	if err := json.Unmarshal(lb, &lgot); err != nil {
		t.Fatalf("legacy unmarshal: %v", err)
	}
	if lgot.Title != "T" || lgot.Message != "M" || len(lgot.Data) != 0 {
		t.Errorf("legacy round-trip mismatch: %+v", lgot)
	}
}
