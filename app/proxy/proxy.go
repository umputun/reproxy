package proxy

import (
	"context"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/go-pkgz/lgr"
	log "github.com/go-pkgz/lgr"
	"github.com/go-pkgz/rest"
	"github.com/go-pkgz/rest/logger"

	"github.com/umputun/docker-proxy/app/proxy/middleware"
)

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
}

type Matcher interface {
	Match(srv, src string) (string, bool)
}

func (h *Http) Do(ctx context.Context) error {
	log.Printf("[INFO] run proxy on %s", h.Address)
	if h.AssetsLocation != "" {
		log.Printf("[DEBUG] assets file server enabled for %s", h.AssetsLocation)
	}

	httpServer := &http.Server{
		Addr: h.Address,
		Handler: h.wrap(h.proxyHandler(),
			rest.Recoverer(lgr.Default()),
			rest.AppInfo("dpx", "umputun", h.Version),
			rest.Ping,
			logger.New(logger.Prefix("[DEBUG] PROXY")).Handler,
			rest.SizeLimit(h.MaxBodySize),
			middleware.Headers(h.ProxyHeaders...),
			h.gzipHandler(),
		),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	go func() {
		<-ctx.Done()
		if err := httpServer.Close(); err != nil {
			log.Printf("[ERROR] failed to close proxy server, %v", err)
		}
	}()

	return httpServer.ListenAndServe()
}

// Run the lister and request's router, activate rest server
func (h *Http) Run(ctx context.Context) {

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

	handler := h.wrap(h.proxyHandler(),
		rest.Recoverer(lgr.Default()),
		rest.AppInfo("dpx", "umputun", h.Version),
		rest.Ping,
		logger.New(logger.Prefix("[DEBUG] PROXY")).Handler,
		rest.SizeLimit(h.MaxBodySize),
		middleware.Headers(h.ProxyHeaders...),
		h.gzipHandler(),
	)

	switch h.SSLConfig.SSLMode {
	case None:
		log.Printf("[INFO] activate http proxy server on %s", h.Address)
		httpServer = h.makeHTTPServer(h.Address, handler)
		httpServer.ErrorLog = log.ToStdLogger(log.Default(), "WARN")
		err := httpServer.ListenAndServe()
		log.Printf("[WARN] http server terminated, %s", err)
	case Static:
		log.Printf("[INFO] activate https server in 'static' mode on %s", h.Address)

		httpsServer = h.makeHTTPSServer(h.Address, handler)
		httpsServer.ErrorLog = log.ToStdLogger(log.Default(), "WARN")

		httpServer = h.makeHTTPServer(h.toHttp(h.Address), h.httpToHTTPSRouter())
		httpServer.ErrorLog = log.ToStdLogger(log.Default(), "WARN")

		go func() {
			log.Printf("[INFO] activate http redirect server on %s", h.toHttp(h.Address))
			err := httpServer.ListenAndServe()
			log.Printf("[WARN] http redirect server terminated, %s", err)
		}()
		err := httpServer.ListenAndServeTLS(h.SSLConfig.Cert, h.SSLConfig.Key)
		log.Printf("[WARN] https server terminated, %s", err)
	case Auto:
		log.Printf("[INFO] activate https server in 'auto' mode on %s", h.Address)

		m := h.makeAutocertManager()
		httpsServer = h.makeHTTPSAutocertServer(h.Address, handler, m)
		httpsServer.ErrorLog = log.ToStdLogger(log.Default(), "WARN")

		httpServer = h.makeHTTPServer(h.toHttp(h.Address), h.httpChallengeRouter(m))
		httpServer.ErrorLog = log.ToStdLogger(log.Default(), "WARN")

		go func() {
			log.Printf("[INFO] activate http challenge server on port %s", h.toHttp(h.Address))
			err := httpServer.ListenAndServe()
			log.Printf("[WARN] http challenge server terminated, %s", err)
		}()

		err := httpsServer.ListenAndServeTLS("", "")
		log.Printf("[WARN] https server terminated, %s", err)
	}
}

func (h *Http) toHttp(address string) string {
	return strings.Replace(address, ":443", ":80", 1)
}

func (h *Http) gzipHandler() func(next http.Handler) http.Handler {
	gzHandler := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
	if h.GzEnabled {
		gzHandler = middleware.Gzip
	}
	return gzHandler
}

// wrap convert a list of middlewares to nested calls, in reversed order
func (h *Http) wrap(p http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	res := p
	for i := len(mws) - 1; i >= 0; i-- {
		res = mws[i](res)
	}
	return res
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

func (h *Http) makeHTTPServer(addr string, router http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
}
