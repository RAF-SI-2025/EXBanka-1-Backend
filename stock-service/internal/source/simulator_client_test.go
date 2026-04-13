package source_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/exbanka/stock-service/internal/source"
)

// fakeSettingStore is an in-memory SettingStore for testing.
type fakeSettingStore struct {
	data map[string]string
}

func newFakeSettingStore() *fakeSettingStore {
	return &fakeSettingStore{data: map[string]string{}}
}

func (f *fakeSettingStore) Get(key string) (string, error) { return f.data[key], nil }
func (f *fakeSettingStore) Set(key, value string) error    { f.data[key] = value; return nil }

func TestSimulatorClient_RegistersWhenKeyMissing(t *testing.T) {
	registered := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/banks/register":
			require.Equal(t, "POST", r.Method)
			registered = true
			_, _ = w.Write([]byte(`{"data":{"id":1,"name":"ExBanka","api_key":"ms_testkey"}}`))
		default:
			http.Error(w, "unexpected path: "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer server.Close()

	store := newFakeSettingStore()
	c := source.NewSimulatorClient(server.URL, "ExBanka", store)
	require.NoError(t, c.EnsureRegistered())
	require.True(t, registered, "expected POST /api/banks/register to be called")
	require.Equal(t, "ms_testkey", store.data["market_simulator_api_key"])
}

func TestSimulatorClient_ReusesStoredKey(t *testing.T) {
	validated := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/banks/me" {
			validated = true
			require.Equal(t, "ms_existing", r.Header.Get("X-API-Key"))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"id": 1, "name": "ExBanka"},
			})
			return
		}
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()

	store := newFakeSettingStore()
	store.data["market_simulator_api_key"] = "ms_existing"
	c := source.NewSimulatorClient(server.URL, "ExBanka", store)
	require.NoError(t, c.EnsureRegistered())
	require.True(t, validated, "expected GET /api/banks/me to be called")
}

func TestSimulatorClient_ReRegistersOnInvalidKey(t *testing.T) {
	registered := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/banks/me":
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		case "/api/banks/register":
			registered = true
			_, _ = w.Write([]byte(`{"data":{"id":1,"name":"ExBanka","api_key":"ms_new"}}`))
		default:
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	store := newFakeSettingStore()
	store.data["market_simulator_api_key"] = "ms_stale"
	c := source.NewSimulatorClient(server.URL, "ExBanka", store)
	require.NoError(t, c.EnsureRegistered())
	require.True(t, registered, "expected re-registration after 401")
	require.Equal(t, "ms_new", store.data["market_simulator_api_key"])
}

func TestSimulatorClient_DoRetriesOn401(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/banks/register":
			_, _ = w.Write([]byte(`{"data":{"id":1,"name":"ExBanka","api_key":"ms_refreshed"}}`))
		case "/api/market/stocks":
			calls++
			if calls == 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			require.Equal(t, "ms_refreshed", r.Header.Get("X-API-Key"))
			_, _ = w.Write([]byte(`{"data":[],"pagination":{"page":1,"per_page":20,"total":0}}`))
		default:
			http.Error(w, "unexpected", http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	store := newFakeSettingStore()
	store.data["market_simulator_api_key"] = "ms_stale"
	c := source.NewSimulatorClient(server.URL, "ExBanka", store)
	require.NoError(t, c.EnsureRegistered())

	req, _ := http.NewRequest("GET", c.URL("/api/market/stocks"), nil)
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, 2, calls, "expected first call to 401, second to succeed")
}
