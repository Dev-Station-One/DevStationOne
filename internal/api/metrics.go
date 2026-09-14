package api

import (
	"expvar"
	"fmt"
	"net/http"

	"devstationone/internal/runtimes"
	"devstationone/internal/sessions"
)

// Basic operational counters (docs/SECURITY.md "metrics"), exposed at
// GET /api/v1/metrics (admin-only). expvar over a Prometheus client:
// stdlib, zero new scrape-format dependency, and this MVP has no metrics
// backend deployed yet to actually consume Prometheus exposition format —
// see docs/DECISIONS.md ADR-026.
var (
	metricRequestsTotal = expvar.NewInt("http_requests_total")
	metricRequests5xx   = expvar.NewInt("http_requests_5xx_total")
)

func recordRequestMetrics(status int) {
	metricRequestsTotal.Add(1)
	if status >= 500 {
		metricRequests5xx.Add(1)
	}
}

// metricsHandler renders every expvar-published counter alongside two live
// gauges (connected runtimes, attached terminal sessions). It reimplements
// expvar.Handler's own JSON format rather than calling expvar.Publish for
// the gauges: Publish panics if the same name is registered twice, and
// NewRouter (this constructor's caller) runs once per test in this
// package's test suite, each with its own Registry/Hub.
func metricsHandler(registry *runtimes.Registry, hub *sessions.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		fmt.Fprint(w, "{\n")
		first := true
		expvar.Do(func(kv expvar.KeyValue) {
			if !first {
				fmt.Fprint(w, ",\n")
			}
			first = false
			fmt.Fprintf(w, "%q: %s", kv.Key, kv.Value)
		})
		fmt.Fprintf(w, ",\n%q: %d,\n%q: %d\n}\n",
			"runtime_connections", registry.Count(),
			"terminal_sessions_attached", hub.AttachedCount())
	}
}
