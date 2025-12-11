package proxy

import (
	"crypto/sha256"
	"crypto/subtle"
	"io"
	"net/http"
	"strings"

	"github.com/didip/tollbooth/v7"
	"github.com/didip/tollbooth/v7/libstring"
	log "github.com/go-pkgz/lgr"
	R "github.com/go-pkgz/rest"
	"github.com/gorilla/handlers"
	"golang.org/x/crypto/bcrypt"

	"github.com/umputun/reproxy/app/discovery"
)

func headersHandler(addHeaders, dropHeaders []string) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(addHeaders) == 0 && len(dropHeaders) == 0 {
				next.ServeHTTP(w, r)
				return
			}

			// add headers to response
			for _, h := range addHeaders {
				// split on first colon only
				if i := strings.Index(h, ":"); i >= 0 {
					key := strings.TrimSpace(h[:i])
					value := strings.TrimSpace(h[i+1:])
					if key != "" {
						w.Header().Set(key, value)
					}
				}
			}

			// drop headers from request
			for _, h := range dropHeaders {
				r.Header.Del(h)
			}

			next.ServeHTTP(w, r)
		})
	}
}

func maxReqSizeHandler(maxSize int64) func(next http.Handler) http.Handler {
	if maxSize <= 0 {
		return passThroughHandler
	}

	log.Printf("[DEBUG] request size limited to %d", maxSize)
	return func(next http.Handler) http.Handler {

		fn := func(w http.ResponseWriter, r *http.Request) {

			// check ContentLength
			if r.ContentLength > maxSize {
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				return
			}

			// check query string size
			if int64(len(r.URL.RawQuery)) > maxSize {
				w.WriteHeader(http.StatusRequestURITooLong)
				return
			}

			r.Body = http.MaxBytesReader(w, r.Body, maxSize)
			next.ServeHTTP(w, r)
		}
		return http.HandlerFunc(fn)
	}
}

func accessLogHandler(wr io.Writer) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return handlers.CombinedLoggingHandler(wr, next)
	}
}

func stdoutLogHandler(enable bool, lh func(next http.Handler) http.Handler) func(next http.Handler) http.Handler {

	if !enable {
		return passThroughHandler
	}

	log.Printf("[DEBUG] stdout logging enabled")
	return func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			// don't log to stdout GET ~/(.*)/ping$ requests
			if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/ping") {
				next.ServeHTTP(w, r)
				return
			}
			lh(next).ServeHTTP(w, r)
		}
		return http.HandlerFunc(fn)
	}
}

func gzipHandler(enabled bool) func(next http.Handler) http.Handler {
	if !enabled {
		return passThroughHandler
	}

	log.Printf("[DEBUG] gzip enabled")
	return handlers.CompressHandler
}

func signatureHandler(enabled bool, version string) func(next http.Handler) http.Handler {
	if !enabled {
		return passThroughHandler
	}
	log.Printf("[DEBUG] signature headers enabled")
	return R.AppInfo("reproxy", "umputun", version)
}

// limiterSystemHandler throttles overall activity of reproxy server, 0 means disabled
func limiterSystemHandler(reqSec int) func(next http.Handler) http.Handler {
	if reqSec <= 0 {
		return passThroughHandler
	}
	return func(h http.Handler) http.Handler {
		lmt := tollbooth.NewLimiter(float64(reqSec), nil)
		fn := func(w http.ResponseWriter, r *http.Request) {
			if httpError := tollbooth.LimitByKeys(lmt, []string{"system"}); httpError != nil {
				http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
				return
			}
			h.ServeHTTP(w, r)
		}
		return http.HandlerFunc(fn)
	}
}

// limiterUserHandler throttles per user activity. In case if match found the limit is per destination
// otherwise global (per user in any case). 0 means disabled
func limiterUserHandler(reqSec int) func(next http.Handler) http.Handler {
	if reqSec <= 0 {
		return passThroughHandler
	}

	return func(h http.Handler) http.Handler {
		lmt := tollbooth.NewLimiter(float64(reqSec), nil)
		fn := func(w http.ResponseWriter, r *http.Request) {
			keys := []string{libstring.RemoteIP(lmt.GetIPLookups(), lmt.GetForwardedForIndexFromBehind(), r)}

			// add dst proxy if matched
			if r.Context().Value(ctxMatch) != nil { // route match detected by matchHandler
				match := r.Context().Value(ctxMatch).(discovery.MatchedRoute)
				matchType := r.Context().Value(ctxMatchType).(discovery.MatchType)
				if matchType == discovery.MTProxy {
					keys = append(keys, match.Mapper.Dst)
				}
			}

			if httpError := tollbooth.LimitByKeys(lmt, keys); httpError != nil {
				http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
				return
			}
			h.ServeHTTP(w, r)
		}
		return http.HandlerFunc(fn)
	}
}

// perRouteAuthHandler is middleware for per-route basic authentication.
// It checks the AuthUsers field of the matched route and validates credentials.
func perRouteAuthHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var authUsers []string
		reqCtx := r.Context()
		if reqCtx.Value(ctxMatch) != nil { // route match detected by matchHandler
			match := reqCtx.Value(ctxMatch).(discovery.MatchedRoute)
			authUsers = match.Mapper.AuthUsers
		}

		if len(authUsers) == 0 {
			// no per-route auth required
			next.ServeHTTP(w, r)
			return
		}

		username, password, ok := r.BasicAuth()
		if !ok {
			sendBasicAuthUnauthorized(w)
			return
		}

		if !validateBasicAuthCredentials(username, password, authUsers) {
			log.Printf("[INFO] auth rejected for user %q on %s", username, r.URL.String())
			sendBasicAuthUnauthorized(w)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// globalBasicAuthHandler is a middleware that authenticates via global basic auth.
// It skips authentication if the route has per-route auth configured (AuthUsers is set).
// allowed is a list of user:bcrypt(passwd) strings generated by `htpasswd -nbB user passwd`
func globalBasicAuthHandler(allowed []string) func(next http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			// skip global auth if route has per-route auth configured
			reqCtx := r.Context()
			if reqCtx.Value(ctxMatch) != nil {
				match := reqCtx.Value(ctxMatch).(discovery.MatchedRoute)
				if len(match.Mapper.AuthUsers) > 0 {
					h.ServeHTTP(w, r)
					return
				}
			}

			username, password, ok := r.BasicAuth()
			if !ok {
				sendBasicAuthUnauthorized(w)
				return
			}

			if !validateBasicAuthCredentials(username, password, allowed) {
				log.Printf("[INFO] auth rejected for user %q on %s", username, r.URL.String())
				sendBasicAuthUnauthorized(w)
				return
			}

			h.ServeHTTP(w, r)
		}
		return http.HandlerFunc(fn)
	}
}

// validateBasicAuthCredentials checks if username:password matches any of the allowed user:hash pairs.
// uses constant-time username comparison and bcrypt for password verification.
func validateBasicAuthCredentials(username, password string, allowed []string) bool {
	if username == "" {
		return false
	}
	passed := false
	usernameHash := sha256.Sum256([]byte(username))
	for _, a := range allowed {
		elems := strings.SplitN(strings.TrimSpace(a), ":", 2)
		if len(elems) != 2 || elems[0] == "" {
			continue
		}

		// hash to ensure constant time comparison not affected by username length
		expectedUsernameHash := sha256.Sum256([]byte(elems[0]))

		expectedPasswordHash := elems[1]
		userMatched := subtle.ConstantTimeCompare(usernameHash[:], expectedUsernameHash[:])
		passMatchErr := bcrypt.CompareHashAndPassword([]byte(expectedPasswordHash), []byte(password))
		if userMatched == 1 && passMatchErr == nil {
			passed = true // don't stop here, check all allowed to keep the overall time consistent
		}
	}
	return passed
}

// sendBasicAuthUnauthorized sends 401 Unauthorized response with WWW-Authenticate header.
func sendBasicAuthUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
	w.WriteHeader(http.StatusUnauthorized)
}

func passThroughHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}
