// Package httpapi serves the Server's REST surface.
package httpapi

import (
	"context"
	"log/slog"
	"math"
	"net/http"
	"time"

	"aagasa/internal/auth"
	"aagasa/internal/catalog"
	"aagasa/internal/domain"
	"aagasa/internal/filestore"
	"aagasa/internal/prediction"
	"aagasa/internal/scheduling"
	"aagasa/internal/store"
)

const checkTimeout = 3 * time.Second

// storeRepository aliases the repository type so handlers read cleanly.
type storeRepository = store.Repository

// Dependencies are what the HTTP layer needs to serve requests.
type Dependencies struct {
	Files      *filestore.Store
	Stores     *store.Stores
	Repository *store.Repository
	Auth       *auth.Service
	Catalog    *catalog.Service
	Scheduling *scheduling.Service
	Prediction *prediction.Client
}

// Server holds the HTTP handlers and their dependencies.
type Server struct {
	logger     *slog.Logger
	version    string
	files      *filestore.Store
	stores     *store.Stores
	repo       *store.Repository
	auth       *auth.Service
	catalog    *catalog.Service
	scheduling *scheduling.Service
	prediction *prediction.Client
}

// NewMux builds the HTTP handler.
func NewMux(logger *slog.Logger, version string, deps Dependencies) http.Handler {
	s := &Server{
		logger: logger, version: version,
		files:  deps.Files,
		stores: deps.Stores, repo: deps.Repository,
		auth: deps.Auth, catalog: deps.Catalog, scheduling: deps.Scheduling,
		prediction: deps.Prediction,
	}

	mux := http.NewServeMux()

	// Health -------------------------------------------------------------
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)

	// Authentication -------------------------------------------------------
	mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", s.requireAuth(s.handleLogout))
	// Reachable while a password change is still owed: it is the way out.
	mux.HandleFunc("POST /api/auth/change-password", s.requireAuth(s.handleChangePassword))
	mux.HandleFunc("GET /api/auth/me", s.requireAuth(s.handleMe))

	// Users ---------------------------------------------------------------
	mux.HandleFunc("GET /api/users", s.roleGated(domain.RoleAdmin, s.handleListUsers))
	mux.HandleFunc("POST /api/users", s.roleGated(domain.RoleAdmin, s.handleCreateUser))
	mux.HandleFunc("PATCH /api/users/{id}", s.roleGated(domain.RoleRoot, s.handleUpdateUser))
	mux.HandleFunc("DELETE /api/users/{id}", s.roleGated(domain.RoleRoot, s.handleDeleteUser))

	// Satellites -----------------------------------------------------------
	mux.HandleFunc("GET /api/satellites", s.authenticated(s.handleListSatellites))
	mux.HandleFunc("GET /api/satellites/{id}", s.authenticated(s.handleGetSatellite))
	mux.HandleFunc("GET /api/satellites/{id}/tles", s.authenticated(s.handleGetSatelliteTLEs))
	mux.HandleFunc("GET /api/satellites/{id}/passes", s.authenticated(s.handlePredictSatellitePasses))
	mux.HandleFunc("POST /api/satellites", s.roleGated(domain.RoleRoot, s.handleAddSatellite))
	mux.HandleFunc("PATCH /api/satellites/{id}", s.roleGated(domain.RoleRoot, s.handleUpdateSatellite))
	mux.HandleFunc("DELETE /api/satellites/{id}", s.roleGated(domain.RoleRoot, s.handleDeleteSatellite))
	mux.HandleFunc("POST /api/satellites/refresh-tles", s.roleGated(domain.RoleAdmin, s.handleRefreshTLEs))
	mux.HandleFunc("POST /api/satellites/{id}/refresh-metadata", s.roleGated(domain.RoleRoot, s.handleRefreshSatelliteMetadata))

	// Passes ---------------------------------------------------------------
	mux.HandleFunc("GET /api/passes", s.authenticated(s.handleListPasses))
	mux.HandleFunc("POST /api/passes", s.authenticated(s.handleRequestPass))
	mux.HandleFunc("GET /api/passes/{id}", s.authenticated(s.handleGetPass))
	mux.HandleFunc("POST /api/passes/{id}/approve", s.roleGated(domain.RoleAdmin, s.handleApprovePass))
	mux.HandleFunc("POST /api/passes/{id}/reject", s.roleGated(domain.RoleAdmin, s.handleRejectPass))
	mux.HandleFunc("POST /api/passes/{id}/cancel", s.authenticated(s.handleCancelPass))
	mux.HandleFunc("GET /api/passes/{id}/plan", s.roleGated(domain.RoleAdmin, s.handleGetPassPlan))

	// Scheduling configuration ----------------------------------------------
	// Readable by Admin, writable by Root only.
	mux.HandleFunc("GET /api/scheduling-config", s.roleGated(domain.RoleAdmin, s.handleGetSchedulingConfig))
	mux.HandleFunc("PUT /api/scheduling-config", s.roleGated(domain.RoleRoot, s.handleUpdateSchedulingConfig))

	// Operational views ------------------------------------------------------
	mux.HandleFunc("GET /api/station", s.roleGated(domain.RoleAdmin, s.handleStationStatus))
	mux.HandleFunc("GET /api/tle-status", s.roleGated(domain.RoleAdmin, s.handleTLEStatus))

	// Station configuration and the audit trail, Root only -------------------
	mux.HandleFunc("GET /api/station/config", s.roleGated(domain.RoleRoot, s.handleGetStationConfig))
	mux.HandleFunc("PATCH /api/station", s.roleGated(domain.RoleRoot, s.handleUpdateStation))
	mux.HandleFunc("PUT /api/station/bands/{band}", s.roleGated(domain.RoleRoot, s.handleUpdateBandConfig))
	mux.HandleFunc("PUT /api/station/hardware", s.roleGated(domain.RoleRoot, s.handleUpdateHardwareConfig))
	mux.HandleFunc("GET /api/audit", s.roleGated(domain.RoleRoot, s.handleListAudit))

	// SatDump pipelines -------------------------------------------------------
	// Anyone may see what is available; uploading a custom one is Admin work.
	mux.HandleFunc("GET /api/pipelines", s.authenticated(s.handleListPipelines))
	mux.HandleFunc("POST /api/pipelines", s.roleGated(domain.RoleAdmin, s.handleCreatePipeline))

	// Recordings -------------------------------------------------------------
	mux.HandleFunc("GET /api/recordings", s.authenticated(s.handleListRecordings))
	mux.HandleFunc("GET /api/passes/{id}/recordings", s.authenticated(s.handleListPassRecordings))
	mux.HandleFunc("GET /api/recordings/{id}/download", s.authenticated(s.handleDownloadRecording))

	// Workers ----------------------------------------------------------------
	mux.HandleFunc("GET /api/workers", s.roleGated(domain.RoleAdmin, s.handleListWorkers))

	// First-run setup -------------------------------------------------------
	mux.HandleFunc("GET /api/setup", s.authenticated(s.handleSetupStatus))
	mux.HandleFunc("POST /api/setup", s.authenticated(s.handleSetup))

	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(s.logger, w, http.StatusOK, map[string]any{
		"status": "ok", "version": s.version,
	})
}

// handleReady reports dependency reachability. It names components and
// booleans only, never addresses or driver errors.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), checkTimeout)
	defer cancel()

	components := map[string]bool{
		"postgres":   s.check(ctx, "postgres", s.stores.PingPostgres),
		"redis":      s.check(ctx, "redis", s.stores.PingRedis),
		"mongodb":    s.check(ctx, "mongodb", s.stores.PingMongo),
		"prediction": s.check(ctx, "prediction", s.prediction.Health),
	}

	ready := true
	for _, ok := range components {
		if !ok {
			ready = false
		}
	}

	code, status := http.StatusOK, "ready"
	if !ready {
		code, status = http.StatusServiceUnavailable, "not_ready"
	}

	response := map[string]any{
		"status": status, "version": s.version, "components": components,
	}
	// Storage is reported but does not decide readiness. A full disk stops
	// uploads; scheduling, approving and browsing all still work, and taking
	// the whole API out of service would help nobody.
	if s.files != nil {
		if usage, err := s.files.RecordingsUsage(); err == nil {
			response["storage"] = map[string]any{
				"recordings_free_bytes":      usage.FreeBytes,
				"recordings_used_percentage": math.Round(usage.UsedPercentage*10) / 10,
				"accepting_uploads":          s.files.EnsureSpaceFor(0) == nil,
			}
		}
	}
	writeJSON(s.logger, w, code, response)
}

func (s *Server) check(ctx context.Context, name string, probe func(context.Context) error) bool {
	if err := probe(ctx); err != nil {
		s.logger.Warn("readiness check failed",
			slog.String("component", name), slog.String("error", err.Error()))
		return false
	}
	return true
}

// audit records a privileged action taken through the API.
func (s *Server) audit(r *http.Request, action, entityType, entityID string, details map[string]any) {
	actor := currentUser(r)
	if _, err := s.repo.AppendAudit(r.Context(), domain.AuditRecord{
		ActorUserID: &actor.ID, Action: action, EntityType: entityType,
		EntityID: entityID, NewState: details,
	}); err != nil {
		s.logger.Error("audit write failed",
			slog.String("action", action), slog.String("error", err.Error()))
	}
}

// auditChange records a configuration change with what it replaced, so the
// trail answers "what was it before" and not only "it changed".
func (s *Server) auditChange(r *http.Request, action, entityType, entityID string,
	previous, next map[string]any) {
	actor := currentUser(r)
	if _, err := s.repo.AppendAudit(r.Context(), domain.AuditRecord{
		ActorUserID: &actor.ID, Action: action, EntityType: entityType,
		EntityID: entityID, PreviousState: previous, NewState: next,
	}); err != nil {
		s.logger.Error("audit write failed",
			slog.String("action", action), slog.String("error", err.Error()))
	}
}
