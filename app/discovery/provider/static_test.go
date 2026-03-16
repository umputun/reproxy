package provider

import (
	"strconv"
	"testing"

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
		err                    bool
	}{
		{"example.com,123,456, ping ", "example.com", "123", "456", "ping", false, false, false, false},
		{"*,123,456,", "*", "123", "456", "", false, false, false, false},
		{"123,456", "", "", "", "", false, false, false, true},
		{"123", "", "", "", "", false, false, false, true},
		{"example.com , 123, 456 ,ping", "example.com", "123", "456", "ping", false, false, false, false},
		{"example.com,123, assets:456, ping ", "example.com", "123", "456", "ping", true, false, false, false},
		{"example.com,123, assets:456 ", "example.com", "123", "456", "", true, false, false, false},
		{"example.com,123, spa:456 ", "example.com", "123", "456", "", true, true, false, false},
		{"example.com,^/(.*),/$1,/ping,true", "example.com", "^/(.*)", "/$1", "/ping", false, false, true, false},
		{"example.com,^/(.*),/$1,,yes", "example.com", "^/(.*)", "/$1", "", false, false, true, false},
		{"example.com,^/(.*),/$1,,false", "example.com", "^/(.*)", "/$1", "", false, false, false, false},
		{"example.com,^/(.*),/$1,,no", "example.com", "^/(.*)", "/$1", "", false, false, false, false},
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
			if tt.static {
				assert.Equal(t, discovery.MTStatic, res[0].MatchType)
				assert.Equal(t, tt.spa, res[0].AssetsSPA)
			} else {
				assert.Equal(t, discovery.MTProxy, res[0].MatchType)
			}
		})
	}

}
