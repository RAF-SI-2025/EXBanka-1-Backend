package sitx

import (
	"strings"

	"github.com/shopspring/decimal"
)

// DecimalNumber wraps decimal.Decimal so it (de)serializes as a JSON
// *number* token rather than a quoted string. SI-TX §2.5 / §2.8.1 require
// monetary amounts to be JSON numbers, while shopspring/decimal defaults to
// quoting. Used only by the wire DTOs; internal storage stays decimal-string.
type DecimalNumber struct {
	decimal.Decimal
}

// MarshalJSON emits the decimal as a bare numeric token (e.g. 260, 1.5).
func (d DecimalNumber) MarshalJSON() ([]byte, error) {
	return []byte(d.Decimal.String()), nil
}

// UnmarshalJSON accepts either a JSON number or a quoted string (tolerant of
// peers that still quote), parsing without float64 rounding. The JSON literal
// null is treated as a no-op (value left unchanged) per encoding/json contract.
func (d *DecimalNumber) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	s := strings.Trim(string(b), `"`)
	v, err := decimal.NewFromString(s)
	if err != nil {
		return err
	}
	d.Decimal = v
	return nil
}
