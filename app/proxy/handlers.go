package proxy

import (
	"io"
	"net/http"
	"strings"

	"github.com/didip/tollbooth/v6"
	"github.com/didip/tollbooth/v6/libstring"
	log "github.com/go-pkgz/lgr"
	R "github.com/go-pkgz/rest"
	"github.com/gorilla/handlers"

	"github.com/umputun/reproxy/app/discovery"
)

func headersHandler(headers []string) func(next http.Handler) http.Handler {

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(headers) == 0 {
				next.ServeHTTP(w, r)
				return
			}
			for _, h := range headers {
				elems := strings.Split(h, ":")
				if len(elems) != 2 {
					continue
				}
				w.Header().Set(strings.TrimSpace(elems[0]), strings.TrimSpace(elems[1]))
			}
			next.ServeHTTP(w, r)
		})
	}
}

func maxReqSizeHandler(maxSize int64) func(next http.Handler) http.Handler {
	if maxSize <= 0 {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r)
			})
		}
	}

	log.Printf("[DEBUG] request size limited to %d", maxSize)
	return func(next http.Handler) http.Handler {

		fn := func(w http.ResponseWriter, r *http.Request) {

			// check ContentLength
			if r.ContentLength > maxSize {
				w.WriteHeader(http.StatusRequestEntityTooLarge)
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

	if enable {
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

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}

func gzipHandler(enabled bool) func(next http.Handler) http.Handler {
	if enabled {
		log.Printf("[DEBUG] gzip enabled")
		return handlers.CompressHandler
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}

func signatureHandler(enabled bool, version string) func(next http.Handler) http.Handler {
	if enabled {
		log.Printf("[DEBUG] signature headers enabled")
		return R.AppInfo("reproxy", "umputun", version)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}

func limiterHandler(reqSec int) func(next http.Handler) http.Handler {

	if reqSec <= 0 {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r)
			})
		}
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
