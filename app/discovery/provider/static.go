package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/umputun/reproxy/app/discovery"
	"github.com/umputun/reproxy/app/plugins"
)

// Static provider, rules are server,from,to
type Static struct {
	Rules []string // each rule is 5 elements comma separated - server,source_url,destination,ping,plugins
}

// Events returns channel updating once
func (s *Static) Events(_ context.Context) <-chan discovery.ProviderID {
	res := make(chan discovery.ProviderID, 1)
	res <- discovery.PIStatic
	return res
}

// List all src dst pairs
func (s *Static) List() (res []discovery.URLMapper, err error) {

	// inp is 5 elements string server,source_url,destination,ping,plugins
	// the ping and/or plugins sections can be omitted
	parse := func(inp string) (discovery.URLMapper, error) {
		elems := strings.Split(inp, ",")
		if len(elems) < 3 {
			return discovery.URLMapper{}, fmt.Errorf("invalid rule %q", inp)
		}

		var allowedPlugins []string
		pingURL := ""
		var hasPluginsSection bool // because 'ping' section is optional, we should check duplicate 'plugins' section for 4 and 5 part

		if len(elems) == 4 {
			if !strings.HasPrefix(elems[3], "plugins:") {
				pingURL = strings.TrimSpace(elems[3])
			} else {
				hasPluginsSection = true
				allowedPlugins = strings.Split(strings.TrimPrefix(elems[3], "plugins:"), ";")
			}
		}
		if len(elems) == 5 {
			if hasPluginsSection || !strings.HasPrefix(elems[4], "plugins:") {
				return discovery.URLMapper{}, fmt.Errorf("invalid rule %q", inp)
			}
			allowedPlugins = strings.Split(strings.TrimPrefix(elems[4], "plugins:"), ";")
		}
		rx, err := regexp.Compile(strings.TrimSpace(elems[1]))
		if err != nil {
			return discovery.URLMapper{}, fmt.Errorf("can't parse regex %s: %w", elems[1], err)
		}

		for _, p := range allowedPlugins {
			if !plugins.HasPlugin(p) {
				return discovery.URLMapper{}, fmt.Errorf("plugin %q not found", p)
			}
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
			Plugins:    map[string]struct{}{},
		}
		for _, p := range allowedPlugins {
			res.Plugins[p] = struct{}{}
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
