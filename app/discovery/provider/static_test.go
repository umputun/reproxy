package provider

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/reproxy/app/discovery"
)

func TestStatic_List(t *testing.T) {

	tbl := []struct {
		rule                   string
		server, src, dst, ping string
		static, spa            bool
		forwardHealthChecks    bool
		timeout                time.Duration
		throttle               int
		err                    bool
	}{
		{"example.com,123,456, ping ", "example.com", "123", "456", "ping", false, false, false, 0, 0, false},
		{"*,123,456,", "*", "123", "456", "", false, false, false, 0, 0, false},
		{"123,456", "", "", "", "", false, false, false, 0, 0, true},
		{"123", "", "", "", "", false, false, false, 0, 0, true},
		{"example.com , 123, 456 ,ping", "example.com", "123", "456", "ping", false, false, false, 0, 0, false},
		{"example.com,123, assets:456, ping ", "example.com", "123", "456", "ping", true, false, false, 0, 0, false},
		{"example.com,123, assets:456 ", "example.com", "123", "456", "", true, false, false, 0, 0, false},
		{"example.com,123, spa:456 ", "example.com", "123", "456", "", true, true, false, 0, 0, false},
		{"example.com,^/(.*),/$1,/ping,true", "example.com", "^/(.*)", "/$1", "/ping", false, false, true, 0, 0, false},
		{"example.com,^/(.*),/$1,,yes", "example.com", "^/(.*)", "/$1", "", false, false, true, 0, 0, false},
		{"example.com,^/(.*),/$1,,false", "example.com", "^/(.*)", "/$1", "", false, false, false, 0, 0, false},
		{"example.com,^/(.*),/$1,,no", "example.com", "^/(.*)", "/$1", "", false, false, false, 0, 0, false},
		{"example.com,^/up/(.*),/$1,,,5m", "example.com", "^/up/(.*)", "/$1", "", false, false, false, 5 * time.Minute, 0, false},
		{"example.com,^/up/(.*),/$1,,,,10", "example.com", "^/up/(.*)", "/$1", "", false, false, false, 0, 10, false},
		{"example.com,^/up/(.*),/$1,,,5m,10", "example.com", "^/up/(.*)", "/$1", "", false, false, false, 5 * time.Minute, 10, false},
		{"example.com,^/up/(.*),/$1,/ping,true,30s,5", "example.com", "^/up/(.*)", "/$1", "/ping", false, false, true, 30 * time.Second, 5, false},
		{"example.com,^/up/(.*),/$1,,,bad", "", "", "", "", false, false, false, 0, 0, true},
		{"example.com,^/up/(.*),/$1,,,-5s", "", "", "", "", false, false, false, 0, 0, true},
		{"example.com,^/up/(.*),/$1,,,,abc", "", "", "", "", false, false, false, 0, 0, true},
		{"example.com,^/up/(.*),/$1,,,,-1", "", "", "", "", false, false, false, 0, 0, true},
	}

	for i, tt := range tbl {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			s := Static{Rules: []string{tt.rule}}
			res, err := s.List()
			if tt.err {
				require.Error(t, err)
				return
			}
			require.Len(t, res, 1)
			assert.Equal(t, tt.server, res[0].Server)
			assert.Equal(t, tt.src, res[0].SrcMatch.String())
			assert.Equal(t, tt.dst, res[0].Dst)
			assert.Equal(t, tt.ping, res[0].PingURL)
			assert.Equal(t, tt.forwardHealthChecks, res[0].ForwardHealthChecks)
			assert.Equal(t, tt.timeout, res[0].Timeout)
			assert.Equal(t, tt.throttle, res[0].Throttle)
			if tt.static {
				assert.Equal(t, discovery.MTStatic, res[0].MatchType)
				assert.Equal(t, tt.spa, res[0].AssetsSPA)
			} else {
				assert.Equal(t, discovery.MTProxy, res[0].MatchType)
			}
		})
	}

}
