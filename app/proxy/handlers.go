package proxy

import (
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/didip/tollbooth/v7"
	"github.com/didip/tollbooth/v7/libstring"
	"github.com/didip/tollbooth/v7/limiter"
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
				if key, value, ok := strings.Cut(h, ":"); ok {
					key = strings.TrimSpace(key)
					value = strings.TrimSpace(value)
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

// gzipHandler compresses responses for clients that accept gzip or deflate, skipping media types that
// match an exclude pattern, exact "audio/mpeg" or prefix "audio/*".
func gzipHandler(enabled bool, exclude []string) func(next http.Handler) http.Handler {
	if !enabled {
		return passThroughHandler
	}

	patterns := make([]string, 0, len(exclude))
	for _, p := range exclude {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			patterns = append(patterns, p)
		}
	}
	log.Printf("[DEBUG] gzip enabled, excluded types: %v", patterns)
	return func(next http.Handler) http.Handler {
		// recover before finalizing compression so the panic response uses the same encoding
		next = R.Recoverer(log.Default())(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Upgrade") != "" {
				next.ServeHTTP(w, r)
				return
			}
			cw := &compressWriter{ResponseWriter: w, encoding: negotiateEncoding(r.Header.Values("Accept-Encoding")),
				exclude: patterns, head: r.Method == http.MethodHead}
			defer cw.finish()
			next.ServeHTTP(cw, r)
		})
	}
}

// negotiateEncoding picks gzip, then deflate, from Accept-Encoding values, skipping codings with q=0.
func negotiateEncoding(values []string) string {
	var gz, fl bool
	for _, v := range values {
		for part := range strings.SplitSeq(v, ",") {
			name, params, _ := strings.Cut(part, ";")
			name = strings.ToLower(strings.TrimSpace(name))
			if (name != "gzip" && name != "deflate") || zeroQuality(params) {
				continue
			}
			gz = gz || name == "gzip"
			fl = fl || name == "deflate"
		}
	}
	switch {
	case gz:
		return "gzip"
	case fl:
		return "deflate"
	}
	return ""
}

func zeroQuality(params string) bool {
	for param := range strings.SplitSeq(params, ";") {
		k, v, _ := strings.Cut(param, "=")
		if !strings.EqualFold(strings.TrimSpace(k), "q") {
			continue
		}
		q, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return err == nil && q == 0
	}
	return false
}

type flushWriteCloser interface {
	io.WriteCloser
	Flush() error
}

// compressWriter holds the status until the response headers are final, then commits either a
// compressed or a plain response exactly once.
type compressWriter struct {
	http.ResponseWriter
	encoding    string   // negotiated coding, empty when the client accepts none
	exclude     []string // lowercased media type patterns
	head        bool
	status      int // first final status, 0 until the handler sets one
	decided     bool
	compressing bool
	comp        flushWriteCloser // nil for plain responses and for HEAD
}

// WriteHeader passes informational statuses through and keeps the first final status.
func (w *compressWriter) WriteHeader(code int) {
	if w.decided {
		w.ResponseWriter.WriteHeader(code)
		return
	}
	if code >= 100 && code < 200 && code != http.StatusSwitchingProtocols {
		if w.status == 0 {
			w.ResponseWriter.WriteHeader(code)
		}
		return
	}
	if w.status != 0 {
		return
	}
	w.status = code
	if w.typeKnown() || !w.bodyAllowed(code) {
		w.decide()
	}
}

// Write sniffs the content type from the first bytes when the handler set none, the same way
// net/http would, so the exclude list sees it.
func (w *compressWriter) Write(b []byte) (int, error) {
	if !w.decided {
		if !w.typeKnown() {
			if len(b) == 0 {
				return 0, nil
			}
			w.Header().Set("Content-Type", http.DetectContentType(b))
		}
		w.decide()
	}
	if w.comp != nil {
		return w.comp.Write(b) //nolint:wrapcheck // pass-through writer keeps the underlying error
	}
	if w.compressing {
		return len(b), nil // for HEAD, net/http would derive Content-Length from these plain bytes
	}
	return w.ResponseWriter.Write(b) //nolint:wrapcheck // pass-through writer keeps the underlying error
}

// Flush commits the headers if still pending, which goes out plain when there is no type to judge.
func (w *compressWriter) Flush() {
	if !w.decided {
		w.decide()
	}
	if w.comp != nil {
		_ = w.comp.Flush()
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap exposes the underlying writer to http.ResponseController.
func (w *compressWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// typeKnown is true when the handler set a Content-Type, even a deliberately empty one, or the body
// is already encoded and must not be sniffed.
func (w *compressWriter) typeKnown() bool {
	_, ok := w.Header()["Content-Type"]
	return ok || w.Header().Get("Content-Encoding") != ""
}

func (w *compressWriter) decide() {
	w.decided = true
	if w.status == 0 {
		w.status = http.StatusOK
	}
	h := w.Header()
	if w.compressible() {
		h.Set("Content-Encoding", w.encoding)
		h.Del("Content-Length")
		h.Del("Accept-Ranges")
		if etag := h.Get("ETag"); strings.HasPrefix(etag, `"`) {
			h.Set("ETag", "W/"+etag) // compressed bytes differ, a strong validator would let If-Range match them
		}
		w.compressing = true
		switch {
		case w.head: // an empty compressed stream would become the advertised Content-Length
		case w.encoding == "gzip":
			w.comp = gzip.NewWriter(w.ResponseWriter)
		default:
			w.comp = zlib.NewWriter(w.ResponseWriter) // http "deflate" is the zlib format, not a raw deflate stream
		}
	}
	w.addVary()
	w.ResponseWriter.WriteHeader(w.status)
}

func (w *compressWriter) compressible() bool {
	h := w.Header()
	if w.encoding == "" || !w.bodyAllowed(w.status) || w.status == http.StatusPartialContent {
		return false
	}
	if h.Get("Content-Encoding") != "" || h.Get("Content-Range") != "" {
		return false
	}
	if _, ok := h["Content-Type"]; !ok {
		return false
	}
	mediaType, _, _ := strings.Cut(h.Get("Content-Type"), ";")
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	for _, p := range w.exclude {
		if mediaType == p || (strings.HasSuffix(p, "/*") && strings.HasPrefix(mediaType, p[:len(p)-1])) {
			return false
		}
	}
	return true
}

// addVary sets Vary at commit time because the reverse proxy clears response headers after a 1xx.
func (w *compressWriter) addVary() {
	for _, v := range w.Header().Values("Vary") {
		for token := range strings.SplitSeq(v, ",") {
			if t := strings.TrimSpace(token); t == "*" || strings.EqualFold(t, "Accept-Encoding") {
				return
			}
		}
	}
	w.Header().Add("Vary", "Accept-Encoding")
}

// finish commits a status set without a body and closes the compressor. A handler that wrote nothing
// at all is left to net/http's default response.
func (w *compressWriter) finish() {
	if !w.decided && w.status != 0 {
		w.decide()
	}
	if w.comp != nil {
		if err := w.comp.Close(); err != nil {
			log.Printf("[DEBUG] failed to close %s writer: %v", w.encoding, err)
		}
	}
}

func (w *compressWriter) bodyAllowed(code int) bool {
	return code >= 200 && code != http.StatusNoContent && code != http.StatusNotModified
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

// limiterUserHandler throttles per-user activity. When the matched route has Throttle > 0,
// it applies a per-route limiter at that rate keyed by [ip, dst]. Otherwise it falls back
// to the global limiter at reqSec, keyed by [ip] and, for MTProxy matches only, [ip, dst].
// A request rejected by either limit returns 429. reqSec = 0 disables the global path while
// per-route overrides still apply.
func limiterUserHandler(reqSec int) func(next http.Handler) http.Handler {
	var routeLimiters sync.Map // map[string]*limiter.Limiter, keyed by server\x00srcMatch\x00rate
	var globalLmt *limiter.Limiter
	if reqSec > 0 {
		globalLmt = tollbooth.NewLimiter(float64(reqSec), nil)
	}
	getRouteLimiter := func(m discovery.URLMapper) *limiter.Limiter {
		// NUL separator is invalid in hostnames and regex source, avoiding key collisions
		key := m.Server + "\x00" + m.SrcMatch.String() + "\x00" + strconv.Itoa(m.Throttle)
		if v, ok := routeLimiters.Load(key); ok {
			return v.(*limiter.Limiter)
		}
		actual, _ := routeLimiters.LoadOrStore(key, tollbooth.NewLimiter(float64(m.Throttle), nil))
		return actual.(*limiter.Limiter)
	}

	return func(h http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			var (
				match   discovery.MatchedRoute
				matched bool
			)
			if v := r.Context().Value(ctxMatch); v != nil {
				match = v.(discovery.MatchedRoute)
				matched = true
			}

			if matched && match.Mapper.Throttle > 0 {
				lmt := getRouteLimiter(match.Mapper)
				ip := libstring.RemoteIP(lmt.GetIPLookups(), lmt.GetForwardedForIndexFromBehind(), r)
				if httpError := tollbooth.LimitByKeys(lmt, []string{ip, match.Mapper.Dst}); httpError != nil {
					http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
					return
				}
				h.ServeHTTP(w, r)
				return
			}

			if globalLmt == nil {
				h.ServeHTTP(w, r)
				return
			}
			lmt := globalLmt
			keys := []string{libstring.RemoteIP(lmt.GetIPLookups(), lmt.GetForwardedForIndexFromBehind(), r)}
			if matched {
				if matchType, ok := r.Context().Value(ctxMatchType).(discovery.MatchType); ok && matchType == discovery.MTProxy {
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

// writeDeadlineGrace extends the connection's write deadline beyond the per-route ctx
// timeout so the proxy's default ErrorHandler has time to deliver 502 to the client
// when the ctx fires; without this buffer the conn is severed at the same instant
// and the client sees EOF instead of the error status.
const writeDeadlineGrace = 500 * time.Millisecond

// routeTimeoutHandler applies a per-route request deadline when the matched mapper has
// Timeout > 0. It derives a deadline-bound request context and also sets the underlying
// connection's read deadline to the timeout and the write deadline to timeout +
// writeDeadlineGrace so the proxy's ErrorHandler can still deliver a 502 to the client
// when the deadline fires. Both connection deadlines are cleared on return so kept-alive
// connections do not carry an expired deadline into subsequent unrelated requests.
func routeTimeoutHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCtx := r.Context()
		if reqCtx.Value(ctxMatch) == nil {
			next.ServeHTTP(w, r)
			return
		}
		match := reqCtx.Value(ctxMatch).(discovery.MatchedRoute)
		if match.Mapper.Timeout <= 0 {
			next.ServeHTTP(w, r)
			return
		}
		readDeadline := time.Now().Add(match.Mapper.Timeout)
		ctx, cancel := context.WithDeadline(reqCtx, readDeadline)
		defer cancel()
		rc := http.NewResponseController(w)
		if err := rc.SetReadDeadline(readDeadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
			log.Printf("[DEBUG] route timeout: SetReadDeadline failed: %v", err)
		}
		if err := rc.SetWriteDeadline(readDeadline.Add(writeDeadlineGrace)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			log.Printf("[DEBUG] route timeout: SetWriteDeadline failed: %v", err)
		}
		// clear per-route deadlines after the request so kept-alive connections do not
		// carry an expired write deadline into subsequent unrelated requests
		defer func() {
			if err := rc.SetReadDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
				log.Printf("[DEBUG] route timeout: clear read deadline failed: %v", err)
			}
			if err := rc.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
				log.Printf("[DEBUG] route timeout: clear write deadline failed: %v", err)
			}
		}()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
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
