package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/umputun/reproxy/app/discovery"
)

// Static provider, rules are server,source_url,destination[,ping[,forward-health-checks]]
type Static struct {
	Rules []string // each rule is up to 5 elements comma separated - server,source_url,destination,ping,forward-health-checks
}

// Events returns channel updating once
func (s *Static) Events(_ context.Context) <-chan discovery.ProviderID {
	res := make(chan discovery.ProviderID, 1)
	res <- discovery.PIStatic
	return res
}

// List all src dst pairs
func (s *Static) List() (res []discovery.URLMapper, err error) {

	// inp is up to 5 elements string server,source_url,destination[,ping[,forward-health-checks]]
	// ping and forward-health-checks can be omitted
	parse := func(inp string) (discovery.URLMapper, error) {
		elems := strings.Split(inp, ",")
		if len(elems) < 3 {
			return discovery.URLMapper{}, fmt.Errorf("invalid rule %q", inp)
		}
		pingURL := ""
		if len(elems) >= 4 {
			pingURL = strings.TrimSpace(elems[3])
		}
		forwardHealthChecks := false
		if len(elems) >= 5 {
			forwardHealthChecks = strings.TrimSpace(elems[4]) != ""
		}
		rx, err := regexp.Compile(strings.TrimSpace(elems[1]))
		if err != nil {
			return discovery.URLMapper{}, fmt.Errorf("can't parse regex %s: %w", elems[1], err)
		}

		dst := strings.TrimSpace(elems[2])
		assets, spa := false, false
		if after, found := strings.CutPrefix(dst, "assets:"); found {
			dst = after
			assets = true
		}
		if after, found := strings.CutPrefix(dst, "spa:"); found {
			dst = after
			assets = true
			spa = true
		}

		res := discovery.URLMapper{
			Server:              strings.TrimSpace(elems[0]),
			SrcMatch:            *rx,
			Dst:                 dst,
			PingURL:             pingURL,
			ForwardHealthChecks: forwardHealthChecks,
			ProviderID:          discovery.PIStatic,
			MatchType:           discovery.MTProxy,
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
