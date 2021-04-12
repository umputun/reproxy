// Package discovery provides a common interface for all providers and Match to
// transform source to destination URL.
// Run func starts event loop checking all providers and retrieving lists of rules.
// All lists combined into a merged one.
package discovery

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	log "github.com/go-pkgz/lgr"
)

//go:generate moq -out provider_mock.go -fmt goimports . Provider

// Service implements discovery with multiple providers and url matcher
type Service struct {
	providers []Provider
	mappers   map[string][]URLMapper
	lock      sync.RWMutex
	interval  time.Duration
}

// URLMapper contains all info about source and destination routes
type URLMapper struct {
	Server     string
	SrcMatch   regexp.Regexp
	Dst        string
	ProviderID ProviderID
	PingURL    string
}

// Provider defines sources of mappers
type Provider interface {
	Events(ctx context.Context) (res <-chan ProviderID)
	List() (res []URLMapper, err error)
}

// ProviderID holds provider identifier to emulate enum of them
type ProviderID string

// enum of all provider ids
const (
	PIDocker ProviderID = "docker"
	PIStatic ProviderID = "static"
	PIFile   ProviderID = "file"
)

// NewService makes service with given providers
func NewService(providers []Provider) *Service {
	return &Service{providers: providers, interval: time.Second}
}

// Run runs blocking loop getting events from all providers
// and updating all mappers on each event
func (s *Service) Run(ctx context.Context) error {

	evChs := make([]<-chan ProviderID, 0, len(s.providers))
	for _, p := range s.providers {
		evChs = append(evChs, p.Events(ctx))
	}
	ch := s.mergeEvents(ctx, evChs...)
	var evRecv bool
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev := <-ch:
			log.Printf("[DEBUG] new update event received, %s", ev)
			evRecv = true
		case <-time.After(s.interval):
			if !evRecv {
				continue
			}
			evRecv = false
			lst := s.mergeLists()
			for _, m := range lst {
				log.Printf("[INFO] match for %s: %s %s -> %s", m.ProviderID, m.Server, m.SrcMatch.String(), m.Dst)
			}
			s.lock.Lock()
			s.mappers = make(map[string][]URLMapper)
			for _, m := range lst {
				s.mappers[m.Server] = append(s.mappers[m.Server], m)
			}
			s.lock.Unlock()
		}
	}
}

// Match url to all mappers
func (s *Service) Match(srv, src string) (string, bool) {

	s.lock.RLock()
	defer s.lock.RUnlock()
	for _, srvName := range []string{srv, "*", ""} {
		for _, m := range s.mappers[srvName] {
			dest := m.SrcMatch.ReplaceAllString(src, m.Dst)
			if src != dest {
				return dest, true
			}
		}
	}
	return src, false
}

// Servers return list of all servers, skips "*" (catch-all/default)
func (s *Service) Servers() (servers []string) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	for key, ms := range s.mappers {
		if key == "*" || key == "" {
			continue
		}
		for _, m := range ms {
			servers = append(servers, m.Server)
		}
	}
	sort.Strings(servers)
	return servers
}

// Mappers return list of all mappers
func (s *Service) Mappers() (mappers []URLMapper) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	for _, m := range s.mappers {
		mappers = append(mappers, m...)
	}
	return mappers
}

func (s *Service) mergeLists() (res []URLMapper) {
	for _, p := range s.providers {
		lst, err := p.List()
		if err != nil {
			log.Printf("[DEBUG] can't get list for %s, %v", p, err)
			continue
		}
		for i := range lst {
			lst[i] = s.extendMapper(lst[i])
		}
		res = append(res, lst...)
	}
	return res
}

// extendMapper from /something/blah->http://example.com/api to ^/something/blah/(.*)->http://example.com/api/$1
// also substitutes @ in dest by $. The reason for this substitution - some providers, for example docker
// treat $ in a special way for variable substitution and user has to escape $, like this reproxy.dest: '/$$1'
// It can be simplified with @, i.e. reproxy.dest: '/@1'
func (s *Service) extendMapper(m URLMapper) URLMapper {

	src := m.SrcMatch.String()

	// TODO: Probably should be ok in practice but we better figure a nicer way to do it
	if strings.Contains(m.Dst, "$1") || strings.Contains(m.Dst, "@1") ||
		strings.Contains(src, "(") || !strings.HasSuffix(src, "/") {

		m.Dst = strings.Replace(m.Dst, "@", "$", -1) // allow group defined as @n instead of $n
		return m
	}
	res := URLMapper{
		Server:     m.Server,
		Dst:        strings.TrimSuffix(m.Dst, "/") + "/$1",
		ProviderID: m.ProviderID,
		PingURL:    m.PingURL,
	}

	rx, err := regexp.Compile("^" + strings.TrimSuffix(src, "/") + "/(.*)")
	if err != nil {
		log.Printf("[WARN] can't extend %s, %v", m.SrcMatch.String(), err)
		return m
	}
	res.SrcMatch = *rx
	return res
}

func (s *Service) mergeEvents(ctx context.Context, chs ...<-chan ProviderID) <-chan ProviderID {
	var wg sync.WaitGroup
	out := make(chan ProviderID)

	output := func(ctx context.Context, c <-chan ProviderID) {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case v, ok := <-c:
				if !ok {
					return
				}
				out <- v
			}
		}
	}

	wg.Add(len(chs))
	for _, c := range chs {
		go output(ctx, c)
	}

	go func() {
		wg.Wait()
		close(out)
	}()
	return out
}
