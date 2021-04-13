package proxy

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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

	const concurrent = 8
	sema := make(chan struct{}, concurrent) // limit health check to 8 concurrent calls

	// runs pings in parallel
	check := func(mappers []discovery.URLMapper) (ok bool, valid int, total int, errs []string) {
		outCh := make(chan error, concurrent)
		var pinged int32
		var wg sync.WaitGroup
		for _, m := range mappers {
			if m.PingURL == "" {
				continue
			}
			sema <- struct{}{}
			wg.Add(1)
			go func(m discovery.URLMapper) {
				defer func() {
					<-sema
					wg.Done()
				}()

				atomic.AddInt32(&pinged, 1)
				client := http.Client{Timeout: 100 * time.Millisecond}
				resp, err := client.Get(m.PingURL)
				if err != nil {
					errMsg := strings.Replace(err.Error(), "\"", "", -1)
					log.Printf("[WARN] failed to ping for health %s, %s", m.PingURL, errMsg)
					outCh <- fmt.Errorf("%s %s: %s, %v", m.Server, m.SrcMatch.String(), m.PingURL, errMsg)
					return
				}
				if resp.StatusCode != http.StatusOK {
					log.Printf("[WARN] failed ping status for health %s (%s)", m.PingURL, resp.Status)
					outCh <- fmt.Errorf("%s %s: %s, %s", m.Server, m.SrcMatch.String(), m.PingURL, resp.Status)
					return
				}
			}(m)
		}

		go func() {
			wg.Wait()
			close(outCh)
		}()

		for e := range outCh {
			errs = append(errs, e.Error())
		}
		return len(errs) == 0, int(atomic.LoadInt32(&pinged)) - len(errs), len(mappers), errs
	}

	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	ok, valid, total, errs := check(h.Mappers())
	if !ok {
		w.WriteHeader(http.StatusExpectationFailed)

		errResp := struct {
			Status string   `json:"status,omitempty"`
			Passed int      `json:"passed,omitempty"`
			Failed int      `json:"failed,omitempty"`
			Errors []string `json:"errors,omitempty"`
		}{Status: "failed", Passed: valid, Failed: total - valid, Errors: errs}

		rest.RenderJSON(w, errResp)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, err := fmt.Fprintf(w, `{"status": "ok", "services": %d}`, valid)
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
