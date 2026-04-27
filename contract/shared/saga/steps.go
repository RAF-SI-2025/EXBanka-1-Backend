package saga

import "fmt"

// StepKind is the typed name of a saga step. Switch statements that branch on
// StepKind in recovery code MUST have a default case that panics — Go's type
// system does not enforce switch exhaustiveness, so we use the panicking default
// as the safety net.
type StepKind string

const (
	// Crossbank accept (5 steps, derived from old phases).
	StepReserveBuyerFunds   StepKind = "reserve_buyer_funds"
	StepCreateContract      StepKind = "create_contract"
	StepReserveSellerShares StepKind = "reserve_seller_shares"
	StepDebitBuyer          StepKind = "debit_buyer"
	StepCreditSeller        StepKind = "credit_seller"
	StepTransferOwnership   StepKind = "transfer_ownership"
	StepFinalizeAccept      StepKind = "finalize_accept"

	// Crossbank exercise.
	StepDebitStrike   StepKind = "debit_strike"
	StepCreditStrike  StepKind = "credit_strike"
	StepDeliverShares StepKind = "deliver_shares"

	// Crossbank expire.
	StepRefundReservation StepKind = "refund_reservation"
	StepMarkExpired       StepKind = "mark_expired"

	// OTC + Fund (already on shared.Saga; listed for completeness).
	StepReserveAndContract   StepKind = "reserve_and_contract"
	StepReservePremium       StepKind = "reserve_premium"
	StepReserveStrike        StepKind = "reserve_strike"
	StepSettlePremiumBuyer   StepKind = "settle_premium_buyer"
	StepSettleStrikeBuyer    StepKind = "settle_strike_buyer"
	StepConsumeSellerHolding StepKind = "consume_seller_holding"
	StepDebitSource          StepKind = "debit_source"
	StepCreditTarget         StepKind = "credit_target"
	StepDebitFund            StepKind = "debit_fund"
	StepCreditFund           StepKind = "credit_fund"
	StepCreditPremiumSeller  StepKind = "credit_premium_seller"
	StepCreditStrikeSeller   StepKind = "credit_strike_seller"
	StepUpsertPosition       StepKind = "upsert_position"
)

var allSteps = map[StepKind]struct{}{
	StepReserveBuyerFunds:   {}, StepCreateContract: {}, StepReserveSellerShares: {},
	StepDebitBuyer:         {}, StepCreditSeller: {}, StepTransferOwnership: {},
	StepFinalizeAccept:     {}, StepDebitStrike: {}, StepCreditStrike: {},
	StepDeliverShares:      {}, StepRefundReservation: {}, StepMarkExpired: {},
	StepReserveAndContract: {}, StepReservePremium: {}, StepReserveStrike: {},
	StepSettlePremiumBuyer: {}, StepSettleStrikeBuyer: {}, StepConsumeSellerHolding: {},
	StepDebitSource:        {}, StepCreditTarget: {}, StepDebitFund: {},
	StepCreditFund:         {}, StepCreditPremiumSeller: {}, StepCreditStrikeSeller: {},
	StepUpsertPosition:     {},
}

// MustStep returns k if k is a registered StepKind. Otherwise it panics.
// Use at saga construction: saga.AddStep(saga.MustStep(saga.StepDebitBuyer), ...)
// to fail fast on typos.
func MustStep(k StepKind) StepKind {
	if _, ok := allSteps[k]; !ok {
		panic(fmt.Sprintf("unknown StepKind: %q", k))
	}
	return k
}

// IsRegistered returns true iff k is a known StepKind.
func IsRegistered(k StepKind) bool {
	_, ok := allSteps[k]
	return ok
}
