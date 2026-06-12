package handler

import (
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// parseSettlementDateArg must reject a settlement date before today (UTC) and
// accept today / future dates, in both RFC3339 and YYYY-MM-DD forms.
func TestParseSettlementDateArg_RejectsBeforeToday(t *testing.T) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	yesterday := today.Add(-24 * time.Hour)
	tomorrow := today.Add(24 * time.Hour)

	cases := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{"yesterday RFC3339", yesterday.Format(time.RFC3339), true},
		{"yesterday date-only", yesterday.Format("2006-01-02"), true},
		{"today RFC3339", today.Format(time.RFC3339), false},
		{"today date-only", today.Format("2006-01-02"), false},
		{"tomorrow date-only", tomorrow.Format("2006-01-02"), false},
		{"garbage", "not-a-date", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseSettlementDateArg("settlement_date", c.value)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", c.value)
				}
				if status.Code(err) != codes.InvalidArgument {
					t.Errorf("expected InvalidArgument, got %v", status.Code(err))
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", c.value, err)
			}
		})
	}
}
