package middleware

import (
	"net/http"
	"strings"
)

// Headers middleware adds headers to request
func Headers(headers ...string) func(http.Handler) http.Handler {

	return func(h http.Handler) http.Handler {

		fn := func(w http.ResponseWriter, r *http.Request) {
			for _, h := range headers {
				elems := strings.Split(h, ":")
				if len(elems) != 2 {
					continue
				}
				r.Header.Set(strings.TrimSpace(elems[0]), strings.TrimSpace(elems[1]))
			}
			h.ServeHTTP(w, r)
		}
		return http.HandlerFunc(fn)
	}
}
