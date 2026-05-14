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
