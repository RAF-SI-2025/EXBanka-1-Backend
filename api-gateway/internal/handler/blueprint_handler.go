package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/exbanka/api-gateway/internal/middleware"
	userpb "github.com/exbanka/contract/userpb"
)

// createBlueprintBody is the request body for creating a blueprint.
type createBlueprintBody struct {
	Name        string          `json:"name" binding:"required" example:"BasicTeller"`
	Description string          `json:"description" example:"Default teller blueprint"`
	Type        string          `json:"type" binding:"required" example:"employee"`
	Values      json.RawMessage `json:"values" binding:"required"`
}

// updateBlueprintBody is the request body for updating a blueprint.
type updateBlueprintBody struct {
	Name        string          `json:"name" example:"BasicTeller"`
	Description string          `json:"description" example:"Default teller blueprint"`
	Values      json.RawMessage `json:"values"`
}

// applyBlueprintBody is the request body for applying a blueprint.
type applyBlueprintBody struct {
	TargetID int64 `json:"target_id" binding:"required" example:"123"`
}

// BlueprintHandler handles REST endpoints for limit blueprints.
type BlueprintHandler struct {
	client userpb.BlueprintServiceClient
}

// NewBlueprintHandler constructs a BlueprintHandler.
func NewBlueprintHandler(client userpb.BlueprintServiceClient) *BlueprintHandler {
	return &BlueprintHandler{client: client}
}

// ListBlueprints godoc
// @Summary      List limit blueprints
// @Description  Returns all limit blueprints, optionally filtered by type
// @Tags         blueprints
// @Produce      json
// @Param        type  query  string  false  "Filter by type (employee, actuary, client)"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "list of blueprints"
// @Failure      400  {object}  map[string]interface{}  "invalid type filter"
// @Failure      401  {object}  map[string]interface{}  "unauthorized"
// @Failure      500  {object}  map[string]interface{}  "error"
// @Router       /api/v1/blueprints [get]
func (h *BlueprintHandler) ListBlueprints(c *gin.Context) {
	bpType := c.Query("type")
	if bpType != "" {
		var err error
		bpType, err = oneOf("type", bpType, "employee", "actuary", "client")
		if err != nil {
			apiError(c, 400, ErrValidation, err.Error())
			return
		}
	}

	resp, err := h.client.ListBlueprints(c.Request.Context(), &userpb.ListBlueprintsRequest{
		Type: bpType,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"blueprints": emptyIfNil(resp.Blueprints)})
}

// CreateBlueprint godoc
// @Summary      Create a limit blueprint
// @Description  Creates a new named limit blueprint
// @Tags         blueprints
// @Accept       json
// @Produce      json
// @Param        body  body  createBlueprintBody  true  "Blueprint definition"
// @Security     BearerAuth
// @Success      201  {object}  map[string]interface{}  "created blueprint"
// @Failure      400  {object}  map[string]interface{}  "invalid input"
// @Failure      401  {object}  map[string]interface{}  "unauthorized"
// @Failure      409  {object}  map[string]interface{}  "duplicate name+type"
// @Failure      500  {object}  map[string]interface{}  "error"
// @Router       /api/v1/blueprints [post]
func (h *BlueprintHandler) CreateBlueprint(c *gin.Context) {
	var body createBlueprintBody
	if err := c.ShouldBindJSON(&body); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	bpType, err := oneOf("type", body.Type, "employee", "actuary", "client")
	if err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	resp, err := h.client.CreateBlueprint(c.Request.Context(), &userpb.CreateBlueprintRequest{
		Name:        body.Name,
		Description: body.Description,
		Type:        bpType,
		ValuesJson:  string(body.Values),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusCreated, resp)
}

// GetBlueprint godoc
// @Summary      Get a limit blueprint
// @Description  Returns a single blueprint by ID
// @Tags         blueprints
// @Produce      json
// @Param        id  path  int  true  "Blueprint ID"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "blueprint"
// @Failure      400  {object}  map[string]interface{}  "invalid id"
// @Failure      401  {object}  map[string]interface{}  "unauthorized"
// @Failure      404  {object}  map[string]interface{}  "not found"
// @Failure      500  {object}  map[string]interface{}  "error"
// @Router       /api/v1/blueprints/{id} [get]
func (h *BlueprintHandler) GetBlueprint(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid blueprint id")
		return
	}

	resp, err := h.client.GetBlueprint(c.Request.Context(), &userpb.GetBlueprintRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// UpdateBlueprint godoc
// @Summary      Update a limit blueprint
// @Description  Updates an existing blueprint's name, description, or values
// @Tags         blueprints
// @Accept       json
// @Produce      json
// @Param        id    path  int                  true  "Blueprint ID"
// @Param        body  body  updateBlueprintBody  true  "Updated fields"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "updated blueprint"
// @Failure      400  {object}  map[string]interface{}  "invalid input"
// @Failure      401  {object}  map[string]interface{}  "unauthorized"
// @Failure      404  {object}  map[string]interface{}  "not found"
// @Failure      500  {object}  map[string]interface{}  "error"
// @Router       /api/v1/blueprints/{id} [put]
func (h *BlueprintHandler) UpdateBlueprint(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid blueprint id")
		return
	}

	var body updateBlueprintBody
	if err := c.ShouldBindJSON(&body); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	resp, err := h.client.UpdateBlueprint(c.Request.Context(), &userpb.UpdateBlueprintRequest{
		Id:          id,
		Name:        body.Name,
		Description: body.Description,
		ValuesJson:  string(body.Values),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// DeleteBlueprint godoc
// @Summary      Delete a limit blueprint
// @Description  Deletes a blueprint by ID (does not affect already-applied limits)
// @Tags         blueprints
// @Produce      json
// @Param        id  path  int  true  "Blueprint ID"
// @Security     BearerAuth
// @Success      204  "deleted"
// @Failure      400  {object}  map[string]interface{}  "invalid id"
// @Failure      401  {object}  map[string]interface{}  "unauthorized"
// @Failure      404  {object}  map[string]interface{}  "not found"
// @Failure      500  {object}  map[string]interface{}  "error"
// @Router       /api/v1/blueprints/{id} [delete]
func (h *BlueprintHandler) DeleteBlueprint(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid blueprint id")
		return
	}

	_, err = h.client.DeleteBlueprint(c.Request.Context(), &userpb.DeleteBlueprintRequest{Id: id})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ApplyBlueprint godoc
// @Summary      Apply a blueprint to a target
// @Description  Copies blueprint limit values to the target entity (employee, actuary, or client based on blueprint type)
// @Tags         blueprints
// @Accept       json
// @Produce      json
// @Param        id    path  int                 true  "Blueprint ID"
// @Param        body  body  applyBlueprintBody  true  "Target to apply to"
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}  "applied"
// @Failure      400  {object}  map[string]interface{}  "invalid input"
// @Failure      401  {object}  map[string]interface{}  "unauthorized"
// @Failure      404  {object}  map[string]interface{}  "blueprint not found"
// @Failure      500  {object}  map[string]interface{}  "error"
// @Router       /api/v1/blueprints/{id}/apply [post]
func (h *BlueprintHandler) ApplyBlueprint(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		apiError(c, 400, ErrValidation, "invalid blueprint id")
		return
	}

	var body applyBlueprintBody
	if err := c.ShouldBindJSON(&body); err != nil {
		apiError(c, 400, ErrValidation, err.Error())
		return
	}

	if body.TargetID <= 0 {
		apiError(c, 400, ErrValidation, "target_id must be positive")
		return
	}

	_, err = h.client.ApplyBlueprint(middleware.GRPCContextWithChangedBy(c), &userpb.ApplyBlueprintRequest{
		BlueprintId: id,
		TargetId:    body.TargetID,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "blueprint applied successfully"})
}
