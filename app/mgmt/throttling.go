package mgmt

import (
	"net/http"
	"strings"

	tollbooth "github.com/didip/tollbooth/v6"
	"github.com/didip/tollbooth/v6/limiter"
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

// proxy server throttler. Encapsulates overall and per-virtual server throttling.
type Throttler struct {
	throttlingConfig      ProxyThrottlingConfig
	proxyRateLimiter      throttlerImplementation
	perServerRateLimiters map[string]throttlerImplementation
}

// interface anstracting away a concrete throttling implementaion
// implement it to add support for alternative throttling mechanisms
type throttlerImplementation interface {
	// counts a request attempt and returns true if it was allowed to proceed
	// TODO: we're using errors.HTTPError structure from tollbooth, will need to replace with the local structure
	// once we have another throttling implementaion
	Allow(w http.ResponseWriter, r *http.Request, alreadyReplied bool) bool
}

type tollboothThrottlerImplementation struct {
	tollboothLimiter *limiter.Limiter
}

func NewTollboothThrottler(throttlingConfig *ProxyThrottlingConfig) *Throttler {
	perServerRateLimiters := make(map[string]throttlerImplementation)
	for server, serverThrottlingConfig := range throttlingConfig.PerServerThrottlingConfig {
		perServerRateLimiters[server] = makeTollboothRateLimiter(serverThrottlingConfig, throttlingConfig.HttpStatusCode)
	}
	return &Throttler{
		throttlingConfig:      *throttlingConfig,
		proxyRateLimiter:      makeTollboothRateLimiter(throttlingConfig.ProxyThrottlingConfig, throttlingConfig.HttpStatusCode),
		perServerRateLimiters: perServerRateLimiters,
	}
}

func (t *Throttler) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// stage one: global rate limit
		proxyThrottled := t.proxyRateLimiter != nil && isThrottled(t.proxyRateLimiter, w, r, false)
		// while we may already know that the request has been throttled, we still need to register it against the respective virual server
		// so that it maintains the right call counts for the subsequent requests

		// stage two: per server rate limit
		server := getServer(r)
		serverRateLimiter := t.perServerRateLimiters[server]
		serverThrottled := serverRateLimiter != nil && isThrottled(serverRateLimiter, w, r, proxyThrottled)
		if proxyThrottled || serverThrottled {
			// the throttled http response was already served, just returning now
			return
		}
		// request is not throttled, let it through
		next.ServeHTTP(w, r)
	})
}

func isThrottled(limiter throttlerImplementation, w http.ResponseWriter, r *http.Request, alreadyThrottled bool) bool {
	if limiter == nil {
		// limiter disabled
		return false
	}
	return limiter.Allow(w, r, alreadyThrottled)
}

func getServer(r *http.Request) string {
	server := r.URL.Hostname()
	if server == "" {
		server = strings.Split(r.Host, ":")[0] // drop port
	}
	// server names are technically case-insensitive, at least from throttling perspective
	return strings.ToLower(server)
}

func makeTollboothServerThrottler(limiter *limiter.Limiter) throttlerImplementation {
	return &tollboothThrottlerImplementation{limiter}
}

func (t *tollboothThrottlerImplementation) Allow(w http.ResponseWriter, r *http.Request, alreadyReplied bool) bool {
	httpError := tollbooth.LimitByRequest(t.tollboothLimiter, w, r)
	if httpError != nil {
		t.tollboothLimiter.ExecOnLimitReached(w, r)
		if !alreadyReplied {
			w.Header().Add("Content-Type", t.tollboothLimiter.GetMessageContentType())
			w.WriteHeader(httpError.StatusCode)
			w.Write([]byte(httpError.Message))
		}
		return false
	}
	return true
}

func makeTollboothRateLimiter(throttlingConfig ServerThrottlingConfig, httpStatusCode int) throttlerImplementation {
	if !throttlingConfig.Enabled {
		return nil
	}
	// this is a very basic implementation the throttler
	// see https://github.com/didip/tollbooth#features for more configuration options
	// there are a number of default settings that can be found here:
	// https://github.com/didip/tollbooth/blob/master/limiter/limiter.go#L14
	tollboothLimiter := tollbooth.NewLimiter(float64(throttlingConfig.Rate), nil).
		SetBurst(throttlingConfig.Burst).
		SetStatusCode(httpStatusCode).
		SetMessage("Request rate limit exceeded, please retry later").
		SetMessageContentType("text/plain")
	return makeTollboothServerThrottler(tollboothLimiter)
}
