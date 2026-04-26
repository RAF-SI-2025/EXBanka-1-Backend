package service

import (
	"github.com/shopspring/decimal"
)

// FeeServiceInterBankAdapter wraps the existing FeeService so InterBankService
// can ask it for the inbound-transfer fee. We reuse the intra-bank
// transfer_fees rules; spec §3.4 states the receiving bank charges its own
// fees on inter-bank inbound — same table, same matching logic.
type FeeServiceInterBankAdapter struct {
	fees *FeeService
}

// NewFeeServiceInterBankAdapter constructs the adapter.
func NewFeeServiceInterBankAdapter(fees *FeeService) *FeeServiceInterBankAdapter {
	return &FeeServiceInterBankAdapter{fees: fees}
}

// ComputeIncomingFee returns the total fee for a transfer of `amount` in
// `currency`. Aggregates all matching rules per the existing transfer_fees
// table semantics. On any internal error, returns zero rather than
// blocking the inbound transfer — fee misconfiguration is an operations
// problem, not a reason to reject a Prepare.
func (a *FeeServiceInterBankAdapter) ComputeIncomingFee(amount decimal.Decimal, currency string) decimal.Decimal {
	if a.fees == nil {
		return decimal.Zero
	}
	total, err := a.fees.CalculateFee(amount, "transfer", currency)
	if err != nil {
		return decimal.Zero
	}
	return total
}
