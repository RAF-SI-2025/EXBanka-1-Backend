package handler

import (
	"net/http"
	"strconv"

	stockpb "github.com/exbanka/contract/stockpb"
	"github.com/gin-gonic/gin"
)

type TaxHandler struct {
	client stockpb.TaxGRPCServiceClient
}

func NewTaxHandler(client stockpb.TaxGRPCServiceClient) *TaxHandler {
	return &TaxHandler{client: client}
}

// ListTaxRecords godoc
// @Summary      List tax records (supervisor)
// @Description  Returns all users with their capital gains tax debt for the current month. Requires tax.manage permission.
// @Tags         tax
// @Produce      json
// @Param        user_type  query  string  false  "Filter by user type (client, actuary)"
// @Param        search     query  string  false  "Search by name"
// @Param        page       query  int     false  "Page number (default 1)"
// @Param        page_size  query  int     false  "Page size (default 10)"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "tax records with total_count"
// @Failure      400  {object}  map[string]string       "validation error"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      403  {object}  map[string]string       "forbidden"
// @Failure      500  {object}  map[string]string       "internal error"
// @Router       /api/tax [get]
func (h *TaxHandler) ListTaxRecords(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))

	userType := c.Query("user_type")
	if userType != "" {
		if _, err := oneOf("user_type", userType, "client", "actuary"); err != nil {
			apiError(c, 400, ErrValidation, err.Error())
			return
		}
	}

	resp, err := h.client.ListTaxRecords(c.Request.Context(), &stockpb.ListTaxRecordsRequest{
		UserType: userType, Search: c.Query("search"), Page: int32(page), PageSize: int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"tax_records": emptyIfNil(resp.TaxRecords), "total_count": resp.TotalCount})
}

// ListMyTaxRecords godoc
// @Summary      List my tax records
// @Description  Returns paginated capital gains tax records and tax balance for the authenticated user (client or employee).
// @Tags         tax
// @Produce      json
// @Param        page       query  int  false  "Page number (default 1)"
// @Param        page_size  query  int  false  "Page size (default 10)"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "records, total_count, tax_paid_this_year, tax_unpaid_this_month"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      500  {object}  map[string]string       "internal error"
// @Router       /api/me/tax [get]
func (h *TaxHandler) ListMyTaxRecords(c *gin.Context) {
	userID, systemType, ok := meIdentity(c)
	if !ok {
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))

	resp, err := h.client.ListUserTaxRecords(c.Request.Context(), &stockpb.ListUserTaxRecordsRequest{
		UserId: userID, SystemType: systemType, Page: int32(page), PageSize: int32(pageSize),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"records":               emptyIfNil(resp.Records),
		"total_count":           resp.TotalCount,
		"tax_paid_this_year":    resp.TaxPaidThisYear,
		"tax_unpaid_this_month": resp.TaxUnpaidThisMonth,
	})
}

// CollectTax godoc
// @Summary      Collect capital gains tax
// @Description  Collects capital gains tax from all users for the current month. Requires tax.manage permission.
// @Tags         tax
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "collected_count, total_collected_rsd, failed_count"
// @Failure      401  {object}  map[string]string       "unauthorized"
// @Failure      403  {object}  map[string]string       "forbidden"
// @Failure      500  {object}  map[string]string       "internal error"
// @Router       /api/tax/collect [post]
func (h *TaxHandler) CollectTax(c *gin.Context) {
	resp, err := h.client.CollectTax(c.Request.Context(), &stockpb.CollectTaxRequest{})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"collected_count":     resp.CollectedCount,
		"total_collected_rsd": resp.TotalCollectedRsd,
		"failed_count":        resp.FailedCount,
	})
}
