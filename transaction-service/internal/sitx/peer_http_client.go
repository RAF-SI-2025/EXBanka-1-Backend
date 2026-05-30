package sitx

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	contractsitx "github.com/exbanka/contract/sitx"
)

// PeerHTTPTarget is the per-call target descriptor for the outbound HTTP
// client. Constructed by callers from a peer_banks row.
type PeerHTTPTarget struct {
	BankCode      string // peer's 3-digit code (the recipient)
	RoutingNumber int64  // peer's routing number
	OwnBankCode   string // OUR 3-digit code; surfaced as X-Bank-Code so the
	//                          peer can resolve us in its own peer_banks table
	//                          to validate the HMAC.
	OwnRouting      int64 // our routing number (for envelope's idempotenceKey)
	BaseURL         string
	APIToken        string // plaintext, sent as X-Api-Key
	HMACOutboundKey string // optional; when set, also attach HMAC bundle headers
}

// PeerHTTPClient POSTs SI-TX `Message<Type>` envelopes to peer banks.
// Phase 3 receiver-side handlers are at `<BaseURL>/interbank`.
type PeerHTTPClient struct {
	httpClient *http.Client
}

func NewPeerHTTPClient(httpClient *http.Client) *PeerHTTPClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &PeerHTTPClient{httpClient: httpClient}
}

// PostNewTx sends a NEW_TX envelope and parses the TransactionVote response.
func (c *PeerHTTPClient) PostNewTx(ctx context.Context, target *PeerHTTPTarget, envelope contractsitx.Message[contractsitx.Transaction]) (*contractsitx.TransactionVote, error) {
	resp, err := c.postEnvelope(ctx, target, envelope)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil, fmt.Errorf("peer returned 204 for NEW_TX (expected vote body)")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("peer NEW_TX HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var vote contractsitx.TransactionVote
	if err := json.NewDecoder(resp.Body).Decode(&vote); err != nil {
		return nil, fmt.Errorf("decode vote: %w", err)
	}
	return &vote, nil
}

// PostCommitTx sends a COMMIT_TX envelope and expects 204 No Content.
func (c *PeerHTTPClient) PostCommitTx(ctx context.Context, target *PeerHTTPTarget, envelope contractsitx.Message[contractsitx.CommitTransaction]) error {
	resp, err := c.postEnvelope(ctx, target, envelope)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("peer COMMIT_TX HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// PostRollbackTx sends a ROLLBACK_TX envelope and expects 204 No Content.
func (c *PeerHTTPClient) PostRollbackTx(ctx context.Context, target *PeerHTTPTarget, envelope contractsitx.Message[contractsitx.RollbackTransaction]) error {
	resp, err := c.postEnvelope(ctx, target, envelope)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("peer ROLLBACK_TX HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// CheckStatusResponse is the decoded body of GET /api/v3/interbank/:txID/status.
type CheckStatusResponse struct {
	TransactionID string `json:"transaction_id"`
	State         string `json:"state"`          // "prepared"|"committed"|"rolled_back"|"dead_letter"|"unknown"
	OurRole       string `json:"our_role"`       // "sender"|"receiver"|""
	LastActionAt  string `json:"last_action_at"` // RFC3339
	LastError     string `json:"last_error"`
}

// CheckStatus performs a Celina-5 CHECK_STATUS GET request to the peer's
// /interbank/:txID/status endpoint. Returns the parsed response or an
// error when the peer is unreachable or returns a non-200 status.
func (c *PeerHTTPClient) CheckStatus(ctx context.Context, target *PeerHTTPTarget, txID string) (*CheckStatusResponse, error) {
	url := strings.TrimRight(target.BaseURL, "/") + "/interbank/" + txID + "/status"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("X-Api-Key", target.APIToken)

	if target.HMACOutboundKey != "" {
		nonce, _ := generateNonce()
		ts := time.Now().UTC().Format(time.RFC3339)
		// Sign empty body for GET requests.
		mac := hmac.New(sha256.New, []byte(target.HMACOutboundKey))
		mac.Write([]byte{})
		sig := hex.EncodeToString(mac.Sum(nil))
		req.Header.Set("X-Bank-Code", target.OwnBankCode)
		req.Header.Set("X-Bank-Signature", sig)
		req.Header.Set("X-Timestamp", ts)
		req.Header.Set("X-Nonce", nonce)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("peer CHECK_STATUS HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var result CheckStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &result, nil
}

func (c *PeerHTTPClient) postEnvelope(ctx context.Context, target *PeerHTTPTarget, envelope interface{}) (*http.Response, error) {
	body, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	// base_url is the peer's configured SI-TX prefix — it includes
	// whatever path the peer routes /interbank under (e.g.
	// "http://bank-b.example/api/v3" or "http://bank-c.example/sitx").
	// The outbound client only knows the SI-TX leaf names; the prefix
	// is the registering admin's responsibility so cohort banks with
	// different gateway layouts all interop.
	url := strings.TrimRight(target.BaseURL, "/") + "/interbank"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", target.APIToken)

	if target.HMACOutboundKey != "" {
		nonce, _ := generateNonce()
		ts := time.Now().UTC().Format(time.RFC3339)
		mac := hmac.New(sha256.New, []byte(target.HMACOutboundKey))
		mac.Write(body)
		sig := hex.EncodeToString(mac.Sum(nil))
		// X-Bank-Code identifies the SENDER, not the recipient — the peer
		// uses it to look up our row in its peer_banks table and pick the
		// HMAC key to validate against.
		req.Header.Set("X-Bank-Code", target.OwnBankCode)
		req.Header.Set("X-Bank-Signature", sig)
		req.Header.Set("X-Timestamp", ts)
		req.Header.Set("X-Nonce", nonce)
	}

	return c.httpClient.Do(req)
}

func generateNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
