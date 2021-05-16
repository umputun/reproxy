package proxy

import (
	"fmt"
	"net/http"
	"strings"

	log "github.com/go-pkgz/lgr"
	"github.com/go-pkgz/rest"
	"github.com/umputun/reproxy/app/discovery"
)

func (h *Http) healthMiddleware(next http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.EqualFold(r.URL.Path, "/health") {
			h.healthHandler(w, r)
			return
		}
		next.ServeHTTP(w, r)
	}
	return http.HandlerFunc(fn)
}

func (h *Http) healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")

	mappers := h.Mappers()
	total := 0
	for _, m := range mappers {
		if m.MatchType == discovery.MTProxy {
			total++
		}
	}

	pingErrs := h.CheckHealth()

	var errs []string
	for _, pingErr := range pingErrs {
		if pingErr != nil {
			errs = append(errs, pingErr.Error())
		}
	}

	if len(errs) > 0 {
		w.WriteHeader(http.StatusExpectationFailed)

		errResp := struct {
			Status   string   `json:"status,omitempty"`
			Services int      `json:"services,omitempty"`
			Passed   int      `json:"passed,omitempty"`
			Failed   int      `json:"failed,omitempty"`
			Errors   []string `json:"errors,omitempty"`
		}{Status: "failed", Services: total, Passed: len(pingErrs) - len(errs), Failed: len(errs), Errors: errs}

		rest.RenderJSON(w, errResp)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, err := fmt.Fprintf(w, `{"status": "ok", "services": %d}`, total)
	if err != nil {
		log.Printf("[WARN] failed to send health, %v", err)
	}
}

// pingHandler middleware response with pong to /ping. Stops chain if ping request detected
func (h *Http) pingHandler(next http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {

		if r.Method == "GET" && strings.EqualFold(r.URL.Path, "/ping") {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("pong"))
			return
		}
		next.ServeHTTP(w, r)
	}
	return http.HandlerFunc(fn)
}
