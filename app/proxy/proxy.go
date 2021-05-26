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
	"github.com/gorilla/handlers"

	"github.com/umputun/reproxy/app/discovery"
	"github.com/umputun/reproxy/app/mgmt"
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
		h.signatureHandler(),
		h.pingHandler,
		h.healthMiddleware,
		h.mgmtHandler(),
		h.headersHandler(h.ProxyHeaders),
		h.accessLogHandler(h.AccessLog),
		h.stdoutLogHandler(h.StdOutEnabled, logger.New(logger.Log(log.Default()), logger.Prefix("[INFO]")).Handler),
		h.maxReqSizeHandler(h.MaxBodySize),
		h.gzipHandler(),
	)

	rand.Seed(time.Now().UnixNano())

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

func (h *Http) proxyHandler() http.HandlerFunc {
	type contextKey string

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

		server := r.URL.Hostname()
		if server == "" {
			server = strings.Split(r.Host, ":")[0]
		}
		matches := h.Match(server, r.URL.Path) // get all matches for the server:path pair
		match, ok := h.getMatch(matches, rand.Intn)
		if !ok { // no route match
			if h.isAssetRequest(r) {
				assetsHandler.ServeHTTP(w, r)
				return
			}
			log.Printf("[WARN] no match for %s %s", r.URL.Hostname(), r.URL.Path)
			h.Reporter.Report(w, http.StatusBadGateway)
			return
		}

		switch matches.MatchType {
		case discovery.MTProxy:
			uu, err := url.Parse(match.Destination)
			if err != nil {
				h.Reporter.Report(w, http.StatusBadGateway)
				return
			}
			log.Printf("[DEBUG] proxy to %s", uu)
			ctx := context.WithValue(r.Context(), contextKey("url"), uu) // set destination url in request's context

			// set keys for plugin conductor
			ctx = context.WithValue(ctx, plugin.ConductorCtxtKey("route"), match.Destination)
			ctx = context.WithValue(ctx, plugin.ConductorCtxtKey("server"), match.Mapper.Server)
			ctx = context.WithValue(ctx, plugin.ConductorCtxtKey("src"), match.Mapper.SrcMatch.String())
			ctx = context.WithValue(ctx, plugin.ConductorCtxtKey("dst"), match.Mapper.Dst)
			ctx = context.WithValue(ctx, plugin.ConductorCtxtKey("provider"), match.Mapper.ProviderID)

			reverseProxy.ServeHTTP(w, r.WithContext(ctx))
		case discovery.MTStatic:
			// static match result has webroot:location, i.e. /www:/var/somedir/
			ae := strings.Split(match.Destination, ":")
			if len(ae) != 2 { // shouldn't happen
				h.Reporter.Report(w, http.StatusInternalServerError)
				return
			}
			fs, err := R.FileServer(ae[0], ae[1])
			if err != nil {
				h.Reporter.Report(w, http.StatusInternalServerError)
				return
			}
			h.CacheControl.Middleware(fs).ServeHTTP(w, r)
		}
	}
}

func (h *Http) getMatch(mm discovery.Matches, picker func(len int) int) (m discovery.MatchedRoute, ok bool) {
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

func (h *Http) gzipHandler() func(next http.Handler) http.Handler {
	if h.GzEnabled {
		log.Printf("[DEBUG] gzip enabled")
		return handlers.CompressHandler
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}

func (h *Http) signatureHandler() func(next http.Handler) http.Handler {
	if h.Signature {
		log.Printf("[DEBUG] signature headers enabled")
		return R.AppInfo("reproxy", "umputun", h.Version)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}

func (h *Http) headersHandler(headers []string) func(next http.Handler) http.Handler {

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(h.ProxyHeaders) == 0 {
				next.ServeHTTP(w, r)
				return
			}
			for _, h := range headers {
				elems := strings.Split(h, ":")
				if len(elems) != 2 {
					continue
				}
				w.Header().Set(strings.TrimSpace(elems[0]), strings.TrimSpace(elems[1]))
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (h *Http) accessLogHandler(wr io.Writer) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return handlers.CombinedLoggingHandler(wr, next)
	}
}

func (h *Http) stdoutLogHandler(enable bool, lh func(next http.Handler) http.Handler) func(next http.Handler) http.Handler {

	if enable {
		log.Printf("[DEBUG] stdout logging enabled")
		return func(next http.Handler) http.Handler {
			fn := func(w http.ResponseWriter, r *http.Request) {
				// don't log to stdout GET ~/(.*)/ping$ requests
				if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/ping") {
					next.ServeHTTP(w, r)
					return
				}
				lh(next).ServeHTTP(w, r)
			}
			return http.HandlerFunc(fn)
		}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}

func (h *Http) maxReqSizeHandler(maxSize int64) func(next http.Handler) http.Handler {
	if maxSize <= 0 {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r)
			})
		}
	}

	log.Printf("[DEBUG] request size limited to %d", maxSize)
	return func(next http.Handler) http.Handler {

		fn := func(w http.ResponseWriter, r *http.Request) {

			// check ContentLength
			if r.ContentLength > maxSize {
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				return
			}

			r.Body = http.MaxBytesReader(w, r.Body, maxSize)
			if err := r.ParseForm(); err != nil {
				http.Error(w, "Request Entity Too Large", http.StatusRequestEntityTooLarge)
				return
			}
			next.ServeHTTP(w, r)
		}
		return http.HandlerFunc(fn)
	}
}

func (h *Http) mgmtHandler() func(next http.Handler) http.Handler {
	if h.Metrics.(*mgmt.Metrics) != nil { // type assertion needed because we compare interface to nil
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
