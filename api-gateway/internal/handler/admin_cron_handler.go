package handler

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/errgroup"

	grpcclients "github.com/exbanka/api-gateway/internal/grpc"
	gatewaykafka "github.com/exbanka/api-gateway/internal/kafka"
	adminpb "github.com/exbanka/contract/adminpb"
)

// AdminCronHandler serves the five /api/v3/admin/crons routes.
// It holds a pool of per-service AdminCron gRPC clients and an audit
// Kafka publisher.
type AdminCronHandler struct {
	clients  []*grpcclients.AdminCronClient
	auditPub *gatewaykafka.AuditProducer
}

// NewAdminCronHandler creates the handler. clients may be nil entries
// (absent services simply produce an "unreachable" entry in List).
func NewAdminCronHandler(clients []*grpcclients.AdminCronClient, pub *gatewaykafka.AuditProducer) *AdminCronHandler {
	return &AdminCronHandler{clients: clients, auditPub: pub}
}

// serviceClient looks up the AdminCronClient by service label (case-sensitive).
func (h *AdminCronHandler) serviceClient(service string) *grpcclients.AdminCronClient {
	for _, c := range h.clients {
		if c != nil && c.Service == service {
			return c
		}
	}
	return nil
}

// cronServiceResult is the per-service fragment of the List response.
type cronServiceResult struct {
	Service string          `json:"service"`
	Status  string          `json:"status"` // "ok" | "unreachable"
	Crons   []*cronInfoJSON `json:"crons"`
	Error   string          `json:"error,omitempty"`
}

// cronInfoJSON is the JSON representation of a CronInfoMsg.
type cronInfoJSON struct {
	Name             string `json:"name"`
	Service          string `json:"service"`
	Description      string `json:"description"`
	Interval         string `json:"interval"`
	CronExpression   string `json:"cron_expression,omitempty"`
	LastStartedAt    string `json:"last_started_at,omitempty"`
	LastFinishedAt   string `json:"last_finished_at,omitempty"`
	LastError        string `json:"last_error,omitempty"`
	NextScheduledAt  string `json:"next_scheduled_at,omitempty"`
	IsPaused         bool   `json:"is_paused"`
	PausedByEmployee int64  `json:"paused_by_employee,omitempty"`
	PausedAt         string `json:"paused_at,omitempty"`
	RunCount         int64  `json:"run_count"`
	ErrorCount       int64  `json:"error_count"`
}

func protoToJSON(m *adminpb.CronInfoMsg) *cronInfoJSON {
	if m == nil {
		return nil
	}
	return &cronInfoJSON{
		Name:             m.GetName(),
		Service:          m.GetService(),
		Description:      m.GetDescription(),
		Interval:         m.GetInterval(),
		CronExpression:   m.GetCronExpression(),
		LastStartedAt:    m.GetLastStartedAt(),
		LastFinishedAt:   m.GetLastFinishedAt(),
		LastError:        m.GetLastError(),
		NextScheduledAt:  m.GetNextScheduledAt(),
		IsPaused:         m.GetIsPaused(),
		PausedByEmployee: m.GetPausedByEmployee(),
		PausedAt:         m.GetPausedAt(),
		RunCount:         m.GetRunCount(),
		ErrorCount:       m.GetErrorCount(),
	}
}

// List godoc
// @Summary      List all crons across services
// @Description  Returns cron job information from every service that exposes the AdminCron gRPC interface. Each service result carries status "ok" or "unreachable". A single unreachable service does NOT fail the whole response.
// @Tags         Admin
// @Security     BearerAuth
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]interface{}
// @Failure      403  {object}  map[string]interface{}
// @Failure      500  {object}  map[string]interface{}
// @Router       /api/v3/admin/crons [get]
func (h *AdminCronHandler) List(c *gin.Context) {
	results := make([]*cronServiceResult, len(h.clients))
	for i := range results {
		if h.clients[i] == nil {
			results[i] = &cronServiceResult{Status: "unreachable"}
			continue
		}
		results[i] = &cronServiceResult{Service: h.clients[i].Service}
	}

	eg, ctx := errgroup.WithContext(c.Request.Context())
	for i, cl := range h.clients {
		i, cl := i, cl
		if cl == nil {
			continue
		}
		eg.Go(func() error {
			resp, err := cl.Client.ListCrons(ctx, &adminpb.ListCronsRequest{})
			if err != nil {
				results[i].Status = "unreachable"
				results[i].Error = err.Error()
				return nil // non-fatal: continue with other services
			}
			crons := make([]*cronInfoJSON, 0, len(resp.GetCrons()))
			for _, cr := range resp.GetCrons() {
				crons = append(crons, protoToJSON(cr))
			}
			results[i].Status = "ok"
			results[i].Crons = crons
			return nil
		})
	}
	// errgroup.Wait() never returns a non-nil error here because we swallow
	// per-service errors above (unreachable → logged in Status field).
	_ = eg.Wait()

	c.JSON(http.StatusOK, gin.H{"services": results})
}

// Get godoc
// @Summary      Get one cron's detail
// @Description  Returns the full CronInfo for a named cron on the specified service.
// @Tags         Admin
// @Security     BearerAuth
// @Produce      json
// @Param        service  path  string  true  "Service name (e.g. stock-service)"
// @Param        name     path  string  true  "Cron name (e.g. tax-collection)"
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]interface{}
// @Failure      403  {object}  map[string]interface{}
// @Failure      404  {object}  map[string]interface{}
// @Failure      500  {object}  map[string]interface{}
// @Router       /api/v3/admin/crons/{service}/{name} [get]
func (h *AdminCronHandler) Get(c *gin.Context) {
	svcName := c.Param("service")
	cronName := c.Param("name")

	cl := h.serviceClient(svcName)
	if cl == nil {
		apiError(c, http.StatusNotFound, ErrNotFound, "service not found or unreachable")
		return
	}

	resp, err := cl.Client.GetCron(c.Request.Context(), &adminpb.GetCronRequest{Name: cronName})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"cron": protoToJSON(resp)})
}

// triggerBody is the optional request body for the Trigger endpoint.
type triggerBody struct {
	Force  bool   `json:"force"`
	Reason string `json:"reason"`
}

// Trigger godoc
// @Summary      Trigger a cron manually
// @Description  Forces an immediate execution of the named cron on the specified service. Optional body: { "force": bool, "reason": string }.
// @Tags         Admin
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        service  path   string       true  "Service name"
// @Param        name     path   string       true  "Cron name"
// @Param        body     body   triggerBody  false "Trigger options"
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]interface{}
// @Failure      403  {object}  map[string]interface{}
// @Failure      404  {object}  map[string]interface{}
// @Failure      500  {object}  map[string]interface{}
// @Router       /api/v3/admin/crons/{service}/{name}/trigger [post]
func (h *AdminCronHandler) Trigger(c *gin.Context) {
	svcName := c.Param("service")
	cronName := c.Param("name")

	var body triggerBody
	_ = c.ShouldBindJSON(&body) // body is optional

	cl := h.serviceClient(svcName)
	if cl == nil {
		apiError(c, http.StatusNotFound, ErrNotFound, "service not found or unreachable")
		return
	}

	employeeID := c.GetInt64("principal_id")

	resp, err := cl.Client.TriggerCron(c.Request.Context(), &adminpb.TriggerRequest{
		Name:        cronName,
		Force:       body.Force,
		TriggeredBy: employeeID,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	h.auditPub.PublishCronAction(context.Background(), "trigger", svcName, cronName, employeeID, body.Reason)

	c.JSON(http.StatusOK, gin.H{"status": resp.GetStatus()})
}

// pauseResumeBody is the optional request body for Pause/Resume.
type pauseResumeBody struct {
	Reason string `json:"reason"`
}

// Pause godoc
// @Summary      Pause a cron
// @Description  Pauses the named cron on the specified service. Optional body: { "reason": string }.
// @Tags         Admin
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        service  path   string           true  "Service name"
// @Param        name     path   string           true  "Cron name"
// @Param        body     body   pauseResumeBody  false "Pause options"
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]interface{}
// @Failure      403  {object}  map[string]interface{}
// @Failure      404  {object}  map[string]interface{}
// @Failure      500  {object}  map[string]interface{}
// @Router       /api/v3/admin/crons/{service}/{name}/pause [post]
func (h *AdminCronHandler) Pause(c *gin.Context) {
	svcName := c.Param("service")
	cronName := c.Param("name")

	var body pauseResumeBody
	_ = c.ShouldBindJSON(&body)

	cl := h.serviceClient(svcName)
	if cl == nil {
		apiError(c, http.StatusNotFound, ErrNotFound, "service not found or unreachable")
		return
	}

	employeeID := c.GetInt64("principal_id")

	resp, err := cl.Client.PauseCron(c.Request.Context(), &adminpb.PauseRequest{
		Name:     cronName,
		PausedBy: employeeID,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	h.auditPub.PublishCronAction(context.Background(), "pause", svcName, cronName, employeeID, body.Reason)

	c.JSON(http.StatusOK, gin.H{"status": resp.GetStatus()})
}

// Resume godoc
// @Summary      Resume a paused cron
// @Description  Resumes a previously paused cron on the specified service. Optional body: { "reason": string }.
// @Tags         Admin
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        service  path   string           true  "Service name"
// @Param        name     path   string           true  "Cron name"
// @Param        body     body   pauseResumeBody  false "Resume options"
// @Success      200  {object}  map[string]interface{}
// @Failure      401  {object}  map[string]interface{}
// @Failure      403  {object}  map[string]interface{}
// @Failure      404  {object}  map[string]interface{}
// @Failure      500  {object}  map[string]interface{}
// @Router       /api/v3/admin/crons/{service}/{name}/resume [post]
func (h *AdminCronHandler) Resume(c *gin.Context) {
	svcName := c.Param("service")
	cronName := c.Param("name")

	var body pauseResumeBody
	_ = c.ShouldBindJSON(&body)

	cl := h.serviceClient(svcName)
	if cl == nil {
		apiError(c, http.StatusNotFound, ErrNotFound, "service not found or unreachable")
		return
	}

	employeeID := c.GetInt64("principal_id")

	resp, err := cl.Client.ResumeCron(c.Request.Context(), &adminpb.ResumeRequest{
		Name:      cronName,
		ResumedBy: employeeID,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	h.auditPub.PublishCronAction(context.Background(), "resume", svcName, cronName, employeeID, body.Reason)

	c.JSON(http.StatusOK, gin.H{"status": resp.GetStatus()})
}
