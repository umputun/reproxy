package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/reproxy/app/discovery"
)

func TestOnlyFrom_Handler(t *testing.T) {
	tbl := []struct {
		name               string
		trusted            []string
		allowedIPs         []string
		remoteAddr         string
		realIP             string
		forwardedFor       string
		expectedStatusCode int
	}{
		{name: "allowed IP", allowedIPs: []string{"192.168.1.1"}, remoteAddr: "192.168.1.1:1234",
			expectedStatusCode: http.StatusOK},
		{name: "disallowed IP", allowedIPs: []string{"192.168.1.1"}, remoteAddr: "192.168.1.2:1234",
			expectedStatusCode: http.StatusForbidden},
		{name: "no restrictions", allowedIPs: []string{}, remoteAddr: "192.168.1.2:1234", expectedStatusCode: http.StatusOK},
		{name: "spoofed X-Real-IP ignored", allowedIPs: []string{"192.168.1.1"}, remoteAddr: "192.168.1.2:1234",
			realIP: "192.168.1.1", expectedStatusCode: http.StatusForbidden},
		{name: "spoofed X-Forwarded-For from untrusted peer ignored", allowedIPs: []string{"192.168.1.1"},
			remoteAddr: "192.168.1.2:1234", forwardedFor: "192.168.1.1", expectedStatusCode: http.StatusForbidden},
		{name: "trusted proxy, allowed client from X-Forwarded-For", trusted: []string{"10.0.0.0/8"},
			allowedIPs: []string{"8.8.8.8"}, remoteAddr: "10.0.0.2:1234", forwardedFor: "1.1.1.1, 8.8.8.8, 10.0.0.5",
			expectedStatusCode: http.StatusOK},
		{name: "trusted proxy, disallowed client from X-Forwarded-For", trusted: []string{"10.0.0.0/8"},
			allowedIPs: []string{"10.0.0.0/8"}, remoteAddr: "10.0.0.2:1234", forwardedFor: "203.0.113.9",
			expectedStatusCode: http.StatusForbidden},
		{name: "trusted proxy, X-Real-IP ignored", trusted: []string{"10.0.0.0/8"}, allowedIPs: []string{"8.8.8.8"},
			remoteAddr: "10.0.0.2:1234", realIP: "8.8.8.8", forwardedFor: "203.0.113.9", expectedStatusCode: http.StatusForbidden},
		{name: "CIDR block, allowed IP", allowedIPs: []string{"192.168.1.0/24"}, remoteAddr: "192.168.1.2:1234",
			expectedStatusCode: http.StatusOK},
		{name: "CIDR block, disallowed IP", allowedIPs: []string{"192.168.1.0/24"}, remoteAddr: "192.168.2.2:1234",
			expectedStatusCode: http.StatusForbidden},
		{name: "invalid remote address format", allowedIPs: []string{"192.168.1.1"}, remoteAddr: "invalid_format",
			expectedStatusCode: http.StatusForbidden},
		{name: "empty remote address", allowedIPs: []string{"192.168.1.1"}, remoteAddr: "",
			expectedStatusCode: http.StatusForbidden},
	}

	for _, tt := range tbl {
		t.Run(tt.name, func(t *testing.T) {
			trusted, err := ParseTrustedProxies(tt.trusted)
			require.NoError(t, err)
			onlyFrom := NewOnlyFrom(trusted)
			handler := onlyFrom.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

			req := httptest.NewRequest("GET", "http://example.com/foo", http.NoBody)
			req.RemoteAddr = tt.remoteAddr
			if tt.realIP != "" {
				req.Header.Set("X-Real-IP", tt.realIP)
			}
			if tt.forwardedFor != "" {
				req.Header.Set("X-Forwarded-For", tt.forwardedFor)
			}
			req = req.WithContext(context.WithValue(req.Context(),
				ctxMatch, discovery.MatchedRoute{Mapper: discovery.URLMapper{OnlyFromIPs: tt.allowedIPs}}))

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			assert.Equal(t, tt.expectedStatusCode, rr.Code)
		})
	}
}
