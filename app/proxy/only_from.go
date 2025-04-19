package proxy

import (
	"bytes"
	"net"
	"net/http"
	"strings"

	log "github.com/go-pkgz/lgr"

	"github.com/umputun/reproxy/app/discovery"
)

// OnlyFrom implements middleware to allow access for a limited list of source IPs.
type OnlyFrom struct {
	lookups []OFLookup
}

// OFLookup defines lookup method for source IP.
type OFLookup string

// enum of possible lookup methods
const (
	OFRemoteAddr OFLookup = "remote-addr"
	OFRealIP     OFLookup = "real-ip"
	OFForwarded  OFLookup = "forwarded"
)

// NewOnlyFrom creates OnlyFrom middleware with given lookup methods.
func NewOnlyFrom(lookups ...OFLookup) *OnlyFrom {
	return &OnlyFrom{lookups: lookups}
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

		realIP := o.realIP(o.lookups, r)
		if realIP != "" && o.matchRemoteIP(realIP, allowedIPs) {
			next.ServeHTTP(w, r)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		log.Printf("[INFO] ip %q rejected for %s", realIP, r.URL.String())
	}
	return http.HandlerFunc(fn)
}

func (o *OnlyFrom) realIP(ipLookups []OFLookup, r *http.Request) string {
	realIP := r.Header.Get("X-Real-IP")
	forwardedFor := r.Header.Get("X-Forwarded-For")

	for _, lookup := range ipLookups {

		if lookup == OFRemoteAddr {
			ip, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				return r.RemoteAddr // can't parse, return as is
			}
			return ip
		}

		if lookup == OFForwarded && forwardedFor != "" {
			// x-Forwarded-For is potentially a list of addresses separated with ","
			// the left-most being the original client, and each successive proxy that passed the request
			// adding the IP address where it received the request from.
			// in case if the original IP is a private behind a proxy, we need to get the first public IP from the list
			return preferPublicIP(strings.Split(forwardedFor, ","))
		}

		if lookup == OFRealIP && realIP != "" {
			return realIP
		}
	}

	return "" // we can't get real ip
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

// preferPublicIP returns first public IP from the list of IPs
// if no public IP found, returns first IP from the list
func preferPublicIP(ips []string) string {
	for _, ip := range ips {
		ip = strings.TrimSpace(ip)
		if net.ParseIP(ip).IsGlobalUnicast() && !isPrivateSubnet(net.ParseIP(ip)) {
			return ip
		}
	}
	return strings.TrimSpace(ips[0])
}

type ipRange struct {
	start net.IP
	end   net.IP
}

var privateRanges = []ipRange{
	{start: net.ParseIP("10.0.0.0"), end: net.ParseIP("10.255.255.255")},
	{start: net.ParseIP("100.64.0.0"), end: net.ParseIP("100.127.255.255")},
	{start: net.ParseIP("172.16.0.0"), end: net.ParseIP("172.31.255.255")},
	{start: net.ParseIP("192.0.0.0"), end: net.ParseIP("192.0.0.255")},
	{start: net.ParseIP("192.168.0.0"), end: net.ParseIP("192.168.255.255")},
	{start: net.ParseIP("198.18.0.0"), end: net.ParseIP("198.19.255.255")},
	{start: net.ParseIP("::1"), end: net.ParseIP("::1")},
	{start: net.ParseIP("fc00::"), end: net.ParseIP("fdff:ffff:ffff:ffff:ffff:ffff:ffff:ffff")},
	{start: net.ParseIP("fe80::"), end: net.ParseIP("febf:ffff:ffff:ffff:ffff:ffff:ffff:ffff")},
}

// isPrivateSubnet - check to see if this ip is in a private subnet
func isPrivateSubnet(ipAddress net.IP) bool {
	inRange := func(r ipRange, ipAddress net.IP) bool {
		// ensure the IPs are in the same format for comparison
		ipAddress = ipAddress.To16()
		r.start = r.start.To16()
		r.end = r.end.To16()
		return bytes.Compare(ipAddress, r.start) >= 0 && bytes.Compare(ipAddress, r.end) <= 0
	}
	for _, r := range privateRanges {
		if inRange(r, ipAddress) {
			return true
		}
	}
	return false
}
