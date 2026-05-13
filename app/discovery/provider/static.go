package provider

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/umputun/reproxy/app/discovery"
)

// Static provider, rules are server,source_url,destination[,ping[,forward-health-checks[,timeout[,throttle]]]]
type Static struct {
	Rules []string // each rule is up to 7 elements comma separated - server,source_url,destination,ping,forward-health-checks,timeout,throttle
}

// Events returns channel updating once
func (s *Static) Events(_ context.Context) <-chan discovery.ProviderID {
	res := make(chan discovery.ProviderID, 1)
	res <- discovery.PIStatic
	return res
}

// List all src dst pairs
func (s *Static) List() (res []discovery.URLMapper, err error) {

	// inp is up to 7 elements string server,source_url,destination[,ping[,forward-health-checks[,timeout[,throttle]]]]
	// ping, forward-health-checks, timeout and throttle can be omitted
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
			v := strings.TrimSpace(elems[4])
			forwardHealthChecks = v == "true" || v == "yes" || v == "y" || v == "1"
		}
		var timeoutStr, throttleStr string
		if len(elems) >= 6 {
			timeoutStr = strings.TrimSpace(elems[5])
		}
		if len(elems) >= 7 {
			throttleStr = strings.TrimSpace(elems[6])
		}
		timeout, err := s.parseTimeout(timeoutStr)
		if err != nil {
			return discovery.URLMapper{}, err
		}
		throttle, err := s.parseThrottle(throttleStr)
		if err != nil {
			return discovery.URLMapper{}, err
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
			Timeout:             timeout,
			Throttle:            throttle,
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

func (s *Static) parseTimeout(v string) (time.Duration, error) {
	if v == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("can't parse timeout %s: %w", v, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("timeout must be non-negative, got %s", v)
	}
	return d, nil
}

func (s *Static) parseThrottle(v string) (int, error) {
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("can't parse throttle %s: %w", v, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("throttle must be non-negative, got %d", n)
	}
	return n, nil
}
