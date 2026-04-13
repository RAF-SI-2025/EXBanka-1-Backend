package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Response wraps an HTTP response with parsed body.
type Response struct {
	StatusCode int
	Body       map[string]interface{}
	RawBody    []byte
}

// APIClient is the HTTP client for the API gateway.
type APIClient struct {
	baseURL    string
	httpClient *http.Client
	token      string // current bearer token
}

// New creates a new APIClient targeting the given gateway URL.
func New(baseURL string) *APIClient {
	return &APIClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// SetToken sets the bearer token for authenticated requests.
func (c *APIClient) SetToken(token string) {
	c.token = token
}

// ClearToken removes the bearer token.
func (c *APIClient) ClearToken() {
	c.token = ""
}

// GET performs an HTTP GET request.
func (c *APIClient) GET(path string) (*Response, error) {
	return c.do("GET", path, nil)
}

// POST performs an HTTP POST request with a JSON body.
func (c *APIClient) POST(path string, body interface{}) (*Response, error) {
	return c.do("POST", path, body)
}

// PUT performs an HTTP PUT request with a JSON body.
func (c *APIClient) PUT(path string, body interface{}) (*Response, error) {
	return c.do("PUT", path, body)
}

// DELETE performs an HTTP DELETE request.
func (c *APIClient) DELETE(path string) (*Response, error) {
	return c.do("DELETE", path, nil)
}

func (c *APIClient) do(method, path string, body interface{}) (*Response, error) {
	var bodyReader io.Reader
	if body != nil {
		jsonBytes, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(jsonBytes)
	}

	req, err := http.NewRequest(method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
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

	// Try to parse as JSON map
	var parsed map[string]interface{}
	if err := json.Unmarshal(rawBody, &parsed); err == nil {
		result.Body = parsed
	}

	return result, nil
}

// Login authenticates and stores the access token.
func (c *APIClient) Login(email, password string) (*Response, error) {
	resp, err := c.POST("/api/v1/auth/login", map[string]string{
		"email":    email,
		"password": password,
	})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == 200 && resp.Body != nil {
		if token, ok := resp.Body["access_token"].(string); ok {
			c.token = token
		}
	}
	return resp, nil
}

// ActivateAccount activates a pending account with the given token and password.
func (c *APIClient) ActivateAccount(activationToken, password string) (*Response, error) {
	return c.POST("/api/v1/auth/activate", map[string]string{
		"token":            activationToken,
		"password":         password,
		"confirm_password": password,
	})
}

// RefreshToken exchanges a refresh token for new token pair.
func (c *APIClient) RefreshToken(refreshToken string) (*Response, error) {
	return c.POST("/api/v1/auth/refresh", map[string]string{
		"refresh_token": refreshToken,
	})
}
