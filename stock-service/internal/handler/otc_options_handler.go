package handler

import (
	"context"
	"errors"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	accountpb "github.com/exbanka/contract/accountpb"
	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

// OTCOptionsHandler implements stockpb.OTCOptionsServiceServer.
//
// Account-id resolution for Accept/Exercise: the gateway-side request
// validates that the caller passes BuyerAccountID/SellerAccountID; the
// gRPC layer forwards them through. (Same pattern as the existing
// stock OrderGRPCService — gateway resolves user identity, downstream
// service trusts the IDs.)
type OTCOptionsHandler struct {
	stockpb.UnimplementedOTCOptionsServiceServer
	svc           *service.OTCOfferService
	contracts     *repository.OptionContractRepository
	peerContracts *repository.OptionContractRepository // optional; surfaces cross-bank (remote) contracts in /me/otc/contracts
	listings      *repository.ListingRepository        // optional; populates market_reference_price
	ownRouting    int64
	ratings       *service.OTCRatingService      // optional; backs SubmitRating / GetTraderProfile / ListReceivedRatings
	negotiations  *service.OTCNegotiationService // optional; backs Phase-2 parallel-chain RPCs (Open/Counter/AcceptChain/Reject/Cancel/List*)
	// fundRepo is optional; needed to validate on_behalf_of_fund_id
	// on Accept/Exercise (E2 Plan E). Without it, on_behalf_of_fund_id
	// requests are rejected with a clear error.
	fundRepo *repository.FundRepository
	// remoteOffers is optional; the persistent cross-bank mirror used by
	// GetOffer to resolve an offer id that is not a local OTCOffer to a
	// peer-bank listing (SP-1). ownBankCode is this bank's 3-digit code,
	// stamped as provenance on local offers.
	remoteOffers RemoteOfferGetter
	ownBankCode  string
	// peerNegs is optional; the cross-bank peer-negotiation mirror used by
	// ListMyNegotiations to merge the caller's REMOTE chains into the same
	// list as the LOCAL ones (SP-1 Task 7). When unset, ListMyNegotiations
	// returns local chains only.
	peerNegs PeerNegotiationLister
	// SP-2b — cross-bank bid dispatch. When OpenNegotiation's parent :id
	// resolves to a folded-in REMOTE OTCOffer (a peer-hosted listing), the
	// handler composes the SI-TX OtcOffer and POSTs it to the seller's bank
	// via peerDispatch, then records the mirror row via remoteNegWriter. The
	// bidder account is re-validated against accounts. All three are optional;
	// when unset, a remote parent :id falls through to the original NotFound.
	peerDispatch    PeerNegotiationDispatcher
	remoteNegWriter RemoteNegotiationWriter
	accounts        OTCAccountClient
	// remoteNegOps backs the SP-2b Task-4 counter/accept/reject/cancel
	// cross-bank dispatch: it resolves a REMOTE chain by surrogate id and
	// mirrors the peer-driven state changes (+ drives cascade-cancel on
	// accept). Optional; when unset a remote :nid on those actions falls
	// through to the local NotFound.
	remoteNegOps RemoteNegotiationOps
	// myNegs is optional; when wired, GetOffer stamps my_negotiation_id /
	// my_negotiation_status on the resolved offer when the authenticated caller
	// has an own (bidder) chain against it (SP-2b). Same source/keying as the
	// unified-list path; reuses h.ownRouting for the remote-chain match.
	myNegs MyNegotiationLister
	// ownerLatestCounter is optional; when wired, GetOffer re-sources the
	// strike/premium/settlement of a LOCAL offer the caller OWNS from the acting
	// principal's most recent counter on that offer (D2). Same source as the
	// unified-list path.
	ownerLatestCounter OwnerLatestCounterFn
	// crossBankExerciser backs the SP-2b Task-5 remote branch of
	// ExerciseContract: when the contract :id resolves to a REMOTE row (a
	// peer-hosted contract this bank holds the buyer side of), the unified
	// exercise delegates to the cross-bank SI-TX exercise dispatch instead of
	// the local saga. Satisfied by *PeerOTCGRPCHandler (its
	// InitiateOptionExercise composes the 4-posting exercise Transaction and
	// dispatches it via transaction-service). Optional; when unset a remote :id
	// on exercise falls through to NotFound (local-only behavior).
	crossBankExerciser CrossBankExerciser
}

// CrossBankExerciser dispatches a cross-bank option exercise for a REMOTE
// contract this bank holds the buyer side of. It composes the spec
// OPTION-pseudo-account exercise Transaction and dispatches it via
// transaction-service, claiming the contract (active → exercising) to serialise
// concurrent exercises. Satisfied by *PeerOTCGRPCHandler.InitiateOptionExercise
// (SP-2b Task 5 — consolidating the gateway peer-exercise path into the unified
// ExerciseContract).
type CrossBankExerciser interface {
	InitiateOptionExercise(ctx context.Context, req *stockpb.InitiateOptionExerciseRequest) (*stockpb.InitiateOptionExerciseResponse, error)
}

// PeerNegotiationDispatcher POSTs a composed SI-TX OtcOffer to a peer bank's
// /negotiations API and returns the peer-assigned (routingNumber, foreignID).
// Proxy forwards a single-negotiation action (counter PUT, accept GET /accept,
// reject/cancel DELETE) to {peer}/negotiations/{rid}/{foreignID}{subpath} and
// returns the raw body + HTTP status. As of the 2026-06-07 cutover both are
// satisfied by *peeregress.Dispatcher, which routes the outbound HTTP through
// interbank-service's PeerEgressService.ProxyToPeer.
type PeerNegotiationDispatcher interface {
	CreateNegotiation(ctx context.Context, peerBankCode string, offer map[string]any) (int64, string, error)
	Proxy(ctx context.Context, peerBankCode, rid, foreignID, method, subpath string, body []byte) ([]byte, int, error)
	// PublicStock fetches the peer's /public-stock listing (§3.1 bare array).
	// Used by the freshness guard in openRemoteNegotiation to verify a shell
	// offer is still live before dispatching a bid. This is a dedicated method
	// rather than a Proxy call to avoid Proxy's hardcoded
	// /negotiations/{rid}/{foreignID}{subpath} path construction.
	PublicStock(ctx context.Context, peerBankCode string) ([]byte, int, error)
}

// RemoteNegotiationOps is the cross-bank negotiation-mirror surface the
// counter/accept/reject/cancel dispatch needs to (a) resolve a REMOTE chain by
// its local surrogate id, (b) mirror peer-driven state changes, and (c) drive
// the cross-bank cascade-cancel on accept. Satisfied by
// *repository.OTCNegotiationRepository (SP-2b Task 4).
type RemoteNegotiationOps interface {
	GetRemoteNegByID(id uint64) (*model.OTCNegotiation, error)
	UpdateRemoteNegOffer(routing int64, native, offerJSON string) error
	UpdateRemoteNegStatus(routing int64, native, status string) error
	CompareAndSetRemoteNegStatus(routing int64, native, from, to string) (bool, error)
	ListRemoteNegBySellerAndParent(sellerRouting int64, sellerID string, parentRouting int64, parentNative string) ([]model.OTCNegotiation, error)
	ListRemoteNegByParent(parentRouting int64, parentNative string) ([]model.OTCNegotiation, error)
	// History-logging variants: mutate AND append a revision atomically (full
	// per-move history parity with local chains, 2026-06-06).
	UpdateRemoteNegOfferWithRevision(routing int64, native, offerJSON string, rev *model.OTCNegotiationRevision) error
	CompareAndSetRemoteNegStatusWithRevision(routing int64, native, from, to string, rev *model.OTCNegotiationRevision) (bool, error)
	SetRemoteNegStatusWithRevision(routing int64, native, to string, rev *model.OTCNegotiationRevision) (bool, error)
}

// RemoteNegotiationWriter persists a REMOTE OTCNegotiation mirror row (the
// cross-bank chain this bank's bidder just opened on a peer). Satisfied by
// *repository.OTCNegotiationRepository (SP-2b).
type RemoteNegotiationWriter interface {
	UpsertRemoteNeg(n *model.OTCNegotiation) error
	// UpsertRemoteNegWithRevision additionally appends the BID revision when the
	// chain is newly created (full-history parity, 2026-06-06).
	UpsertRemoteNegWithRevision(n *model.OTCNegotiation, rev *model.OTCNegotiationRevision) error
}

// OTCAccountClient is the narrow account-service surface the cross-bank OTC
// paths need: OpenNegotiation validates (and reads the account number of) a
// bidder's account by id; exerciseRemoteContract re-asserts the buyer's
// strike-debit account by NUMBER before dispatch (SP-3 Task 5 security gate).
// Satisfied by accountpb.AccountServiceClient (SP-2b / SP-3).
type OTCAccountClient interface {
	GetAccount(ctx context.Context, in *accountpb.GetAccountRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error)
	GetAccountByNumber(ctx context.Context, in *accountpb.GetAccountByNumberRequest, opts ...grpc.CallOption) (*accountpb.AccountResponse, error)
}

// WithPeerOTCDispatch wires the cross-bank bid-dispatch path so OpenNegotiation
// can place a bid on a peer-hosted (REMOTE) OTC listing (SP-2b). dispatcher
// POSTs the SI-TX OtcOffer to the seller's bank; remoteNegWriter persists the
// resulting mirror row; accounts re-validates the bidder account. Without this
// wire-up, a bid on a remote :id falls through to NotFound (local-only behavior).
func (h *OTCOptionsHandler) WithPeerOTCDispatch(dispatcher PeerNegotiationDispatcher, remoteNegWriter RemoteNegotiationWriter, accounts OTCAccountClient) *OTCOptionsHandler {
	cp := *h
	cp.peerDispatch = dispatcher
	cp.remoteNegWriter = remoteNegWriter
	cp.accounts = accounts
	// remoteNegWriter is *repository.OTCNegotiationRepository in production,
	// which also implements the broader RemoteNegotiationOps surface used by
	// the Task-4 counter/accept/reject/cancel dispatch. Capture it when the
	// concrete type satisfies the interface so callers don't need a second
	// wire-up. Tests that pass a narrow writer can use WithRemoteNegOps.
	if ops, ok := remoteNegWriter.(RemoteNegotiationOps); ok {
		cp.remoteNegOps = ops
	}
	return &cp
}

// WithRemoteNegOps wires the cross-bank negotiation-mirror ops used by the
// counter/accept/reject/cancel cross-bank dispatch (SP-2b Task 4). In
// production this is the same *repository.OTCNegotiationRepository passed to
// WithPeerOTCDispatch (which auto-captures it); this explicit setter exists so
// tests can inject a fake independent of the writer.
func (h *OTCOptionsHandler) WithRemoteNegOps(ops RemoteNegotiationOps) *OTCOptionsHandler {
	cp := *h
	cp.remoteNegOps = ops
	return &cp
}

// WithCrossBankExerciser wires the cross-bank exercise dispatch so the unified
// ExerciseContract handles a REMOTE (peer-hosted) contract by delegating to the
// SI-TX option-exercise flow (SP-2b Task 5). In production this is the
// *PeerOTCGRPCHandler. Without this wire-up, exercising a remote :id falls
// through to NotFound (local-only behavior).
func (h *OTCOptionsHandler) WithCrossBankExerciser(e CrossBankExerciser) *OTCOptionsHandler {
	cp := *h
	cp.crossBankExerciser = e
	return &cp
}

// PeerNegotiationLister fetches the caller's cross-bank negotiation mirror
// rows (REMOTE rows in the unified otc_negotiations table). ListMyNegotiations
// merges these REMOTE chains with the LOCAL ones. Satisfied by
// *repository.OTCNegotiationRepository (SP-2a — the dedicated
// peer_otc_negotiation mirror was retired and folded into this table).
type PeerNegotiationLister interface {
	ListRemoteNegByClient(ownRouting int64, clientPrincipal, role string) ([]model.OTCNegotiation, error)
	// ListRemoteNegByBankParty surfaces the REMOTE chains WE host where the
	// hosted side is the BANK (party id "employee-<N>"). role: "buyer" → our
	// cross-bank bids; "seller" → peer bids on our bank-owned offer; "" → either.
	// Lets an employee acting AS THE BANK see/list its own cross-bank chains
	// (SP-3 Task 5b). The bank has no single wire principal, so this matches by
	// prefix; a CLIENT caller must never reach it (use ListRemoteNegByClient).
	ListRemoteNegByBankParty(ownRouting int64, role string) ([]model.OTCNegotiation, error)
}

// WithPeerNegotiations wires the cross-bank peer-negotiation mirror so
// ListMyNegotiations returns a unified local+remote list (SP-1 Task 7).
// ownRouting and ownBankCode come from WithPeerContracts / WithRemoteOffers;
// this method only adds the peer-negotiation source.
func (h *OTCOptionsHandler) WithPeerNegotiations(p PeerNegotiationLister) *OTCOptionsHandler {
	cp := *h
	cp.peerNegs = p
	return &cp
}

func NewOTCOptionsHandler(svc *service.OTCOfferService, contracts *repository.OptionContractRepository) *OTCOptionsHandler {
	return &OTCOptionsHandler{svc: svc, contracts: contracts}
}

// WithMyNegotiations wires the caller's own (bidder) negotiation source so
// GetOffer can stamp my_negotiation_id / my_negotiation_status on the resolved
// offer (SP-2b). The remote-chain match keys on h.ownRouting (set via
// WithPeerContracts / WithRemoteOffers).
func (h *OTCOptionsHandler) WithMyNegotiations(l MyNegotiationLister) *OTCOptionsHandler {
	cp := *h
	cp.myNegs = l
	return &cp
}

// WithOwnerLatestCounter wires the owner-latest-counter source so GetOffer can
// project the acting owner's most recent counter terms onto a LOCAL offer they
// own (D2). Same source as the unified-list path.
func (h *OTCOptionsHandler) WithOwnerLatestCounter(fn OwnerLatestCounterFn) *OTCOptionsHandler {
	cp := *h
	cp.ownerLatestCounter = fn
	return &cp
}

// WithRatings wires the OTC trader-rating service. When unset the
// rating RPCs return Unimplemented.
func (h *OTCOptionsHandler) WithRatings(r *service.OTCRatingService) *OTCOptionsHandler {
	cp := *h
	cp.ratings = r
	return &cp
}

// WithPeerContracts wires the option-contracts repository and this bank's
// routing number so ListMyContracts can also return cross-bank (remote)
// option_contracts rows where the caller is a participant.
func (h *OTCOptionsHandler) WithPeerContracts(peer *repository.OptionContractRepository, ownRouting int64) *OTCOptionsHandler {
	cp := *h
	cp.peerContracts = peer
	cp.ownRouting = ownRouting
	return &cp
}

// WithFundRepo wires the fund repository so AcceptNegotiationChain and
// ExerciseContract can validate on_behalf_of_fund_id (E2, Plan E).
func (h *OTCOptionsHandler) WithFundRepo(repo *repository.FundRepository) *OTCOptionsHandler {
	cp := *h
	cp.fundRepo = repo
	return &cp
}

// WithListings wires the listing repo so contract / offer responses can
// surface market_reference_price for the UI to compute profit-vs-strike
// (Celina-4 §Sklopljeni ugovori "Profit" column).
func (h *OTCOptionsHandler) WithListings(listings *repository.ListingRepository) *OTCOptionsHandler {
	cp := *h
	cp.listings = listings
	return &cp
}

// kindFromLocal returns the FE provenance label derived from a row's explicit
// `local` discriminator (stamped once in BeforeCreate). It is THE source of the
// kind field: local==true → "local", local==false → "remote".
func kindFromLocal(local bool) string {
	if local {
		return "local"
	}
	return "remote"
}

// RemoteOfferGetter fetches a folded-in REMOTE OTCOffer row by surrogate id.
// GetOffer falls back to this when an offer id is not a local OTCOffer
// (SP-1). Returns gorm.ErrRecordNotFound for a local id (a local offer is
// not a remote offer). *repository.OTCOfferRepository satisfies it via
// GetRemoteByID (SP-2a).
type RemoteOfferGetter interface {
	GetRemoteByID(id uint64) (*model.OTCOffer, error)
}

// WithRemoteOffers wires the persistent cross-bank remote-offer mirror plus
// this bank's 3-digit code so GetOffer can resolve a non-local offer id to a
// peer-bank listing and stamp provenance on local offers (SP-1). ownRouting
// (the int form of OWN_BANK_CODE) is taken from WithPeerContracts; pass the
// bank code string here so local offers can also carry bank_code.
func (h *OTCOptionsHandler) WithRemoteOffers(g RemoteOfferGetter, ownBankCode string) *OTCOptionsHandler {
	cp := *h
	cp.remoteOffers = g
	cp.ownBankCode = ownBankCode
	return &cp
}

// marketRefPrice returns the listing's current price for the stock id, or
// "" when no listing is wired or the lookup fails.
func (h *OTCOptionsHandler) marketRefPrice(stockID uint64) string {
	if h.listings == nil {
		return ""
	}
	listing, err := h.listings.GetBySecurityIDAndType(stockID, "stock")
	if err != nil || listing == nil {
		return ""
	}
	return listing.Price.String()
}

func (h *OTCOptionsHandler) CreateOffer(ctx context.Context, in *stockpb.CreateOTCOfferRequest) (*stockpb.OTCOfferResponse, error) {
	qty, err := decimal.NewFromString(in.Quantity)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "quantity is not a valid decimal")
	}
	if in.AccountId == 0 {
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}
	// Option offers are termless inventory: strike_price / premium /
	// settlement_date on the proto request are no longer consumed (terms are
	// negotiated per chain). The proto fields are removed in a later task.
	input := service.CreateOfferInput{
		ActorUserID: in.ActorUserId, ActorSystemType: in.ActorSystemType,
		ActingEmployeeID: optionalPtr(in.GetActingEmployeeId()),
		Direction:        in.Direction, StockID: in.StockId,
		Ticker:             in.Ticker,
		Quantity:           qty,
		InitiatorAccountID: in.AccountId,
	}
	if in.Counterparty != nil && in.Counterparty.UserId != 0 {
		uid := in.Counterparty.UserId
		st := in.Counterparty.SystemType
		input.CounterpartyUserID = &uid
		input.CounterpartySystemType = &st
	}
	o, err := h.svc.Create(ctx, input)
	if err != nil {
		return nil, mapOTCErr(err)
	}
	return h.withOfferMarketRef(o, toOTCOfferProto(o, false)), nil
}

func (h *OTCOptionsHandler) ListMyOffers(ctx context.Context, in *stockpb.ListMyOTCOffersRequest) (*stockpb.ListMyOTCOffersResponse, error) {
	rows, total, err := h.svc.ListMyOffers(in.ActorUserId, in.ActorSystemType, in.Role, in.Statuses, in.StockId, int(in.Page), int(in.PageSize))
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	out := &stockpb.ListMyOTCOffersResponse{Total: total, Offers: make([]*stockpb.OTCOfferResponse, 0, len(rows))}
	for i := range rows {
		// Celina-4 §Aktivne ponude: unread = caller hasn't seen this update
		// yet. Computed from the read-receipt comparing to the offer's
		// updated_at. Caller is its own last-modifier => always read.
		unread := h.computeUnread(&rows[i], in.ActorUserId, in.ActorSystemType)
		out.Offers = append(out.Offers, h.withOfferMarketRef(&rows[i], toOTCOfferProto(&rows[i], unread)))
	}
	return out, nil
}

// ListNegotiationHistory returns the caller's terminal OTC negotiations,
// LOCAL (intra-bank, accepted/rejected/expired/failed) and REMOTE (cross-bank
// peer chains in a terminal status) merged into one list (Celina-3 "Istorija
// pregovora" + SP-1 Task 8b). Items are OTCOfferResponse, which already carries
// kind/routing_number/bank_code/me_owner — NO proto change is needed; the
// handler just stamps and merges.
//
// LOCAL items: kind="local", own routing/bank-code provenance, me_owner = the
// caller posted/originated the offer (initiator side) — the SAME rule as
// GetOffer. A history row where the caller was the bidder/counterparty is
// me_owner=false.
//
// REMOTE items: a CLIENT caller sees its own cross-bank chains by its exact
// "client-<N>" principal; a BANK caller (employee acting as the bank) sees the
// bank's cross-bank chains via the routing-scoped, "employee-%"-prefixed bank
// lister (SP-3 T5b). Each remote chain is mapped onto OTCOfferResponse with
// kind="remote", COUNTERPARTY/peer provenance, and me_owner = WE host the
// seller/poster side (SellerRoutingNumber == ownRouting). Only chains in a
// terminal peer status are surfaced (history is past data) and the request's
// status filter is mapped onto the peer status vocabulary.
//
// Paging: page/page_size apply to the LOCAL set only (the repository paginates
// it). Remote terminal rows are appended in full after the local page — they
// are never silently truncated, consistent with Task 7's ListMyNegotiations.
// total reflects the local total only; the merged slice length may exceed it by
// the remote count. Unified cross-source paging is out of scope for SP-1.
func (h *OTCOptionsHandler) ListNegotiationHistory(ctx context.Context, in *stockpb.ListNegotiationHistoryRequest) (*stockpb.ListMyOTCOffersResponse, error) {
	f := repository.HistoryFilter{
		Statuses: in.Statuses,
		Page:     int(in.Page),
		PageSize: int(in.PageSize),
	}
	if in.SinceUnix > 0 {
		t := time.Unix(in.SinceUnix, 0).UTC()
		f.Since = &t
	}
	if in.UntilUnix > 0 {
		t := time.Unix(in.UntilUnix, 0).UTC()
		f.Until = &t
	}
	if in.CounterpartyId > 0 {
		cp := in.CounterpartyId
		f.CounterpartyID = &cp
	}
	rows, total, err := h.svc.ListNegotiationHistory(in.ActorUserId, in.ActorSystemType, f)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	ownerType, ownerID := model.OwnerFromLegacy(uint64(in.ActorUserId), in.ActorSystemType)
	out := &stockpb.ListMyOTCOffersResponse{Total: total, Offers: make([]*stockpb.OTCOfferResponse, 0, len(rows))}
	for i := range rows {
		// History entries are immutable from the caller's perspective so
		// "unread" is always false — they're explicitly viewing past data.
		item := h.withOfferMarketRef(&rows[i], toOTCOfferProto(&rows[i], false))
		item.Kind = kindFromLocal(rows[i].Local)
		item.RoutingNumber = h.ownRouting
		item.BankCode = h.ownBankCode
		// me_owner ⇔ the caller posted/originated this offer (initiator side) —
		// same rule as GetOffer. A bidder/counterparty history row is false.
		item.MeOwner = otcMeOwner(
			string(ownerType), model.OwnerIDOrZero(ownerID),
			item.Kind, sellerIDForOwner(rows[i].InitiatorOwnerType, rows[i].InitiatorOwnerID),
		)
		out.Offers = append(out.Offers, item)
	}

	// REMOTE merge — cross-bank peer negotiation chains in a terminal status
	// where the caller is a party. Two principal kinds have a cross-bank identity:
	//
	//   - CLIENT (cross-bank party id "client-<N>"): match the exact principal
	//     via ListRemoteNegByClient. Both buyer + seller chains included — history
	//     covers all the client's terminal cross-bank activity.
	//   - BANK (an employee acting AS THE BANK; party id "employee-<N>"): the bank
	//     has no single wire principal across chains, so match by prefix via
	//     ListRemoteNegByBankParty(role="") which surfaces both buyer + seller
	//     chains (SP-3 Task 5b completeness). A client caller never reaches the
	//     bank lister (and vice versa).
	if h.peerNegs != nil {
		var peerRows []model.OTCNegotiation
		var perr error
		switch {
		case ownerType == model.OwnerClient && ownerID != nil:
			principal := "client-" + strconv.FormatUint(*ownerID, 10)
			peerRows, perr = h.peerNegs.ListRemoteNegByClient(h.ownRouting, principal, "")
		case ownerType == model.OwnerBank:
			peerRows, perr = h.peerNegs.ListRemoteNegByBankParty(h.ownRouting, "")
		}
		if perr != nil {
			return nil, status.Errorf(codes.Internal, "list peer negotiations: %v", perr)
		}
		want := historyPeerStatusSet(in.GetStatuses())
		for i := range peerRows {
			if _, ok := want[peerRows[i].Status]; !ok {
				continue // not a terminal/requested peer status
			}
			item := peerNegToOfferProto(&peerRows[i], h.ownRouting)
			if item == nil {
				continue
			}
			out.Offers = append(out.Offers, item)
		}
	}
	return out, nil
}

// peerTerminalStatuses is the set of terminal peer-negotiation statuses that
// belong in the history view. Active states (ongoing/countered) are excluded.
var peerTerminalStatuses = map[string]struct{}{
	"accepted":  {},
	"rejected":  {},
	"declined":  {},
	"cancelled": {},
	"expired":   {},
}

// historyMappedStatuses maps each uppercase history-request status onto the
// peer-negotiation status vocabulary (the two sides use different words).
var historyMappedStatuses = map[string][]string{
	"ACCEPTED": {"accepted"},
	"REJECTED": {"rejected", "declined", "cancelled"},
	"EXPIRED":  {"expired"},
	"FAILED":   {}, // peer rows have no "failed" state — matches nothing
}

// historyPeerStatusSet builds the set of peer statuses to include in the
// remote history merge. With no request filter it is every terminal peer
// status; with a filter it is the union of the mapped peer statuses (still
// constrained to terminal ones).
func historyPeerStatusSet(requested []string) map[string]struct{} {
	if len(requested) == 0 {
		return peerTerminalStatuses
	}
	out := make(map[string]struct{})
	for _, s := range requested {
		for _, mapped := range historyMappedStatuses[s] {
			if _, ok := peerTerminalStatuses[mapped]; ok {
				out[mapped] = struct{}{}
			}
		}
	}
	return out
}

// peerNegToOfferProto maps a cross-bank peer-negotiation mirror row onto the
// OTCOfferResponse wire shape used by the history view (SP-1 Task 8b). It is
// the offer-shaped sibling of peerNegToProto (which maps onto the negotiation
// shape). Provenance + me_owner follow the same rule:
//
//   - Id is the local surrogate primary key of the mirror row.
//   - kind = "remote"; routing_number + bank_code identify the COUNTERPARTY
//     peer bank — the side WE do NOT host.
//   - terms come from the parsed sitx.OtcOffer carried in OfferJSON.
//   - me_owner = WE host the seller/poster side (SellerRoutingNumber ==
//     our own routing).
//
// The ticker is read from the same peerNegToProto decode (second return value)
// so RemoteOfferJSON is only unmarshalled once per row, not twice.
func peerNegToOfferProto(row *model.OTCNegotiation, ownRouting int64) *stockpb.OTCOfferResponse {
	neg, ticker := peerNegToProto(row, ownRouting)
	if neg == nil {
		return nil
	}
	_, sellerID := remoteSeller(row)
	return &stockpb.OTCOfferResponse{
		Id:             neg.GetId(),
		StockTicker:    ticker,
		Quantity:       neg.GetQuantity(),
		StrikePrice:    neg.GetStrikePrice(),
		Premium:        neg.GetPremium(),
		SettlementDate: neg.GetSettlementDate(),
		Status:         neg.GetStatus(),
		CreatedAt:      neg.GetCreatedAt(),
		UpdatedAt:      neg.GetUpdatedAt(),
		Kind:           neg.GetKind(),
		RoutingNumber:  neg.GetRoutingNumber(),
		BankCode:       neg.GetBankCode(),
		MeOwner:        neg.GetMeOwner(),
		Initiator: &stockpb.PartyRef{
			DisplayName: sellerID,
			BankCode:    neg.GetBankCode(),
		},
	}
}

// computeUnread returns true if the offer has been touched since the
// caller last opened it AND the caller wasn't the one who touched it.
func (h *OTCOptionsHandler) computeUnread(o *model.OTCOffer, callerID int64, callerType string) bool {
	if o.LastModifiedByPrincipalID == uint64(callerID) && o.LastModifiedByPrincipalType == callerType {
		return false
	}
	rec, err := h.svc.LastReadReceipt(callerID, callerType, o.ID)
	if err != nil || rec == nil {
		return true // never opened
	}
	return o.UpdatedAt.After(rec.LastSeenUpdatedAt)
}

// GetOffer resolves an OTC offer by id, converging local + remote in the
// service layer (SP-1). A local OTCOffer is returned with kind="local" plus
// this bank's routing/bank-code provenance and me_owner. When the id is not a
// local offer, it falls back to the persistent cross-bank mirror and returns a
// kind="remote" projection (me_owner is always false; remote listings are
// hosted by a peer). NotFound only when neither a local nor a remote row
// exists.
func (h *OTCOptionsHandler) GetOffer(ctx context.Context, in *stockpb.GetOTCOfferRequest) (*stockpb.OTCOfferDetailResponse, error) {
	// SP-2b — the caller's own (bidder) chains, built once and reused for both
	// the local and the remote-resolution branch. Best-effort: an index error
	// logs and falls back to no stamp rather than failing the read.
	myNegIdx, idxErr := buildMyNegotiationIndex(h.myNegs, in.GetActingOwnerType(), in.GetActingOwnerId(), h.ownRouting)
	if idxErr != nil {
		log.Printf("WARN GetOffer: my-negotiation index failed (continuing without my_negotiation_id): %v", idxErr)
		myNegIdx = myNegotiationIndex{}
	}

	o, revs, err := h.svc.GetOffer(in.OfferId, in.ActorUserId, in.ActorSystemType)
	if err != nil {
		// Local offer doesn't exist — try the cross-bank mirror before 404.
		if errors.Is(err, gorm.ErrRecordNotFound) {
			remote, rerr := h.resolveRemoteOffer(in.OfferId, myNegIdx)
			if rerr == nil {
				return remote, nil
			}
			// Genuine mirror miss: fall through to the original NotFound.
			// Any other error (e.g. DB failure) must be surfaced as Internal,
			// not silently swallowed as a 404.
			if !errors.Is(rerr, gorm.ErrRecordNotFound) {
				return nil, status.Errorf(codes.Internal, "remote offer lookup failed: %v", rerr)
			}
		}
		return nil, mapOTCErr(err)
	}
	offer := h.withOfferMarketRef(o, toOTCOfferProto(o, false))
	offer.Kind = kindFromLocal(o.Local)
	offer.RoutingNumber = h.ownRouting
	offer.BankCode = h.ownBankCode
	offer.MeOwner = otcMeOwner(
		in.GetActingOwnerType(), in.GetActingOwnerId(),
		offer.Kind, sellerIDForOwner(o.InitiatorOwnerType, o.InitiatorOwnerID),
	)
	// SP-2b — caller's own (bidder) chain on this LOCAL offer. Keyed by the
	// offer's surrogate id (== parent_offer_id of a local chain). Absent for a
	// poster who never bid on their own listing (me_owner true, my_nid empty).
	stamp, haveStamp := myNegIdx.localFor(offer.GetId())
	if haveStamp {
		offer.MyNegotiationId = stamp.id
		offer.MyNegotiationStatus = stamp.status
	}
	// D2 — the listing is termless; re-source strike/premium/settlement per
	// viewer. The toOTCOfferProto projection above copied the (now zero) listing
	// terms, so clear them first, then fill from the caller's position: a BIDDER
	// sees their own chain's current terms; the OWNER sees their most recent
	// counter; anyone else sees empty strings.
	offer.StrikePrice = ""
	offer.Premium = ""
	offer.SettlementDate = ""
	switch {
	case !offer.GetMeOwner() && haveStamp:
		offer.StrikePrice = stamp.terms.StrikePrice
		offer.Premium = stamp.terms.Premium
		offer.SettlementDate = stamp.terms.SettlementDate
	case offer.GetMeOwner() && h.ownerLatestCounter != nil:
		terms, terr := h.ownerLatestCounter(offer.GetId(), in.GetActorSystemType(), uint64(in.GetActorUserId()))
		if terr != nil {
			log.Printf("WARN GetOffer: owner-latest-counter for offer %d failed (leaving terms empty): %v", offer.GetId(), terr)
		} else if terms != nil {
			offer.StrikePrice = terms.StrikePrice
			offer.Premium = terms.Premium
			offer.SettlementDate = terms.SettlementDate
		}
	}
	out := &stockpb.OTCOfferDetailResponse{
		Offer:     offer,
		Revisions: make([]*stockpb.OTCOfferRevisionItem, 0, len(revs)),
	}
	for _, r := range revs {
		out.Revisions = append(out.Revisions, &stockpb.OTCOfferRevisionItem{
			RevisionNumber: int32(r.RevisionNumber),
			Quantity:       r.Quantity.String(),
			StrikePrice:    r.StrikePrice.String(),
			Premium:        r.Premium.String(),
			SettlementDate: r.SettlementDate.Format("2006-01-02"),
			Action:         r.Action,
			ModifiedBy:     &stockpb.PartyRef{UserId: int64(r.ModifiedByPrincipalID), SystemType: r.ModifiedByPrincipalType},
			CreatedAt:      r.CreatedAt.Format(time.RFC3339),
		})
	}
	return out, nil
}

// remoteOfferToProto projects a folded-in REMOTE OTCOffer row onto the wire
// OTCOfferResponse (kind="remote", me_owner=false — remote listings are hosted
// by a peer). Currencies come from the remote-mirror columns; the seller
// display string from RemoteSellerID; bank_code from InitiatorBankCode.
// Settlement/created timestamps are emitted as RFC3339 (the peer published
// RFC3339; we store time.Time and re-render it).
func remoteOfferToProto(m *model.OTCOffer) *stockpb.OTCOfferResponse {
	bankCode := ""
	if m.InitiatorBankCode != nil {
		bankCode = *m.InitiatorBankCode
	}
	sellerID := ""
	if m.RemoteSellerID != nil {
		sellerID = *m.RemoteSellerID
	}
	return &stockpb.OTCOfferResponse{
		Id:            m.ID,
		Kind:          "remote",
		RoutingNumber: m.RoutingNumber,
		BankCode:      bankCode,
		Direction:     m.Direction,
		StockTicker:   m.Ticker,
		Quantity:      strconv.FormatInt(m.Quantity.IntPart(), 10),
		// Remote listings are termless inventory; strike/premium/settlement are
		// re-sourced per viewer by resolveRemoteOffer's D2 projection.
		StrikePrice:    "",
		Premium:        "",
		SettlementDate: "",
		Status:         m.Status,
		CreatedAt:      m.CreatedAt.UTC().Format(time.RFC3339),
		MeOwner:        false,
		Initiator: &stockpb.PartyRef{
			DisplayName: sellerID,
			BankCode:    bankCode,
		},
	}
}

// resolveRemoteOffer builds an OTCOfferDetailResponse from the folded-in
// remote OTCOffer rows for a non-local offer id. Returns gorm.ErrRecordNotFound
// when the mirror is unwired or has no such row (or the id is a local offer),
// so the caller can surface a plain 404. Remote offers carry no local revision
// chain.
func (h *OTCOptionsHandler) resolveRemoteOffer(id uint64, myNegIdx myNegotiationIndex) (*stockpb.OTCOfferDetailResponse, error) {
	if h.remoteOffers == nil {
		return nil, gorm.ErrRecordNotFound
	}
	m, err := h.remoteOffers.GetRemoteByID(id)
	if err != nil {
		return nil, err
	}
	offer := remoteOfferToProto(m)
	// D2 — a remote shell listing is termless; re-source strike/premium/
	// settlement per viewer. Remote offers are never me_owner, so only the
	// BIDDER branch applies: clear the (zero) shell terms, then fill from the
	// caller's own chain if they have one.
	offer.StrikePrice = ""
	offer.Premium = ""
	offer.SettlementDate = ""
	// SP-2b — caller's own (bidder) chain on this REMOTE offer. The chain keys
	// on the peer-hosted parent (routing, native); the remote offer row carries
	// that as (RoutingNumber, NativeID).
	if m.NativeID != nil {
		if s, ok := myNegIdx.remoteFor(m.RoutingNumber, *m.NativeID); ok {
			offer.MyNegotiationId = s.id
			offer.MyNegotiationStatus = s.status
			offer.StrikePrice = s.terms.StrikePrice
			offer.Premium = s.terms.Premium
			offer.SettlementDate = s.terms.SettlementDate
		}
	}
	return &stockpb.OTCOfferDetailResponse{
		Offer:     offer,
		Revisions: nil,
	}, nil
}

// UpdateOTCOfferQuantity sets the TOTAL quantity of an open option offer the
// caller owns (edit up or down). The acting owner is resolved from
// acting_owner_type / acting_owner_id; the service enforces owner-only + local +
// open and the committed/holding bounds, returning typed sentinels that
// passthrough to gRPC codes (ErrOTCNotOwner → PermissionDenied,
// ErrOTCOfferFieldInvalid → InvalidArgument, not-found → NotFound, opt-lock →
// Aborted).
func (h *OTCOptionsHandler) UpdateOTCOfferQuantity(ctx context.Context, in *stockpb.UpdateOTCOfferQuantityRequest) (*stockpb.OTCOfferResponse, error) {
	qty, err := decimal.NewFromString(in.GetQuantity())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "quantity is not a valid decimal")
	}
	ot, err := ownerTypeFromProto(in.GetActingOwnerType())
	if err != nil {
		return nil, err
	}
	oid, err := resolveOwnerID(ot, in.GetActingOwnerId())
	if err != nil {
		return nil, err
	}
	o, err := h.svc.UpdateQuantity(ctx, in.GetOfferId(), ot, oid, qty)
	if err != nil {
		return nil, mapOTCErr(err)
	}
	return h.withOfferMarketRef(o, toOTCOfferProto(o, false)), nil
}

// ListMyContracts returns a unified local+remote contract list (SP-1 Task 8).
// LOCAL contracts are stamped kind="local", own routing/bank-code provenance,
// and me_owner = (caller is the BUYER/HOLDER) — a formed option is the buyer's
// owned asset, so the seller/writer is never the owner (DIFFERENT from
// offers/negotiations). The caller's REMOTE (cross-bank) contracts are appended
// as kind="remote" projections with COUNTERPARTY provenance and me_owner =
// (Direction == "CREDIT", i.e. we host the buyer side).
//
// Paging: the LOCAL list is paged via the repository (page/page_size). The
// REMOTE list is fetched with the same page/page_size and APPENDED in full
// after the local page — it is not interleaved or globally re-paged. Total
// counts the local matches. Clients should rely on the per-item `kind` field
// to distinguish local vs remote; the unified Contracts[] is the single source
// of truth (the legacy peer_contracts/peer_total fields were removed in SP-2b).
func (h *OTCOptionsHandler) ListMyContracts(ctx context.Context, in *stockpb.ListMyContractsRequest) (*stockpb.ListContractsResponse, error) {
	if h.contracts == nil {
		return &stockpb.ListContractsResponse{}, nil
	}
	ownerType, ownerID := model.OwnerFromLegacy(uint64(in.ActorUserId), in.ActorSystemType)
	rows, total, err := h.contracts.ListByOwner(ownerType, ownerID, in.Role, in.Statuses, int(in.Page), int(in.PageSize))
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	out := &stockpb.ListContractsResponse{Total: total, Contracts: make([]*stockpb.OptionContractResponse, 0, len(rows))}
	for i := range rows {
		item := h.withMarketRef(&rows[i], toContractProto(&rows[i]))
		item.Kind = kindFromLocal(rows[i].Local)
		item.RoutingNumber = h.ownRouting
		item.BankCode = h.ownBankCode
		// me_owner ⇔ the caller is the contract's BUYER/HOLDER (owner of the
		// formed option asset). The seller/writer is never the owner.
		item.MeOwner = rows[i].BuyerOwnerType == ownerType && ownerIDEqual(rows[i].BuyerOwnerID, ownerID)
		out.Contracts = append(out.Contracts, item)
	}

	// Cross-bank contracts. Only fetched when the peer-contract repo is wired
	// (post-Celina-5). Two principal kinds have a cross-bank identity, mirroring
	// ListNegotiationHistory's no-leak structure (SP-3 Task 5b):
	//
	//   - CLIENT (cross-bank participant id "client-<N>"): match the exact
	//     principal via ListRemoteContractsByLocalParticipant.
	//   - BANK (an employee acting AS THE BANK; party id "employee-<N>"): the bank
	//     has no single wire principal across contracts, so match by the
	//     "employee-%" PREFIX via ListRemoteContractsByBankParty — this is the
	//     contracts-analog of the T5b negotiation read-gap and lets a bank caller
	//     SEE (and thus exercise) its cross-bank contracts. A client caller never
	//     reaches the bank lister (and vice versa).
	//
	// page/page_size pass through unchanged so the peer list paginates the same
	// way as the intra-bank list. Remote rows are mapped onto
	// OptionContractResponse (kind="remote") and APPENDED to the same Contracts
	// list so clients see one merged feed (the unified Contracts[] is the single
	// source of truth; the legacy peer_contracts/peer_total fields were removed
	// in SP-2b — SP-1 double-listing fix).
	if h.peerContracts != nil {
		var peerRows []model.OptionContract
		var perr error
		switch {
		case ownerType == model.OwnerClient && ownerID != nil:
			participantID := "client-" + strconv.FormatUint(*ownerID, 10)
			peerRows, _, perr = h.peerContracts.ListRemoteContractsByLocalParticipant(participantID, h.ownRouting, in.Role, int(in.Page), int(in.PageSize))
		case ownerType == model.OwnerBank:
			peerRows, _, perr = h.peerContracts.ListRemoteContractsByBankParty(h.ownRouting, in.Role, int(in.Page), int(in.PageSize))
		}
		if perr != nil {
			return nil, status.Errorf(codes.Internal, "list peer contracts: %v", perr)
		}
		for i := range peerRows {
			out.Contracts = append(out.Contracts, peerContractToUnifiedProto(&peerRows[i]))
		}
	}

	return out, nil
}

// peerContractToUnifiedProto maps a cross-bank (remote) OptionContract row onto
// the unified OptionContractResponse wire shape (SP-1 Task 8).
//
//   - Id is the surrogate primary key of the remote row (so callers can
//     correlate within THIS bank's namespace).
//   - kind = "remote"; routing_number + bank_code identify the COUNTERPARTY
//     peer bank — the side WE do NOT host. When RemoteDirection=="CREDIT" we
//     host the buyer, so the counterparty is the SELLER's bank; otherwise
//     (DEBIT) we host the seller, so the counterparty is the BUYER's bank. The
//     remote row already stamps its RoutingNumber as the counterparty, so we use
//     it directly.
//   - The buyer/seller routings live in the BuyerBankCode/SellerBankCode columns
//     (peer routings as strings); the participant ids live in the Remote* columns.
//   - me_owner = RemoteDirection=="CREDIT": a CREDIT row means this bank holds
//     the BUYER side, and the buyer/holder owns the formed option asset.
func peerContractToUnifiedProto(p *model.OptionContract) *stockpb.OptionContractResponse {
	direction := remoteContractDirection(p)
	meOwner := direction == "CREDIT"
	// The remote row's RoutingNumber IS the counterparty (the side we do NOT
	// host) by construction (buildRemoteContract).
	counterpartyRouting := p.RoutingNumber
	buyerRouting := remoteContractBuyerRouting(p)
	sellerRouting := remoteContractSellerRouting(p)
	return &stockpb.OptionContractResponse{
		Id:             p.ID,
		StockTicker:    p.Ticker,
		Quantity:       strconv.FormatInt(remoteContractQuantityInt(p), 10),
		StrikePrice:    p.StrikePrice.String(),
		StrikeCurrency: p.StrikeCurrency,
		SettlementDate: remoteContractSettlementString(p),
		Status:         p.Status,
		CreatedAt:      p.CreatedAt.UTC().Format(time.RFC3339),
		// Cross-bank parties carry no local user_id/system_type. Surface the
		// SI-TX participant id as the display name and the routing-derived code
		// as bank_code (the wire shape has no separate routing field on PartyRef).
		Buyer:         &stockpb.PartyRef{DisplayName: remoteContractBuyerID(p), BankCode: strconv.FormatInt(buyerRouting, 10)},
		Seller:        &stockpb.PartyRef{DisplayName: remoteContractSellerID(p), BankCode: strconv.FormatInt(sellerRouting, 10)},
		Kind:          "remote",
		RoutingNumber: counterpartyRouting,
		BankCode:      strconv.FormatInt(counterpartyRouting, 10),
		MeOwner:       meOwner,
	}
}

// GetContract resolves an option contract by id, converging local + remote in
// the service layer (SP-1 Task 8). A local OptionContract is returned with
// kind="local", own routing/bank-code provenance, and me_owner = (caller is the
// BUYER/HOLDER) — a formed option is the buyer's owned asset. When the id is not
// a local contract, it falls back to the cross-bank peer_option_contracts mirror
// and returns a kind="remote" projection (me_owner = Direction=="CREDIT"). A
// non-NotFound error from the remote lookup surfaces as Internal — it is NEVER
// masked as a 404. NotFound only when neither a local nor a remote row exists.
func (h *OTCOptionsHandler) GetContract(ctx context.Context, in *stockpb.GetContractRequest) (*stockpb.OptionContractResponse, error) {
	if h.contracts == nil {
		return nil, status.Error(codes.Unimplemented, "contracts repo not wired")
	}
	c, err := h.contracts.GetByID(in.ContractId)
	if err != nil {
		// Local contract doesn't exist — try the cross-bank mirror before 404.
		if errors.Is(err, gorm.ErrRecordNotFound) {
			remote, rerr := h.resolveRemoteContract(in.ContractId, in.ActorUserId, in.ActorSystemType)
			if rerr == nil {
				return remote, nil
			}
			// A genuine mirror miss falls through to the original NotFound. Any
			// other error (e.g. DB failure) MUST surface as Internal, not be
			// silently swallowed as a 404 (same bug we fixed for GetOffer).
			if !errors.Is(rerr, gorm.ErrRecordNotFound) {
				return nil, status.Errorf(codes.Internal, "remote contract lookup failed: %v", rerr)
			}
		}
		return nil, mapOTCErr(err)
	}
	actorOwnerType, actorOwnerID := model.OwnerFromLegacy(uint64(in.ActorUserId), in.ActorSystemType)
	isBuyer := c.BuyerOwnerType == actorOwnerType && ownerIDEqual(c.BuyerOwnerID, actorOwnerID)
	isSeller := c.SellerOwnerType == actorOwnerType && ownerIDEqual(c.SellerOwnerID, actorOwnerID)
	if !isBuyer && !isSeller {
		return nil, status.Error(codes.PermissionDenied, "not a participant")
	}
	resp := h.withMarketRef(c, toContractProto(c))
	resp.Kind = kindFromLocal(c.Local)
	resp.RoutingNumber = h.ownRouting
	resp.BankCode = h.ownBankCode
	// me_owner ⇔ the caller is the contract's BUYER/HOLDER (owner of the formed
	// option asset). The seller/writer is never the owner.
	resp.MeOwner = isBuyer
	return resp, nil
}

// resolveRemoteContract builds an OptionContractResponse from the cross-bank
// peer_option_contracts mirror for a non-local contract id. It enforces a
// participant gate before returning: the caller must be the LOCAL party of the
// contract — i.e. the side whose routing number equals ownRouting. For a CREDIT
// row this bank hosts the buyer, so the caller's SI-TX participant id must equal
// BuyerID; for a DEBIT row this bank hosts the seller, so it must equal SellerID.
// A non-participant gets NotFound (existence must not leak — mirror the local path
// which returns PermissionDenied, but the remote path hides even existence).
// Returns gorm.ErrRecordNotFound when the mirror is unwired or has no such row,
// so the caller can surface a plain 404; any other error propagates so the caller
// can surface Internal.
func (h *OTCOptionsHandler) resolveRemoteContract(id uint64, actorUserID int64, actorSystemType string) (*stockpb.OptionContractResponse, error) {
	if h.peerContracts == nil {
		return nil, gorm.ErrRecordNotFound
	}
	p, err := h.peerContracts.GetRemoteContractByID(id)
	if err != nil {
		return nil, err
	}

	// Determine which side this bank hosts: CREDIT → we hold the buyer;
	// DEBIT → we hold the seller. The local participant's SI-TX id is
	// "client-<actorUserID>" for client callers (cross-bank participant ids
	// always use the "client-N" prefix). Employee callers have no cross-bank
	// participant id and are therefore never the local participant of a remote
	// contract — they receive NotFound.
	//
	// Mirror the local path's identity source: actorUserID + actorSystemType
	// (same fields used by GetContract's local ownership check above).
	var localPartyID string
	if actorSystemType == "client" {
		localPartyID = "client-" + strconv.FormatInt(actorUserID, 10)
	}
	// "" means the caller has no SI-TX identity → never a participant.

	var localContractPartyID string
	if remoteContractDirection(p) == "CREDIT" {
		// This bank hosts the BUYER side.
		localContractPartyID = remoteContractBuyerID(p)
	} else {
		// This bank hosts the SELLER side.
		localContractPartyID = remoteContractSellerID(p)
	}

	if localPartyID == "" || localPartyID != localContractPartyID {
		// Return NotFound — do not leak existence to non-parties (same
		// policy as enforceOwnership in the gateway layer).
		return nil, gorm.ErrRecordNotFound
	}

	return peerContractToUnifiedProto(p), nil
}

// ExerciseContract is the UNIFIED exercise entry point: it dispatches LOCAL vs
// cross-bank (REMOTE) on the contract's routing so the frontend uses ONE route
// (POST /api/v3/otc/contracts/:id/exercise) regardless of kind (SP-2b Task 5).
//
//   - LOCAL (routing == OwnRouting()) → the existing local exercise saga
//     (svc.ExerciseContract). Unchanged. Accounts come from the persisted
//     contract; buyer_account_number is ignored on this path.
//   - REMOTE (routing != OwnRouting(), a peer-hosted contract this bank holds
//     the BUYER side of) → the cross-bank SI-TX exercise dispatch, folding in
//     the logic that previously lived behind the gateway's peer-exercise route
//     (InitiateOptionExercise). The caller must be the contract's BUYER/HOLDER;
//     a non-holder gets NotFound (existence must not leak). The buyer's
//     settlement account (buyer_account_number) is the only client-supplied
//     resource — the counterparty/writer + the contract terms come from the
//     persisted remote row.
//
// Local + remote share the OptionContract table; the dispatch is a single
// GetByID-then-branch-on-routing. GetByID returns NotFound for a remote row (it
// is OwnRouting()-guarded), so a NotFound triggers the remote lookup.
func (h *OTCOptionsHandler) ExerciseContract(ctx context.Context, in *stockpb.ExerciseContractRequest) (*stockpb.ExerciseResponse, error) {
	if h.contracts != nil {
		// Resolve the contract to decide local vs cross-bank. A LOCAL hit takes
		// the existing saga path below; a NotFound means the id is either remote
		// (handled here) or genuinely missing (falls through to the local saga's
		// own NotFound, preserving the existing error semantics).
		if _, err := h.contracts.GetByID(in.GetContractId()); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if resp, handled, rerr := h.exerciseRemoteContract(ctx, in); handled {
					return resp, rerr
				}
				// Not a remote contract (no remote row / exerciser unwired) — fall
				// through so the local saga surfaces the canonical NotFound.
			} else {
				return nil, status.Errorf(codes.Internal, "load contract: %v", err)
			}
		}
	}

	// E2: on_behalf_of_fund_id validation for exercise — manager-only check.
	onBehalfOfFundID := in.GetOnBehalfOfFundId()
	if onBehalfOfFundID != 0 {
		if h.fundRepo == nil {
			return nil, status.Error(codes.FailedPrecondition, "fund support not configured on OTC handler")
		}
		fund, ferr := h.fundRepo.GetByID(onBehalfOfFundID)
		if ferr != nil {
			return nil, status.Errorf(codes.NotFound, "fund %d not found", onBehalfOfFundID)
		}
		// Derive acting employee ID from the legacy (user_id, system_type) pair.
		// For fund exercise, the actor must be the fund manager.
		actingEmpID := in.GetActorUserId()
		if in.GetActorSystemType() != "employee" {
			return nil, status.Error(codes.PermissionDenied, "fund exercise requires employee actor")
		}
		if actingEmpID != fund.ManagerEmployeeID {
			return nil, status.Error(codes.PermissionDenied, "fund_not_managed_by_actor")
		}
	}

	c, err := h.svc.ExerciseContract(ctx, service.ExerciseInput{
		ContractID: in.ContractId, ActorUserID: in.ActorUserId, ActorSystemType: in.ActorSystemType,
		OnBehalfOfFundID: onBehalfOfFundID,
	})
	if err != nil {
		return nil, mapOTCErr(err)
	}
	// The saga doesn't return per-currency amounts — surface the seller-side
	// figure here. Cross-currency callers can compute the buyer-side
	// amount client-side via the exchange rate they observed.
	strikeAmt := c.Quantity.Mul(c.StrikePrice)
	return &stockpb.ExerciseResponse{
		ContractId:            c.ID,
		Status:                c.Status,
		SagaId:                c.SagaID,
		StrikeAmountSellerCcy: strikeAmt.String(),
		StrikeAmountBuyerCcy:  strikeAmt.String(),
		SellerCurrency:        c.StrikeCurrency,
		BuyerCurrency:         c.StrikeCurrency,
		SharesTransferred:     c.Quantity.String(),
	}, nil
}

// exerciseRemoteContract handles the REMOTE branch of the unified
// ExerciseContract (SP-2b Task 5). It resolves the cross-bank contract by its
// surrogate id, AUTHORIZES the caller as the contract's BUYER/HOLDER, then
// delegates to the cross-bank SI-TX exercise dispatch (CrossBankExerciser,
// satisfied by *PeerOTCGRPCHandler.InitiateOptionExercise — the same logic the
// retiring gateway peer-exercise route called directly).
//
// Returns (resp, handled, err):
//   - handled == false  → the id is NOT a remote contract this handler can
//     drive (the peer-contract repo or the exerciser isn't wired, or no remote
//     row exists). The caller falls through to the local saga's NotFound so the
//     error semantics for a genuinely-missing id are unchanged.
//   - handled == true   → this is a remote contract; resp/err carry the outcome
//     (including the holder-authorization NotFound for a non-buyer caller).
//
// Authorization mirrors resolveRemoteContract + the old ExercisePeerContract:
// only the BUYER/HOLDER side this bank hosts may exercise. A REMOTE contract is
// exercisable from this bank only when it carries RemoteDirection=="CREDIT"
// (this bank holds the buyer/holder side). The buyer party id is read from the
// ROW; the caller is authorized as that party by identity:
//
//   - CLIENT buyer (RemoteBuyerID "client-<N>") → the caller's SI-TX participant
//     id "client-<actorUserId>" (client actors only) must equal RemoteBuyerID.
//   - BANK buyer (RemoteBuyerID "employee-<N>") → the caller must be acting AS
//     THE BANK (actor_system_type "bank", on_behalf_of_client_id == 0). The
//     strike settles from the bank's bound account (buyer_account_number,
//     gateway-validated as a bank account) and the holding owner resolves to
//     (bank, nil) via the inbound parser (SP-3 Task 3). ANY employee may drive
//     it (the originating employee is not re-derived here).
//
// A non-buyer/non-holder (the writer, another client, an employee on behalf of a
// client, or a client on a bank-buyer contract) gets NotFound — existence must
// not leak.
func (h *OTCOptionsHandler) exerciseRemoteContract(ctx context.Context, in *stockpb.ExerciseContractRequest) (*stockpb.ExerciseResponse, bool, error) {
	if h.peerContracts == nil || h.crossBankExerciser == nil {
		return nil, false, nil
	}
	contract, err := h.peerContracts.GetRemoteContractByID(in.GetContractId())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Not a remote row either — let the caller surface the local NotFound.
			return nil, false, nil
		}
		return nil, true, status.Errorf(codes.Internal, "load remote contract: %v", err)
	}

	// AUTHORIZE: only the buyer/holder side this bank hosts (the CREDIT side) may
	// exercise; the seller/DEBIT side never does (the writer never exercises).
	buyerID := remoteContractBuyerID(contract)
	authorized := false
	if remoteContractDirection(contract) == "CREDIT" {
		if isBankWireID(buyerID) {
			// Bank-hosted buyer — authorize a caller acting as the bank.
			authorized = in.GetActorSystemType() == "bank" && in.GetOnBehalfOfClientId() == 0
		} else {
			// Client-hosted buyer — authorize the matching client principal.
			authorized = in.GetActorSystemType() == "client" &&
				buyerID == "client-"+strconv.FormatInt(in.GetActorUserId(), 10)
		}
	}
	if !authorized {
		// NotFound — do not leak existence to non-holders (same policy as
		// resolveRemoteContract / the gateway's enforceOwnership).
		return nil, true, status.Error(codes.NotFound, "not_found")
	}

	// The buyer's settlement account is the only client-supplied resource; the
	// gateway has already validated the caller is entitled to it. Everything else
	// (counterparty, terms, routings) comes from the persisted remote row inside
	// InitiateOptionExercise.
	if in.GetBuyerAccountNumber() == "" {
		return nil, true, status.Error(codes.InvalidArgument, "buyer_account_number is required to exercise a cross-bank contract")
	}

	// DEFENSE-IN-DEPTH RE-ASSERT (SP-3 Task 5 security gate): the buyer account
	// number flows straight into InitiateOptionExercise as the strike-DEBIT
	// posting (peer_otc_grpc_handler.go) with no further ownership check — Reserve
	// only verifies active + currency. So if the gateway gate were ever bypassed,
	// a bank exercise could debit the strike from ANY account of the matching
	// currency, including a client's. Re-assert the same predicates the bid path
	// uses (openRemoteNegotiation), branching on the buyer party recorded on the
	// row, BEFORE dispatch. On failure → NotFound (no-leak policy used throughout
	// this function), and DO NOT dispatch.
	if h.accounts == nil {
		return nil, true, status.Error(codes.FailedPrecondition, "account-service client not wired for cross-bank exercise")
	}
	acct, gerr := h.accounts.GetAccountByNumber(ctx, &accountpb.GetAccountByNumberRequest{AccountNumber: in.GetBuyerAccountNumber()})
	if gerr != nil {
		return nil, true, status.Error(codes.NotFound, "not_found")
	}
	if isBankWireID(buyerID) {
		// BANK buyer — the strike must settle from a BANK account (account_kind
		// "bank" or the legacy owner sentinel), never a client's.
		if !isBankAccount(acct) {
			return nil, true, status.Error(codes.NotFound, "not_found")
		}
	} else {
		// CLIENT buyer ("client-<X>") — the strike account must be owned by that
		// buyer client. Belt-and-suspenders to the gateway gate.
		buyerClientID, perr := strconv.ParseUint(strings.TrimPrefix(buyerID, "client-"), 10, 64)
		if perr != nil || acct.GetOwnerId() != buyerClientID {
			return nil, true, status.Error(codes.NotFound, "not_found")
		}
	}
	if acct.GetStatus() != "active" {
		return nil, true, status.Error(codes.FailedPrecondition, "buyer account is not active")
	}
	if acct.GetCurrencyCode() != contract.StrikeCurrency {
		return nil, true, status.Errorf(codes.InvalidArgument,
			"currency mismatch: account is %s but the contract strike is %s",
			acct.GetCurrencyCode(), contract.StrikeCurrency)
	}

	resp, err := h.crossBankExerciser.InitiateOptionExercise(ctx, &stockpb.InitiateOptionExerciseRequest{
		PeerOptionContractId: contract.ID,
		BuyerAccountNumber:   in.GetBuyerAccountNumber(),
	})
	if err != nil {
		return nil, true, err
	}
	// Project the cross-bank dispatch result onto the unified ExerciseResponse.
	// The SI-TX exercise settles asynchronously, so per-currency strike/shares
	// figures aren't known synchronously; the transaction id rides in SagaId
	// (the cross-bank correlation handle the FE polls), mirroring how the old
	// peer-exercise route surfaced transaction_id + status.
	strikeAmt := contract.StrikePrice.Mul(decimal.NewFromInt(remoteContractQuantityInt(contract)))
	return &stockpb.ExerciseResponse{
		ContractId:            contract.ID,
		Status:                resp.GetStatus(),
		SagaId:                resp.GetTransactionId(),
		StrikeAmountSellerCcy: strikeAmt.String(),
		StrikeAmountBuyerCcy:  strikeAmt.String(),
		SellerCurrency:        contract.StrikeCurrency,
		BuyerCurrency:         contract.StrikeCurrency,
		SharesTransferred:     strconv.FormatInt(remoteContractQuantityInt(contract), 10),
	}, true, nil
}

func toContractProto(c *model.OptionContract) *stockpb.OptionContractResponse {
	resp := &stockpb.OptionContractResponse{
		Id:              c.ID,
		OfferId:         derefU64(c.OfferID),
		StockId:         c.StockID,
		Quantity:        c.Quantity.String(),
		StrikePrice:     c.StrikePrice.String(),
		PremiumPaid:     c.PremiumPaid.String(),
		PremiumCurrency: c.PremiumCurrency,
		StrikeCurrency:  c.StrikeCurrency,
		SettlementDate:  c.SettlementDate.Format("2006-01-02"),
		Status:          c.Status,
		Buyer:           &stockpb.PartyRef{UserId: int64(model.OwnerIDOrZero(c.BuyerOwnerID)), SystemType: string(c.BuyerOwnerType)},
		Seller:          &stockpb.PartyRef{UserId: int64(model.OwnerIDOrZero(c.SellerOwnerID)), SystemType: string(c.SellerOwnerType)},
		PremiumPaidAt:   c.PremiumPaidAt.Format(time.RFC3339),
		CreatedAt:       c.CreatedAt.Format(time.RFC3339),
		UpdatedAt:       c.UpdatedAt.Format(time.RFC3339),
		Version:         c.Version,
	}
	if c.ExercisedAt != nil {
		resp.ExercisedAt = c.ExercisedAt.Format(time.RFC3339)
	}
	if c.ExpiredAt != nil {
		resp.ExpiredAt = c.ExpiredAt.Format(time.RFC3339)
	}
	return resp
}

// withMarketRef returns a fresh response with MarketReferencePrice
// populated. Caller wraps a base proto so the toContractProto helper can
// stay simple and free of repo dependencies.
func (h *OTCOptionsHandler) withMarketRef(c *model.OptionContract, resp *stockpb.OptionContractResponse) *stockpb.OptionContractResponse {
	resp.MarketReferencePrice = h.marketRefPrice(c.StockID)
	return resp
}

// withOfferMarketRef populates MarketReferencePrice on an offer response.
func (h *OTCOptionsHandler) withOfferMarketRef(o *model.OTCOffer, resp *stockpb.OTCOfferResponse) *stockpb.OTCOfferResponse {
	resp.MarketReferencePrice = h.marketRefPrice(o.StockID)
	return resp
}

func toOTCOfferProto(o *model.OTCOffer, unread bool) *stockpb.OTCOfferResponse {
	resp := &stockpb.OTCOfferResponse{
		Id:        o.ID,
		Direction: o.Direction,
		StockId:   o.StockID,
		Quantity:  o.Quantity.String(),
		// Option offers are termless inventory: strike/premium/settlement live
		// on the negotiation chain, not the listing. Emit empty term fields here;
		// the per-viewer terms are projected separately (otc_handler.go / the
		// viewer-projection path), mirroring the otccache.OptionOffer DTO which
		// also leaves these blank for the listing.
		StrikePrice:    "",
		Premium:        "",
		SettlementDate: "",
		Status:         o.Status,
		Initiator: &stockpb.PartyRef{
			UserId: int64(model.OwnerIDOrZero(o.InitiatorOwnerID)), SystemType: string(o.InitiatorOwnerType),
		},
		LastModifiedBy: &stockpb.PartyRef{
			UserId: int64(o.LastModifiedByPrincipalID), SystemType: o.LastModifiedByPrincipalType,
		},
		CreatedAt: o.CreatedAt.Format(time.RFC3339),
		UpdatedAt: o.UpdatedAt.Format(time.RFC3339),
		Version:   o.Version,
		Unread:    unread,
		// LOCAL read view's SI-TX seller id ("bank" | "client-<N>"), stamped
		// uniformly on every single-offer response (create/detail/counter/
		// cancel) so it matches the unified marketplace listing. The cross-bank
		// wire id "employee-<N>" is composed only on the SI-TX publish path.
		SellerId: sellerIDForOwner(o.InitiatorOwnerType, o.InitiatorOwnerID),
	}
	if o.CounterpartyOwnerType != nil {
		resp.Counterparty = &stockpb.PartyRef{
			UserId:     int64(model.OwnerIDOrZero(o.CounterpartyOwnerID)),
			SystemType: string(*o.CounterpartyOwnerType),
		}
	}
	return resp
}

// ownerIDEqual reports whether two nullable owner-id pointers reference the
// same logical owner. Both nil = same (bank == bank). Mirror of the helper in
// internal/service.
func ownerIDEqual(a, b *uint64) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// mapOTCErr is now a passthrough. Service-layer sentinels (and the typed
// repository.ErrOptimisticLock sentinel) carry their own gRPC code via
// svcerr.SentinelError. The bare gorm.ErrRecordNotFound branch remains
// because some legacy paths still surface the raw GORM error.
func mapOTCErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return status.Error(codes.NotFound, "not_found")
	}
	return err
}

// --- OTC trader rating RPCs (Celina 3) -----------------------------------

func (h *OTCOptionsHandler) SubmitRating(ctx context.Context, in *stockpb.SubmitOTCRatingRequest) (*stockpb.OTCRatingResponse, error) {
	if h.ratings == nil {
		return nil, status.Error(codes.Unimplemented, "ratings service not wired")
	}
	rt := model.OwnerType(in.RaterOwnerType)
	if !rt.Valid() {
		return nil, status.Errorf(codes.InvalidArgument, "invalid rater_owner_type %q", in.RaterOwnerType)
	}
	var rid *uint64
	if rt != model.OwnerBank {
		if in.RaterOwnerId == 0 {
			return nil, status.Error(codes.InvalidArgument, "rater_owner_id required for non-bank")
		}
		id := in.RaterOwnerId
		rid = &id
	}
	if in.Score < 1 || in.Score > 5 {
		return nil, status.Error(codes.InvalidArgument, "score must be 1..5")
	}
	row, err := h.ratings.Submit(service.SubmitInput{
		OfferID:        in.OfferId,
		RaterOwnerType: rt,
		RaterOwnerID:   rid,
		Score:          int(in.Score),
		Comment:        in.Comment,
	})
	if err != nil {
		return nil, err
	}
	return ratingToProto(row), nil
}

func (h *OTCOptionsHandler) GetTraderProfile(ctx context.Context, in *stockpb.GetTraderProfileRequest) (*stockpb.TraderProfileResponse, error) {
	if h.ratings == nil {
		return nil, status.Error(codes.Unimplemented, "ratings service not wired")
	}
	rt := model.OwnerType(in.OwnerType)
	if !rt.Valid() {
		return nil, status.Errorf(codes.InvalidArgument, "invalid owner_type %q", in.OwnerType)
	}
	var rid *uint64
	if rt != model.OwnerBank {
		if in.OwnerId == 0 {
			return nil, status.Error(codes.InvalidArgument, "owner_id required for non-bank")
		}
		id := in.OwnerId
		rid = &id
	}
	profile, err := h.ratings.GetProfile(rt, rid, int(in.RecentLimit))
	if err != nil {
		return nil, err
	}
	out := &stockpb.TraderProfileResponse{
		OwnerType: string(profile.OwnerType),
		OwnerId:   ownerIDOr0(profile.OwnerID),
		Average:   profile.Avg,
		Count:     profile.Count,
		Recent:    make([]*stockpb.OTCRatingResponse, 0, len(profile.Recent)),
	}
	for i := range profile.Recent {
		out.Recent = append(out.Recent, ratingToProto(&profile.Recent[i]))
	}
	return out, nil
}

func (h *OTCOptionsHandler) ListReceivedRatings(ctx context.Context, in *stockpb.ListReceivedRatingsRequest) (*stockpb.ListOTCRatingsResponse, error) {
	if h.ratings == nil {
		return nil, status.Error(codes.Unimplemented, "ratings service not wired")
	}
	rt := model.OwnerType(in.OwnerType)
	if !rt.Valid() {
		return nil, status.Errorf(codes.InvalidArgument, "invalid owner_type %q", in.OwnerType)
	}
	var rid *uint64
	if rt != model.OwnerBank {
		if in.OwnerId == 0 {
			return nil, status.Error(codes.InvalidArgument, "owner_id required for non-bank")
		}
		id := in.OwnerId
		rid = &id
	}
	rows, err := h.ratings.ListReceived(rt, rid, int(in.Limit))
	if err != nil {
		return nil, err
	}
	out := &stockpb.ListOTCRatingsResponse{Ratings: make([]*stockpb.OTCRatingResponse, 0, len(rows))}
	for i := range rows {
		out.Ratings = append(out.Ratings, ratingToProto(&rows[i]))
	}
	return out, nil
}

func ratingToProto(r *model.OTCTraderRating) *stockpb.OTCRatingResponse {
	return &stockpb.OTCRatingResponse{
		Id:             r.ID,
		OfferId:        r.OfferID,
		RaterOwnerType: string(r.RaterOwnerType),
		RaterOwnerId:   ownerIDOr0(r.RaterOwnerID),
		RatedOwnerType: string(r.RatedOwnerType),
		RatedOwnerId:   ownerIDOr0(r.RatedOwnerID),
		Score:          int32(r.Score),
		Comment:        r.Comment,
		CreatedAtUnix:  r.CreatedAt.Unix(),
	}
}

func ownerIDOr0(p *uint64) uint64 {
	if p == nil {
		return 0
	}
	return *p
}
