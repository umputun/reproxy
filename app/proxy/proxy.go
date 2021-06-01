package proxy

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
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
	Address         string
	AssetsLocation  string
	AssetsWebRoot   string
	MaxBodySize     int64
	GzEnabled       bool
	ProxyHeaders    []string
	SSLConfig       SSLConfig
	Version         string
	AccessLog       io.Writer
	StdOutEnabled   bool
	Signature       bool
	Timeouts        Timeouts
	CacheControl    MiddlewareProvider
	Metrics         MiddlewareProvider
	PluginConductor MiddlewareProvider
	Reporter        Reporter
	LBSelector      func(len int) int
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
	}

	if h.LBSelector == nil {
		h.LBSelector = rand.Intn
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
		R.Recoverer(log.Default()),
		signatureHandler(h.Signature, h.Version),
		h.pingHandler,
		h.healthMiddleware,
		h.matchHandler,
		h.mgmtHandler(),
		h.pluginHandler(),
		headersHandler(h.ProxyHeaders),
		accessLogHandler(h.AccessLog),
		stdoutLogHandler(h.StdOutEnabled, logger.New(logger.Log(log.Default()), logger.Prefix("[INFO]")).Handler),
		maxReqSizeHandler(h.MaxBodySize),
		gzipHandler(h.GzEnabled),
	)

	if len(h.SSLConfig.FQDNs) == 0 && h.SSLConfig.SSLMode == SSLAuto {
		// discovery async and may happen not right away. Try to get servers for some time
		for i := 0; i < 100; i++ {
			h.SSLConfig.FQDNs = h.Servers() // fill all discovered if nothing defined
			if len(h.SSLConfig.FQDNs) > 0 {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	switch h.SSLConfig.SSLMode {
	case SSLNone:
		log.Printf("[INFO] activate http proxy server on %s", h.Address)
		httpServer = h.makeHTTPServer(h.Address, handler)
		httpServer.ErrorLog = log.ToStdLogger(log.Default(), "WARN")
		return httpServer.ListenAndServe()
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
		return httpsServer.ListenAndServeTLS(h.SSLConfig.Cert, h.SSLConfig.Key)
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

		return httpsServer.ListenAndServeTLS("", "")
	}
	return fmt.Errorf("unknown SSL type %v", h.SSLConfig.SSLMode)
}

type contextKey string

const (
	ctxURL       = contextKey("url")
	ctxMatchType = contextKey("type")
)

func (h *Http) proxyHandler() http.HandlerFunc {

	reverseProxy := &httputil.ReverseProxy{
		Director: func(r *http.Request) {
			ctx := r.Context()
			uu := ctx.Value(contextKey("url")).(*url.URL)
			r.Header.Add("X-Forwarded-Host", r.Host)
			r.URL.Path = uu.Path
			r.URL.Host = uu.Host
			r.URL.Scheme = uu.Scheme
			r.Host = uu.Host
			h.setXRealIP(r)
		},
		Transport: &http.Transport{
			ResponseHeaderTimeout: h.Timeouts.ResponseHeader,
			DialContext: (&net.Dialer{
				Timeout:   h.Timeouts.Dial,
				KeepAlive: h.Timeouts.KeepAlive,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       h.Timeouts.IdleConn,
			TLSHandshakeTimeout:   h.Timeouts.TLSHandshake,
			ExpectContinueTimeout: h.Timeouts.ExpectContinue,
		},
		ErrorLog: log.ToStdLogger(log.Default(), "WARN"),
	}
	assetsHandler := h.assetsHandler()

	return func(w http.ResponseWriter, r *http.Request) {

		uuVal := r.Context().Value(ctxURL)
		if uuVal == nil { // no route match detected by matchHandler
			if h.isAssetRequest(r) {
				assetsHandler.ServeHTTP(w, r)
				return
			}
			log.Printf("[WARN] no match for %s %s", r.URL.Hostname(), r.URL.Path)
			h.Reporter.Report(w, http.StatusBadGateway)
			return
		}
		uu := uuVal.(*url.URL)

		match := r.Context().Value(plugin.CtxMatch).(discovery.MatchedRoute)
		matchType := r.Context().Value(ctxMatchType).(discovery.MatchType)

		switch matchType {
		case discovery.MTProxy:
			log.Printf("[DEBUG] proxy to %s", uu)
			reverseProxy.ServeHTTP(w, r)
		case discovery.MTStatic:
			// static match result has webroot:location, i.e. /www:/var/somedir/
			ae := strings.Split(match.Destination, ":")
			if len(ae) != 2 { // shouldn't happen
				log.Printf("[WARN] unexpected static assets destination: %s", match.Destination)
				h.Reporter.Report(w, http.StatusInternalServerError)
				return
			}
			fs, err := R.FileServer(ae[0], ae[1])
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

	getMatch := func(mm discovery.Matches, picker func(len int) int) (m discovery.MatchedRoute, ok bool) {
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
			return matches[picker(len(matches))], true
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server := r.URL.Hostname()
		if server == "" {
			server = strings.Split(r.Host, ":")[0]
		}
		matches := h.Match(server, r.URL.Path) // get all matches for the server:path pair
		match, ok := getMatch(matches, h.LBSelector)
		if ok {
			uu, err := url.Parse(match.Destination)
			if err != nil {
				log.Printf("[WARN] can't parse destination %s, %v", match.Destination, err)
				h.Reporter.Report(w, http.StatusBadGateway)
				return
			}
			ctx := context.WithValue(r.Context(), ctxURL, uu)             // set destination url in request's context
			ctx = context.WithValue(ctx, ctxMatchType, matches.MatchType) // set match type
			ctx = context.WithValue(ctx, plugin.CtxMatch, match)          // set keys for plugin conductor
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}

func (h *Http) assetsHandler() http.HandlerFunc {
	if h.AssetsLocation == "" || h.AssetsWebRoot == "" {
		return func(writer http.ResponseWriter, request *http.Request) {}
	}
	log.Printf("[DEBUG] shared assets server enabled for %s %s", h.AssetsWebRoot, h.AssetsLocation)
	fs, err := R.FileServer(h.AssetsWebRoot, h.AssetsLocation)
	if err != nil {
		log.Printf("[WARN] can't initialize assets server, %v", err)
		return func(writer http.ResponseWriter, request *http.Request) {}
	}
	return h.CacheControl.Middleware(fs).ServeHTTP
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
	if h.PluginConductor != nil {
		log.Printf("[INFO] plugin support enabled")
		return h.PluginConductor.Middleware
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}

func (h *Http) mgmtHandler() func(next http.Handler) http.Handler {
	if h.Metrics != nil {
		log.Printf("[DEBUG] metrics enabled")
		return h.Metrics.Middleware
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
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

	remoteIP := r.Header.Get("X-Forwarded-For")
	if remoteIP == "" {
		remoteIP = r.RemoteAddr
	}

	ip, _, err := net.SplitHostPort(remoteIP)
	if err != nil {
		return
	}

	userIP := net.ParseIP(ip)
	if userIP == nil {
		return
	}
	r.Header.Add("X-Real-IP", ip)
}
