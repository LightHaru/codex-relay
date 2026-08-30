package control

// This file contains the small, stable control-plane contract used by local
// health/management tooling. It intentionally does not expose the native
// account-shaped responses: credentials, e-mail identities, filesystem
// paths, upstream payloads and account-specific failure text stay behind the
// mux and its isolated Codex homes.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/LightHaru/codex-relay/internal/mux"
)

const controlSchemaVersion = "relay.control.v1"

type controlCheck struct {
	Status string `json:"status"`
}

type controlLivenessResponse struct {
	SchemaVersion string                  `json:"schemaVersion"`
	Status        string                  `json:"status"`
	OK            bool                    `json:"ok"`
	GeneratedAt   int64                   `json:"generatedAt"`
	Checks        map[string]controlCheck `json:"checks"`
}

type controlPoolSummary struct {
	PoolID                    string  `json:"poolId"`
	Revision                  uint64  `json:"revision"`
	Health                    string  `json:"health"`
	ActiveLeaseCount          int     `json:"activeLeaseCount"`
	ConnectedSubscriptions    int     `json:"connectedSubscriptions"`
	KnownSubscriptions        int     `json:"knownSubscriptions"`
	UnknownSubscriptions      int     `json:"unknownSubscriptions"`
	AvailableSubscriptions    int     `json:"availableSubscriptions"`
	DepletedSubscriptions     int     `json:"depletedSubscriptions"`
	MaximumPercent            float64 `json:"maximumPercent"`
	ConfirmedRemainingPercent float64 `json:"confirmedRemainingPercent"`
	ConfirmedUsedPercent      float64 `json:"confirmedUsedPercent"`
	NextResetAt               int64   `json:"nextResetAt,omitempty"`
	QuotaUpdatedAt            int64   `json:"quotaUpdatedAt,omitempty"`
}

type controlSessionSummary struct {
	ActiveTurnCount   int `json:"activeTurnCount"`
	RecoveryTaskCount int `json:"recoveryTaskCount"`
}

type controlDiagnosticEvent struct {
	Type            string `json:"type"`
	Timestamp       int64  `json:"timestamp"`
	RouteGeneration uint64 `json:"routeGeneration,omitempty"`
	ReasonCode      string `json:"reasonCode,omitempty"`
	Result          string `json:"result,omitempty"`
}

type controlDiagnostics struct {
	EventCount int                      `json:"eventCount"`
	Events     []controlDiagnosticEvent `json:"events"`
}

type controlSnapshotResponse struct {
	SchemaVersion string                `json:"schemaVersion"`
	GeneratedAt   int64                 `json:"generatedAt"`
	Pool          controlPoolSummary    `json:"pool"`
	Session       controlSessionSummary `json:"session"`
	Diagnostics   *controlDiagnostics   `json:"diagnostics,omitempty"`
}

func (s *Server) controlLiveness(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(response)
		return
	}
	now := time.Now().UnixMilli()
	writeJSON(response, http.StatusOK, controlLivenessResponse{
		SchemaVersion: controlSchemaVersion,
		Status:        "live",
		OK:            true,
		GeneratedAt:   now,
		Checks:        map[string]controlCheck{"process": {Status: "ok"}},
	})
}

func (s *Server) controlReadiness(response http.ResponseWriter, request *http.Request) {
	if !s.authorized(request) {
		writeJSON(response, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	if request.Method != http.MethodGet {
		methodNotAllowed(response)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 5*time.Second)
	defer cancel()
	snapshot, err := s.controlSnapshotValue(ctx, false)
	if err != nil {
		writeJSON(response, http.StatusServiceUnavailable, map[string]any{
			"schemaVersion": controlSchemaVersion,
			"status":        "not_ready",
			"ok":            false,
			"reason":        "router_unavailable",
		})
		return
	}
	status := readinessStatus(snapshot.Pool)
	code := http.StatusOK
	if status != "ready" {
		code = http.StatusServiceUnavailable
	}
	writeJSON(response, code, map[string]any{
		"schemaVersion": controlSchemaVersion,
		"status":        status,
		"ok":            status == "ready",
		"generatedAt":   snapshot.GeneratedAt,
		"pool":          snapshot.Pool,
		"session":       snapshot.Session,
	})
}

func readinessStatus(pool controlPoolSummary) string {
	if pool.Health == "healthy" && pool.AvailableSubscriptions > 0 {
		return "ready"
	}
	if pool.ConnectedSubscriptions > 0 || pool.UnknownSubscriptions > 0 || pool.Health == "depleted" || pool.Health == "transient-error" {
		return "degraded"
	}
	return "not_ready"
}

func (s *Server) controlSnapshot(response http.ResponseWriter, request *http.Request) {
	if !s.authorized(request) {
		writeJSON(response, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	if request.Method != http.MethodGet {
		methodNotAllowed(response)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 10*time.Second)
	defer cancel()
	snapshot, err := s.controlSnapshotValue(ctx, true)
	if err != nil {
		writeJSON(response, http.StatusServiceUnavailable, map[string]any{"error": "control_plane_unavailable"})
		return
	}
	writeJSON(response, http.StatusOK, snapshot)
}

func (s *Server) controlPool(response http.ResponseWriter, request *http.Request) {
	if !s.authorized(request) {
		writeJSON(response, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	if request.Method != http.MethodGet {
		methodNotAllowed(response)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 10*time.Second)
	defer cancel()
	snapshot, err := s.controlSnapshotValue(ctx, false)
	if err != nil {
		writeJSON(response, http.StatusServiceUnavailable, map[string]any{"error": "control_plane_unavailable"})
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"schemaVersion": controlSchemaVersion,
		"generatedAt":   snapshot.GeneratedAt,
		"pool":          snapshot.Pool,
	})
}

func (s *Server) controlDiagnostics(response http.ResponseWriter, request *http.Request) {
	if !s.authorized(request) {
		writeJSON(response, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	if request.Method != http.MethodGet {
		methodNotAllowed(response)
		return
	}
	limit := 50
	if raw := strings.TrimSpace(request.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 200 {
			writeJSON(response, http.StatusBadRequest, map[string]any{"error": "limit must be between 1 and 200"})
			return
		}
		limit = parsed
	}
	ctx, cancel := context.WithTimeout(request.Context(), 10*time.Second)
	defer cancel()
	if s.mux == nil {
		writeJSON(response, http.StatusServiceUnavailable, map[string]any{"error": "control_plane_unavailable"})
		return
	}
	status := s.mux.RouterStatus(ctx)
	snapshot := controlSnapshotResponse{GeneratedAt: time.Now().UnixMilli()}
	diagnostics := diagnosticsFromTimeline(status.Timeline, limit)
	writeJSON(response, http.StatusOK, map[string]any{
		"schemaVersion": controlSchemaVersion,
		"generatedAt":   snapshot.GeneratedAt,
		"diagnostics":   diagnostics,
	})
}

func (s *Server) controlSnapshotValue(ctx context.Context, includeDiagnostics bool) (controlSnapshotResponse, error) {
	if s.mux == nil {
		return controlSnapshotResponse{}, fmt.Errorf("multiplexer is nil")
	}
	status := s.mux.RouterStatus(ctx)
	snapshot := controlSnapshotResponse{
		SchemaVersion: controlSchemaVersion,
		GeneratedAt:   time.Now().UnixMilli(),
		Pool:          poolSummary(status.Pool),
		Session:       controlSessionSummary{ActiveTurnCount: status.ActiveTurnCount, RecoveryTaskCount: status.RecoveryTaskCount},
	}
	if includeDiagnostics {
		diagnostics := diagnosticsFromTimeline(status.Timeline, 50)
		snapshot.Diagnostics = &diagnostics
	}
	return snapshot, nil
}

func poolSummary(pool mux.PoolStatus) controlPoolSummary {
	return controlPoolSummary{
		PoolID: pool.PoolID, Revision: pool.Revision, Health: pool.Health,
		ActiveLeaseCount: pool.ActiveLeaseCount, ConnectedSubscriptions: pool.ConnectedSubscriptions,
		KnownSubscriptions: pool.KnownSubscriptions, UnknownSubscriptions: pool.UnknownSubscriptions,
		AvailableSubscriptions: pool.AvailableSubscriptions, DepletedSubscriptions: pool.DepletedSubscriptions,
		MaximumPercent: pool.MaximumPercent, ConfirmedRemainingPercent: pool.ConfirmedRemainingPercent,
		ConfirmedUsedPercent: pool.ConfirmedUsedPercent, NextResetAt: pool.NextResetAt, QuotaUpdatedAt: pool.QuotaUpdatedAt,
	}
}

func diagnosticsFromTimeline(timeline []mux.RoutingTimelineEvent, limit int) controlDiagnostics {
	// RouterStatus already returns a bounded, sanitized timeline. The control
	// contract intentionally projects only lifecycle fields; account IDs,
	// labels, reasons and arbitrary payloads never cross this boundary.
	if limit < 1 {
		limit = 1
	}
	start := 0
	if len(timeline) > limit {
		start = len(timeline) - limit
	}
	result := controlDiagnostics{EventCount: len(timeline), Events: make([]controlDiagnosticEvent, 0, len(timeline)-start)}
	for _, event := range timeline[start:] {
		result.Events = append(result.Events, controlDiagnosticEvent{
			Type: event.Type, Timestamp: event.Timestamp, RouteGeneration: event.Generation,
			ReasonCode: event.ReasonCode, Result: event.Result,
		})
	}
	return result
}

func (s *Server) controlEvents(response http.ResponseWriter, request *http.Request) {
	if !s.authorized(request) {
		writeJSON(response, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
		return
	}
	if request.Method != http.MethodGet {
		methodNotAllowed(response)
		return
	}
	flusher, ok := response.(http.Flusher)
	if !ok {
		writeJSON(response, http.StatusInternalServerError, map[string]any{"error": "streaming unavailable"})
		return
	}
	response.Header().Set("Content-Type", "text/event-stream")
	response.Header().Set("Cache-Control", "no-cache")
	response.Header().Set("Connection", "keep-alive")
	events, unsubscribe := s.mux.SubscribeEvents()
	defer unsubscribe()
	_, _ = fmt.Fprint(response, ": connected\n\n")
	flusher.Flush()
	for {
		select {
		case <-request.Context().Done():
			return
		case event, open := <-events:
			if !open {
				return
			}
			encoded, _ := json.Marshal(sanitizeControlEvent(event))
			_, _ = fmt.Fprintf(response, "data: %s\n\n", encoded)
			flusher.Flush()
		}
	}
}

func sanitizeControlEvent(event mux.Event) controlDiagnosticEvent {
	return controlDiagnosticEvent{Type: event.Type, Timestamp: event.Timestamp, RouteGeneration: event.RouteGeneration}
}
