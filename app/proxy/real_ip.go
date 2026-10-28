package proxy

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
)

// TrustedProxies is the set of networks whose X-Forwarded-For and X-Forwarded-Host/Proto/Port
// headers are believed. An empty set trusts nobody, so the client address is always the TCP peer.
type TrustedProxies []netip.Prefix

// ParseTrustedProxies parses CIDRs and bare IPs. IPv4-mapped IPv6 entries are converted to IPv4.
func ParseTrustedProxies(items []string) (TrustedProxies, error) {
	res := make(TrustedProxies, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		pfx, err := parseTrustedPrefix(item)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy %q: %w", item, err)
		}
		res = append(res, pfx)
	}
	return res, nil
}

func parseTrustedPrefix(s string) (netip.Prefix, error) {
	var pfx netip.Prefix
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("parse cidr: %w", err)
		}
		pfx = p
	} else {
		addr, err := netip.ParseAddr(s)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("parse ip: %w", err)
		}
		if addr.Zone() != "" {
			return netip.Prefix{}, fmt.Errorf("zoned address not allowed")
		}
		pfx = netip.PrefixFrom(addr, addr.BitLen())
	}
	if pfx.Addr().Is4In6() {
		if pfx.Bits() < 96 {
			return netip.Prefix{}, fmt.Errorf("ipv4-mapped prefix shorter than /96")
		}
		pfx = netip.PrefixFrom(pfx.Addr().Unmap(), pfx.Bits()-96)
	}
	return pfx.Masked(), nil
}

// clientIP returns the address of the client that sent r, or an empty string if the TCP peer
// address can't be parsed. X-Forwarded-For is read right to left only while each hop is trusted,
// and a malformed hop ends the walk at the peer, so a client can't skip over it to a forged entry.
func (tp TrustedProxies) clientIP(r *http.Request) string {
	peer, ok := tp.peerAddr(r)
	if !ok {
		return ""
	}
	if !tp.contains(peer) {
		return peer.String()
	}
	fields := r.Header.Values("X-Forwarded-For")
	if len(fields) == 0 {
		return peer.String()
	}
	hops := strings.Split(strings.Join(fields, ","), ",")
	leftmost := peer
	for _, hop := range slices.Backward(hops) {
		addr, err := netip.ParseAddr(strings.TrimSpace(hop))
		if err != nil || addr.Zone() != "" {
			return peer.String()
		}
		addr = addr.Unmap()
		if !tp.contains(addr) {
			return addr.String()
		}
		leftmost = addr
	}
	return leftmost.String()
}

func (tp TrustedProxies) trustedPeer(r *http.Request) bool {
	peer, ok := tp.peerAddr(r)
	return ok && tp.contains(peer)
}

func (tp TrustedProxies) contains(addr netip.Addr) bool {
	for _, pfx := range tp {
		if pfx.Contains(addr) {
			return true
		}
	}
	return false
}

// peerAddr strips socket-local zones before address matching.
func (tp TrustedProxies) peerAddr(r *http.Request) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return netip.Addr{}, false
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.WithZone("").Unmap(), true
}
