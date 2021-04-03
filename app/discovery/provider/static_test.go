package provider

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatic_List(t *testing.T) {

	tbl := []struct {
		rule             string
		server, src, dst string
		err              bool
	}{
		{"example.com,123,456", "example.com", "123", "456", false},
		{"*,123,456", "*", "123", "456", false},
		{"123,456", "*", "123", "456", false},
		{"123", "", "", "", true},
		{"example.com , 123, 456 ", "example.com", "123", "456", false},
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
			require.Equal(t, 1, len(res))
			assert.Equal(t, tt.server, res[0].Server)
			assert.Equal(t, tt.src, res[0].SrcMatch.String())
			assert.Equal(t, tt.dst, res[0].Dst)
		})
	}

}
