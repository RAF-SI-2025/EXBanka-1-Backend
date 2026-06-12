package sitx

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	accountpb "github.com/exbanka/contract/accountpb"
	exchangepb "github.com/exbanka/contract/exchangepb"
	contractsitx "github.com/exbanka/contract/sitx"
	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AccountClient is the subset of accountpb.AccountServiceClient that
// posting_executor depends on, plus UpdateBalance for sender-side debits
// in InitiateOutboundTx (Phase 3 Task 6/9). Decoupled for testability —
// the real accountpb.AccountServiceClient satisfies this interface, and
// test stubs can implement it directly without grpc.ClientConn.
type AccountClient interface {
	GetAccountByNumber(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error)
	ListAccountsByClient(ctx context.Context, in *accountpb.ListAccountsByClientRequest, opts ...grpc.CallOption) (*accountpb.ListAccountsResponse, error)
	ReserveIncoming(ctx context.Context, in *accountpb.ReserveIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReserveIncomingResponse, error)
	CommitIncoming(ctx context.Context, in *accountpb.CommitIncomingRequest, opts ...grpc.CallOption) (*accountpb.CommitIncomingResponse, error)
	ReleaseIncoming(ctx context.Context, in *accountpb.ReleaseIncomingRequest, opts ...grpc.CallOption) (*accountpb.ReleaseIncomingResponse, error)
	ReserveOutgoing(ctx context.Context, in *accountpb.ReserveOutgoingRequest, opts ...grpc.CallOption) (*accountpb.ReserveOutgoingResponse, error)
	SettleOutgoing(ctx context.Context, in *accountpb.SettleOutgoingRequest, opts ...grpc.CallOption) (*accountpb.SettleOutgoingResponse, error)
	ReleaseOutgoing(ctx context.Context, in *accountpb.ReleaseOutgoingRequest, opts ...grpc.CallOption) (*accountpb.ReleaseOutgoingResponse, error)
	UpdateBalance(ctx context.Context, in *accountpb.UpdateBalanceRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error)
}

// Converter is the subset of exchangepb.ExchangeServiceClient the executor needs
// for seller-side FX on cross-currency OTC credits. Decoupled for testability; the
// real exchangepb.ExchangeServiceClient satisfies it. nil disables FX (the executor
// then preserves the pre-FX NO_SUCH_ACCOUNT / NO_SUCH_ASSET behaviour).
type Converter interface {
	Convert(ctx context.Context, in *exchangepb.ConvertRequest, opts ...grpc.CallOption) (*exchangepb.ConvertResponse, error)
}

// DebitedItem records one immediate-debit performed on a NEW_TX DEBIT
// posting. The receiver persists the list as JSON in
// peer_idempotence_records.debits_json so a subsequent ROLLBACK_TX can
// credit each entry back. (Spec receivers must hold enough state at
// vote-YES time to undo on rollback; reservations cover CREDIT postings,
// this list covers DEBIT postings.)
type DebitedItem struct {
	AccountNumber  string `json:"accountNumber"`
	Amount         string `json:"amount"`
	IdempotencyTag string `json:"idempotencyTag"` // unique per (peer,idem,posting); used to derive the creditback key
}

// OptionItem kinds discriminate which COMMIT settlement an item drives.
// The kind is set at Reserve time from the transaction SHAPE — never carried
// on the wire (the wire encodes accept-vs-exercise structurally: OPTION asset
// = accept; OPTION pseudo-account + STOCK = exercise). materialiseOptions
// (peer_tx_grpc_handler) branches on Kind to call RecordOptionContract with the
// right Intent/Direction. Empty == OptionKindAccept for backward compatibility
// with items persisted before this discriminator existed.
const (
	// OptionKindAccept is an OPTION-asset accept leg → RecordOptionContract
	// with Intent=accept (forms the contract; seller share hold for a DEBIT leg).
	OptionKindAccept = "accept"
	// OptionKindExerciseSeller is the seller bank's STOCK pseudo-account DEBIT leg
	// → RecordOptionContract Intent=exercise Direction=DEBIT (consume the reserved
	// shares + mark exercised). The paired pseudo MONAS CREDIT credits the seller's
	// money account via ReserveIncoming (tracked in ReservationKeys), not here.
	OptionKindExerciseSeller = "exercise_seller"
	// OptionKindExerciseBuyer is the buyer bank's PERSON STOCK CREDIT leg →
	// RecordOptionContract Intent=exercise Direction=CREDIT (credit the buyer's
	// holding + mark exercised). The buyer's strike MONAS DEBIT is the generic
	// ReserveOutgoing path.
	OptionKindExerciseBuyer = "exercise_buyer"
)

// OptionItem records one option posting on this bank's routing that drives a
// COMMIT-time settlement. At reserve time, the option contract has not yet been
// written (accept) or is being looked up (exercise); this item is persisted as
// JSON in peer_idempotence_records.options_json and materialised at COMMIT_TX.
//
// For ACCEPT items (Kind empty/"accept"): Buyer and Seller are extracted by
// pairing this option-asset posting with its counterpart in the same TX (the
// matched posting with opposite direction): a CREDIT posting identifies the
// buyer; the DEBIT posting (same OptionDescription) identifies the seller, and
// OptionDescriptionJSON is the on-wire option description.
//
// For EXERCISE items (Kind "exercise_seller"/"exercise_buyer"): the wire carries
// no OPTION asset, so OptionDescriptionJSON is RECONSTRUCTED to carry at least
// the negotiationId (the only field recordOptionExercise reads from it); the
// contract terms are read from the stored peer_option_contract at settlement.
// Buyer/Seller are not populated for exercise items (RecordOptionContract reads
// the parties from the stored row), but the request requires non-nil ids, so the
// COMMIT step fills placeholders.
type OptionItem struct {
	PostingIndex          int                        `json:"postingIndex"`
	Direction             string                     `json:"direction"` // local-side direction: DEBIT or CREDIT
	OptionDescriptionJSON string                     `json:"optionDescriptionJson"`
	Buyer                 contractsitx.ForeignBankId `json:"buyer"`
	Seller                contractsitx.ForeignBankId `json:"seller"`
	// Kind discriminates the COMMIT settlement (accept vs exercise-seller vs
	// exercise-buyer). Empty == accept (back-compat). See the consts above.
	Kind string `json:"kind,omitempty"`
}

// ReserveResult is the outcome of executing the reserve phase of a NEW_TX.
type ReserveResult struct {
	Vote            Vote
	ReservationKeys []string      // populated on YES; one per credit posting on our routing
	DebitedItems    []DebitedItem // populated on YES; one per debit posting on our routing
	OptionItems     []OptionItem  // populated on YES; one per option-asset posting on our routing
}

// SellerHoldingChecker is the subset of stockpb.PeerOTCServiceClient the
// executor depends on for the NEW_TX-time seller-side share handling.
// Decoupled for testability — production wires the real gRPC client; tests can
// supply a stub.
//
// ReserveSellerSharesForNewTx places a real HOLD on the seller's shares at
// vote time (Celina-5 OTC SAGA step 2 "rezervacija hartija"), keyed on the
// SI-TX identity, so they can't be sold before COMMIT. CheckSellerCanDeliver
// is retained for callers that only need a read-only pre-check.
type SellerHoldingChecker interface {
	CheckSellerCanDeliver(ctx context.Context, in *stockpb.CheckSellerCanDeliverRequest, opts ...grpc.CallOption) (*stockpb.CheckSellerCanDeliverResponse, error)
	ReserveSellerSharesForNewTx(ctx context.Context, in *stockpb.ReserveSellerSharesRequest, opts ...grpc.CallOption) (*stockpb.ReserveSellerSharesResponse, error)
	ReleaseSellerSharesForNewTx(ctx context.Context, in *stockpb.ReleaseSellerSharesRequest, opts ...grpc.CallOption) (*stockpb.ReleaseSellerSharesResponse, error)
	// ValidatePeerOptionMoneyLeg checks an option leg's paired money against the
	// receiver's OWN stored terms (forged-strike defense — see the RPC doc). The
	// executor calls it for seller-side (DEBIT) option legs on this bank's routing.
	ValidatePeerOptionMoneyLeg(ctx context.Context, in *stockpb.ValidatePeerOptionMoneyLegRequest, opts ...grpc.CallOption) (*stockpb.ValidatePeerOptionMoneyLegResponse, error)
	// LookupPeerOptionContract answers "do I hold the SELLER side of this
	// negotiationId?" for the OPTION pseudo-account exercise legs. found=true
	// returns the stored terms (seller_id, strike, qty, currency, settlement_date,
	// status) the executor uses to gate + settle the leg; found=false means a
	// different bank owns it and the executor SKIPS the pseudo-account leg
	// (ownership-by-contract, not routing-prefix — design §3.3.1).
	LookupPeerOptionContract(ctx context.Context, in *stockpb.LookupPeerOptionContractRequest, opts ...grpc.CallOption) (*stockpb.LookupPeerOptionContractResponse, error)
}

// optionDescriptionForCheck mirrors the fields of contract.sitx.OptionDescription
// that the executor's reserve-phase needs. Local copy keeps the executor
// independent of the full wire type; only stock + amount are needed for the
// seller-share reservation at NEW_TX vote time.
type optionDescriptionForCheck struct {
	Stock  stockDescForCheck `json:"stock"`
	Amount int64             `json:"amount"`
}

// stockDescForCheck is the subset of StockDescription needed by optionDescriptionForCheck.
type stockDescForCheck struct {
	Ticker string `json:"ticker"`
}

// PostingExecutor walks an accepted NEW_TX's postings and applies the
// receiver-side reservations via account-service. ownRouting is this
// bank's routing number — postings with a different routing are not
// executed locally (they're the responsibility of the other bank).
type PostingExecutor struct {
	client         AccountClient
	holdingChecker SellerHoldingChecker // optional; nil disables seller-side option pre-check
	exchange       Converter            // optional; nil disables seller-side FX on credits
	ownRouting     int64
}

func NewPostingExecutor(client AccountClient, ownRouting int64) *PostingExecutor {
	return &PostingExecutor{client: client, ownRouting: ownRouting}
}

// SetConverter wires the exchange-service-backed FX client used to convert a
// cross-currency OTC credit so it lands in the recipient's own account currency
// instead of voting NO_SUCH_ACCOUNT. Optional — left nil, a credit whose currency
// the recipient holds no account for fails closed exactly as before.
func (e *PostingExecutor) SetConverter(c Converter) {
	e.exchange = c
}

// SetHoldingChecker wires the stock-service-backed seller pre-check.
// Optional — left nil, the executor still emits OptionItems for option-
// asset postings but does not validate seller holdings at NEW_TX time
// (sufficiency is enforced best-effort at COMMIT_TX time via the
// holding lock in stock-service.RecordOptionContract).
func (e *PostingExecutor) SetHoldingChecker(c SellerHoldingChecker) {
	e.holdingChecker = c
}

// isOptionLeg reports whether a posting carries an option ASSET (accept form).
func isOptionLeg(p contractsitx.InternalPosting) bool { return p.AssetType == "OPTION" }

// isOptionPseudoAccount reports whether a posting is on an OPTION pseudo-account
// (exercise form). Its AccountID is the negotiationId and RoutingNumber is the
// negotiation's routing.
func isOptionPseudoAccount(p contractsitx.InternalPosting) bool {
	return p.AccountType == contractsitx.AccountTypeOption
}

// isStockAsset reports whether a posting moves a STOCK asset (its AssetID is the
// ticker).
func isStockAsset(p contractsitx.InternalPosting) bool {
	return p.AssetType == contractsitx.AssetTypeStock
}

// findExerciseNegotiationID returns the negotiationId ({routing, id}) carried by
// the OPTION pseudo-account legs of an exercise TX. The buyer-side STOCK arrival
// (a PERSON leg) does not itself carry the negotiationId, so the buyer bank reads
// it from the pseudo-account legs present in the same posting set. Returns ok=false
// if the TX has no OPTION pseudo-account leg (not an exercise).
func findExerciseNegotiationID(postings []contractsitx.InternalPosting) (contractsitx.ForeignBankId, bool) {
	for i := range postings {
		p := postings[i]
		if isOptionPseudoAccount(p) {
			return contractsitx.ForeignBankId{RoutingNumber: p.RoutingNumber, ID: p.AccountID}, true
		}
	}
	return contractsitx.ForeignBankId{}, false
}

// reconstructExerciseOptionDesc builds the minimal OptionDescription JSON an
// exercise OptionItem carries so recordOptionExercise can extract the
// negotiationId. recordOptionExercise reads ONLY opt.NegotiationID from this JSON
// (all other terms come from the stored peer_option_contract row), so we populate
// just that.
func reconstructExerciseOptionDesc(negID contractsitx.ForeignBankId) string {
	od := contractsitx.OptionDescription{NegotiationID: negID}
	b, err := json.Marshal(od)
	if err != nil {
		return ""
	}
	return string(b)
}

// exercisePseudoResult is the outcome of reserveExercisePseudoLeg for one
// OPTION pseudo-account leg. Exactly one of the three states is set:
//   - skip: the leg is not ours to settle (no checker wired, or we hold the
//     buyer side as the sender) — the caller continues to the next posting.
//   - noVote: a closed-failure reason — the caller returns it immediately.
//   - reservationKey / optionItem: a successful reservation key to append (MONAS
//     strike credit) or an OptionItem to merge (STOCK consume-at-commit).
type exercisePseudoResult struct {
	skip           bool
	noVote         *ReserveResult
	reservationKey string
	optionItem     *OptionItem
}

// reserveExercisePseudoLeg handles an OPTION pseudo-account leg (exercise form,
// spec §2.7.2). These are processed by OWNERSHIP-BY-CONTRACT, not routing-prefix:
// the pseudo-account's id is the negotiationId, whose routing is the negotiation's
// bank — NOT necessarily the settling (seller) bank. So we ask stock-service "do I
// hold the SELLER side of this negotiationId?"; only the bank that does settles
// these legs. The buyer bank (holds the buyer side, lookup found=false) SKIPS them
// even when the routing matches its own. See the option wire-conformance design
// §3.3.1.
//
// Returns one of: skip (not ours), a NO vote, or a successful reservation key /
// option item to merge. Behaviour is identical to the prior inline block: same
// gate order (not-found→OPTION_NEGOTIATION_NOT_FOUND, status/expired→
// OPTION_USED_OR_EXPIRED, amount→OPTION_AMOUNT_INCORRECT), same skip-vs-NO
// discriminator (peerBankCode == strconv(ownRouting)), same key derivation.
func (e *PostingExecutor) reserveExercisePseudoLeg(ctx context.Context, p contractsitx.InternalPosting, i int, peerBankCode, locallyGeneratedKey string) exercisePseudoResult {
	negID := contractsitx.ForeignBankId{RoutingNumber: p.RoutingNumber, ID: p.AccountID}
	if e.holdingChecker == nil {
		// No checker wired → cannot prove ownership; skip (don't vote NO so a
		// misconfigured/test executor doesn't reject the whole TX).
		return exercisePseudoResult{skip: true}
	}
	look, lerr := e.holdingChecker.LookupPeerOptionContract(ctx, &stockpb.LookupPeerOptionContractRequest{
		NegotiationRoutingNumber: negID.RoutingNumber,
		NegotiationId:            negID.ID,
	})
	if lerr != nil {
		// Transient lookup failure on a leg we might own → fail closed so money
		// never moves on an unverified exercise.
		nv := noVote(contractsitx.NoVoteReasonOptionNegotiationNotFound, i)
		return exercisePseudoResult{noVote: &nv}
	}
	if look == nil || !look.GetFound() {
		// We do NOT hold the seller side. Two cases:
		//  - We are the SENDER (buyer's bank, local reserve): peerBankCode is
		//    our own routing. We hold the BUYER side, so skipping the seller-
		//    side pseudo legs is correct (they settle at the seller's bank).
		//  - We are the RECEIVER (seller's bank, inbound NEW_TX): peerBankCode
		//    is the counterparty's. The pseudo legs MUST settle here, so a
		//    missing seller contract is a closed-failure → vote NO.
		// Distinguish by whether peerBankCode == our own routing (sender path).
		if peerBankCode == strconv.FormatInt(e.ownRouting, 10) {
			return exercisePseudoResult{skip: true}
		}
		nv := noVote(contractsitx.NoVoteReasonOptionNegotiationNotFound, i)
		return exercisePseudoResult{noVote: &nv}
	}
	// We hold the seller side. Gate: used/expired/amount BEFORE any reserve.
	if look.GetStatus() != "active" && look.GetStatus() != "exercising" {
		nv := noVote(contractsitx.NoVoteReasonOptionUsedOrExpired, i)
		return exercisePseudoResult{noVote: &nv}
	}
	if optionExpired(look.GetSettlementDate()) {
		nv := noVote(contractsitx.NoVoteReasonOptionUsedOrExpired, i)
		return exercisePseudoResult{noVote: &nv}
	}
	switch p.AssetType {
	case contractsitx.AssetTypeMonas:
		// Strike arrives at the pseudo-account → credit the SELLER's money
		// account. Validate amount == StrikePrice*Quantity first.
		strike, serr := decimal.NewFromString(look.GetStrikePrice())
		if serr != nil {
			nv := noVote(contractsitx.NoVoteReasonOptionAmountIncorrect, i)
			return exercisePseudoResult{noVote: &nv}
		}
		expected := strike.Mul(decimal.NewFromInt(look.GetQuantity()))
		legAmt, aerr := decimal.NewFromString(p.Amount)
		if aerr != nil || !legAmt.Equal(expected) {
			nv := noVote(contractsitx.NoVoteReasonOptionAmountIncorrect, i)
			return exercisePseudoResult{noVote: &nv}
		}
		// Resolve + credit the seller's money account, tracked like any MONAS
		// credit so COMMIT settles (CommitIncoming) and ROLLBACK releases.
		// Honour the seller's NOMINATED account (the account bound at accept,
		// stored on the contract) when present: credit that concrete 18-digit
		// number directly (resolveAccountForPosting passes account numbers through
		// unchanged), so the strike lands in the account the seller chose rather
		// than their first active <currency> account. Empty ⇒ no nomination stored
		// (older contract / unbound) → fall back to seller_id participant
		// resolution. (Sub-case 2 of the cross-bank OTC nominated-account fix.)
		currency := p.AssetID
		creditTarget := look.GetSellerId()
		if num := look.GetSellerAccountNumber(); num != "" {
			creditTarget = num
		}
		key, reason, ok := e.reserveIncomingCredit(ctx, creditTarget, currency, p.Amount, peerBankCode, locallyGeneratedKey)
		if !ok {
			nv := noVote(reason, i)
			return exercisePseudoResult{noVote: &nv}
		}
		return exercisePseudoResult{reservationKey: key}
	case contractsitx.AssetTypeStock:
		// Underlying leaves the pseudo-account → at COMMIT, consume the
		// seller's RESERVED shares (placed at ACCEPT) + mark exercised. We do
		// NOT place a new share reservation here.
		return exercisePseudoResult{optionItem: &OptionItem{
			PostingIndex:          i,
			Direction:             contractsitx.DirectionDebit,
			OptionDescriptionJSON: reconstructExerciseOptionDesc(negID),
			Kind:                  OptionKindExerciseSeller,
		}}
	}
	// Non-MONAS/non-STOCK asset on a pseudo-account leg: nothing to do here
	// (mirrors the prior inline switch falling through to the trailing continue).
	return exercisePseudoResult{skip: true}
}

// reserveIncomingCredit resolves the account for a credit posting, applies the
// standard credit gates, and places the incoming hold (ReserveIncoming). Returns
// the reservation key on success, or (ok=false) with the NoVote reason for the
// first failing gate. Used by BOTH the generic MONAS-CREDIT arm and the exercise
// pseudo-account MONAS-CREDIT path so the five gate-reason mappings can't diverge:
//
//	resolve fail   → NO_SUCH_ACCOUNT
//	lookup fail    → NO_SUCH_ACCOUNT
//	inactive acct  → UNACCEPTABLE_ASSET
//	currency wrong → NO_SUCH_ASSET
//	reserve fail   → UNACCEPTABLE_ASSET
//
// The reservation key is "<peer>:<idem>" and the idempotency key is
// "sitx-reserve-<key>" — identical to the prior inline derivation at both sites.
func (e *PostingExecutor) reserveIncomingCredit(ctx context.Context, accountID, currency, amount, peerBankCode, locallyGeneratedKey string) (key string, noVoteReason string, ok bool) {
	// Strict resolution first — byte-for-byte the pre-FX behaviour, so a deployment
	// without exchange-service wired is unchanged. The seller-side FX path only ever
	// adds NEW success outcomes; it never changes an existing NO-vote reason.
	accountNumber, resolveErr := e.resolveAccountForPosting(ctx, accountID, currency)
	if resolveErr != nil {
		// The recipient holds no account in the leg currency. Pre-FX: NO_SUCH_ACCOUNT.
		// With a converter wired, land the credit FX-converted in the recipient's
		// first active account ("hit the exchange first") instead of failing.
		if e.exchange == nil {
			return "", contractsitx.NoVoteReasonNoSuchAccount, false
		}
		target, terr := e.resolveCreditFallback(ctx, accountID)
		if terr != nil || target == nil {
			return "", contractsitx.NoVoteReasonNoSuchAccount, false
		}
		return e.fxReserveCredit(ctx, target, currency, amount, peerBankCode, locallyGeneratedKey)
	}
	acct, err := e.client.GetAccountByNumber(ctx, &accountpb.GetAccountByNumberRequest{AccountNumber: accountNumber})
	if err != nil || acct == nil {
		return "", contractsitx.NoVoteReasonNoSuchAccount, false
	}
	if acct.Status != "active" {
		return "", contractsitx.NoVoteReasonUnacceptableAsset, false
	}
	if acct.CurrencyCode != currency {
		// A concrete (nominated) account in a different currency. Pre-FX:
		// NO_SUCH_ASSET. With a converter, FX the credit into the nominated account.
		if e.exchange == nil {
			return "", contractsitx.NoVoteReasonNoSuchAsset, false
		}
		return e.fxReserveCredit(ctx, acct, currency, amount, peerBankCode, locallyGeneratedKey)
	}
	key = peerBankCode + ":" + locallyGeneratedKey
	if _, err := e.client.ReserveIncoming(ctx, &accountpb.ReserveIncomingRequest{
		AccountNumber:  accountNumber,
		Amount:         amount,
		Currency:       currency,
		ReservationKey: key,
		IdempotencyKey: "sitx-reserve-" + key,
	}); err != nil {
		return "", contractsitx.NoVoteReasonUnacceptableAsset, false
	}
	return key, "", true
}

// isPositiveAmount reports whether s parses to a strictly-positive decimal. Used
// to reject a missing / zero / negative / malformed FX conversion result before
// it reserves a garbage amount and silently under-/over-settles a leg.
func isPositiveAmount(s string) bool {
	if s == "" {
		return false
	}
	d, err := decimal.NewFromString(s)
	return err == nil && d.IsPositive()
}

// fxReserveCredit reserves `amount legCurrency` into target, converting via
// exchange-service when target's currency differs (so a cross-currency premium
// /strike lands in the recipient's own account). The reservation key carries the
// settled amount, so COMMIT/ROLLBACK settle/release it unchanged. Callers reach here
// only with a converter wired.
func (e *PostingExecutor) fxReserveCredit(ctx context.Context, target *accountpb.AccountResponse, legCurrency, amount, peerBankCode, locallyGeneratedKey string) (key string, noVoteReason string, ok bool) {
	if target.GetStatus() != "active" {
		return "", contractsitx.NoVoteReasonUnacceptableAsset, false
	}
	reserveAmount := amount
	reserveCurrency := target.GetCurrencyCode()
	if reserveCurrency != legCurrency {
		conv, cerr := e.exchange.Convert(ctx, &exchangepb.ConvertRequest{
			FromCurrency: legCurrency, ToCurrency: reserveCurrency, Amount: amount,
		})
		if cerr != nil || conv == nil || !isPositiveAmount(conv.GetConvertedAmount()) {
			// Reject a missing / non-positive / malformed conversion: a buggy or
			// hostile exchange returning "", "0", or a negative would otherwise
			// reserve a garbage amount and silently under-credit the recipient.
			return "", contractsitx.NoVoteReasonNoSuchAsset, false
		}
		reserveAmount = conv.GetConvertedAmount()
	}
	key = peerBankCode + ":" + locallyGeneratedKey
	if _, err := e.client.ReserveIncoming(ctx, &accountpb.ReserveIncomingRequest{
		AccountNumber:  target.GetAccountNumber(),
		Amount:         reserveAmount,
		Currency:       reserveCurrency,
		ReservationKey: key,
		IdempotencyKey: "sitx-reserve-" + key,
	}); err != nil {
		return "", contractsitx.NoVoteReasonUnacceptableAsset, false
	}
	return key, "", true
}

// reserveOutgoingDebit resolves the account for a DEBIT (outgoing money) posting,
// applies buyer-side FX when the payer holds no account in the leg currency (or a
// nominated account in a different currency), and places a per-posting hold.
// Byte-for-byte the pre-FX behaviour when no converter is wired (the only new
// outcomes are FX successes; existing NO-vote reasons are unchanged). Mirrors
// reserveIncomingCredit. The hold is keyed by the per-posting tag so each DEBIT
// leg settles/releases independently at COMMIT/ROLLBACK.
func (e *PostingExecutor) reserveOutgoingDebit(ctx context.Context, accountID, currency, amount, peerBankCode, locallyGeneratedKey string, i int) (item DebitedItem, noVoteReason string, ok bool) {
	tag := fmt.Sprintf("%s:%s:%d", peerBankCode, locallyGeneratedKey, i)
	accountNumber, resolveErr := e.resolveAccountForPosting(ctx, accountID, currency)
	if resolveErr != nil {
		// The payer holds no account in the leg currency. Pre-FX: NO_SUCH_ACCOUNT.
		// With a converter wired, debit the FX-equivalent from the payer's first
		// active account instead of failing.
		if e.exchange == nil {
			return DebitedItem{}, contractsitx.NoVoteReasonNoSuchAccount, false
		}
		target, terr := e.resolveCreditFallback(ctx, accountID)
		if terr != nil || target == nil {
			return DebitedItem{}, contractsitx.NoVoteReasonNoSuchAccount, false
		}
		return e.fxReserveDebit(ctx, target, currency, amount, tag)
	}
	acct, err := e.client.GetAccountByNumber(ctx, &accountpb.GetAccountByNumberRequest{AccountNumber: accountNumber})
	if err != nil || acct == nil {
		return DebitedItem{}, contractsitx.NoVoteReasonNoSuchAccount, false
	}
	if acct.Status != "active" {
		return DebitedItem{}, contractsitx.NoVoteReasonUnacceptableAsset, false
	}
	if acct.CurrencyCode != currency {
		// A concrete (nominated) account in a different currency. Pre-FX:
		// NO_SUCH_ASSET. With a converter, FX the debit into the nominated account.
		if e.exchange == nil {
			return DebitedItem{}, contractsitx.NoVoteReasonNoSuchAsset, false
		}
		return e.fxReserveDebit(ctx, acct, currency, amount, tag)
	}
	if _, err := e.client.ReserveOutgoing(ctx, &accountpb.ReserveOutgoingRequest{
		AccountNumber:  accountNumber,
		Amount:         amount,
		Currency:       currency,
		ReservationKey: tag,
		IdempotencyKey: "sitx-reserve-out-" + tag,
	}); err != nil {
		// account-service rejects holds above available balance; surface that as
		// INSUFFICIENT_ASSET per SI-TX semantics.
		return DebitedItem{}, contractsitx.NoVoteReasonInsufficientAsset, false
	}
	return DebitedItem{AccountNumber: accountNumber, Amount: amount, IdempotencyTag: tag}, "", true
}

// fxReserveDebit places an outgoing hold of `amount legCurrency` on target,
// converting via exchange-service when target's currency differs (so a
// cross-currency premium/strike is paid out of the payer's own account). The
// hold uses the supplied per-posting tag, so COMMIT/ROLLBACK settle/release it
// unchanged. Callers reach here only with a converter wired.
func (e *PostingExecutor) fxReserveDebit(ctx context.Context, target *accountpb.AccountResponse, legCurrency, amount, tag string) (DebitedItem, string, bool) {
	if target.GetStatus() != "active" {
		return DebitedItem{}, contractsitx.NoVoteReasonUnacceptableAsset, false
	}
	debitAmount := amount
	debitCurrency := target.GetCurrencyCode()
	if debitCurrency != legCurrency {
		conv, cerr := e.exchange.Convert(ctx, &exchangepb.ConvertRequest{
			FromCurrency: legCurrency, ToCurrency: debitCurrency, Amount: amount,
		})
		if cerr != nil || conv == nil || !isPositiveAmount(conv.GetConvertedAmount()) {
			// Reject a missing / non-positive / malformed conversion (symmetric with
			// the credit side): otherwise a "0"/garbage rate would under-charge the
			// payer for the premium/strike.
			return DebitedItem{}, contractsitx.NoVoteReasonNoSuchAsset, false
		}
		debitAmount = conv.GetConvertedAmount()
	}
	if _, err := e.client.ReserveOutgoing(ctx, &accountpb.ReserveOutgoingRequest{
		AccountNumber:  target.GetAccountNumber(),
		Amount:         debitAmount,
		Currency:       debitCurrency,
		ReservationKey: tag,
		IdempotencyKey: "sitx-reserve-out-" + tag,
	}); err != nil {
		return DebitedItem{}, contractsitx.NoVoteReasonInsufficientAsset, false
	}
	return DebitedItem{AccountNumber: target.GetAccountNumber(), Amount: debitAmount, IdempotencyTag: tag}, "", true
}

// resolveCreditFallback returns the recipient's first active account in ANY currency,
// the FX target for a credit whose leg currency the recipient holds no account for.
// Used only on the FX path (a converter is wired). accountID is a participant id here
// (the strict resolver passes concrete account numbers through, so resolveErr — the
// only caller — implies a participant). Returns an error when the recipient has no
// active account at all (genuine NO_SUCH_ACCOUNT).
func (e *PostingExecutor) resolveCreditFallback(ctx context.Context, accountID string) (*accountpb.AccountResponse, error) {
	ownerID, isParticipant := participantOwnerID(accountID)
	if !isParticipant {
		acct, err := e.client.GetAccountByNumber(ctx, &accountpb.GetAccountByNumberRequest{AccountNumber: accountID})
		if err != nil || acct == nil {
			return nil, fmt.Errorf("get account %s: %w", accountID, err)
		}
		return acct, nil
	}
	resp, listErr := e.client.ListAccountsByClient(ctx, &accountpb.ListAccountsByClientRequest{ClientId: ownerID, Page: 1, PageSize: 100})
	if listErr != nil || resp == nil {
		return nil, fmt.Errorf("list accounts for %s: %w", accountID, listErr)
	}
	for _, a := range resp.GetAccounts() {
		if a.GetStatus() == "active" {
			return a, nil // first active account → FX target
		}
	}
	return nil, fmt.Errorf("owner %d (%s) has no active account", ownerID, accountID)
}

// participantOwnerID maps a SI-TX participant id to its account-service owner id:
// "bank"/"employee-<n>" → the bank sentinel owner; "client-<n>" → n. ok=false for a
// raw account number or any other form (the caller then treats it as an account
// number to look up directly).
func participantOwnerID(accountID string) (ownerID uint64, ok bool) {
	if accountID == "bank" || strings.HasPrefix(accountID, "employee-") {
		return bankOwnerSentinelID, true
	}
	if rest, found := strings.CutPrefix(accountID, "client-"); found && rest != "" {
		if id, err := strconv.ParseUint(rest, 10, 64); err == nil {
			return id, true
		}
	}
	return 0, false
}

// Reserve runs the receive-side reserve phase of a NEW_TX. It walks each
// posting whose routingNumber matches ours and applies it to a local
// account-service operation:
//
//   - CREDIT posting (asset is being added to our account) → ReserveIncoming.
//     The reservation is committed by HandleCommitTx or released by
//     HandleRollbackTx, so the receiving account's balance is unaffected
//     until the IB confirms. Reservation key is "<peer>:<idem>".
//
//   - DEBIT posting (asset is leaving our account) → ReserveOutgoing, the
//     debit-side mirror of ReserveIncoming. This places a HOLD (reduces
//     AvailableBalance only); the money doesn't actually leave until
//     HandleCommitTx settles it, and the hold is released by HandleRollbackTx
//     (or the account-service timeout cron if the peer never responds). We
//     track each reserved debit in the returned DebitedItems — keyed by the
//     per-posting idempotency tag "<peer>:<idem>:<i>", which doubles as the
//     reservation key — so the commit/rollback steps can find them. Idempotent
//     on the key, so the reserve is safe to replay.
//
// Option-asset postings (AssetType == "OPTION") are dispatched to the seller-
// share hold path and surfaced as OptionItems; the option contract itself is
// materialised at COMMIT_TX in stock-service, not here.
//
// On any per-posting failure, returns a NO vote with the matching SI-TX
// reason and the failing posting index.
func (e *PostingExecutor) Reserve(ctx context.Context, postings []contractsitx.InternalPosting, peerBankCode, locallyGeneratedKey string) ReserveResult {
	keys := []string{}
	debits := []DebitedItem{}
	options := []OptionItem{}
	// First pass: identify the buyer/seller across ALL option postings
	// in this TX (regardless of routing). Option postings carry
	// participant ids in AccountID. CREDIT direction = buyer side
	// (gains the option); DEBIT direction = seller side (loses it).
	// Matching is by OptionDescription JSON (same string).
	var buyerByDesc = map[string]contractsitx.ForeignBankId{}
	var sellerByDesc = map[string]contractsitx.ForeignBankId{}
	for i := range postings {
		p := postings[i]
		if !isOptionLeg(p) {
			continue
		}
		party := contractsitx.ForeignBankId{RoutingNumber: p.RoutingNumber, ID: p.AccountID}
		switch p.Direction {
		case contractsitx.DirectionCredit:
			buyerByDesc[p.AssetID] = party
		case contractsitx.DirectionDebit:
			sellerByDesc[p.AssetID] = party
		}
	}

	// Forged-money validation pre-pass: validate every option leg on our routing
	// against THIS bank's own stored terms BEFORE placing any reservation, so a
	// forged-money envelope is rejected up-front and never leaves partial holds
	// (an attacker could otherwise spam money-leg-then-NO envelopes to lock a
	// victim's available balance until the timeout cron releases the holds). A
	// DEBIT option leg = we hold the seller (RECEIVES the strike → money CREDIT);
	// a CREDIT option leg = we hold the buyer (PAYS the strike → money DEBIT).
	// On exercise the validator requires the paired money == StrikePrice*Quantity;
	// mismatch / transient validator error → UNACCEPTABLE_ASSET NO vote. Skipped
	// only when the checker isn't wired (tests / misconfig); production wires it.
	if e.holdingChecker != nil {
		for i := range postings {
			p := postings[i]
			if p.RoutingNumber != e.ownRouting || !isOptionLeg(p) {
				continue
			}
			var full contractsitx.OptionDescription
			_ = json.Unmarshal([]byte(p.AssetID), &full)
			money, moneyCcy := pairedMoney(postings, e.ownRouting, p.Direction)
			vresp, verr := e.holdingChecker.ValidatePeerOptionMoneyLeg(ctx, &stockpb.ValidatePeerOptionMoneyLegRequest{
				NegotiationRouting: full.NegotiationID.RoutingNumber,
				NegotiationId:      full.NegotiationID.ID,
				Direction:          p.Direction,
				Intent:             contractsitx.OptionIntentAccept,
				Ticker:             full.Stock.Ticker,
				Quantity:           full.Amount,
				StrikePrice:        full.PricePerUnit.Amount.Decimal.String(),
				MoneyAmount:        money.String(),
				Currency:           moneyCcy,
				PeerBankCode:       peerBankCode,
			})
			if verr != nil || vresp == nil || !vresp.GetOk() {
				return noVote(contractsitx.NoVoteReasonUnacceptableAsset, i)
			}
		}
	}

	// Multi-credit collision guard (audit #4): every MONAS money-CREDIT leg on our
	// routing reserves under the SHARED key "<peer>:<idem>" (reserveIncomingCredit)
	// and is committed/released by that one key. Two money credits to THIS bank in
	// a single TX would therefore collide — account-service treats the 2nd reserve
	// as an idempotent duplicate of the 1st and silently skips it, so that recipient
	// is never credited (money lost at COMMIT). No current flow has >1 money credit
	// per bank, so reject the shape up front rather than silently dropping a credit.
	// (Full per-posting-keyed multi-credit support is a documented follow-up — see
	// docs/superpowers/plans/2026-06-12-saga-weak-spots-fixes.md #4.)
	monasCredits, firstCreditIdx := 0, -1
	for i := range postings {
		p := postings[i]
		if p.RoutingNumber == e.ownRouting && p.Direction == contractsitx.DirectionCredit && p.AssetType == contractsitx.AssetTypeMonas {
			monasCredits++
			if firstCreditIdx < 0 {
				firstCreditIdx = i
			}
		}
	}
	if monasCredits > 1 {
		return noVote(contractsitx.NoVoteReasonUnacceptableAsset, firstCreditIdx)
	}

	for i := range postings {
		p := postings[i]

		// --- Exercise pseudo-account legs (spec §2.7.2 OPTION pseudo-account form).
		// These are processed by OWNERSHIP-BY-CONTRACT, not routing-prefix: the
		// pseudo-account's id is the negotiationId, whose routing is the
		// negotiation's bank — NOT necessarily the settling (seller) bank. So we ask
		// stock-service "do I hold the SELLER side of this negotiationId?"; only the
		// bank that does settles these legs. The buyer bank (holds the buyer side,
		// lookup found=false) SKIPS them even when the routing matches its own. See
		// the option wire-conformance design §3.3.1.
		if isOptionPseudoAccount(p) {
			r := e.reserveExercisePseudoLeg(ctx, p, i, peerBankCode, locallyGeneratedKey)
			if r.noVote != nil {
				return *r.noVote
			}
			if r.reservationKey != "" {
				keys = append(keys, r.reservationKey)
			}
			if r.optionItem != nil {
				options = append(options, *r.optionItem)
			}
			continue
		}

		// --- Exercise STOCK arrival on a PERSON/ACCOUNT leg on our routing (the
		// buyer's underlying). The buyer bank emits an exercise_buyer item that
		// drives RecordOptionContract Intent=exercise Direction=CREDIT at COMMIT
		// (credit the buyer's holding + mark exercised). The negotiationId is read
		// from the OPTION pseudo-account legs in the same TX.
		if p.RoutingNumber == e.ownRouting && isStockAsset(p) && p.Direction == contractsitx.DirectionCredit {
			negID, ok := findExerciseNegotiationID(postings)
			if !ok {
				return noVote(contractsitx.NoVoteReasonUnacceptableAsset, i)
			}
			options = append(options, OptionItem{
				PostingIndex:          i,
				Direction:             contractsitx.DirectionCredit,
				OptionDescriptionJSON: reconstructExerciseOptionDesc(negID),
				Kind:                  OptionKindExerciseBuyer,
			})
			continue
		}

		if p.RoutingNumber != e.ownRouting {
			continue
		}
		// Option-asset postings: surface as an OptionItem so the
		// handler can call into stock-service.RecordOptionContract at
		// COMMIT_TX time. We don't write the option contract here —
		// SI-TX semantics keep all observable side effects bounded by
		// the reservation/commit pair, and contracts shouldn't appear
		// before COMMIT.
		//
		// For DEBIT option postings on our routing (this bank holds the
		// seller), RESERVE the seller's shares now — a real hold keyed on the
		// SI-TX identity (crossbank_tx_id = "<peerCode>:<idem>"). This is the
		// spec's Celina-5 OTC SAGA step 2 ("rezervacija hartija"): the shares
		// must be HELD when we vote YES so they can't be sold before COMMIT_TX,
		// not merely checked. COMMIT_TX then attaches this hold to the minted
		// contract (no re-check that could fail); ROLLBACK releases it. A
		// failed/insufficient reservation → INSUFFICIENT_ASSET NoVote so money
		// never moves on a contract the seller can't fulfil. If the reserver
		// isn't wired we vote NO rather than silently skip (keeps the YES vote
		// honest even when stock-service is briefly down). Replaces the prior
		// read-only CheckSellerCanDeliver pre-check (Fix #6) which left a
		// sell-between-vote-and-commit window.
		if isOptionLeg(p) {
			// Money-leg validation already ran in the pre-pass above. Here we only
			// place the seller-side vote-time share hold for accept-intent legs.
			if p.Direction == contractsitx.DirectionDebit {
				var od optionDescriptionForCheck
				_ = json.Unmarshal([]byte(p.AssetID), &od)
				if e.holdingChecker == nil {
					return noVote(contractsitx.NoVoteReasonInsufficientAsset, i)
				}
				if od.Stock.Ticker != "" && od.Amount > 0 {
					seller := sellerByDesc[p.AssetID]
					crossbankTxID := peerBankCode + ":" + locallyGeneratedKey
					resp, err := e.holdingChecker.ReserveSellerSharesForNewTx(ctx, &stockpb.ReserveSellerSharesRequest{
						SellerId: &stockpb.PeerForeignBankId{
							RoutingNumber: seller.RoutingNumber,
							Id:            seller.ID,
						},
						Ticker:        od.Stock.Ticker,
						Quantity:      od.Amount,
						CrossbankTxId: crossbankTxID,
					})
					if err != nil || resp == nil || !resp.GetOk() {
						return noVote(contractsitx.NoVoteReasonInsufficientAsset, i)
					}
				}
			}
			options = append(options, OptionItem{
				PostingIndex:          i,
				Direction:             p.Direction,
				OptionDescriptionJSON: p.AssetID,
				Buyer:                 buyerByDesc[p.AssetID],
				Seller:                sellerByDesc[p.AssetID],
				Kind:                  OptionKindAccept,
			})
			continue
		}
		// MONAS leg: the currency is the AssetID.
		currency := p.AssetID
		amountStr := p.Amount

		if p.Direction == contractsitx.DirectionCredit {
			// Shared with the exercise pseudo-account MONAS-credit path so the
			// resolve/lookup/active/currency/reserve gate-reason mappings can't
			// diverge between the two sites.
			key, reason, ok := e.reserveIncomingCredit(ctx, p.AccountID, currency, amountStr, peerBankCode, locallyGeneratedKey)
			if !ok {
				return noVote(reason, i)
			}
			keys = append(keys, key)
			continue
		}

		// Non-CREDIT MONAS legs: only DEBIT is valid. The buyer-side premium debit
		// is symmetric with the seller-side credit (reserveIncomingCredit): when the
		// payer holds no account in the leg currency (or a nominated account in a
		// different currency) and a converter is wired, the FX-equivalent is debited
		// from the payer's own account instead of voting NO_SUCH_ACCOUNT/NO_SUCH_ASSET.
		// The hold is keyed by the per-posting tag, so SettleLocal/ReverseLocal
		// finalise the FX'd account/amount transparently at COMMIT/ROLLBACK.
		if p.Direction != contractsitx.DirectionDebit {
			return noVote(contractsitx.NoVoteReasonUnacceptableAsset, i)
		}
		item, reason, ok := e.reserveOutgoingDebit(ctx, p.AccountID, currency, amountStr, peerBankCode, locallyGeneratedKey, i)
		if !ok {
			return noVote(reason, i)
		}
		debits = append(debits, item)
	}
	return ReserveResult{
		Vote:            Vote{Type: contractsitx.VoteYes},
		ReservationKeys: keys,
		DebitedItems:    debits,
		OptionItems:     options,
	}
}

// ReverseLocal undoes the local effects a prior Reserve applied for the same
// (postings, peerBankCode, locallyGeneratedKey): it releases the CREDIT-side
// reservation and releases each DEBIT-side outgoing hold on our routing,
// reusing the same reservation key and per-posting idempotency tags Reserve
// used. Because those keys match, ReverseLocal nets exactly the effects of
// Reserve and is safe to call repeatedly and to interleave with the inline
// rollback path. (DEBIT legs were held — not debited — so releasing the hold
// returns AvailableBalance with no Balance movement.)
//
// Used by OutboundReplayCron (via PeerTxGRPCHandler.ReverseOutboundLocal) to
// return money on a sender-side OTC TX that terminally fails after the local
// legs were already applied. Option-asset postings carry no money and are
// skipped; a NotFound on release is benign (no CREDIT legs landed locally).
func (e *PostingExecutor) ReverseLocal(ctx context.Context, postings []contractsitx.InternalPosting, peerBankCode, locallyGeneratedKey string) error {
	key := peerBankCode + ":" + locallyGeneratedKey
	if _, err := e.client.ReleaseIncoming(ctx, &accountpb.ReleaseIncomingRequest{
		ReservationKey: key,
		IdempotencyKey: "sitx-localrelease-" + key,
	}); err != nil && status.Code(err) != codes.NotFound {
		return err
	}
	// Release any vote-time seller-share hold (DEBIT option leg on our routing).
	// Keyed on the SI-TX identity, so a single release covers the TX regardless
	// of posting index. Idempotent + no-op when absent. Skipped if the reserver
	// isn't wired.
	if e.holdingChecker != nil && hasOwnDebitOptionLeg(postings, e.ownRouting) {
		if _, err := e.holdingChecker.ReleaseSellerSharesForNewTx(ctx, &stockpb.ReleaseSellerSharesRequest{CrossbankTxId: key}); err != nil {
			return err
		}
	}
	for i := range postings {
		p := postings[i]
		if p.RoutingNumber != e.ownRouting || p.Direction != contractsitx.DirectionDebit {
			continue
		}
		if isOptionLeg(p) {
			continue // option-asset leg — no money to return
		}
		tag := fmt.Sprintf("%s:%s:%d", peerBankCode, locallyGeneratedKey, i)
		// Release the outgoing HOLD (no Balance movement). NotFound is benign —
		// the hold may never have landed (e.g. NEW_TX voted NO before reserving
		// this leg) or was already released.
		if _, err := e.client.ReleaseOutgoing(ctx, &accountpb.ReleaseOutgoingRequest{
			ReservationKey: tag,
			IdempotencyKey: "sitx-localrelease-out-" + tag,
		}); err != nil && status.Code(err) != codes.NotFound {
			return err
		}
	}
	return nil
}

// SettleLocal finalises the DEBIT-side outgoing holds a prior Reserve placed
// for the same (postings, peerBankCode, locallyGeneratedKey): for each money
// DEBIT leg on our routing it calls SettleOutgoing, moving the held amount out
// of Balance (the money actually leaves). CREDIT-side reservations are settled
// separately via CommitIncoming, and option legs carry no money — both are
// skipped here. Keyed by the same per-posting tags Reserve used, so this is
// idempotent and safe to call from both the inline commit path and the replay
// cron. NotFound on a leg is benign (no hold landed for it).
func (e *PostingExecutor) SettleLocal(ctx context.Context, postings []contractsitx.InternalPosting, peerBankCode, locallyGeneratedKey string) error {
	for i := range postings {
		p := postings[i]
		if p.RoutingNumber != e.ownRouting || p.Direction != contractsitx.DirectionDebit {
			continue
		}
		if isOptionLeg(p) {
			continue // option-asset leg — no money to settle
		}
		tag := fmt.Sprintf("%s:%s:%d", peerBankCode, locallyGeneratedKey, i)
		if _, err := e.client.SettleOutgoing(ctx, &accountpb.SettleOutgoingRequest{
			ReservationKey: tag,
			IdempotencyKey: "sitx-localsettle-out-" + tag,
		}); err != nil && status.Code(err) != codes.NotFound {
			return err
		}
	}
	return nil
}

// ExtractOwnOptionItems deterministically derives the OptionItems for option
// legs on THIS bank's routing, mirroring the option-collection pass inside
// Reserve (no side effects, no gRPC). Used by the replay cron's
// CommitOutboundLocal so a sender-side option contract can still be materialised
// after a crash between the inline Reserve() and the inline materialise — the
// cron has only the persisted postings, not the in-memory ReserveResult.
//
// This is the SENDER-side mirror only (it owns its OWN routing's legs):
//   - ACCEPT: an OPTION-asset leg on our routing → accept item (buyer/seller maps
//     built from CREDIT/DEBIT pairing by OptionDescription JSON).
//   - EXERCISE: a PERSON/ACCOUNT STOCK CREDIT leg on our routing → exercise_buyer
//     item (the sender is always the buyer's bank; the negotiationId is read from
//     the OPTION pseudo-account legs in the same TX). The seller-side exercise
//     legs are never own-routing on the sender, so they are not emitted here.
func (e *PostingExecutor) ExtractOwnOptionItems(postings []contractsitx.InternalPosting) []OptionItem {
	buyerByDesc := map[string]contractsitx.ForeignBankId{}
	sellerByDesc := map[string]contractsitx.ForeignBankId{}
	for i := range postings {
		p := postings[i]
		if !isOptionLeg(p) {
			continue
		}
		party := contractsitx.ForeignBankId{RoutingNumber: p.RoutingNumber, ID: p.AccountID}
		switch p.Direction {
		case contractsitx.DirectionCredit:
			buyerByDesc[p.AssetID] = party
		case contractsitx.DirectionDebit:
			sellerByDesc[p.AssetID] = party
		}
	}
	exNegID, hasExercise := findExerciseNegotiationID(postings)
	var items []OptionItem
	for i := range postings {
		p := postings[i]
		if p.RoutingNumber != e.ownRouting {
			continue
		}
		switch {
		case isOptionLeg(p):
			items = append(items, OptionItem{
				PostingIndex:          i,
				Direction:             p.Direction,
				OptionDescriptionJSON: p.AssetID,
				Buyer:                 buyerByDesc[p.AssetID],
				Seller:                sellerByDesc[p.AssetID],
				Kind:                  OptionKindAccept,
			})
		case hasExercise && isStockAsset(p) && p.Direction == contractsitx.DirectionCredit:
			// Buyer's underlying arrival on the sender side → exercise_buyer.
			items = append(items, OptionItem{
				PostingIndex:          i,
				Direction:             contractsitx.DirectionCredit,
				OptionDescriptionJSON: reconstructExerciseOptionDesc(exNegID),
				Kind:                  OptionKindExerciseBuyer,
			})
		}
	}
	return items
}

// resolveAccountForPosting maps an accountId string to a concrete bank
// account number. Participant-ID strings ("client-<n>") are resolved
// via account-service to the participant's first active account in the
// requested currency. Anything else passes through unchanged — the
// downstream GetAccountByNumber call will surface NO_SUCH_ACCOUNT for
// genuinely unknown accountIds.
func (e *PostingExecutor) resolveAccountForPosting(ctx context.Context, accountID, currency string) (string, error) {
	// Bank participant ids ("bank" / "employee-<N>") resolve to the bank's own
	// active account in the requested currency. SP-3 lifted the bank party for
	// cross-bank OTC bidding, so a bank can now be the SELLER (its credit leg
	// carries the bank wire id) or the BUYER. The bank's accounts live under the
	// well-known sentinel owner id; the bank holds at most one active account per
	// currency, so the first active match is deterministic. Without this branch
	// the downstream GetAccountByNumber("employee-1") failed → NO_SUCH_ACCOUNT,
	// stranding bank↔bank accept SI-TXes in "committing".
	if accountID == "bank" || strings.HasPrefix(accountID, "employee-") {
		return e.resolveOwnerAccount(ctx, bankOwnerSentinelID, currency, accountID)
	}

	// Participant ID pattern: "client-<digits>".
	rest, ok := strings.CutPrefix(accountID, "client-")
	if !ok || rest == "" {
		return accountID, nil
	}
	clientID, parseErr := strconv.ParseUint(rest, 10, 64)
	if parseErr != nil {
		return accountID, nil
	}
	return e.resolveOwnerAccount(ctx, clientID, currency, accountID)
}

// bankOwnerSentinelID is the well-known owner id for bank-owned accounts
// (mirrors account-service's service.BankOwnerID). A bank participant id
// resolves to this owner's account in the requested currency.
const bankOwnerSentinelID uint64 = 1_000_000_000

// resolveOwnerAccount returns ownerID's first active account in currency, or a
// NO_SUCH_ACCOUNT-shaped error when none exists. accountID is the original
// participant id, used only for error messages.
func (e *PostingExecutor) resolveOwnerAccount(ctx context.Context, ownerID uint64, currency, accountID string) (string, error) {
	resp, listErr := e.client.ListAccountsByClient(ctx, &accountpb.ListAccountsByClientRequest{ClientId: ownerID, Page: 1, PageSize: 100})
	if listErr != nil || resp == nil {
		return "", fmt.Errorf("list accounts for %s: %w", accountID, listErr)
	}
	for _, a := range resp.GetAccounts() {
		if a.GetCurrencyCode() == currency && a.GetStatus() == "active" {
			return a.GetAccountNumber(), nil
		}
	}
	return "", fmt.Errorf("owner %d (%s) has no active %s account", ownerID, accountID, currency)
}

// pairedMoney totals the money this bank moves for an option leg of the given
// direction and reports its currency, so it can be checked against stored terms.
// A DEBIT option leg (we hold the seller, who RECEIVES the strike/premium) pairs
// with the money CREDIT on our routing; a CREDIT option leg (we hold the buyer,
// who PAYS it) pairs with the money DEBIT on our routing. In a cross-bank SI-TX
// each routing carries exactly one side's money leg, so summing by direction on
// our routing yields that participant's amount without needing to match the
// participant id (the seller's money leg uses a participant id while the buyer's
// uses a resolved account number, so id-matching is unreliable). The currency is
// taken from the money leg itself (the premium currency on accept may differ from
// the strike currency on a cross-currency trade, so it must NOT be assumed from
// the option description). Option-asset legs are skipped. Returns ("0", "") when
// no paired money leg is present (a forged/degenerate envelope), which fails
// downstream validation closed.
func pairedMoney(postings []contractsitx.InternalPosting, ownRouting int64, optionDirection string) (decimal.Decimal, string) {
	wantMoneyDir := contractsitx.DirectionCredit // seller (DEBIT option) receives money
	if optionDirection == contractsitx.DirectionCredit {
		wantMoneyDir = contractsitx.DirectionDebit // buyer (CREDIT option) pays money
	}
	total := decimal.Zero
	currency := ""
	for i := range postings {
		p := postings[i]
		if p.RoutingNumber != ownRouting || p.Direction != wantMoneyDir {
			continue
		}
		if isOptionLeg(p) { // option-asset leg, not money
			continue
		}
		if currency != "" && p.AssetID != currency {
			// Mixed currencies for one side is a forged/degenerate envelope —
			// surface an empty currency so validation fails closed.
			return total, ""
		}
		currency = p.AssetID
		amt, err := decimal.NewFromString(p.Amount)
		if err != nil {
			return total, ""
		}
		total = total.Add(amt)
	}
	return total, currency
}

// optionExpired reports whether an option's settlementDate has passed (now >
// settlementDate), per spec §2.7.2 ("should be unable to execute" past
// settlement). Parses the SI-TX ISO-8601-with-timezone string; an unparseable
// date is treated as NOT expired (fail open here — the amount/status gates and
// the downstream stored-terms settlement still guard the money path; rejecting a
// parse error as "expired" would wrongly block a legitimate exercise on a date
// format the peer encoded slightly differently).
func optionExpired(settlementDate string) bool {
	if settlementDate == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, settlementDate)
	if err != nil {
		// Try date-only fallback (some peers may send a bare date).
		t, err = time.Parse("2006-01-02", settlementDate)
		if err != nil {
			return false
		}
	}
	return time.Now().After(t)
}

// hasOwnDebitOptionLeg reports whether the postings contain a DEBIT
// option-asset leg on the given routing — i.e. this bank holds the seller and
// therefore placed a vote-time share hold that must be released on rollback.
func hasOwnDebitOptionLeg(postings []contractsitx.InternalPosting, ownRouting int64) bool {
	for i := range postings {
		p := postings[i]
		if p.RoutingNumber == ownRouting && p.Direction == contractsitx.DirectionDebit && isOptionLeg(p) {
			return true
		}
	}
	return false
}

func noVote(reason string, postingIdx int) ReserveResult {
	return ReserveResult{
		Vote: Vote{
			Type:    contractsitx.VoteNo,
			NoVotes: []NoVote{{Reason: reason, Posting: ptr(postingIdx)}},
		},
	}
}

func ptr(i int) *int { return &i }
