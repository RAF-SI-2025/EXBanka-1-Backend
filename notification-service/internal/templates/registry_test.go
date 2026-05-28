package templates

import (
	"regexp"
	"testing"
)

var placeholderRE = regexp.MustCompile(`\{\{(\w+)\}\}`)

func TestRegistry_AllEmailTypesPresent(t *testing.T) {
	want := []string{
		"ACTIVATION", "PASSWORD_RESET", "CONFIRMATION", "ACCOUNT_CREATED",
		"CARD_VERIFICATION", "CARD_STATUS_CHANGED", "LOAN_APPROVED", "LOAN_REJECTED",
		"INSTALLMENT_FAILED", "VERIFICATION_CODE", "TRANSACTION_VERIFICATION",
		"PAYMENT_CONFIRMATION", "MOBILE_ACTIVATION",
	}
	for _, typ := range want {
		if _, ok := Get(typ, "email"); !ok {
			t.Errorf("registry missing email type %q", typ)
		}
	}
	if got := len(All("email")); got != len(want) {
		t.Errorf("All(email) returned %d, want %d", got, len(want))
	}
}

func TestRegistry_AllPushTypesPresent(t *testing.T) {
	want := []string{
		"ORDER_PLACED", "ORDER_APPROVED", "ORDER_DECLINED", "ORDER_PARTIALLY_FILLED",
		"ORDER_FILLED", "ORDER_CANCELLED",
		"OTC_OFFER_RECEIVED", "OTC_OFFER_COUNTERED", "OTC_OFFER_REJECTED", "OTC_OFFER_EXPIRED",
		"OTC_OFFER_CANCELLED", "OTC_OFFER_CASCADE_CANCELLED",
		"OTC_CONTRACT_CREATED", "OTC_CONTRACT_EXERCISED", "OTC_CONTRACT_EXPIRED", "OTC_CONTRACT_FAILED",
		"PAYMENT_SENT", "PAYMENT_RECEIVED", "PAYMENT_FAILED",
		"TRANSFER_SENT", "TRANSFER_RECEIVED", "TRANSFER_FAILED",
		"LOAN_REQUEST_SUBMITTED", "LOAN_REQUEST_APPROVED", "LOAN_REQUEST_REJECTED",
		"LOAN_DISBURSED", "INSTALLMENT_COLLECTED", "INSTALLMENT_FAILED",
		"ACCOUNT_OPENED", "ACCOUNT_STATUS_CHANGED", "ACCOUNT_NAME_UPDATED",
		"ACCOUNT_LIMITS_UPDATED", "MAINTENANCE_FEE_CHARGED",
		"CARD_CREATED", "CARD_STATUS_CHANGED", "CARD_TEMPORARY_BLOCKED",
		"VIRTUAL_CARD_CREATED", "CARD_REQUEST_CREATED", "CARD_REQUEST_APPROVED", "CARD_REQUEST_REJECTED",
		"RECURRING_ORDER_EXECUTED", "RECURRING_ORDER_SKIPPED",
		"FUND_RECURRING_EXECUTED", "FUND_RECURRING_SKIPPED",
		"PRICE_ALERT_TRIGGERED",
		"WATCHLIST_PRICE_MOVE",
	}
	for _, typ := range want {
		if _, ok := Get(typ, "push"); !ok {
			t.Errorf("registry missing push type %q", typ)
		}
	}
	if got := len(All("push")); got != len(want) {
		t.Errorf("All(push) returned %d, want %d", got, len(want))
	}
}

func TestRegistry_SelfConsistent(t *testing.T) {
	for _, d := range All("") {
		if d.Type == "" || d.Channel == "" || d.DefaultSubject == "" || d.DefaultBody == "" {
			t.Errorf("%s/%s: empty required field", d.Type, d.Channel)
		}
		if len(d.Variables) == 0 {
			t.Errorf("%s/%s: no variables declared", d.Type, d.Channel)
		}
		known, _ := KnownVars(d.Type, d.Channel)
		// Every {{placeholder}} in the default subject+body must be a declared variable.
		for _, m := range placeholderRE.FindAllStringSubmatch(d.DefaultSubject+d.DefaultBody, -1) {
			if !known[m[1]] {
				t.Errorf("%s/%s: default text uses undeclared variable %q", d.Type, d.Channel, m[1])
			}
		}
	}
}
