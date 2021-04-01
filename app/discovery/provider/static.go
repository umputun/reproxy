package provider

import (
	"context"
	"regexp"
	"strings"

	"github.com/pkg/errors"

	"github.com/umputun/docker-proxy/app/discovery"
)

// Static provider, rules are from::to
type Static struct {
	Rules []string
}

// Events returns channel updating on file change only
func (s *Static) Events(ctx context.Context) <-chan struct{} {
	res := make(chan struct{}, 1)
	res <- struct{}{}
	return res
}

// List all src dst pairs
func (s *Static) List() (res []discovery.UrlMapper, err error) {
	for _, r := range s.Rules {
		elems := strings.Split(r, "::")
		if len(elems) != 2 {
			continue
		}
		rx, err := regexp.Compile(elems[0])
		if err != nil {
			return nil, errors.Wrapf(err, "can't parse regex %s", elems[0])
		}
		res = append(res, discovery.UrlMapper{SrcMatch: rx, Dst: elems[1]})
	}
	return res, nil
}

func (s *Static) ID() string { return "static" }
