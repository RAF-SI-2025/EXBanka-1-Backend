package sitx

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	accountpb "github.com/exbanka/contract/accountpb"
	contractsitx "github.com/exbanka/contract/sitx"
	stockpb "github.com/exbanka/contract/stockpb"
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
	UpdateBalance(ctx context.Context, in *accountpb.UpdateBalanceRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error)
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

// OptionItem records one option-asset posting on this bank's routing.
// At reserve time, the option contract has not yet been written; this
// item is persisted as JSON in peer_idempotence_records.options_json
// and then materialised into peer_option_contracts at COMMIT_TX time.
//
// Buyer and Seller are extracted by pairing this option posting with
// its counterpart in the same TX (the matched posting with opposite
// direction): a CREDIT option posting identifies the buyer; the DEBIT
// option posting (same OptionDescription) identifies the seller.
type OptionItem struct {
	PostingIndex          int                        `json:"postingIndex"`
	Direction             string                     `json:"direction"` // local-side direction: DEBIT or CREDIT
	OptionDescriptionJSON string                     `json:"optionDescriptionJson"`
	Buyer                 contractsitx.ForeignBankId `json:"buyer"`
	Seller                contractsitx.ForeignBankId `json:"seller"`
}

// ReserveResult is the outcome of executing the reserve phase of a NEW_TX.
type ReserveResult struct {
	Vote            contractsitx.TransactionVote
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
}

// optionDescriptionForCheck mirrors the fields of contract.sitx.OptionDescription
// that the executor's pre-check needs. Local copy avoids importing the full
// option-description type just for two fields and keeps the executor
// independent of stock-service's option model.
type optionDescriptionForCheck struct {
	Ticker string `json:"ticker"`
	Amount int64  `json:"amount"`
}

// PostingExecutor walks an accepted NEW_TX's postings and applies the
// receiver-side reservations via account-service. ownRouting is this
// bank's routing number — postings with a different routing are not
// executed locally (they're the responsibility of the other bank).
type PostingExecutor struct {
	client         AccountClient
	holdingChecker SellerHoldingChecker // optional; nil disables seller-side option pre-check
	ownRouting     int64
}

func NewPostingExecutor(client AccountClient, ownRouting int64) *PostingExecutor {
	return &PostingExecutor{client: client, ownRouting: ownRouting}
}

// SetHoldingChecker wires the stock-service-backed seller pre-check.
// Optional — left nil, the executor still emits OptionItems for option-
// asset postings but does not validate seller holdings at NEW_TX time
// (sufficiency is enforced best-effort at COMMIT_TX time via the
// holding lock in stock-service.RecordOptionContract).
func (e *PostingExecutor) SetHoldingChecker(c SellerHoldingChecker) {
	e.holdingChecker = c
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
//   - DEBIT posting (asset is leaving our account) → immediate UpdateBalance
//     with amount=-X. We track the debit in the returned DebitedItems so
//     HandleRollbackTx can credit back the same amount with a matching
//     idempotency key. UpdateBalance is itself idempotent on the key, so
//     the debit is safe to replay; only the first call moves money.
//
// Postings whose AssetID is a JSON option-description (currency mismatch
// won't apply) are silently skipped: option contract handling lives in
// stock-service, not here. This is a known scope limit — full OTC option
// formation requires extending the executor to dispatch to stock-service.
//
// On any per-posting failure, returns a NO vote with the matching SI-TX
// reason and the failing posting index.
func (e *PostingExecutor) Reserve(ctx context.Context, postings []contractsitx.Posting, peerBankCode, locallyGeneratedKey string) ReserveResult {
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
		if !strings.HasPrefix(p.AssetID, "{") {
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

	for i := range postings {
		p := postings[i]
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
		if strings.HasPrefix(p.AssetID, "{") {
			if p.Direction == contractsitx.DirectionDebit {
				if e.holdingChecker == nil {
					return noVote(contractsitx.NoVoteReasonInsufficientAsset, i)
				}
				var od optionDescriptionForCheck
				if err := json.Unmarshal([]byte(p.AssetID), &od); err == nil && od.Ticker != "" && od.Amount > 0 {
					seller := sellerByDesc[p.AssetID]
					crossbankTxID := peerBankCode + ":" + locallyGeneratedKey
					resp, err := e.holdingChecker.ReserveSellerSharesForNewTx(ctx, &stockpb.ReserveSellerSharesRequest{
						SellerId: &stockpb.PeerForeignBankId{
							RoutingNumber: seller.RoutingNumber,
							Id:            seller.ID,
						},
						Ticker:        od.Ticker,
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
			})
			continue
		}
		// Resolve participant-ID-style accountId ("client-7", "employee-3")
		// to a concrete bank account number for the requested currency.
		// 18-digit account numbers pass through unchanged.
		accountNumber, resolveErr := e.resolveAccountForPosting(ctx, p.AccountID, p.AssetID)
		if resolveErr != nil {
			return noVote(contractsitx.NoVoteReasonNoSuchAccount, i)
		}
		acct, err := e.client.GetAccountByNumber(ctx, &accountpb.GetAccountByNumberRequest{AccountNumber: accountNumber})
		if err != nil || acct == nil {
			return noVote(contractsitx.NoVoteReasonNoSuchAccount, i)
		}
		if acct.Status != "active" {
			return noVote(contractsitx.NoVoteReasonUnacceptableAsset, i)
		}
		if acct.CurrencyCode != p.AssetID {
			return noVote(contractsitx.NoVoteReasonNoSuchAsset, i)
		}

		switch p.Direction {
		case contractsitx.DirectionCredit:
			key := peerBankCode + ":" + locallyGeneratedKey
			if _, err := e.client.ReserveIncoming(ctx, &accountpb.ReserveIncomingRequest{
				AccountNumber:  accountNumber,
				Amount:         p.Amount.String(),
				Currency:       p.AssetID,
				ReservationKey: key,
				IdempotencyKey: "sitx-reserve-" + key,
			}); err != nil {
				return noVote(contractsitx.NoVoteReasonUnacceptableAsset, i)
			}
			keys = append(keys, key)

		case contractsitx.DirectionDebit:
			tag := fmt.Sprintf("%s:%s:%d", peerBankCode, locallyGeneratedKey, i)
			if _, err := e.client.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
				AccountNumber:   accountNumber,
				Amount:          "-" + p.Amount.String(),
				UpdateAvailable: true,
				IdempotencyKey:  "sitx-debit-" + tag,
			}); err != nil {
				// account-service rejects debits below available balance;
				// surface that as INSUFFICIENT_ASSET per SI-TX semantics.
				return noVote(contractsitx.NoVoteReasonInsufficientAsset, i)
			}
			debits = append(debits, DebitedItem{
				AccountNumber:  accountNumber,
				Amount:         p.Amount.String(),
				IdempotencyTag: tag,
			})

		default:
			return noVote(contractsitx.NoVoteReasonUnacceptableAsset, i)
		}
	}
	return ReserveResult{
		Vote:            contractsitx.TransactionVote{Type: contractsitx.VoteYes},
		ReservationKeys: keys,
		DebitedItems:    debits,
		OptionItems:     options,
	}
}

// ReverseLocal undoes the local effects a prior Reserve applied for the same
// (postings, peerBankCode, locallyGeneratedKey): it releases the CREDIT-side
// reservation and credits back each DEBIT posting on our routing, reusing the
// same reservation key and per-posting idempotency tags Reserve used. Because
// those keys match, ReverseLocal nets exactly the effects of Reserve and is
// safe to call repeatedly and to interleave with the inline rollback path.
//
// Used by OutboundReplayCron (via PeerTxGRPCHandler.ReverseOutboundLocal) to
// return money on a sender-side OTC TX that terminally fails after the local
// legs were already applied. Option-asset postings carry no money and are
// skipped; a NotFound on release is benign (no CREDIT legs landed locally).
func (e *PostingExecutor) ReverseLocal(ctx context.Context, postings []contractsitx.Posting, peerBankCode, locallyGeneratedKey string) error {
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
		if strings.HasPrefix(p.AssetID, "{") {
			continue // option-asset leg — no money to return
		}
		accountNumber, resolveErr := e.resolveAccountForPosting(ctx, p.AccountID, p.AssetID)
		if resolveErr != nil {
			return resolveErr
		}
		tag := fmt.Sprintf("%s:%s:%d", peerBankCode, locallyGeneratedKey, i)
		if _, err := e.client.UpdateBalance(ctx, &accountpb.UpdateBalanceRequest{
			AccountNumber:   accountNumber,
			Amount:          p.Amount.String(),
			UpdateAvailable: true,
			IdempotencyKey:  "sitx-localcreditback-" + tag,
		}); err != nil {
			return err
		}
	}
	return nil
}

// resolveAccountForPosting maps an accountId string to a concrete bank
// account number. Participant-ID strings ("client-<n>") are resolved
// via account-service to the participant's first active account in the
// requested currency. Anything else passes through unchanged — the
// downstream GetAccountByNumber call will surface NO_SUCH_ACCOUNT for
// genuinely unknown accountIds.
func (e *PostingExecutor) resolveAccountForPosting(ctx context.Context, accountID, currency string) (string, error) {
	// Participant ID pattern: "client-<digits>". Only the "client"
	// owner type is resolvable today (employees don't own currency
	// accounts in this codebase). Everything else falls through.
	rest, ok := strings.CutPrefix(accountID, "client-")
	if !ok || rest == "" {
		return accountID, nil
	}
	clientID, parseErr := strconv.ParseUint(rest, 10, 64)
	if parseErr != nil {
		return accountID, nil
	}
	resp, listErr := e.client.ListAccountsByClient(ctx, &accountpb.ListAccountsByClientRequest{ClientId: clientID, Page: 1, PageSize: 100})
	if listErr != nil || resp == nil {
		return "", fmt.Errorf("list accounts for %s: %w", accountID, listErr)
	}
	for _, a := range resp.GetAccounts() {
		if a.GetCurrencyCode() == currency && a.GetStatus() == "active" {
			return a.GetAccountNumber(), nil
		}
	}
	return "", fmt.Errorf("client %d has no active %s account", clientID, currency)
}

// hasOwnDebitOptionLeg reports whether the postings contain a DEBIT
// option-asset leg on the given routing — i.e. this bank holds the seller and
// therefore placed a vote-time share hold that must be released on rollback.
func hasOwnDebitOptionLeg(postings []contractsitx.Posting, ownRouting int64) bool {
	for i := range postings {
		p := postings[i]
		if p.RoutingNumber == ownRouting && p.Direction == contractsitx.DirectionDebit && strings.HasPrefix(p.AssetID, "{") {
			return true
		}
	}
	return false
}

func noVote(reason string, postingIdx int) ReserveResult {
	return ReserveResult{
		Vote: contractsitx.TransactionVote{
			Type:    contractsitx.VoteNo,
			NoVotes: []contractsitx.NoVote{{Reason: reason, Posting: ptr(postingIdx)}},
		},
	}
}

func ptr(i int) *int { return &i }
