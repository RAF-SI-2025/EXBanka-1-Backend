//go:build integration

package workflows

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/exbanka/test-app/internal/client"
	"github.com/exbanka/test-app/peerbank"
)

// MockPeerPort + MockPeerKey match the docker-compose defaults for
// PEER_222_BASE_URL / PEER_222_INBOUND_KEY / PEER_222_OUTBOUND_KEY.
//
// transaction-service in the dockerized stack reaches the test process
// via host.docker.internal:7777 (extra_hosts in compose). api-gateway
// uses the same key to verify inbound HMAC signatures from the mock.
const (
	MockPeerPort = 7777
	MockPeerKey  = "test-222-key"
	PeerCode     = "222"
	OwnBankCode  = "111"
)

// startMockPeerOnHost spins up the peerbank mock listening on
// MockPeerPort. The dockerized transaction-service reaches it via
// host.docker.internal:MockPeerPort. Returns the mock so the caller can
// configure per-action behaviors before driving the SUT.
func startMockPeerOnHost(t *testing.T) *peerbank.MockPeerBank {
	t.Helper()
	return peerbank.NewOnPort(t, MockPeerPort, MockPeerKey)
}

// interbankReceiverAccount builds an account number whose 3-digit prefix
// is the peer bank's code, so the gateway routes it through the
// inter-bank handler.
func interbankReceiverAccount() string {
	return PeerCode + "0000000005678"
}

// pollInterBankStatus polls GET /api/v3/me/transfers/{txID} until either
// `wantStatus` (or any of `wantTerminal`) is observed, or `deadline`
// expires. Returns the final response body.
func pollInterBankStatus(
	t *testing.T,
	c *client.APIClient,
	txID string,
	wantStatuses []string,
	deadline time.Duration,
) map[string]interface{} {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		resp, err := c.GET("/api/v3/me/transfers/" + txID)
		if err != nil {
			t.Fatalf("poll: %v", err)
		}
		if resp.StatusCode == http.StatusOK {
			gotStatus, _ := resp.Body["status"].(string)
			for _, want := range wantStatuses {
				if gotStatus == want {
					return resp.Body
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for status in %v on tx %s", wantStatuses, txID)
	return nil
}

// initiateInterBankTransfer posts to /api/v3/me/transfers and returns the
// transactionId from the 202 response. Caller must first set up the
// client with a funded source account.
func initiateInterBankTransfer(
	t *testing.T,
	c *client.APIClient,
	fromAccount, toAccount string,
	amountRSD int,
) (txID, status string) {
	t.Helper()
	resp, err := c.POST("/api/v3/me/transfers", map[string]any{
		"from_account_number": fromAccount,
		"to_account_number":   toAccount,
		"amount":              amountRSD,
		"currency":            "RSD",
		"memo":                "interbank test",
	})
	if err != nil {
		t.Fatalf("initiate: %v", err)
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", resp.StatusCode, string(resp.RawBody))
	}
	txID, _ = resp.Body["transactionId"].(string)
	status, _ = resp.Body["status"].(string)
	if txID == "" {
		t.Fatalf("no transactionId in response: %s", string(resp.RawBody))
	}
	return txID, status
}

// peerEnvelope mirrors the SI-TX-PROTO 2024/25 outer envelope; defined
// locally so this test file has no dependency on transaction-service.
type peerEnvelope struct {
	TransactionID    string          `json:"transactionId"`
	Action           string          `json:"action"`
	SenderBankCode   string          `json:"senderBankCode"`
	ReceiverBankCode string          `json:"receiverBankCode"`
	Timestamp        string          `json:"timestamp"`
	Body             json.RawMessage `json:"body"`
}

// peerSignedPost signs an envelope with the mock peer's outbound key
// (which equals `MockPeerKey`, the gateway's inbound verification key for
// peer 222) and POSTs to the SUT's /internal/inter-bank/<path>. Used by
// the "incoming" workflow tests to simulate a peer driving us as
// receiver.
func peerSignedPost(t *testing.T, gatewayURL, path, txID, action string, body any) (*http.Response, []byte) {
	t.Helper()
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	env := peerEnvelope{
		TransactionID:    txID,
		Action:           action,
		SenderBankCode:   PeerCode,
		ReceiverBankCode: OwnBankCode,
		Timestamp:        time.Now().UTC().Format(time.RFC3339),
		Body:             bodyJSON,
	}
	envBytes, _ := json.Marshal(env)

	mac := hmac.New(sha256.New, []byte(MockPeerKey))
	mac.Write(envBytes)
	sig := hex.EncodeToString(mac.Sum(nil))

	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)

	req, _ := http.NewRequest(http.MethodPost,
		strings.TrimRight(gatewayURL, "/")+"/internal/inter-bank/"+strings.TrimLeft(path, "/"),
		bytes.NewReader(envBytes),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Bank-Code", PeerCode)
	req.Header.Set("X-Bank-Signature", sig)
	req.Header.Set("X-Idempotency-Key", txID)
	req.Header.Set("X-Timestamp", env.Timestamp)
	req.Header.Set("X-Nonce", hex.EncodeToString(nonce))

	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp, respBody
}

// requireBalanceEquals fetches the account via the admin API and asserts
// `balance` matches `want` to 4 decimal places. Polls for up to 10s because
// inter-bank Commit returns success before the credit propagates through
// the saga to the account-service ledger.
func requireBalanceEquals(t *testing.T, adminC *client.APIClient, accountNumber, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var got string
	for time.Now().Before(deadline) {
		resp, err := adminC.GET("/api/v3/accounts?account_number=" + accountNumber)
		if err != nil {
			t.Fatalf("get account: %v", err)
		}
		if resp.StatusCode != 200 {
			t.Fatalf("get account status %d: %s", resp.StatusCode, string(resp.RawBody))
		}
		accounts, _ := resp.Body["accounts"].([]any)
		if len(accounts) == 0 {
			t.Fatalf("no accounts found for %s", accountNumber)
		}
		first, _ := accounts[0].(map[string]any)
		got, _ = first["balance"].(string)
		if balanceEquals(got, want) {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Errorf("account %s balance after 10s: got %q, want %q", accountNumber, got, want)
}

// balanceEquals compares decimal strings ignoring trailing-zero noise.
func balanceEquals(a, b string) bool {
	return trimZeros(a) == trimZeros(b)
}

func trimZeros(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s
}

// peerReadyTerms returns a Ready response body that matches what the
// mock would produce for a 1000-RSD test transfer to a same-currency
// receiver. Default test case uses RSD↔RSD so fxRate=1, fees=0.
func peerReadyTermsRSD(amount string) peerbank.ReadyTerms {
	return peerbank.ReadyTerms{
		OriginalAmount:   amount,
		OriginalCurrency: "RSD",
		FinalAmount:      amount,
		FinalCurrency:    "RSD",
		FxRate:           "1",
		Fees:             "0",
		ValidUntil:       time.Now().Add(60 * time.Second).UTC().Format(time.RFC3339),
	}
}

// peerCommittedTermsRSD returns the matching Committed response for the
// Ready terms above.
func peerCommittedTermsRSD(amount string) peerbank.CommittedTerms {
	return peerbank.CommittedTerms{
		CreditedAt:       time.Now().UTC().Format(time.RFC3339),
		CreditedAmount:   amount,
		CreditedCurrency: "RSD",
	}
}

// fmt-import keeper to avoid dropping the import on unused refactors.
var _ = fmt.Sprintf
