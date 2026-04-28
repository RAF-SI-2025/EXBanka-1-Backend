package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ErrPeerOTCNotFound is returned by the peer client when the remote bank
// reports 404 for a tx-id / contract-id lookup.
var ErrPeerOTCNotFound = errors.New("peer reports unknown OTC saga / contract")

// CrossbankPeerClient sends HMAC-signed requests to a single peer bank's
// internal cross-bank OTC routes. Pattern mirrors transaction-service's
// PeerBankClient (Spec 3) — same headers, same envelope, just different
// path prefix (`/internal/inter-bank/otc/*` instead of `/transfer/*`).
type CrossbankPeerClient struct {
	ownBankCode      string
	receiverBankCode string
	outboundKey      []byte
	baseURL          string
	httpClient       *http.Client
}

func NewCrossbankPeerClient(ownBankCode, receiverBankCode, outboundKey, baseURL string, timeout time.Duration) *CrossbankPeerClient {
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	return &CrossbankPeerClient{
		ownBankCode:      ownBankCode,
		receiverBankCode: receiverBankCode,
		outboundKey:      []byte(outboundKey),
		baseURL:          baseURL,
		httpClient:       &http.Client{Timeout: timeout},
	}
}

// CrossbankPeerRouter resolves a peer bank code to its CrossbankPeerClient.
type CrossbankPeerRouter interface {
	ClientFor(bankCode string) (*CrossbankPeerClient, error)
}

// StaticCrossbankPeerRouter is the production router — a static map.
type StaticCrossbankPeerRouter struct {
	clients map[string]*CrossbankPeerClient
}

func NewStaticCrossbankPeerRouter(clients map[string]*CrossbankPeerClient) *StaticCrossbankPeerRouter {
	return &StaticCrossbankPeerRouter{clients: clients}
}

func (r *StaticCrossbankPeerRouter) ClientFor(bankCode string) (*CrossbankPeerClient, error) {
	c, ok := r.clients[bankCode]
	if !ok {
		return nil, fmt.Errorf("no cross-bank OTC peer client configured for %q", bankCode)
	}
	return c, nil
}

// ---------- Discovery: PeerListOffers ----------

// PeerListOffersBody is the body for /otc/list-offers — discovery per
// Celina-5 §Dobavljanje OTC ponuda druge banke.
type PeerListOffersBody struct {
	Since         string `json:"since,omitempty"`
	RequesterRole string `json:"requesterRole,omitempty"`
	SelfBankCode  string `json:"selfBankCode"`
}

// PeerOfferItem is the slim shape returned by peer-list-offers — enough
// to render in the discovery list. Full detail comes via PeerFetchOffer.
type PeerOfferItem struct {
	OfferID        uint64 `json:"offerId"`
	BankCode       string `json:"bankCode"`
	StockID        uint64 `json:"stockId"`
	Direction      string `json:"direction"`
	Quantity       string `json:"quantity"`
	StrikePrice    string `json:"strikePrice"`
	Premium        string `json:"premium"`
	SettlementDate string `json:"settlementDate"`
	Status         string `json:"status"`
	UpdatedAt      string `json:"updatedAt"`
}

type PeerListOffersResponseBody struct {
	Offers []PeerOfferItem `json:"offers"`
}

// PeerListOffers asks the remote bank for its public offers.
func (c *CrossbankPeerClient) PeerListOffers(ctx context.Context, since, requesterRole string) (*PeerListOffersResponseBody, error) {
	body := PeerListOffersBody{Since: since, RequesterRole: requesterRole, SelfBankCode: c.ownBankCode}
	var out PeerListOffersResponseBody
	idemKey := "list-" + c.receiverBankCode + "-" + since
	if err := c.do(ctx, "/otc/list-offers", idemKey, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------- Phase-2 RESERVE_SHARES ----------

type PeerReserveSharesRequest struct {
	TxID           string `json:"txId"`
	SagaKind       string `json:"sagaKind"`
	OfferID        uint64 `json:"offerId"`
	ContractID     uint64 `json:"contractId,omitempty"`
	AssetListingID uint64 `json:"assetListingId"`
	Quantity       string `json:"quantity"`
	BuyerBankCode  string `json:"buyerBankCode"`
	SellerBankCode string `json:"sellerBankCode"`
}

type PeerReserveSharesResponse struct {
	Confirmed     bool   `json:"confirmed"`
	ReservationID string `json:"reservationId,omitempty"`
	FailReason    string `json:"failReason,omitempty"`
}

func (c *CrossbankPeerClient) ReserveShares(ctx context.Context, req PeerReserveSharesRequest) (*PeerReserveSharesResponse, error) {
	var out PeerReserveSharesResponse
	if err := c.do(ctx, "/otc/reserve-shares", req.TxID, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------- Phase-4 TRANSFER_OWNERSHIP ----------

type PeerTransferOwnershipRequest struct {
	TxID                 string `json:"txId"`
	ContractID           uint64 `json:"contractId"`
	AssetListingID       uint64 `json:"assetListingId"`
	Quantity             string `json:"quantity"`
	FromBankCode         string `json:"fromBankCode"`
	FromClientIDExternal string `json:"fromClientIdExternal"`
	ToBankCode           string `json:"toBankCode"`
	ToClientIDExternal   string `json:"toClientIdExternal"`
}

type PeerTransferOwnershipResponse struct {
	Confirmed  bool   `json:"confirmed"`
	AssignedAt string `json:"assignedAt,omitempty"`
	Serial     string `json:"serial,omitempty"`
	FailReason string `json:"failReason,omitempty"`
}

func (c *CrossbankPeerClient) TransferOwnership(ctx context.Context, req PeerTransferOwnershipRequest) (*PeerTransferOwnershipResponse, error) {
	var out PeerTransferOwnershipResponse
	if err := c.do(ctx, "/otc/transfer-ownership", req.TxID, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------- Phase-5 FINALIZE ----------

type PeerFinalizeRequest struct {
	TxID                   string `json:"txId"`
	ContractID             uint64 `json:"contractId"`
	OfferID                uint64 `json:"offerId"`
	BuyerBankCode          string `json:"buyerBankCode"`
	SellerBankCode         string `json:"sellerBankCode"`
	BuyerClientIDExternal  string `json:"buyerClientIdExternal"`
	SellerClientIDExternal string `json:"sellerClientIdExternal"`
	StrikePrice            string `json:"strikePrice"`
	Quantity               string `json:"quantity"`
	Premium                string `json:"premium"`
	Currency               string `json:"currency"`
	SettlementDate         string `json:"settlementDate"`
	SagaKind               string `json:"sagaKind"`
}

type PeerFinalizeResponse struct {
	OK bool `json:"ok"`
}

func (c *CrossbankPeerClient) Finalize(ctx context.Context, req PeerFinalizeRequest) (*PeerFinalizeResponse, error) {
	var out PeerFinalizeResponse
	if err := c.do(ctx, "/otc/finalize", req.TxID, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------- Compensation: ROLLBACK_SHARES ----------

type PeerRollbackSharesRequest struct {
	TxID       string `json:"txId"`
	ContractID uint64 `json:"contractId"`
}

type PeerRollbackSharesResponse struct {
	OK bool `json:"ok"`
}

func (c *CrossbankPeerClient) RollbackShares(ctx context.Context, req PeerRollbackSharesRequest) (*PeerRollbackSharesResponse, error) {
	var out PeerRollbackSharesResponse
	if err := c.do(ctx, "/otc/rollback-shares", req.TxID, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------- Recovery: CHECK_STATUS ----------

type PeerCheckStatusRequest struct {
	TxID  string `json:"txId"`
	Phase string `json:"phase,omitempty"`
}

type PeerCheckStatusResponse struct {
	Status      string `json:"status"`
	ErrorReason string `json:"errorReason,omitempty"`
	NotFound    bool   `json:"notFound"`
}

func (c *CrossbankPeerClient) CheckStatus(ctx context.Context, req PeerCheckStatusRequest) (*PeerCheckStatusResponse, error) {
	var out PeerCheckStatusResponse
	if err := c.do(ctx, "/otc/saga-check-status", req.TxID, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------- Expire ----------

type PeerContractExpireRequest struct {
	TxID       string `json:"txId"`
	ContractID uint64 `json:"contractId"`
}

type PeerContractExpireResponse struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
}

func (c *CrossbankPeerClient) ContractExpire(ctx context.Context, req PeerContractExpireRequest) (*PeerContractExpireResponse, error) {
	var out PeerContractExpireResponse
	if err := c.do(ctx, "/otc/contract-expire", req.TxID, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ---------- HMAC-signed POST ----------

func (c *CrossbankPeerClient) do(ctx context.Context, path, idempotencyKey string, body, out any) error {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Bank-Code", c.ownBankCode)
	httpReq.Header.Set("X-Idempotency-Key", idempotencyKey)
	httpReq.Header.Set("X-Timestamp", time.Now().UTC().Format(time.RFC3339))
	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return fmt.Errorf("generate nonce: %w", err)
	}
	httpReq.Header.Set("X-Nonce", hex.EncodeToString(nonceBytes))
	mac := hmac.New(sha256.New, c.outboundKey)
	mac.Write(bodyBytes)
	httpReq.Header.Set("X-Bank-Signature", hex.EncodeToString(mac.Sum(nil)))

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBytes, _ := io.ReadAll(resp.Body)

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrPeerOTCNotFound
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusBadRequest:
		return fmt.Errorf("peer rejected (HTTP %d): %s", resp.StatusCode, string(respBytes))
	case resp.StatusCode >= 500:
		return fmt.Errorf("peer 5xx (HTTP %d): %s", resp.StatusCode, string(respBytes))
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("peer unexpected status %d: %s", resp.StatusCode, string(respBytes))
	}

	if out != nil && len(respBytes) > 0 {
		if err := json.Unmarshal(respBytes, out); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}
	}
	return nil
}
