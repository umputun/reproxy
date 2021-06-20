package mgmt

import (
	"log"
	"net/http"
	"strings"

	"golang.org/x/time/rate"
)

type ProxyThrottlingConfig struct {
	HttpStatusCode            int
	ProxyThrottlingConfig     ServerThrottlingConfig
	PerServerThrottlingConfig map[string]ServerThrottlingConfig
}

type ServerThrottlingConfig struct {
	Enabled bool
	Rate    int
	Burst   int
}

type Throttler struct {
	throttlingConfig        ProxyThrottlingConfig
	proxyRateLimiter        *rate.Limiter
	perServerRateLimiters   map[string]*rate.Limiter
	reportThrottlingHandler http.HandlerFunc
}

func NewThrottler(throttlingConfig *ProxyThrottlingConfig) *Throttler {
	perServerRateLimiters := make(map[string]*rate.Limiter)
	for server, serverThrottlingConfig := range throttlingConfig.PerServerThrottlingConfig {
		perServerRateLimiters[server] = createRateLimiter(serverThrottlingConfig)
	}
	return &Throttler{
		throttlingConfig:        *throttlingConfig,
		proxyRateLimiter:        createRateLimiter(throttlingConfig.ProxyThrottlingConfig),
		perServerRateLimiters:   perServerRateLimiters,
		reportThrottlingHandler: createReportThrottlingHandler(throttlingConfig.HttpStatusCode),
	}
}

func (t *Throttler) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// stage one: global rate limit
		if t.proxyRateLimiter != nil && !t.proxyRateLimiter.Allow() {
			t.reportThrottlingHandler.ServeHTTP(w, r)
			return
		}
		// stage two: per server rate limit
		server := getServer(r)
		serverRateLimiter := t.perServerRateLimiters[server]
		if serverRateLimiter != nil && !serverRateLimiter.Allow() {
			t.reportThrottlingHandler.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func getServer(r *http.Request) string {
	server := r.URL.Hostname()
	if server == "" {
		server = strings.Split(r.Host, ":")[0] // drop port
	}
	return server
}

func createReportThrottlingHandler(httpStatusCode int) http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Content-Type", "text/plain")
		w.WriteHeader(httpStatusCode)
		_, err := w.Write([]byte("Request rate limit exceeded, please retry later"))
		if err != nil {
			log.Printf("[WARN] can't write throttle request output content, %v", err)
		}
	})
}

func createRateLimiter(throttlingConfig ServerThrottlingConfig) *rate.Limiter {
	if !throttlingConfig.Enabled {
		return nil
	}
	return rate.NewLimiter(
		rate.Limit(throttlingConfig.Rate),
		throttlingConfig.Burst,
	)
}
