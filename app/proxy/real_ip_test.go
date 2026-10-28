package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTrustedProxies(t *testing.T) {
	tbl := []struct {
		name    string
		items   []string
		want    []string
		wantErr bool
	}{
		{name: "empty", items: nil, want: []string{}},
		{name: "cidrs and bare ips", items: []string{"10.0.0.0/8", " 192.168.1.5 ", "", "2001:db8::/32", "::1"},
			want: []string{"10.0.0.0/8", "192.168.1.5/32", "2001:db8::/32", "::1/128"}},
		{name: "unmasked cidr is masked", items: []string{"10.1.2.3/8"}, want: []string{"10.0.0.0/8"}},
		{name: "ipv4-mapped cidr converts width", items: []string{"::ffff:10.0.0.0/104"}, want: []string{"10.0.0.0/8"}},
		{name: "ipv4-mapped bare ip", items: []string{"::ffff:1.2.3.4"}, want: []string{"1.2.3.4/32"}},
		{name: "ipv4-mapped cidr shorter than /96", items: []string{"::ffff:0.0.0.0/80"}, wantErr: true},
		{name: "garbage", items: []string{"10.0.0.0/8", "proxy.example.com"}, wantErr: true},
		{name: "bad prefix length", items: []string{"10.0.0.0/33"}, wantErr: true},
		{name: "zoned ip", items: []string{"fe80::1%en0"}, wantErr: true},
	}

	for _, tt := range tbl {
		t.Run(tt.name, func(t *testing.T) {
			res, err := ParseTrustedProxies(tt.items)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			got := make([]string, 0, len(res))
			for _, p := range res {
				got = append(got, p.String())
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTrustedProxies_clientIP(t *testing.T) {
	tbl := []struct {
		name       string
		trusted    []string
		remoteAddr string
		xff        []string
		want       string
	}{
		{name: "no trust, no xff", remoteAddr: "203.0.113.9:1234", want: "203.0.113.9"},
		{name: "no trust, spoofed xff", remoteAddr: "203.0.113.9:1234", xff: []string{"6.6.6.6"}, want: "203.0.113.9"},
		{name: "untrusted peer, spoofed xff", trusted: []string{"10.0.0.0/8"}, remoteAddr: "203.0.113.9:1234",
			xff: []string{"6.6.6.6"}, want: "203.0.113.9"},
		{name: "trusted peer, no xff", trusted: []string{"10.0.0.0/8"}, remoteAddr: "10.0.0.2:1234", want: "10.0.0.2"},
		{name: "trusted peer, empty xff", trusted: []string{"10.0.0.0/8"}, remoteAddr: "10.0.0.2:1234", xff: []string{""},
			want: "10.0.0.2"},
		{name: "trusted peer, client appended", trusted: []string{"10.0.0.0/8"}, remoteAddr: "10.0.0.2:1234",
			xff: []string{"6.6.6.6, 203.0.113.9"}, want: "203.0.113.9"},
		{name: "trusted chain skipped right to left", trusted: []string{"10.0.0.0/8", "172.16.0.0/12"},
			remoteAddr: "10.0.0.2:1234", xff: []string{"6.6.6.6, 203.0.113.9, 172.16.0.3 , 10.0.0.7"}, want: "203.0.113.9"},
		{name: "untrusted private hop wins", trusted: []string{"10.0.0.0/8"}, remoteAddr: "10.0.0.2:1234",
			xff: []string{"203.0.113.9, 192.168.1.5"}, want: "192.168.1.5"},
		{name: "multiple xff fields joined in order", trusted: []string{"10.0.0.0/8"}, remoteAddr: "10.0.0.2:1234",
			xff: []string{"6.6.6.6", "203.0.113.9"}, want: "203.0.113.9"},
		{name: "all hops trusted, left-most", trusted: []string{"10.0.0.0/8"}, remoteAddr: "10.0.0.2:1234",
			xff: []string{"10.0.0.5, 10.0.0.6"}, want: "10.0.0.5"},
		{name: "malformed hop stops walk", trusted: []string{"10.0.0.0/8"}, remoteAddr: "10.0.0.2:1234",
			xff: []string{"6.6.6.6, unknown, 10.0.0.6"}, want: "10.0.0.2"},
		{name: "empty hop stops walk", trusted: []string{"10.0.0.0/8"}, remoteAddr: "10.0.0.2:1234",
			xff: []string{"6.6.6.6,,10.0.0.6"}, want: "10.0.0.2"},
		{name: "malformed hop left of client not examined", trusted: []string{"10.0.0.0/8"}, remoteAddr: "10.0.0.2:1234",
			xff: []string{"garbage, 203.0.113.9, 10.0.0.6"}, want: "203.0.113.9"},
		{name: "zoned hop stops walk", trusted: []string{"10.0.0.0/8"}, remoteAddr: "10.0.0.2:1234",
			xff: []string{"6.6.6.6, fe80::1%en0"}, want: "10.0.0.2"},
		{name: "hop with port stops walk", trusted: []string{"10.0.0.0/8"}, remoteAddr: "10.0.0.2:1234",
			xff: []string{"6.6.6.6, 203.0.113.9:443"}, want: "10.0.0.2"},
		{name: "ipv4-mapped hop unmapped", trusted: []string{"10.0.0.0/8"}, remoteAddr: "10.0.0.2:1234",
			xff: []string{"::ffff:203.0.113.9"}, want: "203.0.113.9"},
		{name: "ipv6 trusted peer", trusted: []string{"2001:db8::/32"}, remoteAddr: "[2001:db8::1]:1234",
			xff: []string{"2001:db9::5"}, want: "2001:db9::5"},
		{name: "ipv4-mapped peer matches ipv4 cidr", trusted: []string{"10.0.0.0/8"}, remoteAddr: "[::ffff:10.0.0.2]:1234",
			xff: []string{"203.0.113.9"}, want: "203.0.113.9"},
		{name: "zoned peer", remoteAddr: "[fe80::1%en0]:1234", xff: []string{"6.6.6.6"}, want: "fe80::1"},
		{name: "unparsable peer", remoteAddr: "garbage", want: ""},
	}

	for _, tt := range tbl {
		t.Run(tt.name, func(t *testing.T) {
			tp, err := ParseTrustedProxies(tt.trusted)
			require.NoError(t, err)
			req := httptest.NewRequest(http.MethodGet, "http://example.com/", http.NoBody)
			req.RemoteAddr = tt.remoteAddr
			for _, v := range tt.xff {
				req.Header.Add("X-Forwarded-For", v)
			}
			assert.Equal(t, tt.want, tp.clientIP(req))
		})
	}
}
