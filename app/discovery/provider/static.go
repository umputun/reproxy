package provider

import (
	"context"
	"regexp"
	"strings"

	"github.com/pkg/errors"

	"github.com/umputun/docker-proxy/app/discovery"
)

// Static provider, rules are server,from,to
type Static struct {
	Rules []string // each rule is 2 or 3 elements comma separated. [server,]source url,destination
}

// Events returns channel updating once
func (s *Static) Events(_ context.Context) <-chan struct{} {
	res := make(chan struct{}, 1)
	res <- struct{}{}
	return res
}

// List all src dst pairs
func (s *Static) List() (res []discovery.UrlMapper, err error) {

	parse := func(inp string) (discovery.UrlMapper, error) {
		elems := strings.Split(inp, ",")
		switch len(elems) {
		case 2:
			rx, err := regexp.Compile(strings.TrimSpace(elems[0]))
			if err != nil {
				return discovery.UrlMapper{}, errors.Wrapf(err, "can't parse regex %s", elems[0])
			}
			return discovery.UrlMapper{Server: "*", SrcMatch: rx, Dst: strings.TrimSpace(elems[1])}, nil
		case 3:
			rx, err := regexp.Compile(strings.TrimSpace(elems[1]))
			if err != nil {
				return discovery.UrlMapper{}, errors.Wrapf(err, "can't parse regex %s", elems[1])
			}
			return discovery.UrlMapper{Server: strings.TrimSpace(elems[0]), SrcMatch: rx, Dst: strings.TrimSpace(elems[2])}, nil
		}
		return discovery.UrlMapper{}, errors.Errorf("can't parse entry %s", inp)
	}

	for _, r := range s.Rules {
		um, err := parse(r)
		if err != nil {
			return nil, err
		}
		res = append(res, um)
	}
	return res, nil
}

func (s *Static) ID() discovery.ProviderID { return discovery.PIStatic }
