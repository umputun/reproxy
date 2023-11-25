package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/umputun/reproxy/app/discovery"
)

func TestOnlyFrom_Handler(t *testing.T) {
	tbl := []struct {
		name               string
		lookups            []OFLookup
		allowedIPs         []string
		remoteAddr         string
		realIP             string
		forwardedFor       string
		expectedStatusCode int
	}{
		{
			name:               "allowed IP",
			lookups:            []OFLookup{OFRemoteAddr},
			allowedIPs:         []string{"192.168.1.1"},
			remoteAddr:         "192.168.1.1:1234",
			expectedStatusCode: http.StatusOK,
		},
		{
			name:               "disallowed IP",
			lookups:            []OFLookup{OFRemoteAddr},
			allowedIPs:         []string{"192.168.1.1"},
			remoteAddr:         "192.168.1.2:1234",
			expectedStatusCode: http.StatusForbidden,
		},
		{
			name:               "no restrictions",
			lookups:            []OFLookup{OFRemoteAddr},
			allowedIPs:         []string{},
			remoteAddr:         "192.168.1.2:1234",
			expectedStatusCode: http.StatusOK,
		},
		{
			name:               "allowed IP with RealIP lookup",
			lookups:            []OFLookup{OFRealIP},
			allowedIPs:         []string{"192.168.1.1"},
			realIP:             "192.168.1.1",
			expectedStatusCode: http.StatusOK,
		},
		{
			name:               "disallowed IP with RealIP lookup",
			lookups:            []OFLookup{OFRealIP},
			allowedIPs:         []string{"192.168.1.1"},
			realIP:             "192.168.1.2",
			expectedStatusCode: http.StatusForbidden,
		},
		{
			name:               "allowed IP with Forwarded lookup",
			lookups:            []OFLookup{OFForwarded},
			allowedIPs:         []string{"192.168.1.1"},
			forwardedFor:       "192.168.1.1",
			expectedStatusCode: http.StatusOK,
		},
		{
			name:               "allowed IP with Forwarded lookup, mix private and public IPs",
			lookups:            []OFLookup{OFForwarded},
			allowedIPs:         []string{"8.8.8.8"},
			forwardedFor:       "192.168.1.1, 10.0.0.5, 8.8.8.8, 10.10.10.10",
			expectedStatusCode: http.StatusOK,
		},
		{
			name:               "disallowed IP with Forwarded lookup",
			lookups:            []OFLookup{OFForwarded},
			allowedIPs:         []string{"192.168.1.1"},
			forwardedFor:       "192.168.1.2",
			expectedStatusCode: http.StatusForbidden,
		},
		{
			name:               "multiple lookups, allowed IP",
			lookups:            []OFLookup{OFRemoteAddr, OFRealIP},
			allowedIPs:         []string{"192.168.1.1", "192.168.1.2"},
			remoteAddr:         "192.168.1.2:1234",
			realIP:             "192.168.1.1",
			expectedStatusCode: http.StatusOK,
		},
		{
			name:               "multiple lookups, disallowed IP",
			lookups:            []OFLookup{OFRemoteAddr, OFRealIP},
			allowedIPs:         []string{"192.168.1.1", "192.168.1.2"},
			remoteAddr:         "192.168.1.3:1234",
			realIP:             "192.168.1.3",
			expectedStatusCode: http.StatusForbidden,
		},
		{
			name:               "CIDR block, allowed IP",
			lookups:            []OFLookup{OFRemoteAddr},
			allowedIPs:         []string{"192.168.1.0/24"},
			remoteAddr:         "192.168.1.2:1234",
			expectedStatusCode: http.StatusOK,
		},
		{
			name:               "CIDR block, disallowed IP",
			lookups:            []OFLookup{OFRemoteAddr},
			allowedIPs:         []string{"192.168.1.0/24"},
			remoteAddr:         "192.168.2.2:1234",
			expectedStatusCode: http.StatusForbidden,
		},
		{
			name:               "invalid remote address format",
			lookups:            []OFLookup{OFRemoteAddr},
			allowedIPs:         []string{"192.168.1.1"},
			remoteAddr:         "invalid_format",
			expectedStatusCode: http.StatusForbidden,
		},
		{
			name:               "empty remote address",
			lookups:            []OFLookup{OFRemoteAddr},
			allowedIPs:         []string{"192.168.1.1"},
			remoteAddr:         "",
			expectedStatusCode: http.StatusForbidden,
		},
	}

	for _, tt := range tbl {
		t.Run(tt.name, func(t *testing.T) {
			onlyFrom := NewOnlyFrom(tt.lookups...)
			handler := onlyFrom.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

			req := httptest.NewRequest("GET", "http://example.com/foo", http.NoBody)
			req.RemoteAddr = tt.remoteAddr
			req.Header.Set("X-Real-IP", tt.realIP)
			req.Header.Set("X-Forwarded-For", tt.forwardedFor)
			req = req.WithContext(context.WithValue(req.Context(),
				ctxMatch, discovery.MatchedRoute{Mapper: discovery.URLMapper{OnlyFromIPs: tt.allowedIPs}}))

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			assert.Equal(t, tt.expectedStatusCode, rr.Code)
		})
	}
}
