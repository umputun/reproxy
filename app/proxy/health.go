package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	log "github.com/go-pkgz/lgr"

	"github.com/umputun/reproxy/app/discovery"
)

// healthCacheTTL is how long one probe run answers /health, so client traffic can't multiply pings.
const healthCacheTTL = 5 * time.Second

// healthCache holds the last /health answer. Its lock is held across a probe run, so concurrent
// requests wait for that run instead of starting their own.
type healthCache struct {
	sync.Mutex
	at   time.Time
	code int
	body []byte
}

func (h *Http) healthMiddleware(next http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.EqualFold(r.URL.Path, "/health") {
			if h.shouldForwardHealthChecks(r) {
				next.ServeHTTP(w, r)
				return
			}
			h.healthHandler(w, r)
			return
		}
		next.ServeHTTP(w, r)
	}
	return http.HandlerFunc(fn)
}

func (h *Http) healthHandler(w http.ResponseWriter, _ *http.Request) {
	h.health.Lock()
	if time.Since(h.health.at) >= healthCacheTTL {
		h.health.code, h.health.body = h.checkHealth()
		h.health.at = time.Now()
	}
	code, body := h.health.code, h.health.body
	h.health.Unlock()

	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(code)
	if _, err := w.Write(body); err != nil {
		log.Printf("[WARN] failed to send health, %v", err)
	}
}

// checkHealth probes the configured ping URLs and renders the public answer. It carries counts only, as probe errors
// name internal servers and URLs; those go to the log.
func (h *Http) checkHealth() (code int, body []byte) {
	total := 0
	for _, m := range h.Mappers() {
		if m.MatchType == discovery.MTProxy {
			total++
		}
	}

	pingErrs := h.CheckHealth()
	failed := 0
	for _, pingErr := range pingErrs {
		if pingErr != nil {
			failed++
			log.Printf("[WARN] health check failed, %v", pingErr)
		}
	}
	if failed == 0 {
		return http.StatusOK, fmt.Appendf(nil, `{"status": "ok", "services": %d}`, total)
	}

	resp := struct {
		Status   string `json:"status,omitempty"`
		Services int    `json:"services,omitempty"`
		Passed   int    `json:"passed,omitempty"`
		Failed   int    `json:"failed,omitempty"`
	}{Status: "failed", Services: total, Passed: len(pingErrs) - failed, Failed: failed}
	body, err := json.Marshal(resp)
	if err != nil {
		log.Printf("[WARN] failed to marshal health response, %v", err)
	}
	return http.StatusExpectationFailed, body
}

// pingHandler middleware response with pong to /ping. Stops chain if ping request detected
func (h *Http) pingHandler(next http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.EqualFold(r.URL.Path, "/ping") {
			if h.shouldForwardHealthChecks(r) {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("pong"))
			return
		}
		next.ServeHTTP(w, r)
	}
	return http.HandlerFunc(fn)
}

// shouldForwardHealthChecks checks if the request matches a route with ForwardHealthChecks enabled
func (h *Http) shouldForwardHealthChecks(r *http.Request) bool {
	matches := h.Match(discovery.HostName(r.Host), r.URL.EscapedPath())
	for _, route := range matches.Routes {
		if route.Mapper.ForwardHealthChecks {
			return true
		}
	}
	return false
}
