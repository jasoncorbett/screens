package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleHealthWithDatabaseOk(t *testing.T) {
	saved := healthChecks
	t.Cleanup(func() { healthChecks = saved })

	healthChecks = []HealthCheckFunc{
		func() HealthCheck {
			return HealthCheck{Name: "database", Status: Status{Ok: true}}
		},
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/health", nil)
	handleHealth(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	dbStatus, ok := body["database"]
	if !ok {
		t.Fatal("response missing \"database\" key")
	}
	if dbStatus != "ok" {
		t.Errorf("database status = %v, want \"ok\"", dbStatus)
	}
}

func TestHandleHealthWithDatabaseUnhealthy(t *testing.T) {
	saved := healthChecks
	t.Cleanup(func() { healthChecks = saved })

	healthChecks = []HealthCheckFunc{
		func() HealthCheck {
			return HealthCheck{
				Name:   "database",
				Status: Status{Ok: false, Message: "error: connection refused"},
			}
		},
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/health", nil)
	handleHealth(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}

	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	dbStatus, ok := body["database"]
	if !ok {
		t.Fatal("response missing \"database\" key")
	}
	if dbStatus != "error: connection refused" {
		t.Errorf("database status = %v, want \"error: connection refused\"", dbStatus)
	}
}
