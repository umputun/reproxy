package proxy

import (
	"context"
	"fmt"
	"io"
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
)

// Http is a proxy server for both http and https
type Http struct { // nolint golint
	Matcher
	Address        string
	AssetsLocation string
	AssetsWebRoot  string
	MaxBodySize    int64
	GzEnabled      bool
	ProxyHeaders   []string
	SSLConfig      SSLConfig
	Version        string
	AccessLog      io.Writer
	StdOutEnabled  bool
	Signature      bool
	Timeouts       Timeouts
	Metrics        Metrics
}

// Matcher source info (server and route) to the destination url
// If no match found return ok=false
type Matcher interface {
	Match(srv, src string) (string, discovery.MatchType, bool)
	Servers() (servers []string)
	Mappers() (mappers []discovery.URLMapper)
}

// Metrics wraps middleware publishing counts
type Metrics interface {
	Middleware(next http.Handler) http.Handler
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
		h.Metrics.Middleware,
		h.headersHandler(h.ProxyHeaders),
		h.accessLogHandler(h.AccessLog),
		h.stdoutLogHandler(h.StdOutEnabled, logger.New(logger.Log(log.Default()), logger.Prefix("[INFO]")).Handler),
		R.SizeLimit(h.MaxBodySize),
		h.gzipHandler(),
	)

	if len(h.SSLConfig.FQDNs) == 0 {
		h.SSLConfig.FQDNs = h.Servers() // fill all discovered if nothing defined
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
			r.Header.Add("X-Forwarded-Host", uu.Host)
			r.Header.Set("X-Origin-Host", r.Host)
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

	// default assetsHandler disabled, returns error on missing matches
	assetsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[WARN] mo match for %s", r.URL)
		http.Error(w, "Server error", http.StatusBadGateway)
	})

	if h.AssetsLocation != "" && h.AssetsWebRoot != "" {
		fs, err := R.FileServer(h.AssetsWebRoot, h.AssetsLocation)
		if err == nil {
			assetsHandler = func(w http.ResponseWriter, r *http.Request) {
				fs.ServeHTTP(w, r)
			}
		}
	}

	return func(w http.ResponseWriter, r *http.Request) {

		server := r.URL.Hostname()
		if server == "" {
			server = strings.Split(r.Host, ":")[0]
		}
		u, mt, ok := h.Match(server, r.URL.Path)
		if !ok {
			assetsHandler.ServeHTTP(w, r)
			return
		}

		switch mt {
		case discovery.MTProxy:
			uu, err := url.Parse(u)
			if err != nil {
				http.Error(w, "Server error", http.StatusBadGateway)
				return
			}
			log.Printf("[DEBUG] proxy to %s", uu)
			ctx := context.WithValue(r.Context(), contextKey("url"), uu) // set destination url in request's context
			reverseProxy.ServeHTTP(w, r.WithContext(ctx))
		case discovery.MTStatic:
			// static match result has webroot:location, i.e. /www:/var/somedir/
			ae := strings.Split(u, ":")
			if len(ae) != 2 { // shouldn't happen
				http.Error(w, "Server error", http.StatusInternalServerError)
				return
			}
			fs, err := R.FileServer(ae[0], ae[1])
			if err != nil {
				http.Error(w, "Server error", http.StatusInternalServerError)
				return
			}
			fs.ServeHTTP(w, r)
		}
	}
}

func (h *Http) toHTTP(address string, httpPort int) string {
	rx := regexp.MustCompile(`(.*):(\d*)`)
	return rx.ReplaceAllString(address, "$1:") + strconv.Itoa(httpPort)
}

func (h *Http) gzipHandler() func(next http.Handler) http.Handler {
	if h.GzEnabled {
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
