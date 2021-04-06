package proxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-pkgz/lgr"
	log "github.com/go-pkgz/lgr"
	"github.com/go-pkgz/rest"
	R "github.com/go-pkgz/rest"
	"github.com/go-pkgz/rest/logger"
	"github.com/gorilla/handlers"
	"github.com/pkg/errors"

	"github.com/umputun/reproxy/app/discovery"
)

// Http is a proxy server for both http and https
type Http struct {
	Matcher
	Address        string
	TimeOut        time.Duration
	AssetsLocation string
	AssetsWebRoot  string
	MaxBodySize    int64
	GzEnabled      bool
	ProxyHeaders   []string
	SSLConfig      SSLConfig
	Version        string
	AccessLog      io.Writer
}

// Matcher source info (server and route) to the destination url
// If no match found return ok=false
type Matcher interface {
	Match(srv, src string) (string, bool)
	Servers() (servers []string)
	Mappers() (mappers []discovery.UrlMapper)
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
		R.Recoverer(lgr.Default()),
		R.AppInfo("reproxy", "umputun", h.Version),
		R.Ping,
		h.healthMiddleware,
		logger.New(logger.Prefix("[DEBUG] PROXY")).Handler,
		h.accessLogHandler(h.AccessLog),
		R.SizeLimit(h.MaxBodySize),
		R.Headers(h.ProxyHeaders...),
		h.gzipHandler(),
	)

	h.SSLConfig.FQDNs = h.Servers() // fill all servers
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

		httpServer = h.makeHTTPServer(h.toHttp(h.Address, h.SSLConfig.RedirHttpPort), h.httpToHTTPSRouter())
		httpServer.ErrorLog = log.ToStdLogger(log.Default(), "WARN")

		go func() {
			log.Printf("[INFO] activate http redirect server on %s", h.toHttp(h.Address, h.SSLConfig.RedirHttpPort))
			err := httpServer.ListenAndServe()
			log.Printf("[WARN] http redirect server terminated, %s", err)
		}()
		return httpServer.ListenAndServeTLS(h.SSLConfig.Cert, h.SSLConfig.Key)
	case SSLAuto:
		log.Printf("[INFO] activate https server in 'auto' mode on %s", h.Address)

		m := h.makeAutocertManager()
		httpsServer = h.makeHTTPSAutocertServer(h.Address, handler, m)
		httpsServer.ErrorLog = log.ToStdLogger(log.Default(), "WARN")

		httpServer = h.makeHTTPServer(h.toHttp(h.Address, h.SSLConfig.RedirHttpPort), h.httpChallengeRouter(m))
		httpServer.ErrorLog = log.ToStdLogger(log.Default(), "WARN")

		go func() {
			log.Printf("[INFO] activate http challenge server on port %s", h.toHttp(h.Address, h.SSLConfig.RedirHttpPort))
			err := httpServer.ListenAndServe()
			log.Printf("[WARN] http challenge server terminated, %s", err)
		}()

		return httpsServer.ListenAndServeTLS("", "")
	}
	return errors.Errorf("unknown SSL type %v", h.SSLConfig.SSLMode)
}

func (h *Http) proxyHandler() http.HandlerFunc {
	type contextKey string

	reverseProxy := &httputil.ReverseProxy{
		Director: func(r *http.Request) {
			ctx := r.Context()
			uu := ctx.Value(contextKey("url")).(*url.URL)
			r.URL.Path = uu.Path
			r.URL.Host = uu.Host
			r.URL.Scheme = uu.Scheme
			r.Header.Add("X-Forwarded-Host", uu.Host)
			r.Header.Add("X-Origin-Host", r.Host)
			h.setXRealIP(r)
		},
	}

	reverseProxy.Transport = http.DefaultTransport
	reverseProxy.Transport.(*http.Transport).ResponseHeaderTimeout = h.TimeOut

	// default assetsHandler disabled, returns error on missing matches
	assetsHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("[WARN] mo match for %s", r.URL)
		http.Error(w, "Server error", http.StatusBadGateway)
	})

	if h.AssetsLocation != "" && h.AssetsWebRoot != "" {
		fs, err := rest.FileServer(h.AssetsWebRoot, h.AssetsLocation)
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
		u, ok := h.Match(server, r.URL.Path)
		if !ok {
			assetsHandler.ServeHTTP(w, r)
			return
		}

		uu, err := url.Parse(u)
		if err != nil {
			http.Error(w, "Server error", http.StatusBadGateway)
			return
		}

		ctx := context.WithValue(r.Context(), contextKey("url"), uu) // set destination url in request's context
		reverseProxy.ServeHTTP(w, r.WithContext(ctx))
	}
}

func (h *Http) toHttp(address string, httpPort int) string {
	rx := regexp.MustCompile(`(.*):(\d*)`)
	return rx.ReplaceAllString(address, "$1:") + strconv.Itoa(httpPort)
}

func (h *Http) gzipHandler() func(next http.Handler) http.Handler {
	if h.GzEnabled {
		return R.Gzip()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}

func (h *Http) accessLogHandler(wr io.Writer) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return handlers.CombinedLoggingHandler(wr, next)
	}
}

func (h *Http) makeHTTPServer(addr string, router http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
}

func (h *Http) setXRealIP(r *http.Request) {

	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return
	}

	userIP := net.ParseIP(ip)
	if userIP == nil {
		return
	}
	r.Header.Add("X-Real-IP", ip)
}
