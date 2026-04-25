// api-gateway/internal/handler/card_handler_test.go
package handler_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	accountpb "github.com/exbanka/contract/accountpb"
	cardpb "github.com/exbanka/contract/cardpb"

	"github.com/exbanka/api-gateway/internal/handler"
)

func cardRouter(h *handler.CardHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	withCtx := func(c *gin.Context) {
		c.Set("user_id", int64(1))
		c.Set("system_type", "employee")
	}
	withClient := func(c *gin.Context) {
		c.Set("user_id", int64(1))
		c.Set("system_type", "client")
	}
	r.POST("/cards", withCtx, h.CreateCard)
	r.GET("/cards/:id", withCtx, h.GetCard)
	r.GET("/cards/account/:account_number", withCtx, h.ListCardsByAccount)
	r.GET("/cards/client/:client_id", withCtx, h.ListCardsByClient)
	r.PUT("/cards/:id/block", withCtx, h.BlockCard)
	r.PUT("/cards/:id/unblock", withCtx, h.UnblockCard)
	r.PUT("/cards/:id/deactivate", withCtx, h.DeactivateCard)
	r.POST("/cards/authorized-person", withCtx, h.CreateAuthorizedPerson)
	r.POST("/me/cards/virtual", withClient, h.CreateVirtualCard)
	r.POST("/cards/:id/pin", withClient, h.SetCardPin)
	r.POST("/cards/:id/verify-pin", withClient, h.VerifyCardPin)
	r.POST("/cards/:id/temporary-block", withClient, h.TemporaryBlockCard)
	r.POST("/cards/:id/client-block", withClient, h.ClientBlockCard)
	r.POST("/cards/requests", withClient, h.CreateCardRequest)
	r.GET("/cards/requests/me", withClient, h.ListMyCardRequests)
	r.GET("/cards/requests", withCtx, h.ListCardRequests)
	r.GET("/cards/requests/:id", withCtx, h.GetCardRequest)
	r.POST("/cards/requests/:id/approve", withCtx, h.ApproveCardRequest)
	r.POST("/cards/requests/:id/reject", withCtx, h.RejectCardRequest)
	r.GET("/me/cards", withClient, h.ListMyCards)
	r.GET("/me/cards/:id", withClient, h.GetMyCard)
	r.GET("/cards", withCtx, h.ListCards)
	return r
}

func TestCard_CreateCard_Success(t *testing.T) {
	cc := &stubCardClient{
		createFn: func(req *cardpb.CreateCardRequest) (*cardpb.CardResponse, error) {
			require.Equal(t, "client", req.OwnerType)
			require.Equal(t, "visa", req.CardBrand)
			return &cardpb.CardResponse{Id: 1}, nil
		},
	}
	h := handler.NewCardHandler(cc, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	body := `{"account_number":"265-1-00","owner_id":1,"owner_type":"client","card_brand":"visa"}`
	req := httptest.NewRequest("POST", "/cards", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestCard_CreateCard_BadOwnerType(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	body := `{"account_number":"265-1-00","owner_id":1,"owner_type":"x"}`
	req := httptest.NewRequest("POST", "/cards", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCard_CreateCard_BadBrand(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	body := `{"account_number":"265-1-00","owner_id":1,"owner_type":"client","card_brand":"weird"}`
	req := httptest.NewRequest("POST", "/cards", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCard_GetCard_Success(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	req := httptest.NewRequest("GET", "/cards/7", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCard_GetCard_BadID(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	req := httptest.NewRequest("GET", "/cards/abc", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCard_ListCardsByAccount(t *testing.T) {
	cc := &stubCardClient{
		listByAccountFn: func(req *cardpb.ListCardsByAccountRequest) (*cardpb.ListCardsResponse, error) {
			require.Equal(t, "265-1-00", req.AccountNumber)
			return &cardpb.ListCardsResponse{Cards: []*cardpb.CardResponse{{Id: 1}}}, nil
		},
	}
	h := handler.NewCardHandler(cc, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	req := httptest.NewRequest("GET", "/cards/account/265-1-00", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCard_ListCardsByClient(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	req := httptest.NewRequest("GET", "/cards/client/5", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCard_ListCardsByClient_BadID(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	req := httptest.NewRequest("GET", "/cards/client/abc", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCard_BlockCard_Success(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	req := httptest.NewRequest("PUT", "/cards/1/block", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCard_BlockCard_BadID(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	req := httptest.NewRequest("PUT", "/cards/abc/block", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCard_ClientBlockCard_OwnerSuccess(t *testing.T) {
	cc := &stubCardClient{
		getFn: func(_ *cardpb.GetCardRequest) (*cardpb.CardResponse, error) {
			return &cardpb.CardResponse{Id: 1, OwnerId: 1}, nil
		},
	}
	h := handler.NewCardHandler(cc, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	req := httptest.NewRequest("POST", "/cards/1/client-block", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCard_ClientBlockCard_NotOwner(t *testing.T) {
	cc := &stubCardClient{
		getFn: func(_ *cardpb.GetCardRequest) (*cardpb.CardResponse, error) {
			return &cardpb.CardResponse{Id: 1, OwnerId: 999}, nil
		},
	}
	h := handler.NewCardHandler(cc, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	req := httptest.NewRequest("POST", "/cards/1/client-block", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestCard_UnblockCard_Success(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	req := httptest.NewRequest("PUT", "/cards/1/unblock", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCard_DeactivateCard_Success(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	req := httptest.NewRequest("PUT", "/cards/1/deactivate", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCard_CreateAuthorizedPerson_Success(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	body := `{"first_name":"A","last_name":"B","account_id":1}`
	req := httptest.NewRequest("POST", "/cards/authorized-person", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestCard_CreateVirtualCard_Success(t *testing.T) {
	acc := &accountFullStub{
		getByNumFn: func(_ *accountpb.GetAccountByNumberRequest) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{OwnerId: 1}, nil
		},
	}
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, acc)
	r := cardRouter(h)
	body := `{"account_number":"265-1-00","card_brand":"visa","usage_type":"single_use","expiry_months":1,"card_limit":"1000.00"}`
	req := httptest.NewRequest("POST", "/me/cards/virtual", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestCard_CreateVirtualCard_BadUsageType(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	body := `{"account_number":"x","card_brand":"visa","usage_type":"weird","expiry_months":1,"card_limit":"1"}`
	req := httptest.NewRequest("POST", "/me/cards/virtual", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCard_CreateVirtualCard_BadExpiry(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	body := `{"account_number":"x","card_brand":"visa","usage_type":"single_use","expiry_months":99,"card_limit":"1"}`
	req := httptest.NewRequest("POST", "/me/cards/virtual", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCard_CreateVirtualCard_MultiUseRequiresMaxUses(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	body := `{"account_number":"x","card_brand":"visa","usage_type":"multi_use","expiry_months":1,"card_limit":"1","max_uses":1}`
	req := httptest.NewRequest("POST", "/me/cards/virtual", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "multi_use cards must have max_uses")
}

func TestCard_CreateVirtualCard_NotOwnerOfAccount(t *testing.T) {
	acc := &accountFullStub{
		getByNumFn: func(_ *accountpb.GetAccountByNumberRequest) (*accountpb.AccountResponse, error) {
			return &accountpb.AccountResponse{OwnerId: 999}, nil
		},
	}
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, acc)
	r := cardRouter(h)
	body := `{"account_number":"x","card_brand":"visa","usage_type":"single_use","expiry_months":1,"card_limit":"1"}`
	req := httptest.NewRequest("POST", "/me/cards/virtual", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestCard_SetCardPin_Success(t *testing.T) {
	cc := &stubCardClient{
		getFn: func(_ *cardpb.GetCardRequest) (*cardpb.CardResponse, error) {
			return &cardpb.CardResponse{Id: 1, OwnerId: 1}, nil
		},
	}
	h := handler.NewCardHandler(cc, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	body := `{"pin":"1234"}`
	req := httptest.NewRequest("POST", "/cards/1/pin", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCard_SetCardPin_BadPin(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	body := `{"pin":"12"}`
	req := httptest.NewRequest("POST", "/cards/1/pin", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCard_SetCardPin_BadID(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	req := httptest.NewRequest("POST", "/cards/abc/pin", strings.NewReader(`{"pin":"1234"}`))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCard_VerifyCardPin_Success(t *testing.T) {
	cc := &stubCardClient{
		getFn: func(_ *cardpb.GetCardRequest) (*cardpb.CardResponse, error) {
			return &cardpb.CardResponse{Id: 1, OwnerId: 1}, nil
		},
	}
	h := handler.NewCardHandler(cc, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	body := `{"pin":"1234"}`
	req := httptest.NewRequest("POST", "/cards/1/verify-pin", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCard_TemporaryBlockCard_Success(t *testing.T) {
	cc := &stubCardClient{
		getFn: func(_ *cardpb.GetCardRequest) (*cardpb.CardResponse, error) {
			return &cardpb.CardResponse{Id: 1, OwnerId: 1}, nil
		},
	}
	h := handler.NewCardHandler(cc, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	body := `{"duration_hours":24,"reason":"Lost"}`
	req := httptest.NewRequest("POST", "/cards/1/temporary-block", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCard_TemporaryBlockCard_BadDuration(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	body := `{"duration_hours":0}`
	req := httptest.NewRequest("POST", "/cards/1/temporary-block", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCard_CreateCardRequest_Success(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	body := `{"account_number":"265-1-00","card_brand":"visa"}`
	req := httptest.NewRequest("POST", "/cards/requests", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code)
}

func TestCard_CreateCardRequest_BadBrand(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	body := `{"account_number":"x","card_brand":"weird"}`
	req := httptest.NewRequest("POST", "/cards/requests", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCard_ListMyCardRequests(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	req := httptest.NewRequest("GET", "/cards/requests/me?page=1&page_size=10", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCard_ListCardRequests_Success(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	req := httptest.NewRequest("GET", "/cards/requests?status=pending", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCard_ListCardRequests_BadStatus(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	req := httptest.NewRequest("GET", "/cards/requests?status=foo", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCard_GetCardRequest_Success(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	req := httptest.NewRequest("GET", "/cards/requests/1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCard_ApproveCardRequest_Success(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	req := httptest.NewRequest("POST", "/cards/requests/1/approve", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCard_ApproveCardRequest_BadID(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	req := httptest.NewRequest("POST", "/cards/requests/abc/approve", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCard_RejectCardRequest_Success(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	body := `{"reason":"insufficient history"}`
	req := httptest.NewRequest("POST", "/cards/requests/1/reject", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCard_RejectCardRequest_MissingReason(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	req := httptest.NewRequest("POST", "/cards/requests/1/reject", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCard_ListMyCards_Success(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	req := httptest.NewRequest("GET", "/me/cards", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCard_GetMyCard_Owner(t *testing.T) {
	cc := &stubCardClient{
		getFn: func(_ *cardpb.GetCardRequest) (*cardpb.CardResponse, error) {
			return &cardpb.CardResponse{Id: 7, OwnerId: 1}, nil
		},
	}
	h := handler.NewCardHandler(cc, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	req := httptest.NewRequest("GET", "/me/cards/7", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCard_GetMyCard_NotOwner(t *testing.T) {
	cc := &stubCardClient{
		getFn: func(_ *cardpb.GetCardRequest) (*cardpb.CardResponse, error) {
			return &cardpb.CardResponse{Id: 7, OwnerId: 999}, nil
		},
	}
	h := handler.NewCardHandler(cc, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	req := httptest.NewRequest("GET", "/me/cards/7", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestCard_ListCards_FilterByClient(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	req := httptest.NewRequest("GET", "/cards?client_id=1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCard_ListCards_FilterByAccount(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	req := httptest.NewRequest("GET", "/cards?account_number=265-1-00", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestCard_ListCards_BothFilters(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	req := httptest.NewRequest("GET", "/cards?client_id=1&account_number=x", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "not both")
}

func TestCard_ListCards_NoFilter(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	req := httptest.NewRequest("GET", "/cards", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCard_ListCards_BadClientID(t *testing.T) {
	h := handler.NewCardHandler(&stubCardClient{}, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	req := httptest.NewRequest("GET", "/cards?client_id=abc", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCard_BlockCard_GRPCError(t *testing.T) {
	cc := &stubCardClient{
		blockFn: func(_ *cardpb.BlockCardRequest) (*cardpb.CardResponse, error) {
			return nil, status.Error(codes.NotFound, "no card")
		},
	}
	h := handler.NewCardHandler(cc, &stubVirtualCardClient{}, &stubCardRequestClient{}, &accountFullStub{})
	r := cardRouter(h)
	req := httptest.NewRequest("PUT", "/cards/1/block", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code)
}
