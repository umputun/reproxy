package proxy

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"

	log "github.com/go-pkgz/lgr"
	"golang.org/x/crypto/bcrypt"

	"github.com/umputun/reproxy/app/discovery"
)

// PerRouteAuth implements per-route basic authentication middleware.
// It checks the AuthUsers field of the matched route and validates credentials.
type PerRouteAuth struct{}

// NewPerRouteAuth creates new PerRouteAuth middleware.
func NewPerRouteAuth() *PerRouteAuth {
	return &PerRouteAuth{}
}

// Handler implements middleware interface for per-route basic auth.
func (p *PerRouteAuth) Handler(next http.Handler) http.Handler {
	unauthorized := func(w http.ResponseWriter) {
		w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
		w.WriteHeader(http.StatusUnauthorized)
	}

	fn := func(w http.ResponseWriter, r *http.Request) {
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
			unauthorized(w)
			return
		}

		if !validateBasicAuthCredentials(username, password, authUsers) {
			log.Printf("[INFO] auth rejected for user %q on %s", username, r.URL.String())
			unauthorized(w)
			return
		}

		next.ServeHTTP(w, r)
	}
	return http.HandlerFunc(fn)
}

// validateBasicAuthCredentials checks if username:password matches any of the allowed user:hash pairs.
// Uses constant-time comparison to prevent timing attacks.
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
