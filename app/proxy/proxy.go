package proxy

import (
	"context"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

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
	Version        string
}

type Matcher interface {
	Match(url string) (string, bool)
}

func (h *Http) Do(ctx context.Context) error {
	log.Printf("[INFO] run proxy on %s", h.Address)
	if h.AssetsLocation != "" {
		log.Printf("[DEBUG] assets file server enabled for %s", h.AssetsLocation)
	}

	httpServer := &http.Server{
		Addr: h.Address,
		Handler: h.wrap(h.proxyHandler(),
			rest.AppInfo("dpx", "umputun", h.Version),
			rest.Ping,
			logger.New(logger.Prefix("[DEBUG] PROXY")).Handler,
			rest.SizeLimit(h.MaxBodySize),
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

		u, ok := h.Match(r.URL.Path)
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
