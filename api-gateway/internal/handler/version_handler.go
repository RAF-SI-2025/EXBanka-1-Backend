package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/exbanka/api-gateway/internal/version"
)

// VersionHandler serves the public backend version endpoint. It carries no
// dependencies — the version is baked into the binary at build time.
type VersionHandler struct{}

// NewVersionHandler constructs the version handler.
func NewVersionHandler() *VersionHandler {
	return &VersionHandler{}
}

// GetVersion godoc
// @Summary      Backend version
// @Description  Returns the semantic version of the running backend. Public and unauthenticated, so front-end developers can confirm which backend build they are talking to.
// @Tags         version
// @Produce      json
// @Success      200  {object}  map[string]string  "example: {\"version\": \"1.0.0\"}"
// @Router       /api/v3/version [get]
func (h *VersionHandler) GetVersion(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"version": version.Version})
}
