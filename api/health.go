package api

import (
	"encoding/json"
	"net/http"
)

// HealthCheckFunc is a function that performs a health check.
type HealthCheckFunc func() HealthCheck

// HealthCheck is the result of a single health check.
type HealthCheck struct {
	Name   string
	Status Status
}

// Status represents the health status of a component.
type Status struct {
	Ok      bool
	Message string
}

func (s Status) MarshalJSON() ([]byte, error) {
	if s.Ok {
		return json.Marshal("ok")
	}
	if s.Message != "" {
		return json.Marshal(s.Message)
	}
	return json.Marshal("error")
}

var healthChecks []HealthCheckFunc

// RegisterHealthCheck adds a health check function to be evaluated on each
// GET /health request.
func RegisterHealthCheck(fn HealthCheckFunc) {
	healthChecks = append(healthChecks, fn)
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	results := map[string]Status{
		"running": {Ok: true},
	}

	allOk := true
	for _, fn := range healthChecks {
		check := fn()
		results[check.Name] = check.Status
		if !check.Status.Ok {
			allOk = false
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if !allOk {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	json.NewEncoder(w).Encode(results)
}
