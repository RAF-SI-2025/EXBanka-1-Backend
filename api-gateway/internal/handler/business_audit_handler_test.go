package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/exbanka/api-gateway/internal/handler"
	notificationpb "github.com/exbanka/contract/notificationpb"
	"github.com/gin-gonic/gin"
)

func TestListBusinessActions_MapsFiltersAndResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var gotReq *notificationpb.ListBusinessAuditLogsRequest
	stub := &stubNotificationClient{
		listBusinessAuditFn: func(in *notificationpb.ListBusinessAuditLogsRequest) (*notificationpb.ListBusinessAuditLogsResponse, error) {
			gotReq = in
			return &notificationpb.ListBusinessAuditLogsResponse{
				Entries: []*notificationpb.BusinessAuditLogEntry{
					{Id: 1, Action: "limit.set", ActorId: 7, TargetType: "employee", TargetId: "9", Detail: "max_single=5000", Timestamp: 1700000000},
				},
				Total: 1, Page: 1, PageSize: 50,
			}, nil
		},
	}
	h := handler.NewAdminAuditHandler(nil, nil, nil, nil, nil, stub, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v3/admin/audit/business-actions?action=limit.set&actor_id=7&target_type=employee", nil)

	h.ListBusinessActions(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
	if gotReq == nil || gotReq.Action != "limit.set" || gotReq.ActorId != 7 || gotReq.TargetType != "employee" {
		t.Fatalf("filters not forwarded: %+v", gotReq)
	}
	var body struct {
		Entries []struct {
			Action  string `json:"action"`
			ActorID int64  `json:"actor_id"`
		} `json:"entries"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Total != 1 || len(body.Entries) != 1 || body.Entries[0].Action != "limit.set" || body.Entries[0].ActorID != 7 {
		t.Fatalf("response mismatch: %+v", body)
	}
}

func TestListBusinessActions_RejectsUnknownAction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := handler.NewAdminAuditHandler(nil, nil, nil, nil, nil, &stubNotificationClient{}, nil)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v3/admin/audit/business-actions?action=bogus", nil)

	h.ListBusinessActions(c)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for unknown action (body=%s)", w.Code, w.Body.String())
	}
}
