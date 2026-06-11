package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	contractkafka "github.com/exbanka/contract/kafka"
	contractsitx "github.com/exbanka/contract/sitx"
	stockpb "github.com/exbanka/contract/stockpb"
	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
	"github.com/exbanka/stock-service/internal/service"
)

// PeerNotifier is the producer dependency for cross-bank inbound
// notification intents. Each bank notifies ONLY its own local users
// — the SI-TX protocol propagates state changes between banks, and
// each side's handler emits its own notification independently.
type PeerNotifier interface {
	PublishGeneralNotification(ctx context.Context, msg contractkafka.GeneralNotificationMessage) error
}

// HoldingReader is the subset of HoldingRepository methods that
// PeerOTCGRPCHandler needs. Decoupled for testability.
//   - GetByOwnerAndTicker backs CheckSellerCanDeliver.
//
// GetPublicStocks no longer reads holdings — it serves our open option offers
// via OTCOfferReader.ListPublicOptionOffersForPeer (Task C1).
type HoldingReader interface {
	GetByOwnerAndTicker(ownerType model.OwnerType, ownerID *uint64, securityType, ticker string) (*model.Holding, error)
}

// LocalSellerValidator answers "does this client-<n> participant id resolve to a
// real client on THIS bank?" for inbound cross-bank negotiations. Only the
// client-<n> form is ever passed in — bank/employee-<n> sellers are validated
// structurally by the handler (the bank always exists) before the validator is
// consulted. Wired in production against client-service GetClient; left nil in
// tests / legacy mode (then existence is not enforced at create time).
//
// It closes the phantom-row loophole: without it, a raw peer could POST
// /cross-bank-protocol/negotiations with sellerId.id="client-<bogus>" (correct
// routing, non-existent client) and the handler would persist an inert junk row
// (HTTP 201) instead of returning a clean 4xx — an unbounded resource-pollution
// vector. SellerExists==false ⇒ NotFound, no row persisted.
type LocalSellerValidator interface {
	SellerExists(ctx context.Context, participantID string) bool
}

// LocalParentChecker answers "is the LOCAL parent listing with this offer id
// still open?" for the inbound orphan-accept guard. Satisfied in production by
// *service.OTCNegotiationService (LocalParentIsOpen). Optional — nil disables
// the check (legacy/test mode where no parent-listing status is available).
//
// It closes the inbound orphan-accept hole: when WE host the listing
// (remote_parent_routing == ownRouting), an inbound AcceptNegotiation must be
// rejected if the local parent listing has been cancelled/consumed —
// authoritatively, regardless of the best-effort cascade timing.
type LocalParentChecker interface {
	LocalParentIsOpen(offerID uint64) bool
	// LocalSellOfferOpenForSeller is the TERMLESS analogue of LocalParentIsOpen:
	// the /public-stock wire carries no offer id, so a termless bid's parent
	// native id is "ps:<…>" (non-numeric) or absent and the numeric orphan guard
	// above is skipped. When WE host the seller, the inbound accept resolves the
	// listing by its (owner, ticker, sell_initiated) unique key to reject an accept
	// on a listing already consumed (a prior accept) or cancelled.
	LocalSellOfferOpenForSeller(ownerType model.OwnerType, ownerID *uint64, ticker string) bool
	// ConsumeLocalSellOfferForSeller flips that listing to consumed once an inbound
	// accept forms a contract against it — one listing backs exactly one accepted
	// contract (symmetric with the outbound acceptRemoteNegotiation consume).
	ConsumeLocalSellOfferForSeller(ownerType model.OwnerType, ownerID *uint64, ticker string) error
}

// SellerAccountResolver returns the seller's NOMINATED account NUMBER for a
// cross-bank negotiation WE host the seller side of, so the seller-credit legs
// we compose target that exact account (spec §2.6 TxAccount.ACCOUNT{num})
// instead of being resolved loosely to "the seller's first active account in
// the currency" on our own posting executor.
//
// The nominated account is the local parent listing's InitiatorAccountID (the
// account the seller bound at offer creation — it RECEIVES the premium on a
// sell_initiated offer, mirroring the local accept saga's
// sellerAccountID = offer.InitiatorAccountID). Returns "" when no nomination is
// available (free-form negotiation with no local parent listing, an unbound
// account, or an account that fails the active/owner/currency checks) — the
// caller then falls back to the participant id (the documented first-active
// path). Optional — nil disables the pin (legacy/test mode keeps the prior
// participant-id behaviour).
type SellerAccountResolver interface {
	ResolveSellerAccountNumber(ctx context.Context, neg *model.OTCNegotiation, premiumCurrency string) string
}

// PeerOTCGRPCHandler implements stockpb.PeerOTCServiceServer.
//
// GetPublicStocks serves our OPEN, sell-initiated, public, non-private, LOCAL
// option offers (the optionable inventory peers negotiate options off) as
// PeerPublicStock entries — one seller entry per (owner, ticker).
//
// Negotiation lifecycle: peers POST/PUT/GET/DELETE on
// /negotiations/{rid}/{id}; we persist in peer_otc_negotiations.
//
// Acceptance: GET /negotiations/{rid}/{id}/accept composes 4 postings
// (premium money + 1× OptionDescription both directions) and dispatches
// via transaction-service.PeerTxService.InitiateOutboundTxWithPostings.
// HoldingReserver is the subset of HoldingReservationService used by
// RecordOptionContract / RecordOptionExercise to manage the seller's
// underlying share lock and the buyer's gained holding when SI-TX
// flows touch this bank.
type HoldingReserver interface {
	ReserveForPeerOptionContract(
		ctx context.Context,
		sellerOwnerType model.OwnerType,
		sellerOwnerID *uint64,
		securityType, ticker string,
		peerOptionContractID uint64,
		qty int64,
	) (*service.ReserveHoldingResult, error)
	// ReserveForCrossBankNewTx holds shares at NEW_TX time keyed on the SI-TX
	// identity (crossbank_tx_id), before the contract row exists.
	ReserveForCrossBankNewTx(
		ctx context.Context,
		sellerOwnerType model.OwnerType,
		sellerOwnerID *uint64,
		securityType, ticker, crossbankTxID string,
		qty int64,
	) (*service.ReserveHoldingResult, error)
	// AttachCrossBankReservationToContract links the vote-time hold to the
	// contract minted at COMMIT_TX. Returns NotFound if no vote-time hold
	// exists (caller falls back to ReserveForPeerOptionContract).
	AttachCrossBankReservationToContract(ctx context.Context, crossbankTxID string, peerOptionContractID uint64) error
	// ReleaseForCrossBankNewTx releases a vote-time hold on ROLLBACK.
	ReleaseForCrossBankNewTx(ctx context.Context, crossbankTxID string) (*service.ReleaseHoldingResult, error)
	ConsumeForPeerOptionContract(
		ctx context.Context,
		peerOptionContractID uint64,
		qty int64,
	) (*service.PartialSettleHoldingResult, error)
	ExerciseBuyerCreditForPeerOption(
		ctx context.Context,
		peerOptionContractID uint64,
		ownerType model.OwnerType,
		ownerID *uint64,
		ticker string,
		qty int64,
		strikePrice decimal.Decimal,
	) error
}

type PeerOTCGRPCHandler struct {
	stockpb.UnimplementedPeerOTCServiceServer
	negRepo         *repository.OTCNegotiationRepository
	peerOptionRepo  *repository.OptionContractRepository
	holdings        HoldingReader
	peerTx          transactionpb.PeerTxServiceClient
	ownRouting      int64
	holdingReserver HoldingReserver // optional; nil disables seller-side share locking

	// Open OTC option offers backing the cross-bank /public-stock catalog.
	// Wired via WithOTCOfferReader. When nil, GetPublicStocks returns
	// Unimplemented instead of nil-deref.
	otcOffers OTCOfferReader

	// Optional in-app notification producer. nil ⇒ silent (legacy mode).
	notifier PeerNotifier

	// capitalGainRepo records the seller's realised P/L on cross-bank
	// exercise (DEBIT direction). Optional — nil falls back to the
	// pre-fix degraded mode where no CG is written. Wired via
	// WithCapitalGain.
	capitalGainRepo PeerCapitalGainRepo

	// sellerValidator gates inbound CreateNegotiation on the seller actually
	// existing locally (client-<n> only). Optional — nil disables the check
	// (legacy/test mode). Wired via WithSellerValidator. Closes the
	// phantom-seller row loophole.
	sellerValidator LocalSellerValidator

	// parentChecker gates inbound AcceptNegotiation on the LOCAL parent listing
	// still being open (when WE host the listing). Optional — nil disables the
	// check. Wired via WithParentChecker. Closes the inbound orphan-accept hole.
	parentChecker LocalParentChecker

	// sellerAccountResolver resolves the seller's nominated account number so the
	// seller-credit legs we compose target a concrete account (ACCOUNT{num})
	// rather than the participant id (resolved first-active). Optional — nil keeps
	// the prior participant-id behaviour. Wired via WithSellerAccountResolver.
	sellerAccountResolver SellerAccountResolver
}

// PeerCapitalGainRepo is the narrow surface PeerOTCGRPCHandler uses to
// persist a CapitalGain on cross-bank exercise — satisfied by
// *repository.CapitalGainRepository.
type PeerCapitalGainRepo interface {
	Create(gain *model.CapitalGain) error
}

// WithCapitalGain wires the repository that records seller-side P/L on
// cross-bank exercise. Returns the handler for chaining.
func (h *PeerOTCGRPCHandler) WithCapitalGain(repo PeerCapitalGainRepo) *PeerOTCGRPCHandler {
	h.capitalGainRepo = repo
	return h
}

// WithSellerValidator wires the local-seller existence check consulted by
// CreateNegotiation (closes the phantom-row loophole). Returns the handler for
// chaining. nil leaves the check disabled (legacy/test mode).
func (h *PeerOTCGRPCHandler) WithSellerValidator(v LocalSellerValidator) *PeerOTCGRPCHandler {
	h.sellerValidator = v
	return h
}

// WithParentChecker wires the local-parent-open check consulted by inbound
// AcceptNegotiation (closes the inbound orphan-accept hole). Returns the handler
// for chaining. nil leaves the check disabled (legacy/test mode).
func (h *PeerOTCGRPCHandler) WithParentChecker(c LocalParentChecker) *PeerOTCGRPCHandler {
	h.parentChecker = c
	return h
}

// WithSellerAccountResolver wires the seller-nominated-account resolver consulted
// by AcceptNegotiation (and the COMMIT-time RecordOptionContract) so the
// seller-credit legs target the bound account (ACCOUNT{num}) instead of the
// loosely-resolved participant id. Returns the handler for chaining. nil leaves
// the prior participant-id behaviour in place (legacy/test mode).
func (h *PeerOTCGRPCHandler) WithSellerAccountResolver(r SellerAccountResolver) *PeerOTCGRPCHandler {
	h.sellerAccountResolver = r
	return h
}

// WithNotifier wires the in-app notification producer. Returns the
// handler for chaining.
func (h *PeerOTCGRPCHandler) WithNotifier(n PeerNotifier) *PeerOTCGRPCHandler {
	h.notifier = n
	return h
}

// localClientUserID resolves "client-N" → N when the row's matching
// routing number is this bank's. Returns (0, false) when:
//   - the row is for the other bank's user (no local notification)
//   - the participant id isn't a plain "client-N" string (employee,
//     bank, malformed)
//
// Used by the inbound peer handlers to determine whether to publish
// a notification, and to whom.
func (h *PeerOTCGRPCHandler) localClientUserID(routing int64, participantID string) (uint64, bool) {
	if routing != h.ownRouting {
		return 0, false
	}
	const prefix = "client-"
	if !strings.HasPrefix(participantID, prefix) {
		return 0, false
	}
	id, err := strconv.ParseUint(participantID[len(prefix):], 10, 64)
	if err != nil || id == 0 {
		return 0, false
	}
	return id, true
}

// publishPeerNotif is best-effort. Logs a warning on failure. Skips
// when the notifier isn't wired or the recipient resolution failed.
func (h *PeerOTCGRPCHandler) publishPeerNotif(
	ctx context.Context,
	userID uint64,
	notifType string,
	data map[string]string,
	refType string,
	refID uint64,
) {
	if h.notifier == nil || userID == 0 {
		return
	}
	if err := h.notifier.PublishGeneralNotification(ctx, contractkafka.GeneralNotificationMessage{
		UserID:  userID,
		Type:    notifType,
		Data:    data,
		RefType: refType,
		RefID:   refID,
	}); err != nil {
		log.Printf("WARN: peer otc notif %s for user %d failed: %v", notifType, userID, err)
	}
}

// notifDataFromOffer extracts the ticker/strike/premium fields from
// a decoded sitx.OtcOffer, defensive against partially-populated rows.
func notifDataFromOffer(offer contractsitx.OtcOffer) map[string]string {
	return map[string]string{
		"ticker":       offer.Ticker,
		"quantity":     strconv.FormatInt(offer.Amount, 10),
		"strike_price": offer.PricePerStock.String(),
		"premium":      offer.Premium.String(),
	}
}

// OTCOfferReader is the narrow interface the peer endpoint uses to
// read open OTC listings. *OTCOfferRepository satisfies it.
//   - ListPublicOptionOffersForPeer backs GetPublicStocks (/public-stock) —
//     our open sell-initiated public local option offers (the optionable
//     inventory peers negotiate options off).
//   - ListOpenForCache is retained for the option-offer cache refresher
//     (otccache), which consumes the same repository.
type OTCOfferReader interface {
	ListOpenForCache(limit int) ([]model.OTCOffer, error)
	ListPublicOptionOffersForPeer() ([]model.OTCOffer, error)
}

func NewPeerOTCGRPCHandler(
	negRepo *repository.OTCNegotiationRepository,
	peerOptionRepo *repository.OptionContractRepository,
	holdings HoldingReader,
	peerTx transactionpb.PeerTxServiceClient,
	ownRouting int64,
) *PeerOTCGRPCHandler {
	return &PeerOTCGRPCHandler{
		negRepo:        negRepo,
		peerOptionRepo: peerOptionRepo,
		holdings:       holdings,
		peerTx:         peerTx,
		ownRouting:     ownRouting,
	}
}

// SetHoldingReserver wires the seller-side share-locking dependency.
// Optional — left nil, RecordOptionContract still persists the
// peer_option_contracts row but does not lock the seller's holdings.
// (Useful for tests and for stages where the reserver isn't ready.)
func (h *PeerOTCGRPCHandler) SetHoldingReserver(r HoldingReserver) {
	h.holdingReserver = r
}

// WithOTCOfferReader wires the cross-bank option-discovery data source
// backing the /public-stock catalog. Returns a copy so the caller can
// chain wire-up calls.
func (h *PeerOTCGRPCHandler) WithOTCOfferReader(offers OTCOfferReader) *PeerOTCGRPCHandler {
	cp := *h
	cp.otcOffers = offers
	return &cp
}

// composePeerSellerID builds the conformant SI-TX party id
// (^(client|employee)-\d+$) a peer bank uses to address this offer's poster
// when bidding cross-bank. It NEVER returns the legacy literal "bank":
//   - a BANK-owned offer publishes as "employee-<ActingEmployeeID>" — the
//     stable wire identity of the employee who originated it. Legacy/seed bank
//     rows have no acting employee → "" (not exposable cross-bank; the caller
//     filters these out).
//   - a CLIENT offer publishes as "client-<InitiatorOwnerID>" (or "" when the
//     owner id is somehow unset).
func composePeerSellerID(o *model.OTCOffer) string {
	if o.InitiatorOwnerType == model.OwnerBank {
		if o.ActingEmployeeID != nil {
			return "employee-" + strconv.FormatUint(*o.ActingEmployeeID, 10)
		}
		return "" // legacy/seed bank offer w/o acting employee — not exposable cross-bank
	}
	if o.InitiatorOwnerID == nil {
		return ""
	}
	return "client-" + strconv.FormatUint(*o.InitiatorOwnerID, 10)
}

// isWellFormedLocalSellerID reports whether sellerID is a resolvable LOCAL
// participant id: "bank", "employee-<digits>", or "client-<digits>". The seller
// on an inbound bid is OURS — it must address a real local participant, never an
// arbitrary string. This bounds the junk-row vector (an "employee-<garbage>"
// seller used to persist an inert row). It does NOT touch the BUYER's opaque id,
// which stays verbatim per SI-TX §2.3.
func isWellFormedLocalSellerID(sellerID string) bool {
	if sellerID == "bank" {
		return true
	}
	for _, prefix := range []string{"client-", "employee-"} {
		if rest, ok := strings.CutPrefix(sellerID, prefix); ok {
			n, err := strconv.ParseUint(rest, 10, 64)
			return err == nil && n != 0
		}
	}
	return false
}

// deriveLastModifiedBy stamps an inbound offer's lastModifiedBy.routingNumber
// from the AUTHENTICATED sender (HOLE 1). An inbound CreateNegotiation (bid) or
// UpdateNegotiation (counter) was, by definition, last-modified by the peer that
// sent it — so the routing the receiving bank persists is the authenticated
// peerRouting, OVERRIDING whatever the payload claimed (a forged {ownRouting}
// is simply ignored, not rejected). The opaque participant id is kept VERBATIM
// (§2.3 — a bank MUST NOT interpret another bank's opaque id). The derived value
// is what the authoritative accept guard reads from the persisted row, so a peer
// can never make itself look like our local side proposed the current terms.
func deriveLastModifiedBy(lm contractsitx.ForeignBankId, peerRouting int64) contractsitx.ForeignBankId {
	lm.RoutingNumber = peerRouting
	return lm
}

func (h *PeerOTCGRPCHandler) GetPublicStocks(ctx context.Context, req *stockpb.GetPublicStocksRequest) (*stockpb.GetPublicStocksResponse, error) {
	if h.otcOffers == nil {
		return nil, status.Error(codes.Unimplemented, "OTCOfferReader not wired")
	}
	// Cross-bank discovery now serves our OPEN, sell-initiated, public,
	// non-private, LOCAL option offers — the optionable inventory peers
	// negotiate options off (Task C1) — instead of the holdings table. The
	// partial unique index guarantees one open sell offer per (owner, ticker,
	// direction), so the result is already one seller entry per (owner, ticker).
	rows, err := h.otcOffers.ListPublicOptionOffersForPeer()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list public option offers: %v", err)
	}
	out := make([]*stockpb.PeerPublicStock, 0, len(rows))
	for i := range rows {
		o := &rows[i]
		// Publish the conformant SI-TX seller id (composePeerSellerID):
		// "client-<n>" / "employee-<n>", NEVER the legacy literal "bank" — the
		// id form a discovering bank echoes back verbatim on POST /negotiations
		// (SI-TX §2.3).
		sellerID := composePeerSellerID(o)
		if sellerID == "" {
			// A legacy/seed bank offer with no acting employee (or a client offer
			// with no owner id) has no addressable seller id; skip rather than
			// ever publishing an empty / bare-numeric id a peer cannot address back.
			log.Printf("WARN: public stock offer %d skipped: no conformant seller id", o.ID)
			continue
		}
		// The /public-stock wire shape carries ONLY seller + amount (the gateway
		// groups by ticker). A termless option offer has no preset price/currency
		// in this discovery surface, so PricePerStock/Currency are intentionally
		// left unset — nothing downstream reads them for /public-stock.
		out = append(out, &stockpb.PeerPublicStock{
			OwnerId: &stockpb.PeerForeignBankId{RoutingNumber: h.ownRouting, Id: sellerID},
			Ticker:  o.Ticker,
			Amount:  o.Quantity.IntPart(),
		})
	}
	return &stockpb.GetPublicStocksResponse{Stocks: out}, nil
}

func (h *PeerOTCGRPCHandler) CreateNegotiation(ctx context.Context, req *stockpb.CreateNegotiationRequest) (*stockpb.CreateNegotiationResponse, error) {
	if req.GetOffer() == nil || req.GetBuyerId() == nil || req.GetSellerId() == nil {
		return nil, status.Error(codes.InvalidArgument, "offer, buyer_id, seller_id are required")
	}
	// Fix #7 (2026-05-16, SECURITY) — the authenticated peer's routing
	// MUST match the claimed buyer's routing. Without this, a peer
	// authenticated as Bank A could submit a bid claiming Bank C as the
	// buyer; on accept, the cross-bank SI-TX would route the premium
	// debit to Bank C's posting_executor which would resolve "client-N"
	// against Bank C's local users — debiting a third bank's account
	// for an option that bank never agreed to. Fix #1 partially
	// mitigates by pinning a specific account number, but a peer that
	// omits buyerAccountNumber would still hit the participant-id
	// resolution path. Defense at the source: require auth-routing ==
	// claimed buyer-routing.
	peerRouting, parseErr := strconv.ParseInt(req.GetPeerBankCode(), 10, 64)
	if parseErr != nil {
		return nil, status.Errorf(codes.InvalidArgument, "peer_bank_code %q is not numeric", req.GetPeerBankCode())
	}
	// Ingestion collision guard (SP-2a): a peer must not write a row that looks
	// LOCAL. The unified table keys remote rows on routing_number=<peer> — if
	// the claimed peer routing equals our own, the row would alias a local
	// chain and could leak into local money paths. Reject up front.
	if peerRouting == model.OwnRouting() {
		return nil, status.Errorf(codes.InvalidArgument,
			"peer_bank_code %q collides with this bank's own routing (%d)", req.GetPeerBankCode(), model.OwnRouting())
	}
	if req.GetBuyerId().GetRoutingNumber() != peerRouting {
		return nil, status.Errorf(codes.PermissionDenied,
			"buyer_id.routing_number (%d) must match the authenticated peer's routing (%d)",
			req.GetBuyerId().GetRoutingNumber(), peerRouting)
	}
	// Fix #9 (2026-05-16) — the seller on an inbound bid MUST be a
	// user of this bank. A bid with a foreign seller_routing would
	// create an orphaned row (no local user matches it, no
	// notification fires, accept fails). Reject up front.
	if req.GetSellerId().GetRoutingNumber() != h.ownRouting {
		return nil, status.Errorf(codes.InvalidArgument,
			"seller_id.routing_number (%d) must match this bank's routing (%d) — inbound bids target a seller on this bank only",
			req.GetSellerId().GetRoutingNumber(), h.ownRouting)
	}
	// Well-formed-seller guard (HOLE 3): the seller is OURS — its id must address
	// a resolvable LOCAL participant ("bank" / "employee-<digits>" /
	// "client-<digits>"). An "employee-<garbage>" (or any other free-form id)
	// used to persist an inert junk row — an unbounded row-spam vector. Reject
	// before persisting. This validates OUR OWN side; it does NOT interpret the
	// BUYER's opaque id (kept verbatim per SI-TX §2.3).
	sellerID := req.GetSellerId().GetId()
	if !isWellFormedLocalSellerID(sellerID) {
		return nil, status.Errorf(codes.InvalidArgument,
			"seller_id.id %q is not a well-formed local participant id (bank, employee-<n>, or client-<n>)", sellerID)
	}
	// Phantom-seller guard: the seller routing is ours and the id is well-formed,
	// but a client-<n> must also resolve to a REAL local client. Without this a
	// raw peer could spam inbound rows naming a non-existent client-<n> (correct
	// routing, bogus id): they fail closed at accept (NO_SUCH_ACCOUNT) but still
	// persist as inert junk rows. Only client-<n> needs the existence check;
	// "bank"/"employee-<n>" always resolve to a local participant. Skipped when no
	// validator is wired (legacy/test mode).
	if h.sellerValidator != nil {
		if strings.HasPrefix(sellerID, "client-") {
			if !h.sellerValidator.SellerExists(ctx, sellerID) {
				return nil, status.Errorf(codes.NotFound,
					"seller_id.id %q does not resolve to a client on this bank", sellerID)
			}
		}
	}
	offer := protoToOffer(req.GetOffer())
	// Derive lastModifiedBy from the AUTHENTICATED sender (HOLE 1). An inbound
	// bid was, by definition, last-modified by the peer that POSTed it — so the
	// stored lastModifiedBy.routingNumber is the authenticated peer's routing,
	// NOT whatever the payload claimed. Override it here so the authoritative
	// accept guard (which reads the persisted lastModifiedBy to decide who last
	// proposed) is trustworthy by construction: a peer that forges {ownRouting}
	// has it overridden to its own routing and can never self-accept. The opaque
	// participant id is kept VERBATIM (§2.3 — never interpreted by us).
	offer.LastModifiedBy = deriveLastModifiedBy(offer.LastModifiedBy, peerRouting)
	offerJSON, err := json.Marshal(offer)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal offer: %v", err)
	}
	foreignID := uuid.NewString()
	// Phase 10 — capture the bidder-supplied parent_offer_id for the
	// cross-bank cascade-cancel grouping. Both fields must be set for
	// the row to participate in cascade matching; either-or absent
	// means free-form (no cascade).
	var parentRouting *int64
	var parentNativeID *string
	if p := req.GetOffer().GetParentOfferId(); p != nil && p.GetId() != "" {
		r := p.GetRoutingNumber()
		id := p.GetId()
		parentRouting = &r
		parentNativeID = &id
	}
	neg := buildRemoteNeg(
		peerRouting, foreignID, offer, string(offerJSON),
		req.GetBuyerId().GetRoutingNumber(), req.GetBuyerId().GetId(),
		req.GetSellerId().GetRoutingNumber(), req.GetSellerId().GetId(),
		parentRouting, parentNativeID, "ongoing",
	)
	// Record the inbound BID as the chain's first revision (full-history parity).
	// The bidder is the peer's BUYER; wire id is the buyer's opaque participant id.
	buyerWire := req.GetBuyerId().GetId()
	bidRev := &model.OTCNegotiationRevision{
		Quantity:                decimal.NewFromInt(offer.Amount),
		StrikePrice:             offer.PricePerStock,
		Premium:                 offer.Premium,
		SettlementDate:          parseSITXDate(offer.SettlementDate),
		Action:                  model.OTCNegotiationActionBid,
		ModifiedByPrincipalType: "buyer",
		RemoteActorWireID:       &buyerWire,
	}
	if err := h.negRepo.UpsertRemoteNegWithRevision(neg, bidRev); err != nil {
		return nil, status.Errorf(codes.Internal, "create: %v", err)
	}
	// Inbound bid from a peer → notify our local seller (if the seller
	// side is local). Best-effort, after-commit.
	if uid, ok := h.localClientUserID(req.GetSellerId().GetRoutingNumber(), req.GetSellerId().GetId()); ok {
		h.publishPeerNotif(ctx, uid, "OTC_OFFER_RECEIVED",
			notifDataFromOffer(offer),
			"otc_negotiation", neg.ID,
		)
	}
	return &stockpb.CreateNegotiationResponse{
		NegotiationId: &stockpb.PeerForeignBankId{RoutingNumber: h.ownRouting, Id: foreignID},
	}, nil
}

func (h *PeerOTCGRPCHandler) UpdateNegotiation(ctx context.Context, req *stockpb.UpdateNegotiationRequest) (*stockpb.UpdateNegotiationResponse, error) {
	if req.GetOffer() == nil || req.GetNegotiationId() == nil {
		return nil, status.Error(codes.InvalidArgument, "offer and negotiation_id required")
	}
	peerRouting := peerRoutingForCode(req.GetPeerBankCode())

	// SI-TX §3.3 ("Posting a counter-offer"): "If the receiving bank deems that
	// it is its turn to make a counter-offer, rather than the [other party's]
	// bank, or if negotiations are closed, a 409 Conflict response code is
	// produced." Both guards run on the PERSISTED row BEFORE we persist the new
	// counter. FailedPrecondition is mapped by the gateway to 409
	// business_rule_violation, which reaches the peer as the required 409.
	existing, err := h.negRepo.GetRemoteNegByRoutingAndNative(peerRouting, req.GetNegotiationId().GetId())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "negotiation not found")
		}
		return nil, status.Errorf(codes.Internal, "load negotiation: %v", err)
	}
	// Closed guard: a counter may only be posted while the negotiation is
	// ongoing. Cancelled / accepted / rejected / expired → 409.
	if existing.Status != "ongoing" {
		return nil, status.Errorf(codes.FailedPrecondition,
			"negotiation is closed (status %q): no counter-offer may be posted", existing.Status)
	}
	// Turn guard (§3.3): a party may counter ONLY when the OTHER side made the
	// last modification. Because we DERIVE lastModifiedBy from the authenticated
	// sender (HOLE 1), the persisted lastModifiedBy.routingNumber is the side
	// that last acted. So the calling peer may counter iff WE (ownRouting) last
	// acted — i.e. it is now the peer's turn. If the stored routing is the peer
	// itself (it already made the last modification), it is NOT its turn → 409.
	// A brand-new chain right after the peer's own bid stores routing=peerRouting
	// (the peer last acted), so an immediate peer PUT is correctly out-of-turn —
	// the receiving side must counter or accept first (§3.3 intended behaviour).
	var storedLM contractsitx.OtcOffer
	if existing.RemoteOfferJSON != nil {
		_ = json.Unmarshal([]byte(*existing.RemoteOfferJSON), &storedLM)
	}
	if storedLM.LastModifiedBy.RoutingNumber != h.ownRouting {
		return nil, status.Errorf(codes.FailedPrecondition,
			"not your turn: the last counter-offer was made by your side")
	}

	offer := protoToOffer(req.GetOffer())
	// Derive lastModifiedBy from the AUTHENTICATED sender (HOLE 1). An inbound
	// counter was, by definition, last-modified by the peer that PUT it — so the
	// stored lastModifiedBy.routingNumber is the authenticated peer's routing,
	// NOT whatever the payload claimed. Override it before persisting so a forged
	// {ownRouting} counter cannot later slip its own /accept past the
	// authoritative accept guard. The opaque participant id is kept VERBATIM
	// (§2.3 — never interpreted by us).
	offer.LastModifiedBy = deriveLastModifiedBy(offer.LastModifiedBy, peerRouting)
	offerJSON, err := json.Marshal(offer)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal offer: %v", err)
	}
	// Record the inbound counter as a revision (full-history parity). The mover is
	// the authenticated peer; its role + wire id are read from the persisted row.
	counterRole, counterWire := remoteSideAtRouting(existing, peerRouting)
	counterRev := &model.OTCNegotiationRevision{
		Quantity:                decimal.NewFromInt(offer.Amount),
		StrikePrice:             offer.PricePerStock,
		Premium:                 offer.Premium,
		SettlementDate:          parseSITXDate(offer.SettlementDate),
		Action:                  model.OTCNegotiationActionCounter,
		ModifiedByPrincipalType: counterRole,
		RemoteActorWireID:       &counterWire,
	}
	if err := h.negRepo.UpdateRemoteNegOfferWithRevision(peerRouting, req.GetNegotiationId().GetId(), string(offerJSON), counterRev); err != nil {
		return nil, status.Errorf(codes.Internal, "update: %v", err)
	}
	// Inbound counter — the authenticated peer is the actor. The OTHER party in
	// our local row is the recipient. Use the DERIVED lastModifiedBy (routing =
	// peerRouting) as the actor identity, consistent with what was persisted.
	if h.notifier != nil {
		row, gerr := h.negRepo.GetRemoteNegByRoutingAndNative(peerRouting, req.GetNegotiationId().GetId())
		if gerr == nil {
			actorRouting := offer.LastModifiedBy.RoutingNumber
			actorID := offer.LastModifiedBy.ID
			buyerRouting, buyerID := remoteBuyer(row)
			sellerRouting, sellerID := remoteSeller(row)
			// Identify the local party that is NOT the actor.
			var localUID uint64
			if buyerRouting == h.ownRouting &&
				!(buyerRouting == actorRouting && buyerID == actorID) {
				if uid, ok := h.localClientUserID(buyerRouting, buyerID); ok {
					localUID = uid
				}
			}
			if localUID == 0 && sellerRouting == h.ownRouting &&
				!(sellerRouting == actorRouting && sellerID == actorID) {
				if uid, ok := h.localClientUserID(sellerRouting, sellerID); ok {
					localUID = uid
				}
			}
			if localUID != 0 {
				h.publishPeerNotif(ctx, localUID, "OTC_OFFER_COUNTERED",
					notifDataFromOffer(offer),
					"otc_negotiation", row.ID,
				)
			}
		}
	}
	return &stockpb.UpdateNegotiationResponse{}, nil
}

func (h *PeerOTCGRPCHandler) GetNegotiation(ctx context.Context, req *stockpb.GetNegotiationRequest) (*stockpb.GetNegotiationResponse, error) {
	if req.GetNegotiationId() == nil {
		return nil, status.Error(codes.InvalidArgument, "negotiation_id required")
	}
	row, err := h.negRepo.GetRemoteNegByRoutingAndNative(peerRoutingForCode(req.GetPeerBankCode()), req.GetNegotiationId().GetId())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "negotiation not found")
		}
		return nil, status.Errorf(codes.Internal, "get: %v", err)
	}
	var offer contractsitx.OtcOffer
	_ = json.Unmarshal([]byte(remoteOfferJSONOf(row)), &offer)
	buyerRouting, buyerID := remoteBuyer(row)
	sellerRouting, sellerID := remoteSeller(row)
	return &stockpb.GetNegotiationResponse{
		Id:        &stockpb.PeerForeignBankId{RoutingNumber: h.ownRouting, Id: remoteNativeIDOf(row)},
		BuyerId:   &stockpb.PeerForeignBankId{RoutingNumber: buyerRouting, Id: buyerID},
		SellerId:  &stockpb.PeerForeignBankId{RoutingNumber: sellerRouting, Id: sellerID},
		Offer:     offerToProto(offer),
		Status:    row.Status,
		UpdatedAt: row.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}, nil
}

func (h *PeerOTCGRPCHandler) DeleteNegotiation(ctx context.Context, req *stockpb.DeleteNegotiationRequest) (*stockpb.DeleteNegotiationResponse, error) {
	if req.GetNegotiationId() == nil {
		return nil, status.Error(codes.InvalidArgument, "negotiation_id required")
	}
	// Load BEFORE the status flip so we can distinguish caller-driven
	// cancel (ParentOfferID nil — free-form chain) from cascade-cancel
	// (ParentOfferID set — discovered chain whose seller accepted a
	// competing bid). Only used for the notification choice; the row
	// state change is identical either way.
	peerRouting := peerRoutingForCode(req.GetPeerBankCode())
	row, gerr := h.negRepo.GetRemoteNegByRoutingAndNative(peerRouting, req.GetNegotiationId().GetId())

	// Record the inbound terminal as a REJECT revision (full-history parity) when
	// the row is known; the mover is the authenticated peer. SetRemoteNegStatusWith
	// Revision only records on a real ongoing→cancelled transition (idempotent).
	// Falls back to the plain status flip when the row could not be loaded.
	if gerr == nil && row != nil {
		var rejOffer contractsitx.OtcOffer
		_ = json.Unmarshal([]byte(remoteOfferJSONOf(row)), &rejOffer)
		rejRole, rejWire := remoteSideAtRouting(row, peerRouting)
		rejectRev := &model.OTCNegotiationRevision{
			Quantity:                decimal.NewFromInt(rejOffer.Amount),
			StrikePrice:             rejOffer.PricePerStock,
			Premium:                 rejOffer.Premium,
			SettlementDate:          parseSITXDate(rejOffer.SettlementDate),
			Action:                  model.OTCNegotiationActionReject,
			ModifiedByPrincipalType: rejRole,
			RemoteActorWireID:       &rejWire,
		}
		if _, err := h.negRepo.SetRemoteNegStatusWithRevision(peerRouting, req.GetNegotiationId().GetId(), "cancelled", rejectRev); err != nil {
			return nil, status.Errorf(codes.Internal, "cancel: %v", err)
		}
	} else if err := h.negRepo.UpdateRemoteNegStatus(peerRouting, req.GetNegotiationId().GetId(), "cancelled"); err != nil {
		return nil, status.Errorf(codes.Internal, "cancel: %v", err)
	}
	if gerr == nil && row != nil && h.notifier != nil {
		notifType := "OTC_OFFER_CANCELLED"
		data := map[string]string{}
		// Cascade heuristic: a discovered-group chain DELETEd by the
		// seller side means the cascade fired (the seller would have
		// accepted a competing bid). Free-form chains (no parent)
		// can't be cascade victims, so they're plain cancels.
		if row.RemoteParentRouting != nil && row.RemoteParentNativeID != nil && *row.RemoteParentNativeID != "" {
			notifType = "OTC_OFFER_CASCADE_CANCELLED"
			var offer contractsitx.OtcOffer
			_ = json.Unmarshal([]byte(remoteOfferJSONOf(row)), &offer)
			data["ticker"] = offer.Ticker
			data["accepted_premium"] = offer.Premium.String()
		} else {
			var offer contractsitx.OtcOffer
			_ = json.Unmarshal([]byte(remoteOfferJSONOf(row)), &offer)
			data["ticker"] = offer.Ticker
		}
		// Recipient: the LOCAL party in this row (whichever side has
		// own_routing). For caller-driven cancels the caller is the
		// other bank's user, so the local party is the recipient.
		buyerRouting, buyerID := remoteBuyer(row)
		sellerRouting, sellerID := remoteSeller(row)
		if uid, ok := h.localClientUserID(buyerRouting, buyerID); ok {
			h.publishPeerNotif(ctx, uid, notifType, data, "otc_negotiation", row.ID)
		} else if uid, ok := h.localClientUserID(sellerRouting, sellerID); ok {
			h.publishPeerNotif(ctx, uid, notifType, data, "otc_negotiation", row.ID)
		}
	}
	return &stockpb.DeleteNegotiationResponse{}, nil
}

func (h *PeerOTCGRPCHandler) AcceptNegotiation(ctx context.Context, req *stockpb.AcceptNegotiationRequest) (*stockpb.AcceptNegotiationResponse, error) {
	if req.GetNegotiationId() == nil {
		return nil, status.Error(codes.InvalidArgument, "negotiation_id required")
	}
	peerRouting := peerRoutingForCode(req.GetPeerBankCode())
	row, err := h.negRepo.GetRemoteNegByRoutingAndNative(peerRouting, req.GetNegotiationId().GetId())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "negotiation not found")
		}
		return nil, status.Errorf(codes.Internal, "get: %v", err)
	}
	var offer contractsitx.OtcOffer
	if err := json.Unmarshal([]byte(remoteOfferJSONOf(row)), &offer); err != nil {
		return nil, status.Errorf(codes.Internal, "decode offer: %v", err)
	}
	buyerRouting, buyerID := remoteBuyer(row)
	sellerRouting, sellerID := remoteSeller(row)
	foreignID := remoteNativeIDOf(row)

	// Authoritative anti-self-accept guard (HOLE 1, fix #2). Per SI-TX §3.6 the
	// accepting party is "the person whose negotiation term it is" — i.e. the side
	// that did NOT last propose — and THEIR bank sends the GET /accept to the
	// bank of the side that DID last propose. So on an inbound /accept WE receive,
	// the LOCAL side must be the last proposer: require
	// lastModifiedBy.routingNumber == ownRouting. The calling peer is the
	// accepting counterparty; it may NEVER accept terms its own side (or a forged
	// proposal) last proposed. Combined with the forge-proof create/counter guards
	// (lastModifiedBy can only ever be the peer itself), a peer can never accept
	// its own (or a forged) proposal — forming a contract + settling premium with
	// no agreement from our local party. A zero/absent lastModifiedBy fails this
	// (we can't prove WE proposed) → rejected, never granting a self-accept.
	if lm := offer.LastModifiedBy; lm.RoutingNumber != h.ownRouting {
		return nil, status.Error(codes.PermissionDenied,
			"accept must come from the counterparty: the local side must have last proposed the current terms")
	}

	// Orphan-accept guard (HOLE 2). When WE host the parent listing
	// (remote_parent_routing == ownRouting), the parent native id is our local
	// offer id. An inbound accept against a child of a CANCELLED/CONSUMED listing
	// must be rejected authoritatively — regardless of the best-effort
	// cascade-cancel timing (a concurrent inbound accept could otherwise win the
	// ongoing→accepted CAS before the cascade flips the child). Mirrors the
	// OUTBOUND acceptRemoteNegotiation gate. Skipped when the parent is on a peer
	// bank (we can't read its status) or when no parent checker is wired.
	if h.parentChecker != nil && row.RemoteParentRouting != nil &&
		*row.RemoteParentRouting == h.ownRouting && row.RemoteParentNativeID != nil {
		if parentID, perr := strconv.ParseUint(*row.RemoteParentNativeID, 10, 64); perr == nil {
			if !h.parentChecker.LocalParentIsOpen(parentID) {
				return nil, status.Error(codes.FailedPrecondition,
					"parent listing is no longer open (cancelled or already consumed)")
			}
		}
	}

	// Termless orphan-accept guard. The /public-stock wire carries no offer id, so
	// a termless bid's parent native id is non-numeric ("ps:…") or absent and the
	// numeric guard above is skipped. When WE host the seller, resolve the listing
	// by its (owner, ticker, sell_initiated) unique key and reject an accept once
	// the listing is no longer open — the seller already consumed it (a prior
	// accept) or cancelled it. Without this an inbound accept of a still-"ongoing"
	// sibling bid forms a second contract and over-commits the seller's shares, or
	// forms a contract + moves premium on a withdrawn listing. Symmetric with the
	// OUTBOUND acceptRemoteNegotiation gate. Skipped when ticker is unknown so a
	// malformed mirror never blocks a legitimate accept.
	if h.parentChecker != nil && sellerRouting == h.ownRouting && offer.Ticker != "" {
		if ot, oid, perr := parseSellerOwner(sellerID); perr == nil {
			if !h.parentChecker.LocalSellOfferOpenForSeller(ot, oid, offer.Ticker) {
				return nil, status.Error(codes.FailedPrecondition,
					"listing is no longer open (cancelled or already consumed)")
			}
		}
	}

	// Atomically claim the negotiation for acceptance (ongoing → accepted) BEFORE
	// composing/dispatching the option-formation SI-TX. This serialises concurrent
	// accepts of the same negotiation: only one wins the compare-and-set and
	// dispatches; the loser is rejected. Without it, two simultaneous accepts each
	// charged the buyer the premium, reserved the seller's shares again, and minted
	// a duplicate contract. On a synchronous dispatch failure we revert the claim
	// (accepted → ongoing) so the negotiation can be re-accepted.
	claimed, cerr := h.negRepo.CompareAndSetRemoteNegStatus(peerRouting, req.GetNegotiationId().GetId(), "ongoing", "accepted")
	if cerr != nil {
		return nil, status.Errorf(codes.Internal, "claim negotiation: %v", cerr)
	}
	if !claimed {
		return nil, status.Errorf(codes.FailedPrecondition,
			"negotiation %s is not acceptable (already accepted/cancelled or an accept is in progress)", req.GetNegotiationId().GetId())
	}

	// Compose the 4 postings:
	// 1. Buyer debits premium (in premium currency)
	// 2. Seller credits premium
	// 3. Seller debits 1× OptionDescription (asset)
	// 4. Buyer credits 1× OptionDescription
	optDesc := contractsitx.OptionDescription{
		// The option pseudo-account always lives in the SELLER's bank (SI-TX §7), so
		// the negotiationId authority is the seller's routing — NOT ours. These match
		// when WE host as seller (Direction 1: sellerRouting == ownRouting), but when
		// the seller is a peer (Direction 2: our bank is the buyer) using ownRouting
		// made the seller's bank vote NO: UNACCEPTABLE_ASSET (it can't resolve an
		// option keyed to our routing).
		NegotiationID:  contractsitx.ForeignBankId{RoutingNumber: sellerRouting, ID: foreignID},
		Stock:          contractsitx.StockDescription{Ticker: offer.Ticker},
		PricePerUnit:   contractsitx.MonetaryValue{Amount: contractsitx.DecimalNumber{Decimal: offer.PricePerStock}, Currency: offer.Currency},
		SettlementDate: offer.SettlementDate,
		Amount:         offer.Amount,
	}
	optDescJSON, err := json.Marshal(optDesc)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "marshal option description: %v", err)
	}
	optAssetID := string(optDescJSON)

	premium := offer.Premium.String()
	// Fix #1 (2026-05-16) — prefer the buyer-pinned account number from
	// the OtcOffer wire payload over the participant-id ("client-N")
	// fallback. The buyer's bank set BuyerAccountNumber at bid time so
	// the SI-TX posting executor doesn't have to pick "first active
	// <currency> account" for the buyer — which was non-deterministic
	// and silently failed when the buyer had no account in the offer's
	// currency. Sellers stay on participant-id resolution (the
	// seller-credit is a credit; no per-account binding needed since
	// any active <currency> account works for incoming funds).
	buyerAccountID := buyerID
	if offer.BuyerAccountNumber != "" {
		buyerAccountID = offer.BuyerAccountNumber
	}
	// Seller-credit nomination (the symmetric fix to the buyer-debit pin above).
	// WE host the seller, so the seller-CREDIT money leg is composed by us and
	// resolved by OUR OWN posting executor. Bind the seller's NOMINATED account
	// number — the local parent listing's InitiatorAccountID (mirrors the local
	// accept saga's sellerAccountID = offer.InitiatorAccountID on a sell_initiated
	// offer) — and emit it as a concrete ACCOUNT{num} leg, so the premium lands in
	// the account the seller chose at offer creation rather than "the seller's
	// first active <currency> account". When no nomination is resolvable (free-form
	// negotiation with no local parent listing, an unbound account, or one failing
	// the active/owner/currency checks) the resolver returns "" and we keep the
	// participant id (the documented first-active fallback). The OPTION legs ALWAYS
	// keep the seller PARTICIPANT id — it becomes the contract's seller_id used for
	// the exercise share-consume + /me/otc/contracts listing.
	sellerAccountID := sellerID
	if h.sellerAccountResolver != nil {
		if num := h.sellerAccountResolver.ResolveSellerAccountNumber(ctx, row, offer.PremiumCurrency); num != "" {
			sellerAccountID = num
		}
	}
	// Money legs (premium) carry account numbers — the buyer's pinned account
	// for the DEBIT and the seller's nominated account for the CREDIT (so the
	// executor credits the exact account); when either nomination is absent the
	// leg falls back to the participant id, which the executor resolves to an
	// active currency account. Option-asset legs carry PARTICIPANT ids
	// ("client-<n>") on BOTH sides: that id becomes the peer_option_contract's
	// buyer_id/seller_id, which must be parseable as a participant for the exercise
	// share-credit and for the /me/otc/contracts listing (ListByLocalParticipant
	// matches buyer_id = "client-<principal>"). Using an account number on the
	// option leg (the old bug) left an unparseable buyer_id/seller_id — exercise
	// couldn't credit/consume and the contract was invisible in their listing.
	// Type tags (SI-TX §3.6) — the downstream executor detects an option leg via
	// AssetType=="OPTION" (not by sniffing the asset_id prefix) and the outbound
	// wire builder uses AccountType/AssetType to construct the spec account/asset
	// tagged unions. The two premium legs carry MONAS; the two option legs OPTION.
	// AccountType is derived from the AccountId form: a raw 18-digit bank account
	// number → ACCOUNT, a "client-N"/"employee-N" participant id → PERSON. The two
	// premium money legs (postings 0 and 1) carry a pinned account number when the
	// buyer / seller nominated one (else the participant id); the two OPTION legs
	// (postings 2 and 3) always carry participant ids.
	postings := []*transactionpb.SiTxPosting{
		{RoutingNumber: buyerRouting, AccountId: buyerAccountID, AccountType: accountTypeFor(buyerAccountID), AssetId: offer.PremiumCurrency, AssetType: contractsitx.AssetTypeMonas, Amount: premium, Direction: contractsitx.DirectionDebit},
		{RoutingNumber: sellerRouting, AccountId: sellerAccountID, AccountType: accountTypeFor(sellerAccountID), AssetId: offer.PremiumCurrency, AssetType: contractsitx.AssetTypeMonas, Amount: premium, Direction: contractsitx.DirectionCredit},
		{RoutingNumber: sellerRouting, AccountId: sellerID, AccountType: accountTypeFor(sellerID), AssetId: optAssetID, AssetType: contractsitx.AssetTypeOption, Amount: "1", Direction: contractsitx.DirectionDebit},
		{RoutingNumber: buyerRouting, AccountId: buyerID, AccountType: accountTypeFor(buyerID), AssetId: optAssetID, AssetType: contractsitx.AssetTypeOption, Amount: "1", Direction: contractsitx.DirectionCredit},
	}

	resp, err := h.peerTx.InitiateOutboundTxWithPostings(ctx, &transactionpb.SiTxInitiateWithPostingsRequest{
		PeerBankCode: req.GetPeerBankCode(),
		Postings:     postings,
		TxKind:       "otc-accept",
	})
	if err != nil {
		// Dispatch failed — release the acceptance claim (accepted → ongoing) so
		// the negotiation can be re-accepted after the cause is resolved.
		if _, rerr := h.negRepo.CompareAndSetRemoteNegStatus(peerRouting, req.GetNegotiationId().GetId(), "accepted", "ongoing"); rerr != nil {
			log.Printf("WARN: peer-otc accept: failed to revert claim for %s/%s after dispatch error: %v",
				req.GetPeerBankCode(), req.GetNegotiationId().GetId(), rerr)
		}
		// Preserve the underlying gRPC code so a business rejection (e.g. seller
		// has insufficient shares → FailedPrecondition INSUFFICIENT_ASSET) surfaces
		// as 409 at the gateway, not a misleading 500 internal_error.
		if st, ok := status.FromError(err); ok {
			return nil, status.Errorf(st.Code(), "dispatch: %v", err)
		}
		return nil, status.Errorf(codes.Internal, "dispatch: %v", err)
	}

	// Settlement-outcome gate. InitiateOutboundTxWithPostings returns the SI-TX
	// row's terminal status. "rolled_back"/"failed" means a bank voted NO (e.g.
	// genuine insufficiency) and NO contract formed. Revert the acceptance claim
	// (accepted → ongoing) so the chain can be re-accepted, record NO accept
	// revision, do NOT consume the listing, and return an error — so the OUTBOUND
	// acceptRemoteNegotiation fails at its HTTP-code guard and the seller KEEPS
	// their listing (closes the "listing deleted, no contract, money error" bug).
	// committed/committing/pending all proceed (the contract has/will settle).
	if txStatus := resp.GetStatus(); txStatus == "rolled_back" || txStatus == "failed" {
		if _, rerr := h.negRepo.CompareAndSetRemoteNegStatus(peerRouting, req.GetNegotiationId().GetId(), "accepted", "ongoing"); rerr != nil {
			log.Printf("WARN: peer-otc accept: failed to revert claim for %s/%s after %s settlement: %v",
				req.GetPeerBankCode(), req.GetNegotiationId().GetId(), txStatus, rerr)
		}
		return nil, status.Errorf(codes.FailedPrecondition,
			"cross-bank settlement did not commit (%s): no contract formed", txStatus)
	}

	// Negotiation was already claimed as "accepted" before dispatch (the
	// concurrency guard); no post-dispatch status update needed.
	//
	// Record the ACCEPT revision NOW (on the success path, not at the claim CAS)
	// so a rolled-back claim never leaves a stray ACCEPT in the history. The
	// acceptor is the peer (its side hosts the accepting party). Best-effort: the
	// contract already formed, so a revision-log failure must not fail the accept.
	acceptRole, acceptWire := remoteSideAtRouting(row, peerRouting)
	acceptRev := &model.OTCNegotiationRevision{
		Quantity:                decimal.NewFromInt(offer.Amount),
		StrikePrice:             offer.PricePerStock,
		Premium:                 offer.Premium,
		SettlementDate:          parseSITXDate(offer.SettlementDate),
		Action:                  model.OTCNegotiationActionAccept,
		ModifiedByPrincipalType: acceptRole,
		RemoteActorWireID:       &acceptWire,
	}
	if aerr := h.negRepo.AppendRemoteRevision(peerRouting, foreignID, acceptRev); aerr != nil {
		log.Printf("WARN: peer-otc accept: failed to record accept revision for %s/%s: %v",
			req.GetPeerBankCode(), req.GetNegotiationId().GetId(), aerr)
	}

	// Consume the LOCAL listing this contract formed against (we host the seller):
	// one listing backs exactly one accepted contract. The termless /public-stock
	// wire carries no offer id, so the listing is resolved by the seller's
	// (owner, ticker, sell_initiated) key. Best-effort — the contract already
	// formed on both banks, so a consume failure must not fail the accept (the
	// guard above is the authoritative pre-form gate; this just retires the
	// listing). Symmetric with the OUTBOUND acceptRemoteNegotiation consume.
	if h.parentChecker != nil && sellerRouting == h.ownRouting && offer.Ticker != "" {
		if ot, oid, perr := parseSellerOwner(sellerID); perr == nil {
			if cerr := h.parentChecker.ConsumeLocalSellOfferForSeller(ot, oid, offer.Ticker); cerr != nil {
				log.Printf("WARN: peer-otc accept: consume local listing failed for %s/%s: %v",
					req.GetPeerBankCode(), req.GetNegotiationId().GetId(), cerr)
			}
		}
	}

	// Seller-side notification: this bank is the SELLER's bank (the
	// inbound /accept lands here because the buyer's bank POSTed). The
	// local user is the seller. The buyer's bank emits its own
	// OTC_CONTRACT_CREATED notification independently when it processes
	// the SI-TX postings on its side.
	if uid, ok := h.localClientUserID(sellerRouting, sellerID); ok {
		h.publishPeerNotif(ctx, uid, "OTC_CONTRACT_CREATED",
			map[string]string{
				"ticker":       offer.Ticker,
				"quantity":     strconv.FormatInt(offer.Amount, 10),
				"strike_price": offer.PricePerStock.String(),
				"premium_paid": offer.Premium.String(),
			},
			"otc_negotiation", row.ID,
		)
	}

	return &stockpb.AcceptNegotiationResponse{
		TransactionId: resp.GetTransactionId(),
		Status:        resp.GetStatus(),
	}, nil
}

// accountTypeFor classifies a SI-TX posting AccountId into its §2.7 account-type
// tag. A raw bank account number (all digits, >=15 long — own-bank numbers are 18
// digits) is an "ACCOUNT"; anything else (a "client-<n>"/"employee-<n>" participant
// id) is a "PERSON". The accept flow never emits OPTION-typed accounts.
func accountTypeFor(accountID string) string {
	if len(accountID) >= 15 && isAllDigits(accountID) {
		return contractsitx.AccountTypeAccount
	}
	return contractsitx.AccountTypePerson
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// buildRemoteNeg constructs a REMOTE model.OTCNegotiation row from the SI-TX
// offer + party ids. It satisfies the unified table's NOT-NULL / CHECK /
// ValidateOwner constraints for a remote row:
//   - RoutingNumber = the peer's routing (peerRouting, guaranteed != ownRouting
//     by the ingestion guard at the call sites). NativeID = the foreign id.
//   - BidderOwnerType=OwnerBank + BidderOwnerID=nil (ValidateOwner-valid; a
//     remote chain has no LOCAL bidder identity — the real parties live in the
//     Remote* columns). ParentOfferID stays 0 (remote rows reference no local
//     parent listing).
//   - Quantity/StrikePrice/Premium/SettlementDate are parsed from the offer to
//     satisfy NOT-NULL; they are advisory only (RemoteOfferJSON is the
//     authoritative source the read-merge decodes). LastActionBy* audit fields
//     are stamped with neutral system values.
//   - Status carries the peer status vocabulary ("ongoing" by default).
func buildRemoteNeg(
	peerRouting int64,
	foreignID string,
	offer contractsitx.OtcOffer,
	offerJSON string,
	buyerRouting int64, buyerID string,
	sellerRouting int64, sellerID string,
	parentRouting *int64, parentNativeID *string,
	status string,
) *model.OTCNegotiation {
	now := time.Now().UTC()
	settle := offer.SettlementDate
	settleTime := now
	if settle != "" {
		if t, e := time.Parse(time.RFC3339, settle); e == nil {
			settleTime = t
		} else if t, e := time.Parse("2006-01-02", settle); e == nil {
			settleTime = t
		}
	}
	buyerR := buyerRouting
	sellerR := sellerRouting
	bID := buyerID
	sID := sellerID
	oJSON := offerJSON
	return &model.OTCNegotiation{
		RoutingNumber:   peerRouting,
		NativeID:        &foreignID,
		ParentOfferID:   0,
		BidderOwnerType: model.OwnerBank,
		BidderOwnerID:   nil,
		Quantity:        decimal.NewFromInt(offer.Amount),
		StrikePrice:     offer.PricePerStock,
		Premium:         offer.Premium,
		SettlementDate:  settleTime,
		Status:          status,
		// Audit fields — neutral system values (no local principal acts on a
		// remote mirror row).
		LastActionByPrincipalType: "system",
		LastActionByPrincipalID:   0,
		LastActionByOwnerType:     string(model.OwnerBank),
		LastActionByOwnerID:       nil,
		LastActionAt:              now,
		// Remote-mirror columns.
		RemoteOfferJSON:      &oJSON,
		RemoteBuyerRouting:   &buyerR,
		RemoteBuyerID:        &bID,
		RemoteSellerRouting:  &sellerR,
		RemoteSellerID:       &sID,
		RemoteParentRouting:  parentRouting,
		RemoteParentNativeID: parentNativeID,
	}
}

// remoteContractNativeID composes the unified-table native_id for a cross-bank
// option contract from the retired mirror's natural key (crossbank_tx_id,
// posting_index). Keeping the natural key inside native_id makes
// UpsertRemoteContract idempotent on the (routing_number, native_id) unique
// index exactly like the retired UpsertIdempotent was on (crossbank_tx_id,
// posting_index).
func remoteContractNativeID(crossbankTxID string, postingIndex int32) string {
	return crossbankTxID + ":" + strconv.FormatInt(int64(postingIndex), 10)
}

// remoteContractCounterpartyRouting returns the routing of the COUNTERPARTY —
// the side this bank does NOT host — which the unified row stamps as its
// RoutingNumber (so routing != ownRouting marks it remote). CREDIT → this bank
// hosts the buyer → counterparty is the seller's bank; DEBIT → this bank hosts
// the seller → counterparty is the buyer's bank.
func remoteContractCounterpartyRouting(direction string, buyerRouting, sellerRouting int64) int64 {
	if direction == contractsitx.DirectionCredit {
		return sellerRouting
	}
	return buyerRouting
}

// buildRemoteContract constructs a REMOTE model.OptionContract row from the
// SI-TX option description + party ids. It satisfies the unified table's
// NOT-NULL / CHECK / ValidateOwner constraints for a remote row:
//   - RoutingNumber = the COUNTERPARTY routing (the side we do NOT host;
//     guaranteed != ownRouting because exactly one side is local and the other
//     is the peer). NativeID = "<crossbank_tx_id>:<posting_index>".
//   - OfferID = nil (a remote contract has no local OTCOffer).
//   - Buyer/SellerOwnerType = OwnerBank with nil ids (ValidateOwner-valid; the
//     real SI-TX participants live in RemoteBuyerID/RemoteSellerID +
//     BuyerBankCode/SellerBankCode).
//   - Quantity is the int amount as a decimal (whole units; IntPart() round-trips
//     it exactly). StrikePrice/StrikeCurrency/Ticker/SettlementDate carry the
//     terms. The NOT-NULL money/account/saga fields get sensible remote defaults:
//     PremiumPaid=0, PremiumCurrency=StrikeCurrency, Buyer/SellerAccountID=0,
//     SagaID=crossbankTxID, PremiumPaidAt=CreatedAt(now).
//   - Status carries the peer vocabulary ("active" on formation).
//   - Remote* columns carry the negotiation key, direction, and participant ids.
func buildRemoteContract(
	crossbankTxID string,
	postingIndex int32,
	opt contractsitx.OptionDescription,
	direction string,
	buyerRouting int64, buyerID string,
	sellerRouting int64, sellerID string,
) *model.OptionContract {
	now := time.Now().UTC()
	settle := now
	if s := opt.SettlementDate; s != "" {
		if t, e := time.Parse(time.RFC3339, s); e == nil {
			settle = t
		} else if t, e := time.Parse("2006-01-02", s); e == nil {
			settle = t
		}
	}
	counterparty := remoteContractCounterpartyRouting(direction, buyerRouting, sellerRouting)
	native := remoteContractNativeID(crossbankTxID, postingIndex)
	buyerBankCode := strconv.FormatInt(buyerRouting, 10)
	sellerBankCode := strconv.FormatInt(sellerRouting, 10)
	cbTx := crossbankTxID
	pIdx := postingIndex
	negRouting := opt.NegotiationID.RoutingNumber
	negNative := opt.NegotiationID.ID
	dir := direction
	bID := buyerID
	sID := sellerID
	currency := opt.PricePerUnit.Currency
	return &model.OptionContract{
		RoutingNumber:   counterparty,
		NativeID:        &native,
		OfferID:         nil,
		BuyerOwnerType:  model.OwnerBank,
		BuyerOwnerID:    nil,
		BuyerBankCode:   &buyerBankCode,
		SellerOwnerType: model.OwnerBank,
		SellerOwnerID:   nil,
		SellerBankCode:  &sellerBankCode,
		Ticker:          opt.Stock.Ticker,
		Quantity:        decimal.NewFromInt(opt.Amount),
		StrikePrice:     opt.PricePerUnit.Amount.Decimal,
		PremiumPaid:     decimal.Zero,
		PremiumCurrency: currency,
		StrikeCurrency:  currency,
		SettlementDate:  settle,
		BuyerAccountID:  0,
		SellerAccountID: 0,
		Status:          "active",
		SagaID:          crossbankTxID,
		PremiumPaidAt:   now,
		CrossbankTxID:   &cbTx,
		// Remote-mirror columns.
		RemotePostingIndex:        &pIdx,
		RemoteNegotiationRouting:  &negRouting,
		RemoteNegotiationNativeID: &negNative,
		RemoteDirection:           &dir,
		RemoteBuyerID:             &bID,
		RemoteSellerID:            &sID,
	}
}

// The remoteContract* accessors read the cross-bank fields off a unified
// OptionContract remote row in the value forms the cross-bank handler logic
// expects, dereferencing the nullable pointers (zero values for unset pointers
// — never on a well-formed remote row written by buildRemoteContract).

func remoteContractDirection(c *model.OptionContract) string {
	if c.RemoteDirection != nil {
		return *c.RemoteDirection
	}
	return ""
}

func remoteContractBuyerID(c *model.OptionContract) string {
	if c.RemoteBuyerID != nil {
		return *c.RemoteBuyerID
	}
	return ""
}

func remoteContractSellerID(c *model.OptionContract) string {
	if c.RemoteSellerID != nil {
		return *c.RemoteSellerID
	}
	return ""
}

// remoteContractSellerAccountNumber returns the seller's stored nominated account
// number (the bound account on the local listing), or "" when none was stored.
func remoteContractSellerAccountNumber(c *model.OptionContract) string {
	if c.RemoteSellerAccountNumber != nil {
		return *c.RemoteSellerAccountNumber
	}
	return ""
}

func remoteContractBuyerRouting(c *model.OptionContract) int64 {
	if c.BuyerBankCode != nil {
		if n, err := strconv.ParseInt(*c.BuyerBankCode, 10, 64); err == nil {
			return n
		}
	}
	return 0
}

func remoteContractSellerRouting(c *model.OptionContract) int64 {
	if c.SellerBankCode != nil {
		if n, err := strconv.ParseInt(*c.SellerBankCode, 10, 64); err == nil {
			return n
		}
	}
	return 0
}

func remoteContractNegRouting(c *model.OptionContract) int64 {
	if c.RemoteNegotiationRouting != nil {
		return *c.RemoteNegotiationRouting
	}
	return 0
}

func remoteContractNegNativeID(c *model.OptionContract) string {
	if c.RemoteNegotiationNativeID != nil {
		return *c.RemoteNegotiationNativeID
	}
	return ""
}

// remoteContractQuantityInt returns the contract quantity as the int64 the
// cross-bank wire / settlement paths use. Remote rows always carry whole-unit
// quantities, so IntPart round-trips the stored decimal exactly.
func remoteContractQuantityInt(c *model.OptionContract) int64 {
	return c.Quantity.IntPart()
}

// remoteContractSettlementString formats the contract's settlement date back to
// the RFC3339 string form the SI-TX wire / optionExpired check consume. The
// instant is preserved across the store/read round-trip, so the expiry decision
// is identical to the retired raw-string mirror.
func remoteContractSettlementString(c *model.OptionContract) string {
	return c.SettlementDate.UTC().Format(time.RFC3339)
}

// peerRoutingForCode parses a peer bank code string into its int64 routing.
// A non-numeric code yields 0 (no remote row matches), mirroring the repo's
// tolerant lookup behaviour.
func peerRoutingForCode(peerCode string) int64 {
	n, err := strconv.ParseInt(peerCode, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// remoteBuyer / remoteSeller / remoteOfferJSONOf / remoteNativeIDOf /
// remoteParentOf read the Remote* columns off a unified OTCNegotiation row,
// dereferencing the nullable pointers to the value forms the cross-bank handler
// logic expects (zero values for unset pointers — never on a well-formed remote
// row written by buildRemoteNeg / UpsertRemoteNeg).
func remoteBuyer(n *model.OTCNegotiation) (int64, string) {
	var r int64
	var id string
	if n.RemoteBuyerRouting != nil {
		r = *n.RemoteBuyerRouting
	}
	if n.RemoteBuyerID != nil {
		id = *n.RemoteBuyerID
	}
	return r, id
}

func remoteSeller(n *model.OTCNegotiation) (int64, string) {
	var r int64
	var id string
	if n.RemoteSellerRouting != nil {
		r = *n.RemoteSellerRouting
	}
	if n.RemoteSellerID != nil {
		id = *n.RemoteSellerID
	}
	return r, id
}

// remoteSideAtRouting returns (role, wireID) for whichever side (buyer/seller) of
// the remote chain is hosted at the given routing — used to stamp each revision's
// mover. role is "buyer" or "seller"; ("", "") when neither side matches.
func remoteSideAtRouting(n *model.OTCNegotiation, routing int64) (string, string) {
	bR, bID := remoteBuyer(n)
	sR, sID := remoteSeller(n)
	if routing == bR {
		return "buyer", bID
	}
	if routing == sR {
		return "seller", sID
	}
	return "", ""
}

func remoteOfferJSONOf(n *model.OTCNegotiation) string {
	if n.RemoteOfferJSON != nil {
		return *n.RemoteOfferJSON
	}
	return ""
}

func remoteNativeIDOf(n *model.OTCNegotiation) string {
	if n.NativeID != nil {
		return *n.NativeID
	}
	return ""
}

func protoToOffer(p *stockpb.PeerOtcOffer) contractsitx.OtcOffer {
	pricePerStock, _ := decimal.NewFromString(p.GetPricePerStock())
	premium, _ := decimal.NewFromString(p.GetPremium())
	var lastModBy contractsitx.ForeignBankId
	if p.GetLastModifiedBy() != nil {
		lastModBy = contractsitx.ForeignBankId{
			RoutingNumber: p.GetLastModifiedBy().GetRoutingNumber(),
			ID:            p.GetLastModifiedBy().GetId(),
		}
	}
	var parentOfferID contractsitx.ForeignBankId
	if p.GetParentOfferId() != nil {
		parentOfferID = contractsitx.ForeignBankId{
			RoutingNumber: p.GetParentOfferId().GetRoutingNumber(),
			ID:            p.GetParentOfferId().GetId(),
		}
	}
	return contractsitx.OtcOffer{
		Ticker:             p.GetTicker(),
		Amount:             p.GetAmount(),
		PricePerStock:      pricePerStock,
		Currency:           p.GetCurrency(),
		Premium:            premium,
		PremiumCurrency:    p.GetPremiumCurrency(),
		SettlementDate:     p.GetSettlementDate(),
		LastModifiedBy:     lastModBy,
		ParentOfferID:      parentOfferID,
		BuyerAccountNumber: p.GetBuyerAccountNumber(),
	}
}

func offerToProto(o contractsitx.OtcOffer) *stockpb.PeerOtcOffer {
	return &stockpb.PeerOtcOffer{
		Ticker:          o.Ticker,
		Amount:          o.Amount,
		PricePerStock:   o.PricePerStock.String(),
		Currency:        o.Currency,
		Premium:         o.Premium.String(),
		PremiumCurrency: o.PremiumCurrency,
		SettlementDate:  o.SettlementDate,
		LastModifiedBy: &stockpb.PeerForeignBankId{
			RoutingNumber: o.LastModifiedBy.RoutingNumber,
			Id:            o.LastModifiedBy.ID,
		},
		ParentOfferId: &stockpb.PeerForeignBankId{
			RoutingNumber: o.ParentOfferID.RoutingNumber,
			Id:            o.ParentOfferID.ID,
		},
		BuyerAccountNumber: o.BuyerAccountNumber,
	}
}

// RecordOptionContract is called by transaction-service at COMMIT_TX
// time for each option-asset posting on this bank's routing. Behaviour
// switches on req.intent:
//
//   - "" / "accept" → form a new contract: persist a peer_option_contracts
//     row keyed on (crossbank_tx_id, posting_index) and lock the seller's
//     holdings.
//
//   - "exercise" → transition the existing contract (looked up by
//     OptionDescription.negotiationId + this side's direction) to
//     status="exercised", run role-specific stock ops: seller side
//     consumes the reservation and decrements the holding; buyer side
//     credits a holding for the gained shares.
//
// Idempotent on (crossbank_tx_id, posting_index) for both intents —
// retries return the same contract row without double-effects.
func (h *PeerOTCGRPCHandler) RecordOptionContract(ctx context.Context, req *stockpb.RecordOptionContractRequest) (*stockpb.RecordOptionContractResponse, error) {
	if h.peerOptionRepo == nil {
		return nil, status.Error(codes.Unimplemented, "peer option repo not wired")
	}
	if req.GetCrossbankTxId() == "" || req.GetOptionDescriptionJson() == "" {
		return nil, status.Error(codes.InvalidArgument, "crossbank_tx_id and option_description_json are required")
	}
	if req.GetBuyerId() == nil || req.GetSellerId() == nil {
		return nil, status.Error(codes.InvalidArgument, "buyer_id and seller_id are required")
	}
	if d := req.GetDirection(); d != contractsitx.DirectionDebit && d != contractsitx.DirectionCredit {
		return nil, status.Errorf(codes.InvalidArgument, "direction must be DEBIT or CREDIT, got %q", d)
	}

	var opt contractsitx.OptionDescription
	if err := json.Unmarshal([]byte(req.GetOptionDescriptionJson()), &opt); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "decode option description: %v", err)
	}

	if req.GetIntent() == "exercise" {
		return h.recordOptionExercise(ctx, req, opt)
	}

	// Ingestion collision guard (SP-2a): the remote contract row is keyed on the
	// COUNTERPARTY routing (the side we do NOT host). If that routing equals our
	// own, then both buyer and seller are on THIS bank — this is an intra-bank
	// contract that must go through the local OTC flow, not the cross-bank path.
	// Persisting it as "remote" would alias a local contract and corrupt the
	// local-vs-remote invariant. Reject up-front.
	counterpartyRouting := remoteContractCounterpartyRouting(
		req.GetDirection(),
		req.GetBuyerId().GetRoutingNumber(),
		req.GetSellerId().GetRoutingNumber(),
	)
	if counterpartyRouting == h.ownRouting {
		return nil, status.Errorf(codes.InvalidArgument,
			"RecordOptionContract: counterparty routing %d equals this bank's own routing (%d) — cross-bank contract must involve a different bank on at least one side",
			counterpartyRouting, h.ownRouting)
	}

	row := buildRemoteContract(
		req.GetCrossbankTxId(), req.GetPostingIndex(), opt, req.GetDirection(),
		req.GetBuyerId().GetRoutingNumber(), req.GetBuyerId().GetId(),
		req.GetSellerId().GetRoutingNumber(), req.GetSellerId().GetId(),
	)
	// Sub-case 2 (producer side): on a SELLER-side (DEBIT) contract THIS bank
	// hosts the seller. Resolve the seller's NOMINATED account number (the local
	// listing's InitiatorAccountID) from the originating negotiation and persist it
	// on the row, so the exercise strike credit (read back via
	// LookupPeerOptionContract) lands in the bound account instead of the seller's
	// first active <currency> account. Best-effort: an unresolved nomination leaves
	// it NULL → the executor falls back to participant resolution. Buyer-side
	// (CREDIT) rows never carry the seller's nomination.
	if req.GetDirection() == contractsitx.DirectionDebit && h.sellerAccountResolver != nil {
		if neg, nerr := h.negRepo.GetRemoteNegByNative(opt.NegotiationID.ID); nerr == nil && neg != nil {
			if num := h.sellerAccountResolver.ResolveSellerAccountNumber(ctx, neg, opt.PricePerUnit.Currency); num != "" {
				row.RemoteSellerAccountNumber = &num
			}
		}
	}
	if err := h.peerOptionRepo.UpsertRemoteContract(row); err != nil {
		return nil, status.Errorf(codes.Internal, "persist peer option contract: %v", err)
	}
	rowSellerID := remoteContractSellerID(row)
	rowQuantity := remoteContractQuantityInt(row)

	// Seller-side share lock. Only meaningful when this bank holds the
	// seller (DEBIT direction = seller loses option = our bank tracks
	// the seller). Idempotent on peer_option_contract_id, so safe to
	// retry: a second commit replay finds the existing reservation
	// and returns it without double-locking.
	if req.GetDirection() == contractsitx.DirectionDebit && h.holdingReserver != nil {
		ownerType, ownerID, parseErr := parseSellerOwner(rowSellerID)
		if parseErr != nil {
			// A DEBIT-side contract means this bank holds the seller, so we
			// MUST be able to lock the seller's shares. An unparseable
			// seller_id means we cannot — fail loudly instead of leaving an
			// "active" contract with no holding reservation behind it (the
			// seller's shares would otherwise stay tradeable until exercise).
			// The NEW_TX-time CheckSellerCanDeliver pre-check already rejects
			// unparseable sellers, so reaching here implies data corruption.
			return nil, status.Errorf(codes.Internal,
				"peer-option contract %d: seller_id %q not parseable, cannot lock shares: %v",
				row.ID, rowSellerID, parseErr)
		}
		// Spec-aligned path (Celina-5 OTC SAGA): the shares were already RESERVED
		// at NEW_TX time (vote-YES) keyed on crossbank_tx_id. At COMMIT we simply
		// ATTACH that hold to the freshly-minted contract row — no re-check that
		// could fail because the seller sold in the meantime (they couldn't: the
		// shares were held). The existing consume/release-by-contract-id paths
		// then operate unchanged.
		attached := false
		if cbTx := req.GetCrossbankTxId(); cbTx != "" {
			err := h.holdingReserver.AttachCrossBankReservationToContract(ctx, cbTx, row.ID)
			if err == nil {
				attached = true
			} else if status.Code(err) != codes.NotFound {
				return nil, status.Errorf(codes.Internal,
					"peer-option contract %d: attach vote-time share hold (tx %s): %v", row.ID, cbTx, err)
			}
			// NotFound → no vote-time hold (older NEW_TX before this change, or a
			// transaction-service that didn't reserve) → fall through to the
			// legacy reserve-at-commit below for backward compatibility.
		}
		if !attached {
			if _, err := h.holdingReserver.ReserveForPeerOptionContract(
				ctx, ownerType, ownerID, "stock", row.Ticker, row.ID, rowQuantity,
			); err != nil {
				// Legacy fallback. Reservation failed — e.g. the seller traded the
				// shares away in the window between the NEW_TX vote and this
				// COMMIT-time lock (the very gap the NEW_TX reservation closes).
				// Surface the failure so the SI-TX COMMIT does not ack; both the
				// contract row (idempotent on crossbank_tx_id, posting_index) and
				// the reservation (idempotent on peer_option_contract_id) are
				// replay-safe, so a COMMIT retry re-attempts and heals once shares
				// are available.
				if st, ok := status.FromError(err); ok {
					return nil, st.Err()
				}
				return nil, status.Errorf(codes.Internal,
					"peer-option contract %d: lock seller %s ticker %s qty %d: %v",
					row.ID, rowSellerID, row.Ticker, rowQuantity, err)
			}
		}
	}

	return &stockpb.RecordOptionContractResponse{ContractId: row.ID}, nil
}

// CheckSellerCanDeliver validates that a seller participant has at
// least `quantity` unreserved shares of the requested ticker. Used by
// transaction-service at NEW_TX time to vote NO with INSUFFICIENT_ASSET
// before money moves, instead of degrading silently at COMMIT_TX.
//
// ok=true means the seller has the holding AND
// (quantity - reserved_quantity) >= req.quantity. Any other condition
// (no holding, insufficient available, unparseable seller_id) returns
// ok=false with available_quantity=0. This is information-leak-safe:
// the caller learns "can deliver?" but not how short the seller is or
// whether they exist on this bank at all.
func (h *PeerOTCGRPCHandler) CheckSellerCanDeliver(ctx context.Context, req *stockpb.CheckSellerCanDeliverRequest) (*stockpb.CheckSellerCanDeliverResponse, error) {
	if req.GetSellerId() == nil || req.GetTicker() == "" || req.GetQuantity() <= 0 {
		return nil, status.Error(codes.InvalidArgument, "seller_id, ticker, and positive quantity are required")
	}
	// Fix #8 (2026-05-16, defense) — caller MUST be asking about a
	// seller on THIS bank's routing. The only intended caller
	// (transaction-service posting_executor) pre-filters by ownRouting
	// before invoking us, but we don't rely on that — a future caller
	// that forgets the pre-filter would otherwise silently look up
	// LOCAL client-N's holdings against a foreign seller's request,
	// returning a misleading "can deliver" or "insufficient" verdict.
	if req.GetSellerId().GetRoutingNumber() != h.ownRouting {
		return &stockpb.CheckSellerCanDeliverResponse{Ok: false, AvailableQuantity: 0}, nil
	}
	ownerType, ownerID, parseErr := parseSellerOwner(req.GetSellerId().GetId())
	if parseErr != nil {
		return &stockpb.CheckSellerCanDeliverResponse{Ok: false, AvailableQuantity: 0}, nil
	}
	holding, err := h.holdings.GetByOwnerAndTicker(ownerType, ownerID, "stock", req.GetTicker())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &stockpb.CheckSellerCanDeliverResponse{Ok: false, AvailableQuantity: 0}, nil
		}
		return nil, status.Errorf(codes.Internal, "lookup holding: %v", err)
	}
	available := holding.Quantity - holding.ReservedQuantity
	if available < 0 {
		available = 0
	}
	return &stockpb.CheckSellerCanDeliverResponse{
		Ok:                available >= req.GetQuantity(),
		AvailableQuantity: available,
	}, nil
}

// ValidatePeerOptionMoneyLeg verifies, at NEW_TX (vote) time, that the money a
// sender proposes for an option leg equals THIS bank's own stored terms — never
// trusting the posting amount. For an exercise leg it loads the stored
// peer_option_contract by (negotiation_id, direction) and requires the paired
// money to equal StrikePrice*Quantity (and ticker/quantity/strike to match).
// Closes the forged-strike theft: a peer crafting an exercise that delivers the
// full (trusted) contract.Quantity of shares while crediting the seller an
// under-stated strike. ok=false → the caller (posting executor) votes NO.
//
// Accept-intent legs are not yet enforced here (premium validation against the
// stored negotiation is a follow-up of the same class, lower severity); they
// return ok=true so accept behaviour is unchanged.
func (h *PeerOTCGRPCHandler) ValidatePeerOptionMoneyLeg(ctx context.Context, req *stockpb.ValidatePeerOptionMoneyLegRequest) (*stockpb.ValidatePeerOptionMoneyLegResponse, error) {
	if req.GetNegotiationId() == "" || req.GetDirection() == "" {
		return nil, status.Error(codes.InvalidArgument, "negotiation_id and direction are required")
	}
	deny := func(reason string) (*stockpb.ValidatePeerOptionMoneyLegResponse, error) {
		log.Printf("ValidatePeerOptionMoneyLeg DENY (neg=%d/%s dir=%s intent=%s): %s",
			req.GetNegotiationRouting(), req.GetNegotiationId(), req.GetDirection(), req.GetIntent(), reason)
		return &stockpb.ValidatePeerOptionMoneyLegResponse{Ok: false, Reason: reason}, nil
	}

	money, err := decimal.NewFromString(req.GetMoneyAmount())
	if err != nil {
		return deny("unparseable money_amount: " + req.GetMoneyAmount())
	}

	if !strings.EqualFold(req.GetIntent(), "exercise") {
		// ACCEPT intent: the contract doesn't exist on the receiver yet (minted at
		// COMMIT), so validate the PREMIUM money against the stored NEGOTIATION.
		// Look up by foreign_id alone (the negotiation UUID, identical on both
		// banks and unique per bank) — NOT by peer_bank_code: this validator runs on
		// both the coordinator (sees its OWN routing as the peer code) and the
		// receiver (sees the counterparty), so the peer_bank_code is unreliable here.
		neg, nerr := h.negRepo.GetRemoteNegByNative(req.GetNegotiationId())
		if nerr != nil {
			if errors.Is(nerr, gorm.ErrRecordNotFound) {
				return deny("no stored negotiation for peer/id")
			}
			return nil, status.Errorf(codes.Internal, "lookup negotiation: %v", nerr)
		}
		var offer contractsitx.OtcOffer
		if jerr := json.Unmarshal([]byte(remoteOfferJSONOf(neg)), &offer); jerr != nil {
			return nil, status.Errorf(codes.Internal, "decode offer: %v", jerr)
		}
		// Option terms must match the agreed negotiation (rejects forged ticker/
		// quantity/strike regardless of currency).
		if req.GetQuantity() != offer.Amount {
			return deny(fmt.Sprintf("quantity %d != negotiated %d", req.GetQuantity(), offer.Amount))
		}
		if req.GetTicker() != "" && !strings.EqualFold(req.GetTicker(), offer.Ticker) {
			return deny(fmt.Sprintf("ticker %q != negotiated %q", req.GetTicker(), offer.Ticker))
		}
		if req.GetStrikePrice() != "" {
			if sp, e := decimal.NewFromString(req.GetStrikePrice()); e == nil && !sp.Equal(offer.PricePerStock) {
				return deny(fmt.Sprintf("strike %s != negotiated %s", sp, offer.PricePerStock))
			}
		}
		// Premium money check. The seller ALWAYS receives offer.Premium in
		// offer.PremiumCurrency (no FX on the seller's receipt), so when the money
		// leg is in the premium currency we require an exact match — this covers the
		// seller side of every trade and the buyer side of a same-currency trade.
		//
		// KNOWN RESIDUAL — cross-currency BUYER premium (low severity). When the
		// money leg currency != offer.PremiumCurrency, the buyer paid an FX-converted
		// premium (offer.Premium converted to the buyer's currency at the live rate at
		// accept-compose time). The receiver can't reproduce that exact amount here
		// (re-running the conversion would drift against the rate used at compose and
		// REJECT legitimate accepts), so we only reject a non-positive amount and let
		// any positive value through. This is bounded: the SELLER side is always exact
		// (the underpayment victim), and the option TERMS (ticker/quantity/strike) are
		// validated above regardless of currency — so the worst case is a buyer's own
		// bank accepting an FX-mispriced premium debit, not seller theft or wrong terms.
		// Exercise (the strike) has NO such residual: this codebase never FX-converts
		// the strike, so it is exactly validated in every currency.
		// TODO(crossbank-otc): close this by converting offer.Premium via
		// exchange-service into req.currency and comparing within a small tolerance
		// band (to absorb rate drift between accept-compose and this vote-time check).
		if strings.EqualFold(req.GetCurrency(), offer.PremiumCurrency) {
			if !money.Equal(offer.Premium) {
				return deny(fmt.Sprintf("premium %s != negotiated %s", money, offer.Premium))
			}
		} else if money.LessThanOrEqual(decimal.Zero) {
			return deny("cross-currency premium must be positive")
		}
		return &stockpb.ValidatePeerOptionMoneyLegResponse{Ok: true}, nil
	}

	contract, err := h.peerOptionRepo.GetRemoteContractByNegotiationAndDirection(req.GetNegotiationRouting(), req.GetNegotiationId(), req.GetDirection())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return deny("no stored contract for negotiation/direction")
		}
		return nil, status.Errorf(codes.Internal, "lookup contract: %v", err)
	}
	contractQty := remoteContractQuantityInt(contract)
	contractCurrency := contract.StrikeCurrency
	// Replay/double-exercise defense: only an exercisable contract may move money.
	// "active" (unclaimed) and "exercising" (buyer-side claim) are the valid
	// pre-exercise states; an already-"exercised" (or expired/cancelled) contract
	// must NOT vote YES — otherwise a forged second exercise debits the buyer the
	// strike again while COMMIT's materialise no-ops (no delivery), double-charging
	// the buyer. Mirrors recordOptionExercise's COMMIT-time guard, but at vote time
	// so no money is ever reserved for the replay.
	if contract.Status != "active" && contract.Status != "exercising" {
		return deny("contract not exercisable, status=" + contract.Status)
	}
	if req.GetQuantity() != contractQty {
		return deny(fmt.Sprintf("quantity %d != stored %d", req.GetQuantity(), contractQty))
	}
	if req.GetTicker() != "" && !strings.EqualFold(req.GetTicker(), contract.Ticker) {
		return deny(fmt.Sprintf("ticker %q != stored %q", req.GetTicker(), contract.Ticker))
	}
	if req.GetCurrency() != "" && !strings.EqualFold(req.GetCurrency(), contractCurrency) {
		return deny(fmt.Sprintf("currency %q != stored %q", req.GetCurrency(), contractCurrency))
	}
	if req.GetStrikePrice() != "" {
		if sp, e := decimal.NewFromString(req.GetStrikePrice()); e == nil && !sp.Equal(contract.StrikePrice) {
			return deny(fmt.Sprintf("per-unit strike %s != stored %s", sp, contract.StrikePrice))
		}
	}
	// The crux: the money moved for an exercise MUST equal the agreed
	// StrikePrice * Quantity from THIS bank's stored contract.
	expected := contract.StrikePrice.Mul(decimal.NewFromInt(contractQty))
	if !money.Equal(expected) {
		return deny(fmt.Sprintf("strike money %s != stored %s (%s x %d)", money, expected, contract.StrikePrice, contractQty))
	}
	return &stockpb.ValidatePeerOptionMoneyLegResponse{Ok: true}, nil
}

// LookupPeerOptionContract returns the SELLER-side (DEBIT) peer_option_contract
// this bank holds for a negotiationId, with the stored terms the
// transaction-service executor needs to recognise and settle an OPTION
// pseudo-account exercise leg. The exercise wire pins the OPTION pseudo-account
// id to the negotiationId (spec §2.7.2), whose routingNumber is the
// negotiation's bank — NOT necessarily the seller's. So the executor decides
// ownership of a pseudo-account leg by asking each candidate bank "do you hold
// the SELLER side of this negotiation?" via this RPC: found=true means this bank
// is the settling (seller) bank and should process the leg; found=false means a
// different bank owns it and this bank must SKIP the leg (see the option
// wire-conformance design §3.3.1). Always the DEBIT row — the seller side.
// Read-only; no status mutation here (the gates + settlement run in the executor
// + RecordOptionContract).
func (h *PeerOTCGRPCHandler) LookupPeerOptionContract(_ context.Context, req *stockpb.LookupPeerOptionContractRequest) (*stockpb.LookupPeerOptionContractResponse, error) {
	if h.peerOptionRepo == nil {
		return nil, status.Error(codes.Unimplemented, "peer option repo not wired")
	}
	if req.GetNegotiationId() == "" {
		return nil, status.Error(codes.InvalidArgument, "negotiation_id is required")
	}
	contract, err := h.peerOptionRepo.GetRemoteContractByNegotiationAndDirection(
		req.GetNegotiationRoutingNumber(), req.GetNegotiationId(), contractsitx.DirectionDebit,
	)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// No seller-side row → this bank does not hold the seller side.
			return &stockpb.LookupPeerOptionContractResponse{Found: false}, nil
		}
		return nil, status.Errorf(codes.Internal, "lookup contract: %v", err)
	}
	return &stockpb.LookupPeerOptionContractResponse{
		Found:               true,
		SellerId:            remoteContractSellerID(contract),
		Ticker:              contract.Ticker,
		StrikePrice:         contract.StrikePrice.String(),
		Quantity:            remoteContractQuantityInt(contract),
		Currency:            contract.StrikeCurrency,
		SettlementDate:      remoteContractSettlementString(contract),
		Status:              contract.Status,
		SellerAccountNumber: remoteContractSellerAccountNumber(contract),
	}, nil
}

// ReserveSellerSharesForNewTx places a real HOLD on the seller's shares at
// SI-TX NEW_TX (vote) time, keyed on crossbank_tx_id. Unlike
// CheckSellerCanDeliver this increments reserved_quantity so the shares cannot
// be sold before COMMIT_TX (Celina-5 OTC SAGA step 2). Same routing/seller
// validation as CheckSellerCanDeliver; ok=false on insufficient/missing so the
// caller votes NO with INSUFFICIENT_ASSET. Idempotent on crossbank_tx_id.
func (h *PeerOTCGRPCHandler) ReserveSellerSharesForNewTx(ctx context.Context, req *stockpb.ReserveSellerSharesRequest) (*stockpb.ReserveSellerSharesResponse, error) {
	if req.GetSellerId() == nil || req.GetTicker() == "" || req.GetQuantity() <= 0 || req.GetCrossbankTxId() == "" {
		return nil, status.Error(codes.InvalidArgument, "seller_id, ticker, positive quantity, and crossbank_tx_id are required")
	}
	if h.holdingReserver == nil {
		// No reserver wired — cannot hold shares, so we must not vote YES.
		return &stockpb.ReserveSellerSharesResponse{Ok: false}, nil
	}
	// Must be a seller on THIS bank's routing (mirror of CheckSellerCanDeliver
	// Fix #8 defense — never reserve a local client's shares against a foreign
	// seller's request).
	if req.GetSellerId().GetRoutingNumber() != h.ownRouting {
		return &stockpb.ReserveSellerSharesResponse{Ok: false}, nil
	}
	ownerType, ownerID, parseErr := parseSellerOwner(req.GetSellerId().GetId())
	if parseErr != nil {
		return &stockpb.ReserveSellerSharesResponse{Ok: false}, nil
	}
	res, err := h.holdingReserver.ReserveForCrossBankNewTx(
		ctx, ownerType, ownerID, "stock", req.GetTicker(), req.GetCrossbankTxId(), req.GetQuantity(),
	)
	if err != nil {
		// FailedPrecondition (holding not found / insufficient) → ok=false so the
		// caller votes NO. Other errors propagate.
		if status.Code(err) == codes.FailedPrecondition {
			return &stockpb.ReserveSellerSharesResponse{Ok: false}, nil
		}
		return nil, status.Errorf(codes.Internal, "reserve seller shares: %v", err)
	}
	return &stockpb.ReserveSellerSharesResponse{
		Ok:                true,
		ReservedQuantity:  res.ReservedQuantity,
		AvailableQuantity: res.AvailableQuantity,
	}, nil
}

// ReleaseSellerSharesForNewTx releases a vote-time share hold by crossbank_tx_id
// on ROLLBACK_TX (or a partial NO mid-NEW_TX). Idempotent: missing/non-active
// reservation → released_quantity=0.
func (h *PeerOTCGRPCHandler) ReleaseSellerSharesForNewTx(ctx context.Context, req *stockpb.ReleaseSellerSharesRequest) (*stockpb.ReleaseSellerSharesResponse, error) {
	if req.GetCrossbankTxId() == "" {
		return nil, status.Error(codes.InvalidArgument, "crossbank_tx_id is required")
	}
	if h.holdingReserver == nil {
		return &stockpb.ReleaseSellerSharesResponse{ReleasedQuantity: 0}, nil
	}
	res, err := h.holdingReserver.ReleaseForCrossBankNewTx(ctx, req.GetCrossbankTxId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "release seller shares: %v", err)
	}
	return &stockpb.ReleaseSellerSharesResponse{ReleasedQuantity: res.ReleasedQuantity}, nil
}

// parseSellerOwner maps an SI-TX participant id to the OwnerType + numeric
// owner id used by the holdings / capital-gain tables. Despite its name it
// parses ANY party id (seller OR buyer) — it is called for both sides at the
// call sites. Recognised forms:
//
//   - "bank"          → (OwnerBank, nil): back-compat, a peer may still send
//     the literal "bank" for a bank-owned party.
//   - "employee-<N>"  → (OwnerBank, nil): a peer bank acting as a cross-bank
//     OTC principal publishes itself as "employee-<N>" (SP-3 wire identity).
//     The numeric id is WIRE IDENTITY ONLY — it is intentionally NOT used to
//     look up an employee. Local ownership/settlement (share locks, capital
//     gains, exercise credits) binds the BANK, exactly as for "bank".
//   - "client-<n>"    → (OwnerClient, &n): a client principal on this bank.
//
// Errors on unparseable ids — callers choose whether to fail the RPC or log.
func parseSellerOwner(partyID string) (model.OwnerType, *uint64, error) {
	if partyID == "bank" {
		return model.OwnerBank, nil, nil // back-compat: a peer may still send literal "bank"
	}
	if rest, ok := strings.CutPrefix(partyID, "employee-"); ok {
		if _, err := strconv.ParseUint(rest, 10, 64); err != nil {
			return "", nil, fmt.Errorf("invalid employee party id %q: %w", partyID, err)
		}
		// employee-<N> is SI-TX WIRE IDENTITY only; local ownership/settlement is the
		// BANK. The numeric id is intentionally NOT used to look up an employee — it is
		// kept verbatim in RemoteBuyerID/RemoteSellerID for audit/round-trip.
		return model.OwnerBank, nil, nil
	}
	rest, ok := strings.CutPrefix(partyID, "client-")
	if !ok {
		return "", nil, errors.New("unsupported party id; expected client-<n>, employee-<n>, or bank")
	}
	id, parseErr := strconv.ParseUint(rest, 10, 64)
	if parseErr != nil {
		return "", nil, parseErr
	}
	return model.OwnerClient, &id, nil
}

// recordOptionExercise handles the intent="exercise" branch of
// RecordOptionContract. Looks up the existing peer_option_contracts
// row by (negotiation, direction), validates status, runs the
// role-specific stock operations:
//
//   - DEBIT direction (this bank holds the seller): consume the
//     reservation pinned to the contract id (settle it), which
//     decrements the seller's holding by the contract's quantity.
//
//   - CREDIT direction (this bank holds the buyer): credit a
//     holding for the buyer, creating a new (owner, ticker) row
//     when needed.
//
// Then transitions the contract to status="exercised". Idempotent
// on (crossbank_tx_id, posting_index): repeated calls land on the
// same contract row and the underlying stock ops are themselves
// idempotent (settlements unique on synthetic txn id; credit-to-
// holding is upsert-shaped).
func (h *PeerOTCGRPCHandler) recordOptionExercise(ctx context.Context, req *stockpb.RecordOptionContractRequest, opt contractsitx.OptionDescription) (*stockpb.RecordOptionContractResponse, error) {
	if h.holdingReserver == nil {
		return nil, status.Error(codes.Unimplemented, "holding reserver not wired")
	}
	contract, err := h.peerOptionRepo.GetRemoteContractByNegotiationAndDirection(opt.NegotiationID.RoutingNumber, opt.NegotiationID.ID, req.GetDirection())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.FailedPrecondition, "no active peer_option_contract for this negotiation/direction")
		}
		return nil, status.Errorf(codes.Internal, "lookup contract: %v", err)
	}
	contractQty := remoteContractQuantityInt(contract)
	contractSellerID := remoteContractSellerID(contract)
	contractBuyerID := remoteContractBuyerID(contract)
	contractCurrency := contract.StrikeCurrency
	// Idempotent: if already exercised, just return the existing id.
	if contract.Status == "exercised" {
		return &stockpb.RecordOptionContractResponse{ContractId: contract.ID}, nil
	}
	// "active" (seller/DEBIT side, never claimed) and "exercising" (buyer/CREDIT
	// side, claimed at InitiateOptionExercise to serialise concurrent exercises)
	// are both valid pre-exercise states.
	if contract.Status != "active" && contract.Status != "exercising" {
		return nil, status.Errorf(codes.FailedPrecondition, "cannot exercise contract in status %q", contract.Status)
	}

	switch req.GetDirection() {
	case contractsitx.DirectionDebit:
		// Seller side. Consume the reservation, decrement holding,
		// then record realised P/L for the seller (strike price minus
		// cost basis × qty). The cost basis is snapshotted under the
		// row lock inside ConsumeForPeerOptionContract so this CG
		// write is race-free with concurrent buys/sells on the same
		// holding.
		settle, err := h.holdingReserver.ConsumeForPeerOptionContract(ctx, contract.ID, contractQty)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "consume seller reservation: %v", err)
		}
		// Skip the capital-gain write on a replayed consume — the
		// settlement already existed, so no shares moved this time and a
		// second CapitalGain row would double-count the realised P/L
		// (CapitalGain.Create is not idempotent).
		if !settle.AlreadySettled && h.capitalGainRepo != nil {
			sellerType, sellerID, parseErr := parseSellerOwner(contractSellerID)
			if parseErr != nil {
				log.Printf("WARN: peer-option contract %d exercise: seller_id %q not parseable; capital gain not recorded: %v", contract.ID, contractSellerID, parseErr)
			} else {
				gain := contract.StrikePrice.Sub(settle.AveragePriceBefore).Mul(decimal.NewFromInt(contractQty))
				cg := &model.CapitalGain{
					OwnerType:        sellerType,
					OwnerID:          sellerID,
					OTC:              true,
					SecurityType:     "stock",
					Ticker:           contract.Ticker,
					Quantity:         contractQty,
					BuyPricePerUnit:  settle.AveragePriceBefore,
					SellPricePerUnit: contract.StrikePrice,
					TotalGain:        gain,
					Currency:         contractCurrency,
					TaxYear:          time.Now().Year(),
					TaxMonth:         int(time.Now().Month()),
				}
				if cgErr := h.capitalGainRepo.Create(cg); cgErr != nil {
					log.Printf("WARN: peer-option contract %d exercise: seller capital gain create failed (money/shares already moved): %v", contract.ID, cgErr)
				}
			}
		}
	case contractsitx.DirectionCredit:
		// Buyer side. Credit the buyer's holding for the gained shares.
		// AveragePrice = StrikePrice (the per-share price paid on
		// exercise). Premium paid at acceptance is tracked separately
		// as a SecurityType="option" CapitalGain row written by the
		// acceptance saga — never folded into the stock cost basis,
		// so later stock sells produce the same P/L as a matching
		// market buy at the strike would.
		ownerType, ownerID, parseErr := parseSellerOwner(contractBuyerID)
		if parseErr != nil {
			// The buyer paid the strike (money moved cross-bank at exercise),
			// so failing to credit their shares is delivery failure, not a
			// cosmetic gap. Surface it so the contract is NOT marked exercised
			// and the SI-TX exercise commit retries — silently degrading would
			// leave the buyer paid-but-undelivered with no recovery (Bug 2's
			// exercise-time analog).
			return nil, status.Errorf(codes.Internal,
				"peer-option contract %d exercise: buyer_id %q not parseable, cannot credit shares: %v",
				contract.ID, contractBuyerID, parseErr)
		}
		// Credit the buyer AND flip the contract to "exercised" atomically.
		// The status transition lives inside this call (guarded by a row
		// lock on the contract), so a replayed exercise is a no-op and the
		// buyer's shares are never double-credited. Returns early — the
		// shared SetStatus below is only for the DEBIT path.
		if err := h.holdingReserver.ExerciseBuyerCreditForPeerOption(ctx, contract.ID, ownerType, ownerID, contract.Ticker, contractQty, contract.StrikePrice); err != nil {
			return nil, status.Errorf(codes.Internal,
				"peer-option contract %d exercise: credit buyer holding: %v", contract.ID, err)
		}
		return &stockpb.RecordOptionContractResponse{ContractId: contract.ID}, nil
	}

	if err := h.peerOptionRepo.SetRemoteContractStatus(contract.ID, "exercised"); err != nil {
		return nil, status.Errorf(codes.Internal, "mark exercised: %v", err)
	}
	return &stockpb.RecordOptionContractResponse{ContractId: contract.ID}, nil
}

// InitiateOptionExercise builds the 4-posting exercise Transaction in the spec
// pseudo-account form (MONAS strike: buyer account -> OPTION pseudo-account;
// STOCK: OPTION pseudo-account -> buyer PERSON record) and dispatches it via
// transaction-service. No OPTION asset or intent is carried on the wire.
// Called by the gateway when the buyer hits POST /api/v3/me/otc/contracts/peer/:id/exercise.
//
// Validates: contract exists on this bank, this bank holds the buyer
// side (so this bank is the IB), contract is active. The CompareAndSetStatus
// claim (active → exercising) guards against double-exercise races; on any
// synchronous dispatch failure the claim is reverted (exercising → active)
// so the buyer can retry.
func (h *PeerOTCGRPCHandler) InitiateOptionExercise(ctx context.Context, req *stockpb.InitiateOptionExerciseRequest) (*stockpb.InitiateOptionExerciseResponse, error) {
	if req.GetPeerOptionContractId() == 0 || req.GetBuyerAccountNumber() == "" {
		return nil, status.Error(codes.InvalidArgument, "peer_option_contract_id and buyer_account_number are required")
	}
	contract, err := h.peerOptionRepo.GetRemoteContractByID(req.GetPeerOptionContractId())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Error(codes.NotFound, "contract not found")
		}
		return nil, status.Errorf(codes.Internal, "load contract: %v", err)
	}
	if remoteContractDirection(contract) != contractsitx.DirectionCredit {
		return nil, status.Error(codes.FailedPrecondition, "this bank does not hold the buyer side of the contract; only the buyer's bank can initiate exercise")
	}
	// Expiry pre-check (mirrors the LOCAL exercise path). Reject an exercise on
	// an expired contract BEFORE claiming it. Without this, the buyer's bank
	// claimed (active → exercising) and dispatched the SI-TX; the seller's bank
	// correctly votes NO (optionExpired) so no money moves, but the NO vote is a
	// valid protocol outcome (not a transport error), so the claim was never
	// reverted and the buyer-side contract was left stuck in "exercising"
	// (verified live 2026-06-05). Gating here keeps the contract "active" and
	// returns a clean 409. settlement_date <= today => expired.
	if !contract.SettlementDate.After(time.Now().UTC().Truncate(24 * time.Hour)) {
		return nil, status.Error(codes.FailedPrecondition, "contract has expired (settlement_date <= today)")
	}
	contractQty := remoteContractQuantityInt(contract)
	// Atomically claim the contract for exercise (active → exercising). This is
	// the concurrency guard: of two simultaneous exercise attempts only one wins
	// the compare-and-set, so only one exercise SI-TX is ever dispatched and the
	// buyer is charged the strike exactly once. Without it, both attempts pass a
	// non-locked status read and each settles strike money — a double charge
	// (the share delivery is idempotent, but the money leg was not). On any
	// synchronous dispatch failure below we revert exercising → active so the
	// buyer can retry (e.g. after funding their account).
	claimed, cerr := h.peerOptionRepo.CompareAndSetRemoteContractStatus(contract.ID, "active", "exercising")
	if cerr != nil {
		return nil, status.Errorf(codes.Internal, "claim contract for exercise: %v", cerr)
	}
	if !claimed {
		return nil, status.Errorf(codes.FailedPrecondition,
			"contract status %q is not exercisable (already exercised, expired, or an exercise is already in progress)", contract.Status)
	}

	strikeAmount := contract.StrikePrice.Mul(decimal.NewFromInt(contractQty)).String()
	qty := strconv.FormatInt(contractQty, 10)

	// Build the spec pseudo-account postings for option exercise.
	// The spec expresses exercise as a transaction between the buyer and an
	// OPTION pseudo-account (AccountType=OPTION, AccountId=negotiationId):
	//  1. strike MONAS leaves the buyer's currency account  (DEBIT)
	//  2. strike MONAS arrives at the OPTION pseudo-account (CREDIT — seller bank credits the seller)
	//  3. STOCK leaves the OPTION pseudo-account            (DEBIT — seller bank releases reserved shares)
	//  4. STOCK arrives at the buyer PERSON record          (CREDIT — buyer bank credits the holding)
	negRouting := remoteContractNegRouting(contract)
	negID := remoteContractNegNativeID(contract)
	buyerRouting := remoteContractBuyerRouting(contract)
	buyerID := remoteContractBuyerID(contract)
	sellerRouting := remoteContractSellerRouting(contract)
	postings := []*transactionpb.SiTxPosting{
		// 1. Buyer pays strike (MONAS, from the pinned buyer account).
		{RoutingNumber: buyerRouting, AccountId: req.GetBuyerAccountNumber(), AccountType: contractsitx.AccountTypeAccount, AssetId: contract.StrikeCurrency, AssetType: contractsitx.AssetTypeMonas, Amount: strikeAmount, Direction: contractsitx.DirectionDebit},
		// The OPTION pseudo-account's id IS the negotiationId (spec §2.7.2), so its
		// routingNumber is the negotiation's routing — NOT necessarily the seller's
		// bank. The receiver claims these pseudo-account legs by matching the stored
		// contract (ownership-by-contract), not by routing-prefix; see the option
		// wire-conformance design doc §3.3.1. Do not change to SellerRoutingNumber.
		// 2. Strike arrives at the option pseudo-account (seller bank credits the seller).
		{RoutingNumber: negRouting, AccountId: negID, AccountType: contractsitx.AccountTypeOption, AssetId: contract.StrikeCurrency, AssetType: contractsitx.AssetTypeMonas, Amount: strikeAmount, Direction: contractsitx.DirectionCredit},
		// 3. Underlying leaves the option pseudo-account (seller bank releases reserved shares).
		{RoutingNumber: negRouting, AccountId: negID, AccountType: contractsitx.AccountTypeOption, AssetId: contract.Ticker, AssetType: contractsitx.AssetTypeStock, Amount: qty, Direction: contractsitx.DirectionDebit},
		// 4. Underlying arrives at the buyer (buyer bank credits the holding).
		{RoutingNumber: buyerRouting, AccountId: buyerID, AccountType: contractsitx.AccountTypePerson, AssetId: contract.Ticker, AssetType: contractsitx.AssetTypeStock, Amount: qty, Direction: contractsitx.DirectionCredit},
	}

	resp, err := h.peerTx.InitiateOutboundTxWithPostings(ctx, &transactionpb.SiTxInitiateWithPostingsRequest{
		PeerBankCode: strconv.FormatInt(sellerRouting, 10),
		Postings:     postings,
		TxKind:       "otc-exercise",
	})
	if err != nil {
		// Dispatch failed synchronously (e.g. buyer can't afford the strike →
		// INSUFFICIENT_ASSET) — release the exercise claim so the contract is
		// exercisable again after the buyer funds their account.
		if _, rerr := h.peerOptionRepo.CompareAndSetRemoteContractStatus(contract.ID, "exercising", "active"); rerr != nil {
			log.Printf("WARN: peer-option contract %d: failed to revert exercise claim after dispatch error: %v", contract.ID, rerr)
		}
		// Preserve the underlying gRPC code (FailedPrecondition for a business
		// rejection like insufficient funds) instead of masking it as Internal,
		// so the gateway returns 409 not 500.
		if st, ok := status.FromError(err); ok {
			return nil, status.Errorf(st.Code(), "dispatch exercise: %v", err)
		}
		return nil, status.Errorf(codes.Internal, "dispatch exercise: %v", err)
	}
	return &stockpb.InitiateOptionExerciseResponse{
		TransactionId: resp.GetTransactionId(),
		Status:        resp.GetStatus(),
	}, nil
}
