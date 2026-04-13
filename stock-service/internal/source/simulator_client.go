package source

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const settingKeyAPIKey = "market_simulator_api_key"

// SettingStore is a tiny interface over system_settings so tests can mock it.
// stock-service's SystemSettingRepository will satisfy this via an adapter.
type SettingStore interface {
	Get(key string) (string, error)
	Set(key, value string) error
}

// SimulatorClient handles auth + HTTP against Market-Simulator.
type SimulatorClient struct {
	baseURL  string
	bankName string
	store    SettingStore
	http     *http.Client
	apiKey   string
}

// NewSimulatorClient creates a new SimulatorClient. EnsureRegistered must be
// called before any authenticated requests are made.
func NewSimulatorClient(baseURL, bankName string, store SettingStore) *SimulatorClient {
	return &SimulatorClient{
		baseURL:  baseURL,
		bankName: bankName,
		store:    store,
		http:     &http.Client{Timeout: 10 * time.Second},
	}
}

// EnsureRegistered either validates the stored API key or registers a new bank.
// After a successful call, the client's apiKey field is populated and ready for use.
func (c *SimulatorClient) EnsureRegistered() error {
	existing, _ := c.store.Get(settingKeyAPIKey)
	if existing != "" {
		c.apiKey = existing
		if err := c.validate(); err == nil {
			return nil
		}
		// Fall through to re-register on validation failure.
	}
	return c.register()
}

// validate checks the stored API key against GET /api/banks/me.
func (c *SimulatorClient) validate() error {
	req, err := http.NewRequest("GET", c.baseURL+"/api/banks/me", nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("validate: status %d", resp.StatusCode)
	}
	return nil
}

// register calls POST /api/banks/register to obtain a fresh API key and
// persists it via the SettingStore.
func (c *SimulatorClient) register() error {
	body, _ := json.Marshal(map[string]string{"name": c.bankName})
	req, err := http.NewRequest("POST", c.baseURL+"/api/banks/register", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("register: status %d body %s", resp.StatusCode, string(b))
	}
	var parsed struct {
		Data struct {
			APIKey string `json:"api_key"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		return fmt.Errorf("register: decode: %w", err)
	}
	if parsed.Data.APIKey == "" {
		return fmt.Errorf("register: empty api_key in response")
	}
	c.apiKey = parsed.Data.APIKey
	return c.store.Set(settingKeyAPIKey, c.apiKey)
}

// Do performs an authenticated HTTP request against Market-Simulator.
// It sets the X-API-Key header automatically and retries once on 401 by
// re-registering and replaying the request with a cloned copy.
func (c *SimulatorClient) Do(req *http.Request) (*http.Response, error) {
	req.Header.Set("X-API-Key", c.apiKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		_ = resp.Body.Close()
		if err := c.register(); err != nil {
			return nil, err
		}
		// Clone the request for replay — http.Request is not safe to reuse after
		// the body has been consumed or the response read.
		replay := req.Clone(req.Context())
		replay.Header.Set("X-API-Key", c.apiKey)
		return c.http.Do(replay)
	}
	return resp, nil
}

// URL builds an absolute URL against the simulator base URL.
func (c *SimulatorClient) URL(path string) string { return c.baseURL + path }
