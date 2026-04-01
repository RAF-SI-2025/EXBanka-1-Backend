package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	stockpb "github.com/exbanka/contract/stockpb"
)

type TaxHandler struct {
	client stockpb.TaxGRPCServiceClient
}

func NewTaxHandler(client stockpb.TaxGRPCServiceClient) *TaxHandler {
	return &TaxHandler{client: client}
}

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
	c.JSON(http.StatusOK, gin.H{"tax_records": resp.TaxRecords, "total_count": resp.TotalCount})
}

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
