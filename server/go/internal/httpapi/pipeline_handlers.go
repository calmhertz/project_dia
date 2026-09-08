package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"aagasa/internal/store"
)

// SatDump pipelines (spec.md section 19.1).
//
// Two kinds. Standard pipelines are whatever the station's SatDump provides,
// reported by the Worker when it registers; the Server has no SatDump of its
// own and does not invent them. Custom pipelines are JSON an operator
// uploads, validated for structure, stored on disk and carried whole inside
// the PassPlan so the Worker needs nothing from the Server mid-pass.
//
// There is no pipeline editor here, and a pipeline is never inferred from a
// satellite name.

// maxPipelineBytes bounds an upload. SatDump pipeline definitions are small;
// anything larger is a mistake or an attempt.
const maxPipelineBytes = 256 << 10

type pipelineView struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	IsCustom bool   `json:"is_custom"`
	Checksum string `json:"checksum_sha256,omitempty"`
}

// handleListPipelines returns what a pass may be given.
func (s *Server) handleListPipelines(w http.ResponseWriter, r *http.Request) {
	pipelines, err := s.repo.ListPipelines(r.Context())
	if err != nil {
		s.internalError(w, "list pipelines", err)
		return
	}

	views := make([]pipelineView, 0, len(pipelines))
	for _, pipeline := range pipelines {
		views = append(views, pipelineView{
			ID: pipeline.ID.String(), Name: pipeline.Name,
			IsCustom: pipeline.IsCustom, Checksum: pipeline.ChecksumSHA256,
		})
	}
	writeJSON(s.logger, w, http.StatusOK, map[string]any{
		"pipelines": views,
		// Nothing to choose from means no Worker has reported an
		// installation yet, which reads differently from an empty catalogue.
		"reported_by_station": len(views) > 0,
	})
}

type createPipelineRequest struct {
	Name string `json:"name"`
	// Definition is the pipeline JSON itself, as text.
	Definition string `json:"definition"`
}

// handleCreatePipeline accepts a custom pipeline JSON.
//
// Validation is structural only: that it parses, that it is an object, and
// that it names at least one pipeline. SatDump's own semantics are its
// business, and pretending to validate them here would be a lie
// (server-spec section 21).
func (s *Server) handleCreatePipeline(w http.ResponseWriter, r *http.Request) {
	var request createPipelineRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request", "malformed request body")
		return
	}

	name := strings.TrimSpace(request.Name)
	if name == "" {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request", "name is required")
		return
	}
	if len(request.Definition) == 0 {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request",
			"definition is required")
		return
	}
	if len(request.Definition) > maxPipelineBytes {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_request",
			"the pipeline definition is too large")
		return
	}
	if message, ok := validatePipelineJSON(request.Definition); !ok {
		writeError(s.logger, w, http.StatusBadRequest, "invalid_pipeline", message)
		return
	}

	if s.files == nil {
		// A Server without file storage cannot keep a custom definition, and
		// crashing on it would be worse than saying so.
		writeError(s.logger, w, http.StatusServiceUnavailable, "storage_unavailable",
			"this server cannot store pipeline definitions")
		return
	}

	sum := sha256.Sum256([]byte(request.Definition))
	checksum := hex.EncodeToString(sum[:])

	// The row is created first so the file is named by its identity, and a
	// failed write leaves no unreferenced content behind.
	actor := currentUser(r)
	created, err := s.repo.CreatePipeline(r.Context(), store.Pipeline{
		Name: name, IsCustom: true, ChecksumSHA256: checksum,
		DefinitionPath: "pending",
	}, &actor.ID)
	if err != nil {
		s.writeDomainError(w, "create pipeline", err)
		return
	}

	path, err := s.files.WritePipeline(created.ID.String(), []byte(request.Definition))
	if err != nil {
		s.internalError(w, "write pipeline", err)
		return
	}
	if err := s.repo.SetPipelineDefinitionPath(r.Context(), created.ID, path); err != nil {
		s.internalError(w, "record pipeline path", err)
		return
	}

	s.audit(r, "pipeline.uploaded", "pipeline", created.ID.String(), map[string]any{
		"name": name, "checksum_sha256": checksum,
		"size_bytes": len(request.Definition),
	})

	writeJSON(s.logger, w, http.StatusCreated, pipelineView{
		ID: created.ID.String(), Name: name, IsCustom: true, Checksum: checksum,
	})
}

// validatePipelineJSON checks the shape a SatDump pipeline file must have.
//
// A SatDump pipeline file is an object keyed by pipeline identifier, each
// value describing one pipeline. Anything else cannot be run, whatever
// SatDump would make of the contents.
func validatePipelineJSON(definition string) (string, bool) {
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal([]byte(definition), &parsed); err != nil {
		return "the definition is not valid JSON", false
	}
	if len(parsed) == 0 {
		return "the definition names no pipeline", false
	}
	for identifier, body := range parsed {
		if strings.TrimSpace(identifier) == "" {
			return "a pipeline identifier is empty", false
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(body, &fields); err != nil {
			return "pipeline " + identifier + " is not an object", false
		}
	}
	return "", true
}
