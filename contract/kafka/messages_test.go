package kafka

import (
	"encoding/json"
	"testing"
)

func TestClientCreatedMessage_CarriesJMBGAndVersion(t *testing.T) {
	in := ClientCreatedMessage{
		ClientID: 7, Email: "a@b.com", FirstName: "Ana", LastName: "Anic",
		JMBG: "1234567890123", Version: 5,
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out ClientCreatedMessage
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.JMBG != "1234567890123" || out.Version != 5 {
		t.Fatalf("lost fields: %+v", out)
	}
}

func TestGeneralNotificationMessage_DataRoundTrip(t *testing.T) {
	msg := GeneralNotificationMessage{
		UserID:  42,
		Type:    "ORDER_FILLED",
		Data:    map[string]string{"ticker": "AAPL", "quantity": "10", "direction": "buy"},
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

func TestClientLimitsUpdatedMessage_CarriesValuesAndVersion(t *testing.T) {
	in := ClientLimitsUpdatedMessage{
		ClientID: 3, SetByEmployee: 7, Action: "set",
		DailyLimit: "50000.0000", MonthlyLimit: "500000.0000",
		TransferLimit: "20000.0000", Version: 2,
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out ClientLimitsUpdatedMessage
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.DailyLimit != "50000.0000" || out.MonthlyLimit != "500000.0000" ||
		out.TransferLimit != "20000.0000" || out.Version != 2 {
		t.Fatalf("lost fields: %+v", out)
	}
}

func TestEmployeeLimitsUpdatedMessage_CarriesValuesAndVersion(t *testing.T) {
	in := EmployeeLimitsUpdatedMessage{
		EmployeeID: 9, Action: "set",
		MaxLoanApprovalAmount: "50000.0000", MaxSingleTransaction: "10000.0000",
		MaxDailyTransaction: "20000.0000", MaxClientDailyLimit: "5000.0000",
		MaxClientMonthlyLimit: "100000.0000", Version: 4,
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out EmployeeLimitsUpdatedMessage
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.MaxLoanApprovalAmount != "50000.0000" || out.Version != 4 || out.MaxClientMonthlyLimit != "100000.0000" {
		t.Fatalf("lost fields: %+v", out)
	}
}
