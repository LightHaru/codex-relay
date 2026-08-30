package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LightHaru/codex-relay/internal/mux"
)

func TestControlLivenessIsUnauthenticatedAndStable(t *testing.T) {
	server := testServer(t)
	recorder := httptest.NewRecorder()
	server.http.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/control/healthz", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", recorder.Code)
	}
	var body controlLivenessResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.SchemaVersion != controlSchemaVersion || body.Status != "live" || !body.OK || body.Checks["process"].Status != "ok" {
		t.Fatalf("unexpected liveness body: %#v", body)
	}
}

func TestControlReadinessRequiresTokenAndReportsNotReady(t *testing.T) {
	server := testServer(t)
	unauthorized := httptest.NewRecorder()
	server.http.Handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/v1/control/readyz", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized readyz status = %d, want 401", unauthorized.Code)
	}
	recorder := httptest.NewRecorder()
	server.http.Handler.ServeHTTP(recorder, requestWithToken(http.MethodGet, "/v1/control/readyz", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("empty pool readyz status = %d, want 503: %s", recorder.Code, recorder.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["schemaVersion"] != controlSchemaVersion || body["status"] != "not_ready" || body["ok"] != false {
		t.Fatalf("unexpected readiness body: %#v", body)
	}
}

func TestReadinessStatusDistinguishesReadyDegradedAndNotReady(t *testing.T) {
	cases := []struct {
		name   string
		pool   controlPoolSummary
		status string
	}{
		{name: "ready", pool: controlPoolSummary{Health: "healthy", AvailableSubscriptions: 1}, status: "ready"},
		{name: "degraded", pool: controlPoolSummary{Health: "depleted", ConnectedSubscriptions: 1}, status: "degraded"},
		{name: "warming", pool: controlPoolSummary{Health: "warming"}, status: "not_ready"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := readinessStatus(test.pool); got != test.status {
				t.Fatalf("readinessStatus() = %q, want %q", got, test.status)
			}
		})
	}
}

func TestControlDiagnosticsIsBoundedAndRedactsSourceIdentity(t *testing.T) {
	secretEmail := "someone@example.com"
	timeline := make([]mux.RoutingTimelineEvent, 0, 4)
	for i := 0; i < 4; i++ {
		timeline = append(timeline, mux.RoutingTimelineEvent{
			ID: "decision-secret", Type: "route", Timestamp: int64(i + 1), Generation: uint64(i + 1),
			AccountID: secretEmail, SourceAccountID: "filesystem-secret", TargetAccountID: "token-secret",
			ReasonCode: "safe_reason", Result: "completed", Reason: "raw upstream payload must not escape",
		})
	}
	result := diagnosticsFromTimeline(timeline, 2)
	if result.EventCount != 4 || len(result.Events) != 2 {
		t.Fatalf("bounded diagnostics = %#v", result)
	}
	if result.Events[0].Timestamp != 3 || result.Events[1].Timestamp != 4 {
		t.Fatalf("diagnostics ordering = %#v", result.Events)
	}
	encoded, _ := json.Marshal(result)
	text := string(encoded)
	for _, secret := range []string{secretEmail, "filesystem-secret", "token-secret", "raw upstream payload"} {
		if strings.Contains(text, secret) {
			t.Fatalf("diagnostics leaked %q: %s", secret, text)
		}
	}
}

func TestSanitizeControlEventDropsDataAndIdentity(t *testing.T) {
	event := sanitizeControlEvent(mux.Event{
		ID: "event-id", Type: "response.completed", ThreadID: "thread-secret", AccountID: "account-secret",
		Timestamp: 42, RouteGeneration: 7, Message: "Bearer very-secret", Data: map[string]any{"email": "a@example.com"},
	})
	encoded, _ := json.Marshal(event)
	text := string(encoded)
	if event.Type != "response.completed" || event.Timestamp != 42 || event.RouteGeneration != 7 {
		t.Fatalf("event projection lost lifecycle fields: %#v", event)
	}
	for _, secret := range []string{"event-id", "thread-secret", "account-secret", "Bearer", "a@example.com"} {
		if strings.Contains(text, secret) {
			t.Fatalf("event projection leaked %q: %s", secret, text)
		}
	}
}
