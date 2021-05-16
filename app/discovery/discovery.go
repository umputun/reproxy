// Package discovery provides a common interface for all providers and Match to
// transform source to destination URL.
// Run func starts event loop checking all providers and retrieving lists of rules.
// All lists combined into a merged one.
package discovery

import (
	"context"
	"fmt"
	"net/http"
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
	MatchType  MatchType

	AssetsLocation string
	AssetsWebRoot  string

	dead bool
}

// Matches returns result of url mapping. May have multiple routes. Lack of any routes means no match was wound
type Matches struct {
	MatchType MatchType
	Routes    []MatchedRoute
}

// MatchedRoute contains a single match used to produce multi-matched Matches
type MatchedRoute struct {
	Destination string
	Alive       bool
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
	PIDocker        ProviderID = "docker"
	PIStatic        ProviderID = "static"
	PIFile          ProviderID = "file"
	PIConsulCatalog ProviderID = "consul-catalog"
)

// MatchType defines the type of mapper (rule)
type MatchType int

// enum of all match types
const (
	MTProxy MatchType = iota
	MTStatic
)

func (m MatchType) String() string {
	switch m {
	case MTProxy:
		return "proxy"
	case MTStatic:
		return "static"
	default:
		return "unknown"
	}
}

// NewService makes service with given providers
func NewService(providers []Provider, interval time.Duration) *Service {
	return &Service{providers: providers, interval: interval}
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
				if m.MatchType == MTProxy {
					log.Printf("[INFO] proxy  %s: %s %s -> %s", m.ProviderID, m.Server, m.SrcMatch.String(), m.Dst)
				}
				if m.MatchType == MTStatic {
					log.Printf("[INFO] assets %s: %s %s -> %s", m.ProviderID, m.Server, m.AssetsWebRoot, m.AssetsLocation)
				}
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

// Match url to all mappers. Returns Matches with potentially multiple destinations for MTProxy.
// For MTStatic always a single match because fail-over doesn't supported for assets
func (s *Service) Match(srv, src string) (res Matches) {

	s.lock.RLock()
	defer s.lock.RUnlock()

	lastSrcMatch := ""
	for _, srvName := range []string{srv, "*", ""} {
		for _, m := range s.mappers[srvName] {

			// if the first match found and the next src match is not identical we can stop as src match regexes presorted
			if len(res.Routes) > 0 && m.SrcMatch.String() != lastSrcMatch {
				return res
			}

			switch m.MatchType {
			case MTProxy:
				dest := m.SrcMatch.ReplaceAllString(src, m.Dst)
				if src != dest { // regex matched
					lastSrcMatch = m.SrcMatch.String()
					res.MatchType = MTProxy
					res.Routes = append(res.Routes, MatchedRoute{dest, m.IsAlive()})
				}
			case MTStatic:
				if src == m.AssetsWebRoot || strings.HasPrefix(src, m.AssetsWebRoot+"/") {
					res.MatchType = MTStatic
					res.Routes = append(res.Routes, MatchedRoute{m.AssetsWebRoot + ":" + m.AssetsLocation, true})
					return res
				}
			}
		}
	}

	return res
}

// ScheduleHealthCheck starts background loop with health-check
func (s *Service) ScheduleHealthCheck(ctx context.Context, interval time.Duration) {
	log.Printf("health-check scheduled every %s", interval)

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				pingErrs := s.CheckHealth()

				s.lock.Lock()
				for _, mappers := range s.mappers {
					for i := range mappers {
						if err, ok := pingErrs[mappers[i].PingURL]; ok {
							mappers[i].dead = false
							if err != nil {
								mappers[i].dead = true
							}
						}
					}
				}
				s.lock.Unlock()
			case <-ctx.Done():
				return
			}
		}
	}()
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
	sort.Slice(mappers, func(i, j int) bool {
		// sort by len first, to make longer matches first
		if len(mappers[i].SrcMatch.String()) != len(mappers[j].SrcMatch.String()) {
			return len(mappers[i].SrcMatch.String()) > len(mappers[j].SrcMatch.String())
		}
		// if len identical sort by SrcMatch string to keep same SrcMatch grouped together
		return mappers[i].SrcMatch.String() < mappers[j].SrcMatch.String()
	})
	return mappers
}

// CheckHealth starts health-check for service's mappers
func (s *Service) CheckHealth() (pingResult map[string]error) {
	s.lock.RLock()
	defer s.lock.RUnlock()

	const concurrent = 8
	sema := make(chan struct{}, concurrent) // limit health check to 8 concurrent calls

	// runs pings in parallel
	type pingError struct {
		pingURL string
		err     error
	}
	outCh := make(chan pingError, concurrent)

	services, pinged := 0, 0
	var wg sync.WaitGroup
	for _, mappers := range s.mappers {
		for _, m := range mappers {
			if m.MatchType != MTProxy {
				continue
			}
			services++
			if m.PingURL == "" {
				continue
			}
			pinged++
			wg.Add(1)

			go func(m URLMapper) {
				sema <- struct{}{}
				defer func() {
					<-sema
					wg.Done()
				}()

				errMsg, err := m.ping()
				if err != nil {
					log.Print(errMsg)
				}
				outCh <- pingError{m.PingURL, err}
			}(m)
		}
	}

	go func() {
		wg.Wait()
		close(outCh)
	}()

	pingResult = make(map[string]error)
	for res := range outCh {
		pingResult[res.pingURL] = res.err
	}

	return pingResult
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
	m.Dst = strings.Replace(m.Dst, "@", "$", -1) // allow group defined as @n instead of $n (yaml friendly)

	// static match with assets uses AssetsWebRoot and AssetsLocation
	if m.MatchType == MTStatic && m.AssetsWebRoot != "" && m.AssetsLocation != "" {
		m.AssetsWebRoot = strings.TrimSuffix(m.AssetsWebRoot, "/")
		m.AssetsLocation = strings.TrimSuffix(m.AssetsLocation, "/") + "/"
	}

	// static match without assets defined defaulted to src:dst/
	if m.MatchType == MTStatic && m.AssetsWebRoot == "" && m.AssetsLocation == "" {
		m.AssetsWebRoot = strings.TrimSuffix(src, "/")
		m.AssetsLocation = strings.TrimSuffix(m.Dst, "/") + "/"
	}

	// don't extend src and dst with dst or src regex groups
	if strings.Contains(m.Dst, "$") || strings.Contains(m.Dst, "@") || strings.Contains(src, "(") {
		return m
	}

	if !strings.HasSuffix(src, "/") && m.MatchType == MTProxy {
		return m
	}

	res := URLMapper{
		Server:         m.Server,
		Dst:            strings.TrimSuffix(m.Dst, "/") + "/$1",
		ProviderID:     m.ProviderID,
		PingURL:        m.PingURL,
		MatchType:      m.MatchType,
		AssetsWebRoot:  m.AssetsWebRoot,
		AssetsLocation: m.AssetsLocation,
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

// Contains checks if the input string (e) in the given slice
func Contains(e string, s []string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

// IsAlive indicates whether mapper destination is alive
func (m URLMapper) IsAlive() bool {
	return !m.dead
}

func (m URLMapper) ping() (string, error) {
	client := http.Client{Timeout: 500 * time.Millisecond}

	resp, err := client.Get(m.PingURL)
	if err != nil {
		errMsg := strings.Replace(err.Error(), "\"", "", -1)
		errMsg = fmt.Sprintf("[WARN] failed to ping for health %s, %s", m.PingURL, errMsg)
		return errMsg, fmt.Errorf("%s %s: %s, %v", m.Server, m.SrcMatch.String(), m.PingURL, errMsg)
	}
	if resp.StatusCode != http.StatusOK {
		errMsg := fmt.Sprintf("[WARN] failed ping status for health %s (%s)", m.PingURL, resp.Status)
		return errMsg, fmt.Errorf("%s %s: %s, %s", m.Server, m.SrcMatch.String(), m.PingURL, resp.Status)
	}

	return "", err
}
