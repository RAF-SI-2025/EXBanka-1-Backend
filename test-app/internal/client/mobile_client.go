package client

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// MobileAPIClient extends APIClient with mobile device authentication.
// It adds X-Device-ID, X-Device-Timestamp, and X-Device-Signature headers
// for signed requests to mobile-protected endpoints.
type MobileAPIClient struct {
	*APIClient
	DeviceID     string
	DeviceSecret string // hex-encoded secret
}

// NewMobile creates a MobileAPIClient from an existing APIClient with device credentials.
func NewMobile(base *APIClient, deviceID, deviceSecret string) *MobileAPIClient {
	return &MobileAPIClient{
		APIClient:    base,
		DeviceID:     deviceID,
		DeviceSecret: deviceSecret,
	}
}

// SignedGET performs an HTTP GET with mobile device signature headers.
func (m *MobileAPIClient) SignedGET(path string) (*Response, error) {
	return m.doSigned("GET", path, nil)
}

// SignedPOST performs an HTTP POST with mobile device signature headers.
func (m *MobileAPIClient) SignedPOST(path string, body interface{}) (*Response, error) {
	return m.doSigned("POST", path, body)
}

func (m *MobileAPIClient) doSigned(method, path string, body interface{}) (*Response, error) {
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
	}

	ts, sig := m.sign(method, path, bodyBytes)

	var bodyReader io.Reader
	if bodyBytes != nil {
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequest(method, m.baseURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if m.token != "" {
		req.Header.Set("Authorization", "Bearer "+m.token)
	}
	req.Header.Set("X-Device-ID", m.DeviceID)
	req.Header.Set("X-Device-Timestamp", ts)
	req.Header.Set("X-Device-Signature", sig)

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	result := &Response{
		StatusCode: resp.StatusCode,
		RawBody:    rawBody,
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(rawBody, &parsed); err == nil {
		result.Body = parsed
	}

	return result, nil
}

func (m *MobileAPIClient) sign(method, path string, body []byte) (timestamp, signature string) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)

	if body == nil {
		body = []byte{}
	}
	h := sha256.Sum256(body)
	bodyHash := hex.EncodeToString(h[:])

	payload := fmt.Sprintf("%s:%s:%s:%s", ts, method, path, bodyHash)

	secret, _ := hex.DecodeString(m.DeviceSecret)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	sig := hex.EncodeToString(mac.Sum(nil))

	return ts, sig
}
