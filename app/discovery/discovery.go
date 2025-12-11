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
	providers    []Provider
	mappers      map[string][]URLMapper
	mappersCache map[string][]URLMapper
	lock         sync.RWMutex
	interval     time.Duration
}

// URLMapper contains all info about source and destination routes
type URLMapper struct {
	Server       string
	SrcMatch     regexp.Regexp
	Dst          string
	ProviderID   ProviderID
	PingURL      string
	MatchType    MatchType
	RedirectType RedirectType
	KeepHost     *bool
	OnlyFromIPs  []string
	AuthUsers    []string // basic auth credentials as user:bcrypt_hash pairs

	AssetsLocation string // local FS root location
	AssetsWebRoot  string // web root location
	AssetsSPA      bool   // spa mode, redirect to webroot/index.html on not found

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
	Mapper      URLMapper
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

var reGroup = regexp.MustCompile(`(^.*)/\(.*\)`) // capture regex group lil (anything) from src like /blah/foo/(.*)

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

// RedirectType defines types of redirects
type RedirectType int

// enum of all redirect types
const (
	RTNone RedirectType = 0
	RTPerm RedirectType = 301
	RTTemp RedirectType = 302
)

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
			return fmt.Errorf("discovery service interrupted: %w", ctx.Err())
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
				onlyFrom := ""
				if len(m.OnlyFromIPs) > 0 {
					onlyFrom = fmt.Sprintf(" +[%v]", strings.Join(m.OnlyFromIPs, ",")) // show onlyFrom if set
				}
				if m.MatchType == MTProxy {
					log.Printf("[INFO] proxy  %s: %s %s -> %s%s", m.ProviderID, m.Server, m.SrcMatch.String(), m.Dst, onlyFrom)
				}
				if m.MatchType == MTStatic {
					log.Printf("[INFO] assets %s: %s %s -> %s%s", m.ProviderID, m.Server, m.AssetsWebRoot,
						m.AssetsLocation, onlyFrom)
				}
			}
			s.lock.Lock()
			s.mappers = make(map[string][]URLMapper)
			s.mappersCache = make(map[string][]URLMapper)
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

	replaceHost := func(dest, srv string) string {
		// $host or ${host} in dest replaced by srv
		dest = strings.ReplaceAll(dest, "$host", srv)
		if strings.Contains(dest, "${host}") {
			dest = strings.ReplaceAll(dest, "${host}", srv)
		}
		return dest
	}

	s.lock.RLock()
	defer s.lock.RUnlock()

	lastSrcMatch := ""
	for _, srvName := range []string{srv, "*", ""} {
		for _, m := range findMatchingMappers(s, srvName) {

			// if the first match found and the next src match is not identical we can stop as src match regexes presorted
			if len(res.Routes) > 0 && m.SrcMatch.String() != lastSrcMatch {
				return res
			}

			switch m.MatchType {
			case MTProxy:
				dest := replaceHost(m.Dst, srv) // replace $host and ${host} in dest first, before regex match
				dest = m.SrcMatch.ReplaceAllString(src, dest)
				if src != dest { // regex matched because dest changed after replacement
					lastSrcMatch = m.SrcMatch.String()
					res.MatchType = MTProxy
					res.Routes = append(res.Routes, MatchedRoute{Destination: dest, Alive: m.IsAlive(), Mapper: m})
				}
			case MTStatic:
				wr := m.AssetsWebRoot
				if wr != "/" {
					wr += "/"
				}
				if src == m.AssetsWebRoot || strings.HasPrefix(src, wr) {
					res.MatchType = MTStatic
					destSfx := ":norm"
					if m.AssetsSPA {
						destSfx = ":spa"
					}
					res.Routes = append(res.Routes, MatchedRoute{
						Destination: m.AssetsWebRoot + ":" + m.AssetsLocation + destSfx, Alive: true, Mapper: m})
					return res
				}
			}
		}
	}

	// if match returns both default and concrete server(s), drop default as we have a better match with concrete
	if len(res.Routes) > 1 {
		for i := range res.Routes {
			if res.Routes[i].Mapper.Server == "*" || res.Routes[i].Mapper.Server == "" {
				res.Routes = append(res.Routes[:i], res.Routes[i+1:]...)
				break
			}
		}
	}

	return res
}

func findMatchingMappers(s *Service, srvName string) []URLMapper {
	// strict match - for backward compatibility
	if mappers, isStrictMatch := s.mappers[srvName]; isStrictMatch {
		return mappers
	}

	if cachedMapper, isCached := s.mappersCache[srvName]; isCached {
		return cachedMapper
	}

	for mapperServer, mapper := range s.mappers {
		// * and "" should not be treated as regex and require exact match (above)
		if mapperServer == "*" || mapperServer == "" {
			continue
		}

		// handle *.example.com simple patterns
		if strings.HasPrefix(mapperServer, "*.") {
			domainPattern := mapperServer[1:] // strip the '*'
			if strings.HasSuffix(srvName, domainPattern) {
				s.mappersCache[srvName] = mapper
				return mapper
			}
			continue
		}

		re, err := regexp.Compile(mapperServer)
		if err != nil {
			log.Printf("[WARN] invalid regexp %s: %s", mapperServer, err)
			continue
		}

		if re.MatchString(srvName) {
			s.mappersCache[srvName] = mapper
			return mapper
		}
	}

	return nil
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
		return mappers[i].ProviderID < mappers[j].ProviderID
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
					log.Printf("[DEBUG] %s", errMsg)
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
			log.Printf("[DEBUG] can't get list for %T, %v", p, err)
			continue
		}
		for i := range lst {
			lst[i] = s.redirects(lst[i])
			lst[i] = s.extendMapper(lst[i])
		}
		res = append(res, lst...)
	}

	// sort rules to make assets last and prioritize longer rules first
	sort.Slice(res, func(i, j int) bool {

		src1 := reGroup.ReplaceAllString(res[i].SrcMatch.String(), "$1")
		src2 := reGroup.ReplaceAllString(res[j].SrcMatch.String(), "$1")

		// sort by len first, to make longer matches first
		if len(src1) != len(src2) {
			return len(src1) > len(src2)
		}
		// if len identical sort by SrcMatch string to keep same SrcMatch grouped together
		return res[i].SrcMatch.String() < res[j].SrcMatch.String()
	})

	// sort to put assets down in the list
	sort.Slice(res, func(i, j int) bool {
		return res[i].MatchType < res[j].MatchType
	})

	return res
}

// extendMapper from /something/blah->http://example.com/api to ^/something/blah/(.*)->http://example.com/api/$1
// also substitutes @ in dest by $. The reason for this substitution - some providers, for example docker
// treat $ in a special way for variable substitution and user has to escape $, like this reproxy.dest: '/$$1'
// It can be simplified with @, i.e. reproxy.dest: '/@1'
func (s *Service) extendMapper(m URLMapper) URLMapper {

	src := m.SrcMatch.String()
	m.Dst = strings.ReplaceAll(m.Dst, "@", "$") // allow group defined as @n instead of $n (yaml friendly)

	// static match with assets uses AssetsWebRoot and AssetsLocation
	if m.MatchType == MTStatic && m.AssetsWebRoot != "" && m.AssetsLocation != "" {
		if m.AssetsWebRoot != "/" {
			m.AssetsWebRoot = strings.TrimSuffix(m.AssetsWebRoot, "/")
		}
		m.AssetsLocation = strings.TrimSuffix(m.AssetsLocation, "/") + "/"
	}

	// static match without assets defined defaulted to src:dst/
	if m.MatchType == MTStatic && m.AssetsWebRoot == "" && m.AssetsLocation == "" {
		m.AssetsWebRoot = src
		if m.AssetsWebRoot != "/" {
			m.AssetsWebRoot = strings.TrimSuffix(m.AssetsWebRoot, "/")
		}
		m.AssetsLocation = strings.TrimSuffix(m.Dst, "/") + "/"
	}

	// don't extend src and dst with dst or src regex groups
	if strings.Contains(m.Dst, "$") || strings.Contains(m.Dst, "@") || strings.Contains(src, "(") {
		return m
	}

	// destination with with / suffix don't need more dst extension
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
		AssetsSPA:      m.AssetsSPA,
		RedirectType:   m.RedirectType,
		KeepHost:       m.KeepHost,
		OnlyFromIPs:    m.OnlyFromIPs,
		AuthUsers:      m.AuthUsers,
	}
	rx, err := regexp.Compile("^" + strings.TrimSuffix(src, "/") + "/(.*)")
	if err != nil {
		log.Printf("[WARN] can't extend %s, %v", m.SrcMatch.String(), err)
		return m
	}
	res.SrcMatch = *rx
	return res
}

// redirects process @code prefix and sets redirect type, i.e. "@302 /something"
func (s *Service) redirects(m URLMapper) URLMapper {
	switch {
	case strings.HasPrefix(m.Dst, "@301 ") && len(m.Dst) > 4:
		m.Dst = m.Dst[5:]
		m.RedirectType = RTPerm
	case strings.HasPrefix(m.Dst, "@perm ") && len(m.Dst) > 5:
		m.Dst = m.Dst[6:]
		m.RedirectType = RTPerm
	case (strings.HasPrefix(m.Dst, "@302 ") || strings.HasPrefix(m.Dst, "@tmp ")) && len(m.Dst) > 4:
		m.Dst = m.Dst[5:]
		m.RedirectType = RTTemp
	case strings.HasPrefix(m.Dst, "@temp ") && len(m.Dst) > 5:
		m.Dst = m.Dst[6:]
		m.RedirectType = RTTemp
	default:
		m.RedirectType = RTNone
	}
	return m
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
				select {
				case out <- v:
				case <-ctx.Done():
					return
				}
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

// IsAlive indicates whether mapper destination is alive
func (m URLMapper) IsAlive() bool {
	return !m.dead
}

func (m URLMapper) ping() (string, error) {
	client := http.Client{Timeout: 500 * time.Millisecond}

	resp, err := client.Get(m.PingURL)
	if err != nil {
		errMsg := strings.ReplaceAll(err.Error(), "\"", "")
		errMsg = fmt.Sprintf("failed to ping for health %s, %s", m.PingURL, errMsg)
		return errMsg, fmt.Errorf("%s %s: %s, %v", m.Server, m.SrcMatch.String(), m.PingURL, errMsg)
	}
	if resp.StatusCode != http.StatusOK {
		errMsg := fmt.Sprintf("failed ping status for health %s (%s)", m.PingURL, resp.Status)
		return errMsg, fmt.Errorf("%s %s: %s, %s", m.Server, m.SrcMatch.String(), m.PingURL, resp.Status)
	}

	return "", nil
}

// ParseOnlyFrom parses comma separated list of IPs
func ParseOnlyFrom(s string) []string {
	return parseCommaSeparated(s)
}

// ParseAuth parses comma separated list of user:bcrypt_hash pairs for basic auth
func ParseAuth(s string) []string {
	return parseCommaSeparated(s)
}

// parseCommaSeparated splits a comma-separated string and returns trimmed non-empty values
func parseCommaSeparated(s string) (res []string) {
	if s == "" {
		return []string{}
	}
	for v := range strings.SplitSeq(s, ",") {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			res = append(res, trimmed)
		}
	}
	return res
}
