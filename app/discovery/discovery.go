// Package discovery provides a common interface for all providers and Match to
// transform source to destination URL.
// Run func starts event loop checking all providers and retrieving lists of rules.
// All lists combined into a merged one.
package discovery

import (
	"context"
	"log"
	"regexp"
	"sync"
)

//go:generate moq -out provider_mock.go -fmt goimports . Provider

// Service implements discovery with multiple providers and url matcher
type Service struct {
	providers []Provider
	mappers   []UrlMapper
	lock      sync.RWMutex
}

// UrlMapper contains all info about source and destination routes
type UrlMapper struct {
	Server     string
	SrcMatch   *regexp.Regexp
	Dst        string
	ProviderID ProviderID
}

// Provider defines sources of mappers
type Provider interface {
	Events(ctx context.Context) (res <-chan struct{})
	List() (res []UrlMapper, err error)
	ID() ProviderID
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
	return &Service{providers: providers}
}

// Run runs blocking loop getting events from all providers
// and updating mappers on each event
func (s *Service) Run(ctx context.Context) error {

	var evChs []<-chan struct{}
	for _, p := range s.providers {
		evChs = append(evChs, p.Events(ctx))
	}
	ch := s.mergeEvents(ctx, evChs...)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ch:
			log.Printf("[DEBUG] new update event received")
			lst := s.mergeLists()
			for _, m := range lst {
				log.Printf("[INFO] match for %s: %s %s %s", m.ProviderID, m.Server, m.SrcMatch.String(), m.Dst)
			}
			s.lock.Lock()
			s.mappers = make([]UrlMapper, len(lst))
			copy(s.mappers, lst)
			s.lock.Unlock()
		}
	}
}

// Match url to all providers mappers
func (s *Service) Match(srv, src string) (string, bool) {

	s.lock.RLock()
	defer s.lock.RUnlock()
	for _, m := range s.mappers {
		if m.Server != "*" && m.Server != "" && m.Server != srv {
			continue
		}
		dest := m.SrcMatch.ReplaceAllString(src, m.Dst)
		if src != dest {
			return dest, true
		}
	}
	return src, false
}

func (s *Service) mergeLists() (res []UrlMapper) {
	for _, p := range s.providers {
		lst, err := p.List()
		if err != nil {
			continue
		}
		for i := range lst {
			lst[i].ProviderID = p.ID()
		}
		res = append(res, lst...)
	}
	return res
}

func (s *Service) mergeEvents(ctx context.Context, chs ...<-chan struct{}) <-chan struct{} {
	var wg sync.WaitGroup
	out := make(chan struct{})

	output := func(ctx context.Context, c <-chan struct{}) {
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
