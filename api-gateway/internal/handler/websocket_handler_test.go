package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestNewWebSocketHandler(t *testing.T) {
	h := NewWebSocketHandler(nil)
	if h == nil {
		t.Fatal("nil handler")
	}
	if h.connections == nil {
		t.Fatal("connections map nil")
	}
}

func TestPushToUser_NoConnection(t *testing.T) {
	h := NewWebSocketHandler(nil)
	// No connection registered — should be a no-op (return early), no panic.
	h.PushToUser(42, map[string]string{"hi": "there"})
}

func TestPushToUser_MarshalError(t *testing.T) {
	h := NewWebSocketHandler(nil)
	// Register a fake wsConnection so PushToUser proceeds past the
	// connections-map lookup but exits on json.Marshal failure.
	h.connections[42] = &wsConnection{}
	// chan int isn't JSON-encodable — Marshal returns an error.
	h.PushToUser(42, make(chan int))
}

func TestHandleConnect_MissingToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewWebSocketHandler(nil)
	r := gin.New()
	r.GET("/ws", h.HandleConnect)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/ws", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", w.Code)
	}
}

func TestHandleConnect_MalformedAuthHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewWebSocketHandler(nil)
	r := gin.New()
	r.GET("/ws", h.HandleConnect)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Authorization", "NotBearer xxx")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", w.Code)
	}
}

func TestStartKafkaConsumer_CancelsCleanly(t *testing.T) {
	h := NewWebSocketHandler(nil)
	ctx, cancel := context.WithCancel(context.Background())
	h.StartKafkaConsumer(ctx, "127.0.0.1:1")
	// Give the goroutine a moment, then cancel; it should exit on ctx.Err.
	time.Sleep(20 * time.Millisecond)
	cancel()
	time.Sleep(20 * time.Millisecond)
}
