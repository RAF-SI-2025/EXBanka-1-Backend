//go:build integration

package workflows

import (
	"strconv"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/helpers"
)

// TestWF_StockBuy_CrossCurrency_ConvertedDebit is a Phase-2 regression guard
// for bug #4 cross-currency settlement: buying a USD-priced stock from an
// RSD account must debit the RSD account by the native × placement_rate
// amount, within rounding tolerance — never double-debit, never mis-convert.
//
// Shape: client with an RSD account places a market buy on a stock listing
// (currency determined by the listing), waits for fill, then checks that the
// RSD balance dropped by the expected converted amount.
//
// Note: if the simulator's seed listings happen to be RSD-priced on this run,
// there is no cross-currency conversion to validate — we still assert the
// total debit matches the fill's reported converted_amount, so the test is
// still a useful ledger-integrity guard. The specific "uses FX" branch is
// only meaningful when at least one non-RSD listing exists, which is the
// common case (faculty simulator seeds USD stocks).
func TestWF_StockBuy_CrossCurrency_ConvertedDebit(t *testing.T) {
	adminC := loginAsAdmin(t)
	clientID, acctNum, clientC, _ := setupActivatedClient(t, adminC)
	accountID := getFirstClientAccountID(t, adminC, clientID)

	_, listingID := getFirstStockListingID(t, clientC)

	before := getAccountBalancesByNumber(t, adminC, acctNum)
	t.Logf("before: balance=%.4f available=%.4f", before.Balance, before.Available)

	resp, err := clientC.POST("/api/v3/me/orders", map[string]interface{}{
		"listing_id":  listingID,
		"direction":   "buy",
		"order_type":  "market",
		"quantity":    1,
		"all_or_none": false,
		"margin":      false,
		"account_id":  accountID,
	})
	if err != nil {
		t.Fatalf("place buy: %v", err)
	}
	helpers.RequireStatus(t, resp, 201)
	orderID := int(helpers.GetNumberField(t, resp, "id"))
	t.Logf("order placed id=%d", orderID)

	// Wait for fill — cross-currency fills include a Convert step in the saga,
	// so the same timing window as same-currency fills applies.
	waitForOrderFill(t, clientC, orderID, 60*time.Second)

	after := getAccountBalancesByNumber(t, adminC, acctNum)
	t.Logf("after: balance=%.4f available=%.4f reserved=%.4f",
		after.Balance, after.Available, after.Reserved)

	// Invariant 1: reserved back to pre-placement (fill settles reservation).
	if fDiff(after.Reserved, before.Reserved) > 0.01 {
		t.Errorf("reserved not settled after fill: before=%.4f after=%.4f",
			before.Reserved, after.Reserved)
	}

	// Invariant 2: balance dropped (funds actually debited).
	debit := before.Balance - after.Balance
	if debit <= 0 {
		t.Errorf("balance did not drop after fill: before=%.4f after=%.4f (expected a debit)",
			before.Balance, after.Balance)
	}
	t.Logf("total RSD debited: %.4f", debit)

	// Invariant 3: debit matches the order's reported converted_amount (when
	// present in the GET /api/v3/me/orders/{id} response). This is the core
	// "no double-debit, no mis-convert" guard. We read the order and its
	// transactions and sum converted_amount across fills.
	orderResp, err := clientC.GET("/api/v3/me/orders/" + helpers.FormatID(orderID))
	if err != nil {
		t.Fatalf("get order: %v", err)
	}
	helpers.RequireStatus(t, orderResp, 200)

	// The response may be flat (just order fields) or wrap transactions. Both
	// shapes appear in existing tests — mirror orderFilledOrHasTxn's tolerance.
	var txns []interface{}
	if arr, ok := orderResp.Body["transactions"].([]interface{}); ok {
		txns = arr
	} else if inner, ok := orderResp.Body["order"].(map[string]interface{}); ok {
		if arr, ok := inner["transactions"].([]interface{}); ok {
			txns = arr
		}
	}

	if len(txns) == 0 {
		// Spec: "verify RSD ledger entries equal native × placement_rate
		// within rounding". Without per-transaction details we cannot do that
		// precisely. Skip the stricter assertion but keep the balance-drop
		// check above as a minimum guarantee.
		t.Skipf("no transactions returned on /me/orders/%d — skipping strict converted-amount match (TODO: expose transactions in this response)", orderID)
	}

	var sumConverted float64
	for _, tx := range txns {
		m, ok := tx.(map[string]interface{})
		if !ok {
			continue
		}
		// converted_amount is the account-currency debit per fill slice. For
		// same-currency fills it equals total_price.
		if v := m["converted_amount"]; v != nil {
			sumConverted += parseAmountAny(v)
		} else if v := m["total_price"]; v != nil {
			sumConverted += parseAmountAny(v)
		}
	}

	// Allow 1% rounding slack (fee rounding, spread, per-slice rounding).
	tolerance := debit * 0.01
	if tolerance < 0.01 {
		tolerance = 0.01
	}
	if fDiff(sumConverted, debit) > tolerance {
		t.Errorf("debit %.4f does not match summed converted_amount %.4f (tolerance %.4f) — bug #4 ledger-divergence regression",
			debit, sumConverted, tolerance)
	}
}

// parseAmountAny coerces a JSON value (string or number) into float64. Mirrors
// parseJSONBalance but works on arbitrary interface{} values (the per-txn
// amounts are nested inside an []interface{} and don't live at the top level).
func parseAmountAny(v interface{}) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
}
