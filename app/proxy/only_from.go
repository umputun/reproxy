package proxy

import (
	"net"
	"net/http"

	log "github.com/go-pkgz/lgr"

	"github.com/umputun/reproxy/app/discovery"
)

// OnlyFrom implements middleware to allow access for a limited list of source IPs.
type OnlyFrom struct {
	trusted TrustedProxies
}

// NewOnlyFrom creates OnlyFrom middleware. The source IP is the TCP peer, or the address reported
// through X-Forwarded-For when the peer is one of the trusted proxies.
func NewOnlyFrom(trusted TrustedProxies) *OnlyFrom {
	return &OnlyFrom{trusted: trusted}
}

// Handler implements middleware interface.
func (o *OnlyFrom) Handler(next http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		var allowedIPs []string
		reqCtx := r.Context()
		if reqCtx.Value(ctxMatch) != nil { // route match detected by matchHandler
			match := reqCtx.Value(ctxMatch).(discovery.MatchedRoute)
			allowedIPs = match.Mapper.OnlyFromIPs
		}
		if len(allowedIPs) == 0 {
			// no restrictions if no ips defined
			next.ServeHTTP(w, r)
			return
		}

		realIP := o.trusted.clientIP(r)
		if realIP != "" && o.matchRemoteIP(realIP, allowedIPs) {
			next.ServeHTTP(w, r)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		log.Printf("[INFO] ip %q rejected for %s", realIP, r.URL.String())
	}
	return http.HandlerFunc(fn)
}

// matchRemoteIP returns true if request's ip matches any of ips in the list of allowedIPs.
// allowedIPs can be defined as IP (like 192.168.1.12) or CIDR (192.168.0.0/16)
func (o *OnlyFrom) matchRemoteIP(remoteIP string, allowedIPs []string) bool {
	for _, allowedIP := range allowedIPs {
		// check for ip prefix or CIDR
		if _, cidrnet, err := net.ParseCIDR(allowedIP); err == nil {
			if cidrnet.Contains(net.ParseIP(remoteIP)) {
				return true
			}
		}
		// check for ip match
		if remoteIP == allowedIP {
			return true
		}
	}
	return false
}
