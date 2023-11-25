package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/umputun/reproxy/app/discovery"
)

// Static provider, rules are server,source_url,destination[,ping]
type Static struct {
	Rules []string // each rule is 4 elements comma separated - server,source_url,destination,ping
}

// Events returns channel updating once
func (s *Static) Events(_ context.Context) <-chan discovery.ProviderID {
	res := make(chan discovery.ProviderID, 1)
	res <- discovery.PIStatic
	return res
}

// List all src dst pairs
func (s *Static) List() (res []discovery.URLMapper, err error) {

	// inp is 4 elements string server,source_url,destination,ping
	// the last one can be omitted if no ping required
	parse := func(inp string) (discovery.URLMapper, error) {
		elems := strings.Split(inp, ",")
		if len(elems) < 3 {
			return discovery.URLMapper{}, fmt.Errorf("invalid rule %q", inp)
		}
		pingURL := ""
		if len(elems) == 4 {
			pingURL = strings.TrimSpace(elems[3])
		}
		rx, err := regexp.Compile(strings.TrimSpace(elems[1]))
		if err != nil {
			return discovery.URLMapper{}, fmt.Errorf("can't parse regex %s: %w", elems[1], err)
		}

		dst := strings.TrimSpace(elems[2])
		assets, spa := false, false
		if strings.HasPrefix(dst, "assets:") {
			dst = strings.TrimPrefix(dst, "assets:")
			assets = true
		}
		if strings.HasPrefix(dst, "spa:") {
			dst = strings.TrimPrefix(dst, "spa:")
			assets = true
			spa = true
		}

		res := discovery.URLMapper{
			Server:     strings.TrimSpace(elems[0]),
			SrcMatch:   *rx,
			Dst:        dst,
			PingURL:    pingURL,
			ProviderID: discovery.PIStatic,
			MatchType:  discovery.MTProxy,
		}
		if assets {
			res.MatchType = discovery.MTStatic
			res.AssetsSPA = spa
		}

		return res, nil
	}

	for _, r := range s.Rules {
		if strings.TrimSpace(r) == "" {
			continue
		}
		um, err := parse(r)
		if err != nil {
			return nil, err
		}
		res = append(res, um)
	}
	return res, nil
}
