// Package service — PeerOTCNegotiationReconciler is the safety-net
// background poller for missed cross-bank negotiation state changes (SP-1 Task 9).
//
// Normal flow: when a peer cancels a negotiation they call our inbound
// DELETE /api/v3/cross-bank-protocol/negotiations/:rid/:id webhook, which
// flips our peer_otc_negotiation.status to "cancelled".  If that webhook
// is lost (network, restart, or peer crash) our row stays "ongoing" forever.
//
// This reconciler ticks every `interval` (default 2 min), lists our local
// "ongoing" rows, and for each row where the COUNTERPARTY bank is
// authoritative (i.e. they issued the ForeignID — identified as outbound
// rows where our ownRouting matches the BUYER's routing and the SELLER's
// routing belongs to the peer), it polls the peer's GET /negotiations/{rid}/{id}.
// If the peer reports non-ongoing (isOngoing=false) with a 2xx response, we
// determine the correct terminal status:
//   - If a local peer_option_contract row exists for this negotiation, the
//     negotiation was accepted — we reconcile our row to "accepted".
//   - Otherwise, we reconcile our row to "cancelled".
//
// This invariant is CRITICAL: an accepted negotiation must NEVER be
// reconciled to "cancelled". The peer wire only exposes isOngoing (not the
// full status), so we use the local contract table as the acceptance oracle.
//
// False-cancel guard: ANY transport error, non-2xx, or JSON parse failure
// on a poll causes that row to be SKIPPED for this tick. We never cancel
// based on ambiguous data.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	contractkafka "github.com/exbanka/contract/kafka"
	transactionpb "github.com/exbanka/contract/transactionpb"
	"github.com/exbanka/stock-service/internal/model"
	"github.com/exbanka/stock-service/internal/repository"
)

// PeerNegStatusFetcher is the narrow HTTP-poll dependency. Defined as a
// function type so tests can inject a fake without standing up an HTTP
// server. The real implementation is newHTTPStatusFetcher.
//
// Returns (isOngoing bool, err error).  err non-nil means "could not
// determine status — skip this row" (false-cancel guard). isOngoing==false
// with err==nil means the peer reports a terminal state.
type PeerNegStatusFetcher func(ctx context.Context, baseURL, apiKey, rid, foreignID string) (isOngoing bool, err error)

// ReconcilerNotifier is the narrow notification dependency. The Kafka
// producer (*kafkaprod.Producer) satisfies it.  nil ⇒ silent (no
// notifications on reconciler-driven cancels).
type ReconcilerNotifier interface {
	PublishGeneralNotification(ctx context.Context, msg contractkafka.GeneralNotificationMessage) error
}

// peerOtcNegRepo is the narrow repo surface the reconciler uses. Satisfied
// by *repository.OTCNegotiationRepository (SP-2a — cross-bank negotiations are
// REMOTE rows in the unified otc_negotiations table). Routing is passed as an
// int64 (the peer's routing), not a bank-code string.
type peerOtcNegRepo interface {
	ListRemoteNegOngoing() ([]model.OTCNegotiation, error)
	UpdateRemoteNegStatus(routing int64, native, status string) error
}

// peerContractChecker is the narrow interface the reconciler uses to determine
// whether a remote option-contract row exists for a given negotiation. When the
// peer reports isOngoing=false, a contract row proves the negotiation was
// ACCEPTED (not cancelled) — so we reconcile our row to "accepted" instead of
// "cancelled". Satisfied by *repository.OptionContractRepository (the unified
// table; remote contracts folded in by SP-2a).
type peerContractChecker interface {
	// HasRemoteContractForNegotiation returns true if any remote option-contract
	// row exists with the given negotiation routing + native id.
	// A DB error is returned as (false, err); not-found is (false, nil).
	HasRemoteContractForNegotiation(negRouting int64, negNative string) (bool, error)
}

// PeerOTCNegotiationReconciler polls every active peer bank for the current
// status of our outbound "ongoing" negotiations, reconciling any that the
// peer has moved to a terminal state.
type PeerOTCNegotiationReconciler struct {
	repo            peerOtcNegRepo
	contractChecker peerContractChecker // nil ⇒ acceptance check skipped (always cancels)
	peerAdmin       transactionpb.PeerBankAdminServiceClient
	fetcher         PeerNegStatusFetcher
	notifier        ReconcilerNotifier // optional; nil ⇒ silent
	ownRouting      int64
	interval        time.Duration
}

// NewPeerOTCNegotiationReconciler constructs the reconciler with a real
// HTTP-based fetcher. Pass httpClient=nil to use the default client with a
// 5-second timeout. contractChecker may be nil (disables the acceptance guard —
// should only be nil in legacy tests; production always passes the real repo).
func NewPeerOTCNegotiationReconciler(
	repo *repository.OTCNegotiationRepository,
	contractChecker peerContractChecker,
	peerAdmin transactionpb.PeerBankAdminServiceClient,
	httpClient *http.Client,
	ownRouting int64,
	interval time.Duration,
) *PeerOTCNegotiationReconciler {
	if interval <= 0 {
		interval = 2 * time.Minute
	}
	client := httpClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &PeerOTCNegotiationReconciler{
		repo:            repo,
		contractChecker: contractChecker,
		peerAdmin:       peerAdmin,
		fetcher:         newHTTPStatusFetcher(client),
		ownRouting:      ownRouting,
		interval:        interval,
	}
}

// WithNotifier wires the optional notification producer. Returns the
// reconciler so callers can chain.
func (r *PeerOTCNegotiationReconciler) WithNotifier(n ReconcilerNotifier) *PeerOTCNegotiationReconciler {
	r.notifier = n
	return r
}

// Run blocks until ctx is cancelled. Runs an initial reconcile immediately,
// then ticks at r.interval. Suitable for standalone use in tests.
func (r *PeerOTCNegotiationReconciler) Run(ctx context.Context) {
	r.reconcile(ctx)
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.reconcile(ctx)
		}
	}
}

// RunOnce executes a single reconcile cycle. Used by the cronreg-gated
// goroutine in main.go so each tick is registered with the cron registry
// for operator visibility and manual triggering.
func (r *PeerOTCNegotiationReconciler) RunOnce(ctx context.Context) {
	r.reconcile(ctx)
}

// reconcile performs one full poll cycle. Per-row errors are logged and
// skipped — the loop never aborts on a single failure.
func (r *PeerOTCNegotiationReconciler) reconcile(ctx context.Context) {
	rows, err := r.repo.ListRemoteNegOngoing()
	if err != nil {
		log.Printf("peer-otc-reconciler: list ongoing failed: %v", err)
		return
	}
	if len(rows) == 0 {
		return
	}

	// Use a per-cycle context with a generous timeout so the full fan-out
	// of HTTP polls doesn't run forever if peers are slow.
	cycleCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Resolve all active peers once per cycle (avoids an RPC per row).
	peerMap, err := r.buildPeerMap(cycleCtx)
	if err != nil {
		log.Printf("peer-otc-reconciler: resolve peers failed: %v", err)
		return
	}

	for i := range rows {
		row := &rows[i]
		if err := r.reconcileRow(cycleCtx, row, peerMap); err != nil {
			log.Printf("peer-otc-reconciler: row peer=%d fid=%s error: %v",
				row.RoutingNumber, reconcilerNativeID(row), err)
		}
	}
}

// reconcilerNativeID / reconcilerRemoteBuyer / reconcilerRemoteSeller read the
// unified remote-row columns the reconciler needs (SP-2a). A REMOTE
// negotiation carries its parties in the Remote* columns and its foreign id in
// NativeID; the row's RoutingNumber is the counterparty/peer routing.
func reconcilerNativeID(n *model.OTCNegotiation) string {
	if n.NativeID != nil {
		return *n.NativeID
	}
	return ""
}

func reconcilerRemoteBuyer(n *model.OTCNegotiation) (int64, string) {
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

func reconcilerRemoteSeller(n *model.OTCNegotiation) (int64, string) {
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

// peerEntry holds resolved base URL + API key for one peer bank.
type peerEntry struct {
	baseURL string
	apiKey  string
}

func (r *PeerOTCNegotiationReconciler) buildPeerMap(ctx context.Context) (map[string]peerEntry, error) {
	list, err := r.peerAdmin.ListPeerBanks(ctx, &transactionpb.ListPeerBanksRequest{ActiveOnly: true})
	if err != nil {
		return nil, err
	}
	out := make(map[string]peerEntry, len(list.GetPeerBanks()))
	for _, p := range list.GetPeerBanks() {
		resp, err := r.peerAdmin.ResolvePeerByBankCode(ctx,
			&transactionpb.ResolvePeerByBankCodeRequest{BankCode: p.GetBankCode()})
		if err != nil {
			log.Printf("peer-otc-reconciler: resolve peer %s failed: %v", p.GetBankCode(), err)
			continue
		}
		full := resp.GetPeerBank()
		if full == nil || !full.GetActive() {
			continue
		}
		out[p.GetBankCode()] = peerEntry{
			baseURL: strings.TrimRight(full.GetBaseUrl(), "/"),
			apiKey:  full.GetApiTokenPlaintext(),
		}
	}
	return out, nil
}

// reconcileRow checks one ongoing row against its authoritative peer bank.
//
// The primary use case is OUTBOUND negotiations: we are the buyer (our
// ownRouting == buyerRoutingNumber) and the peer's bank issued the ForeignID
// (they are authoritative over that negotiation id). We poll the seller's
// (peer's) bank for the current status.
//
// Rows where we are the seller (sellerRoutingNumber == ownRouting) are
// INBOUND negotiations: the peer's bank received the POST from us, we
// issued the ForeignID, and WE are authoritative. These rows are also
// polled (peerRoutingForRow returns the buyer's routing) in case the
// buyer cancelled without hitting our DELETE webhook.
//
// The decision logic:
//  1. Determine the peer routing (the routing number of the counterparty)
//     via peerRoutingForRow. Use it as the rid in the GET request.
//  2. Poll GET /negotiations/{rid}/{id} on the peer. Strict 2xx-only guard.
//  3. If the peer reports isOngoing=true → no change needed.
//  4. If the peer reports isOngoing=false (terminal):
//     a. Check whether a local peer_option_contracts row exists for this
//     negotiation (using NegotiationID == row.ForeignID). If yes, the
//     negotiation was ACCEPTED — reconcile our row to "accepted" (never
//     cancel an accepted negotiation).
//     b. If no contract row exists → reconcile our row to "cancelled".
//
// False-cancel guard: ANY transport error, non-2xx, JSON parse failure, or
// contract-checker error on a poll causes that row to be SKIPPED for this
// tick. We never cancel based on ambiguous data.
func (r *PeerOTCNegotiationReconciler) reconcileRow(
	ctx context.Context,
	row *model.OTCNegotiation,
	peerMap map[string]peerEntry,
) error {
	// The remote row's RoutingNumber IS the counterparty/peer routing (the
	// bank that issued the foreign id); its string form is the peer bank code
	// used to key the resolved-peer map.
	peerBankCode := strconv.FormatInt(row.RoutingNumber, 10)
	foreignID := reconcilerNativeID(row)
	peer, ok := peerMap[peerBankCode]
	if !ok {
		// Peer not active or not reachable — skip silently.
		return nil
	}

	// Determine the rid to use for the GET request. The ForeignID was
	// minted by the peer, so we use the peer's routing number. We derive
	// routing from whichever party (buyer/seller) belongs to the peer (not us).
	peerRouting := r.peerRoutingForRow(row)
	if peerRouting == 0 {
		// Can't determine peer routing — skip.
		return nil
	}

	ridStr := fmt.Sprintf("%d", peerRouting)
	ongoing, err := r.fetcher(ctx, peer.baseURL, peer.apiKey, ridStr, foreignID)
	if err != nil {
		// False-cancel guard: any error ⇒ skip this row.
		return fmt.Errorf("poll peer %s: %w", peerBankCode, err)
	}
	if ongoing {
		// Peer says still ongoing — nothing to do.
		return nil
	}

	// Peer reports terminal status and our local row is still "ongoing".
	// Before marking cancelled, rule out acceptance: if a local
	// peer_option_contracts row exists for this negotiation, the option
	// formation TX was committed — the negotiation was ACCEPTED.
	// An accepted negotiation must never be reconciled to "cancelled".
	targetStatus := "cancelled"
	if r.contractChecker != nil {
		// The remote contract's negotiation routing is the routing number of
		// the bank that issued the negotiation ForeignID, which is the peer
		// routing number for this row.
		hasContract, checkErr := r.contractChecker.HasRemoteContractForNegotiation(peerRouting, foreignID)
		if checkErr != nil {
			// False-cancel guard: contract check error ⇒ skip this row.
			return fmt.Errorf("contract check peer=%s fid=%s: %w", peerBankCode, foreignID, checkErr)
		}
		if hasContract {
			targetStatus = "accepted"
		}
	}

	log.Printf("peer-otc-reconciler: reconciling peer=%s fid=%s → %s (peer reports non-ongoing)",
		peerBankCode, foreignID, targetStatus)
	if err := r.repo.UpdateRemoteNegStatus(row.RoutingNumber, foreignID, targetStatus); err != nil {
		return fmt.Errorf("update status: %w", err)
	}

	// Best-effort notification to the local party. Only send a cancellation
	// notification when the outcome is actually a cancel.
	if targetStatus == "cancelled" {
		r.notifyLocalParty(ctx, row)
	}
	return nil
}

// peerRoutingForRow returns the routing number of the peer bank by choosing
// whichever party routing in the row does NOT match ownRouting. If both sides
// are ownRouting (shouldn't happen in practice), returns 0.
func (r *PeerOTCNegotiationReconciler) peerRoutingForRow(row *model.OTCNegotiation) int64 {
	buyerRouting, _ := reconcilerRemoteBuyer(row)
	sellerRouting, _ := reconcilerRemoteSeller(row)
	if buyerRouting != r.ownRouting {
		return buyerRouting
	}
	if sellerRouting != r.ownRouting {
		return sellerRouting
	}
	// Both sides are ownRouting — intra-bank or malformed row; skip.
	return 0
}

// notifyLocalParty sends a best-effort OTC_OFFER_CANCELLED notification to
// whichever party in the row is a local client (client-N on ownRouting).
func (r *PeerOTCNegotiationReconciler) notifyLocalParty(ctx context.Context, row *model.OTCNegotiation) {
	if r.notifier == nil {
		return
	}
	buyerRouting, buyerID := reconcilerRemoteBuyer(row)
	sellerRouting, sellerID := reconcilerRemoteSeller(row)
	uid, ok := localClientID(r.ownRouting, buyerRouting, buyerID)
	if !ok {
		uid, ok = localClientID(r.ownRouting, sellerRouting, sellerID)
	}
	if !ok || uid == 0 {
		return
	}
	if err := r.notifier.PublishGeneralNotification(ctx, contractkafka.GeneralNotificationMessage{
		UserID:  uid,
		Type:    "OTC_OFFER_CANCELLED",
		Data:    map[string]string{},
		RefType: "otc_negotiation",
		RefID:   row.ID,
	}); err != nil {
		log.Printf("peer-otc-reconciler: notification for user %d failed: %v", uid, err)
	}
}

// localClientID resolves "client-N" → N when routing matches ownRouting.
func localClientID(ownRouting, routing int64, participantID string) (uint64, bool) {
	if routing != ownRouting {
		return 0, false
	}
	const prefix = "client-"
	if !strings.HasPrefix(participantID, prefix) {
		return 0, false
	}
	var id uint64
	if _, err := fmt.Sscanf(participantID[len(prefix):], "%d", &id); err != nil || id == 0 {
		return 0, false
	}
	return id, true
}

// peerNegStatusResponse is the minimal wire shape of GET /negotiations/{rid}/{id}.
// The full OtcNegotiation struct isn't needed — we only care about isOngoing.
type peerNegStatusResponse struct {
	IsOngoing bool `json:"isOngoing"`
}

// newHTTPStatusFetcher returns a PeerNegStatusFetcher backed by the given
// *http.Client. It calls GET {baseURL}/negotiations/{rid}/{id} with
// X-Api-Key authentication and returns (isOngoing, nil) on success, or
// (false, err) on any transport error or non-2xx response (false-cancel
// guard — caller skips the row on error).
func newHTTPStatusFetcher(client *http.Client) PeerNegStatusFetcher {
	return func(ctx context.Context, baseURL, apiKey, rid, foreignID string) (bool, error) {
		url := baseURL + "/negotiations/" + rid + "/" + foreignID
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return false, fmt.Errorf("build request: %w", err)
		}
		req.Header.Set("X-Api-Key", apiKey)

		resp, err := client.Do(req)
		if err != nil {
			return false, fmt.Errorf("http get: %w", err)
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			// Non-2xx — could be a temporary peer error; false-cancel guard.
			return false, fmt.Errorf("peer returned status %d: %s", resp.StatusCode, string(body))
		}

		// Empty-body guard: a truncated or empty response must never
		// deserialise to a zero-value {IsOngoing:false} and trigger a
		// spurious cancel. Treat an empty body as an ambiguous response
		// and skip this row (false-cancel guard).
		if len(body) == 0 {
			return false, fmt.Errorf("peer returned empty body for negotiation %s", foreignID)
		}

		var parsed peerNegStatusResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			return false, fmt.Errorf("parse response: %w", err)
		}
		return parsed.IsOngoing, nil
	}
}
