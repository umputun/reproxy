package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	log "github.com/go-pkgz/lgr"
	R "github.com/go-pkgz/rest"
	"github.com/go-pkgz/rest/logger"

	"github.com/umputun/reproxy/app/discovery"
	"github.com/umputun/reproxy/app/plugin"
)

// Http is a proxy server for both http and https
type Http struct { // nolint golint
	Matcher
	Address          string
	AssetsLocation   string
	AssetsWebRoot    string
	Assets404        string
	AssetsSPA        bool
	MaxBodySize      int64
	GzEnabled        bool
	ProxyHeaders     []string
	DropHeader       []string
	SSLConfig        SSLConfig
	Insecure         bool
	Version          string
	AccessLog        io.Writer
	StdOutEnabled    bool
	Signature        bool
	Timeouts         Timeouts
	CacheControl     MiddlewareProvider
	Metrics          MiddlewareProvider
	PluginConductor  MiddlewareProvider
	Reporter         Reporter
	LBSelector       LBSelector
	OnlyFrom         *OnlyFrom
	BasicAuthEnabled bool
	BasicAuthAllowed []string

	ThrottleSystem int
	ThrottleUser   int

	KeepHost bool

	UpstreamMaxIdleConns    int
	UpstreamMaxConnsPerHost int

	dnsResolvers []string // used to mock DNS resolvers for testing
}

// Matcher source info (server and route) to the destination url
// If no match found return ok=false
type Matcher interface {
	Match(srv, src string) (res discovery.Matches)
	Servers() (servers []string)
	Mappers() (mappers []discovery.URLMapper)
	CheckHealth() (pingResult map[string]error)
}

// MiddlewareProvider interface defines http middleware handler
type MiddlewareProvider interface {
	Middleware(next http.Handler) http.Handler
}

// Reporter defines error reporting service
type Reporter interface {
	Report(w http.ResponseWriter, code int)
}

// LBSelector defines load balancer strategy
type LBSelector interface {
	Select(size int) int // return index of picked server
}

// Timeouts consolidate timeouts for both server and transport
type Timeouts struct {
	// server timeouts
	ReadHeader time.Duration
	Write      time.Duration
	Idle       time.Duration
	// transport timeouts
	Dial           time.Duration
	KeepAlive      time.Duration
	IdleConn       time.Duration
	TLSHandshake   time.Duration
	ExpectContinue time.Duration
	ResponseHeader time.Duration
}

// Run the lister and request's router, activate rest server
func (h *Http) Run(ctx context.Context) error {

	if h.AssetsLocation != "" {
		log.Printf("[DEBUG] assets file server enabled for %s, webroot %s", h.AssetsLocation, h.AssetsWebRoot)
		if h.Assets404 != "" {
			log.Printf("[DEBUG] assets 404 file enabled for %s", h.Assets404)
		}
	}

	if h.LBSelector == nil {
		h.LBSelector = &RandomSelector{}
	}

	var httpServer, httpsServer *http.Server

	go func() {
		<-ctx.Done()
		if httpServer != nil {
			if err := httpServer.Close(); err != nil {
				log.Printf("[ERROR] failed to close proxy http server, %v", err)
			}
		}
		if httpsServer != nil {
			if err := httpsServer.Close(); err != nil {
				log.Printf("[ERROR] failed to close proxy https server, %v", err)
			}
		}
	}()

	handler := R.Wrap(h.proxyHandler(),
		R.Recoverer(log.Default()),                   // recover on errors
		signatureHandler(h.Signature, h.Version),     // send app signature
		h.pingHandler,                                // respond to /ping
		h.healthMiddleware,                           // respond to /health
		h.matchHandler,                               // set matched routes to context
		h.OnlyFrom.Handler,                           // limit source (remote) IPs if defined
		perRouteAuthHandler,                          // per-route basic auth (if route has auth configured)
		h.basicAuthHandler(),                         // global basic auth (skipped if per-route auth is set)
		limiterSystemHandler(h.ThrottleSystem),       // limit total requests/sec
		limiterUserHandler(h.ThrottleUser),           // req/seq per user/route match
		h.mgmtHandler(),                              // handles /metrics and /routes for prometheus
		h.pluginHandler(),                            // prc to external plugins
		headersHandler(h.ProxyHeaders, h.DropHeader), // add response headers and delete some request headers
		accessLogHandler(h.AccessLog),                // apache-format log file
		stdoutLogHandler(h.StdOutEnabled, logger.New(logger.Log(log.Default()), logger.Prefix("[INFO]")).Handler),
		maxReqSizeHandler(h.MaxBodySize), // limit request max size
		gzipHandler(h.GzEnabled),         // gzip response
	)

	// no FQDNs defined, use the list of discovered servers
	if len(h.SSLConfig.FQDNs) == 0 && h.SSLConfig.SSLMode == SSLAuto {
		h.SSLConfig.FQDNs = h.discoveredServers(ctx, 50*time.Millisecond)
	}

	switch h.SSLConfig.SSLMode {
	case SSLNone:
		log.Printf("[INFO] activate http proxy server on %s", h.Address)
		httpServer = h.makeHTTPServer(h.Address, handler)
		httpServer.ErrorLog = log.ToStdLogger(log.Default(), "WARN")
		if err := httpServer.ListenAndServe(); err != nil {
			return fmt.Errorf("http proxy server failed: %w", err)
		}
		return nil
	case SSLStatic:
		log.Printf("[INFO] activate https server in 'static' mode on %s", h.Address)

		httpsServer = h.makeHTTPSServer(h.Address, handler)
		httpsServer.ErrorLog = log.ToStdLogger(log.Default(), "WARN")

		httpServer = h.makeHTTPServer(h.toHTTP(h.Address, h.SSLConfig.RedirHTTPPort), h.httpToHTTPSRouter())
		httpServer.ErrorLog = log.ToStdLogger(log.Default(), "WARN")

		go func() {
			log.Printf("[INFO] activate http redirect server on %s", h.toHTTP(h.Address, h.SSLConfig.RedirHTTPPort))
			err := httpServer.ListenAndServe()
			log.Printf("[WARN] http redirect server terminated, %s", err)
		}()
		if err := httpsServer.ListenAndServeTLS(h.SSLConfig.Cert, h.SSLConfig.Key); err != nil {
			return fmt.Errorf("https static server failed: %w", err)
		}
		return nil
	case SSLAuto:
		log.Printf("[INFO] activate https server in 'auto' mode on %s", h.Address)
		log.Printf("[DEBUG] FQDNs %v", h.SSLConfig.FQDNs)

		m := h.makeAutocertManager()
		httpsServer = h.makeHTTPSAutocertServer(h.Address, handler, m)
		httpsServer.ErrorLog = log.ToStdLogger(log.Default(), "WARN")

		httpServer = h.makeHTTPServer(h.toHTTP(h.Address, h.SSLConfig.RedirHTTPPort), h.httpChallengeRouter(m))
		httpServer.ErrorLog = log.ToStdLogger(log.Default(), "WARN")

		go func() {
			log.Printf("[INFO] activate http challenge server on port %s", h.toHTTP(h.Address, h.SSLConfig.RedirHTTPPort))
			err := httpServer.ListenAndServe()
			log.Printf("[WARN] http challenge server terminated, %s", err)
		}()

		if err := httpsServer.ListenAndServeTLS("", ""); err != nil {
			return fmt.Errorf("https auto server failed: %w", err)
		}
		return nil
	}
	return fmt.Errorf("unknown SSL type %v", h.SSLConfig.SSLMode)
}

type contextKey string

const (
	ctxURL       = contextKey("url")
	ctxMatchType = contextKey("type")
	ctxMatch     = contextKey("match")
	ctxKeepHost  = contextKey("keepHost")
)

func (h *Http) proxyHandler() http.HandlerFunc {

	reverseProxy := &httputil.ReverseProxy{
		Director: func(r *http.Request) {
			ctx := r.Context()
			uu := ctx.Value(ctxURL).(*url.URL)
			keepHost := ctx.Value(ctxKeepHost).(bool)
			r.Header.Add("X-Forwarded-Host", r.Host)
			scheme := "http"
			if h.SSLConfig.SSLMode == SSLAuto || h.SSLConfig.SSLMode == SSLStatic {
				h.setHeaderIfNotExists(r, "X-Forwarded-Proto", "https")
				h.setHeaderIfNotExists(r, "X-Forwarded-Port", "443")
				scheme = "https"
			}
			r.Header.Set("X-Forwarded-URL", fmt.Sprintf("%s://%s%s", scheme, r.Host, r.URL.String()))
			r.URL.Path = uu.Path
			r.URL.Host = uu.Host
			r.URL.Scheme = uu.Scheme
			if !keepHost {
				r.Host = uu.Host
			} else {
				log.Printf("[DEBUG] keep host %s", r.Host)
			}
			h.setXRealIP(r)
		},
		Transport: &http.Transport{
			ResponseHeaderTimeout: h.Timeouts.ResponseHeader,
			DialContext: (&net.Dialer{
				Timeout:   h.Timeouts.Dial,
				KeepAlive: h.Timeouts.KeepAlive,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          h.UpstreamMaxIdleConns,
			MaxConnsPerHost:       h.UpstreamMaxConnsPerHost,
			IdleConnTimeout:       h.Timeouts.IdleConn,
			TLSHandshakeTimeout:   h.Timeouts.TLSHandshake,
			ExpectContinueTimeout: h.Timeouts.ExpectContinue,
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: h.Insecure}, //nolint:gosec // g402: User defined option to disable verification for self-signed certificates
		},
		ErrorLog: log.ToStdLogger(log.Default(), "WARN"),
	}
	assetsHandler := h.assetsHandler()

	return func(w http.ResponseWriter, r *http.Request) {

		if r.Context().Value(ctxMatch) == nil { // no route match detected by matchHandler
			if h.isAssetRequest(r) {
				assetsHandler.ServeHTTP(w, r)
				return
			}
			log.Printf("[WARN] no match for %s %s", r.URL.Hostname(), r.URL.Path)
			h.Reporter.Report(w, http.StatusBadGateway)
			return
		}

		match := r.Context().Value(ctxMatch).(discovery.MatchedRoute)
		matchType := r.Context().Value(ctxMatchType).(discovery.MatchType)

		switch matchType {
		case discovery.MTProxy:
			switch match.Mapper.RedirectType {
			case discovery.RTNone:
				uu := r.Context().Value(ctxURL).(*url.URL)
				log.Printf("[DEBUG] proxy to %s", uu)
				reverseProxy.ServeHTTP(w, r)
			case discovery.RTPerm:
				log.Printf("[DEBUG] redirect (301) to %s", match.Destination)
				http.Redirect(w, r, match.Destination, http.StatusMovedPermanently)
			case discovery.RTTemp:
				log.Printf("[DEBUG] redirect (302) to %s", match.Destination)
				http.Redirect(w, r, match.Destination, http.StatusFound)
			}

		case discovery.MTStatic:
			// static match result has webroot:location:[spa:normal], i.e. /www:/var/somedir/:normal
			ae := strings.Split(match.Destination, ":")
			if len(ae) != 3 { // shouldn't happen
				log.Printf("[WARN] unexpected static assets destination: %s", match.Destination)
				h.Reporter.Report(w, http.StatusInternalServerError)
				return
			}
			fs, err := h.fileServer(ae[0], ae[1], ae[2] == "spa", nil)
			if err != nil {
				log.Printf("[WARN] file server error, %v", err)
				h.Reporter.Report(w, http.StatusInternalServerError)
				return
			}
			h.CacheControl.Middleware(fs).ServeHTTP(w, r)
		}
	}
}

// matchHandler is a part of middleware chain. Matches incoming request to one or more matched rules
// and if match found sets it to the request context. Context used by proxy handler as well as by plugin conductor
func (h *Http) matchHandler(next http.Handler) http.Handler {

	getMatch := func(mm discovery.Matches, picker LBSelector) (m discovery.MatchedRoute, ok bool) {
		if len(mm.Routes) == 0 {
			return m, false
		}

		var matches []discovery.MatchedRoute
		for _, m := range mm.Routes {
			if m.Alive {
				matches = append(matches, m)
			}
		}
		switch len(matches) {
		case 0:
			return m, false
		case 1:
			return matches[0], true
		default:
			return matches[picker.Select(len(matches))], true
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server := r.URL.Hostname()
		if server == "" {
			server = strings.Split(r.Host, ":")[0] // drop port
		}
		matches := h.Match(server, r.URL.EscapedPath()) // get all matches for the server:path pair
		match, ok := getMatch(matches, h.LBSelector)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}

		ctx := context.WithValue(r.Context(), ctxMatch, match)        // set match info
		ctx = context.WithValue(ctx, ctxMatchType, matches.MatchType) // set match type
		ctx = context.WithValue(ctx, plugin.CtxMatch, match)          // set match info for plugin conductor

		if matches.MatchType == discovery.MTProxy {
			uu, err := url.Parse(match.Destination)
			if err != nil {
				log.Printf("[WARN] can't parse destination %s, %v", match.Destination, err)
				h.Reporter.Report(w, http.StatusBadGateway)
				return
			}
			ctx = context.WithValue(ctx, ctxURL, uu) // set destination url in request's context
			keepHost := h.KeepHost
			if match.Mapper.KeepHost != nil {
				keepHost = *match.Mapper.KeepHost
			}
			ctx = context.WithValue(ctx, ctxKeepHost, keepHost) // set keep host in request's context
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (h *Http) assetsHandler() http.HandlerFunc {
	if h.AssetsLocation == "" || h.AssetsWebRoot == "" {
		return func(_ http.ResponseWriter, _ *http.Request) {}
	}

	var notFound []byte
	var err error
	if h.Assets404 != "" {
		if notFound, err = os.ReadFile(filepath.Join(h.AssetsLocation, h.Assets404)); err != nil {
			log.Printf("[WARN] can't read  404 file %s, %v", h.Assets404, err)
			notFound = nil
		}
	}

	log.Printf("[DEBUG] shared assets server enabled for %s %s, spa=%v, not-found=%q",
		h.AssetsLocation, h.AssetsWebRoot, h.AssetsSPA, h.Assets404)

	fs, err := h.fileServer(h.AssetsWebRoot, h.AssetsLocation, h.AssetsSPA, notFound)
	if err != nil {
		log.Printf("[WARN] can't initialize assets server, %v", err)
		return func(_ http.ResponseWriter, _ *http.Request) {}
	}
	return h.CacheControl.Middleware(fs).ServeHTTP
}

func (h *Http) fileServer(assetsWebRoot, assetsLocation string, spa bool, notFound []byte) (http.Handler, error) {
	var notFoundReader io.Reader
	if notFound != nil {
		notFoundReader = bytes.NewReader(notFound)
	}
	var fs http.Handler
	var err error
	if spa {
		fs, err = R.NewFileServer(assetsWebRoot, assetsLocation, R.FsOptCustom404(notFoundReader), R.FsOptSPA)
	} else {
		fs, err = R.NewFileServer(assetsWebRoot, assetsLocation, R.FsOptCustom404(notFoundReader))
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create file server: %w", err)
	}
	return fs, nil
}

func (h *Http) isAssetRequest(r *http.Request) bool {
	if h.AssetsLocation == "" || h.AssetsWebRoot == "" {
		return false
	}
	root := strings.TrimSuffix(h.AssetsWebRoot, "/")
	return r.URL.Path == root || strings.HasPrefix(r.URL.Path, root+"/")
}

func (h *Http) toHTTP(address string, httpPort int) string {
	rx := regexp.MustCompile(`(.*):(\d*)`)
	return rx.ReplaceAllString(address, "$1:") + strconv.Itoa(httpPort)
}

func (h *Http) pluginHandler() func(next http.Handler) http.Handler {
	if h.PluginConductor == nil {
		return passThroughHandler
	}
	log.Printf("[INFO] plugin support enabled")
	return h.PluginConductor.Middleware
}

func (h *Http) mgmtHandler() func(next http.Handler) http.Handler {
	if h.Metrics == nil {
		return passThroughHandler
	}
	log.Printf("[DEBUG] metrics enabled")
	return h.Metrics.Middleware
}

// basicAuthHandler provides global basic auth that skips routes with per-route auth configured.
func (h *Http) basicAuthHandler() func(next http.Handler) http.Handler {
	if !h.BasicAuthEnabled {
		return passThroughHandler
	}
	return globalBasicAuthHandler(h.BasicAuthAllowed)
}

func (h *Http) makeHTTPServer(addr string, router http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadHeaderTimeout: h.Timeouts.ReadHeader,
		WriteTimeout:      h.Timeouts.Write,
		IdleTimeout:       h.Timeouts.Idle,
	}
}

func (h *Http) setXRealIP(r *http.Request) {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		// use the left-most non-private client IP address
		// if there is no any non-private IP address, use the left-most address
		r.Header.Set("X-Real-IP", preferPublicIP(strings.Split(forwarded, ",")))
		return
	}

	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return
	}
	userIP := net.ParseIP(ip)
	if userIP == nil {
		return
	}
	r.Header.Set("X-Real-IP", ip)
}

// discoveredServers gets the list of servers discovered by providers.
// The underlying discovery is async and may happen not right away.
// We should try to get servers for some time and make sure we have the complete list of servers
// by checking if the number of servers has not changed between two calls.
func (h *Http) discoveredServers(ctx context.Context, interval time.Duration) (servers []string) {
	discoveredServers := 0

	for range 100 {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		servers = h.Servers() // fill all discovered if nothing defined
		if len(servers) > 0 && len(servers) == discoveredServers {
			break
		}
		discoveredServers = len(servers)
		time.Sleep(interval)
	}
	return servers
}

func (h *Http) setHeaderIfNotExists(r *http.Request, key, value string) {
	if _, ok := r.Header[key]; !ok {
		r.Header.Set(key, value)
	}
}
