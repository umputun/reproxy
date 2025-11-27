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
		err                    bool
	}{
		{"example.com,123,456, ping ", "example.com", "123", "456", "ping", false, false, false},
		{"*,123,456,", "*", "123", "456", "", false, false, false},
		{"123,456", "", "", "", "", false, false, true},
		{"123", "", "", "", "", false, false, true},
		{"example.com , 123, 456 ,ping", "example.com", "123", "456", "ping", false, false, false},
		{"example.com,123, assets:456, ping ", "example.com", "123", "456", "ping", true, false, false},
		{"example.com,123, assets:456 ", "example.com", "123", "456", "", true, false, false},
		{"example.com,123, spa:456 ", "example.com", "123", "456", "", true, true, false},
	}

	for i, tt := range tbl {
		tt := tt
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
			if tt.static {
				assert.Equal(t, discovery.MTStatic, res[0].MatchType)
				assert.Equal(t, tt.spa, res[0].AssetsSPA)
			} else {
				assert.Equal(t, discovery.MTProxy, res[0].MatchType)
			}
		})
	}

}
