// Package mgmt provide management server. Provides API to get info about reproxy routes and settings
package mgmt

import (
	"context"
	"net/http"
	"time"

	log "github.com/go-pkgz/lgr"
	"github.com/go-pkgz/rest"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/umputun/reproxy/app/discovery"
)

//go:generate moq -out inofrmer_mock.go -fmt goimports . Informer

// Server represents management server
type Server struct {
	Listen         string
	Informer       Informer
	Version        string
	AssetsLocation string
	AssetsWebRoot  string
	Metrics        *Metrics
}

// Informer wraps interface to get info about servers and mappers
type Informer interface {
	Mappers() (mappers []discovery.URLMapper)
}

// Run the lister and management router, activate rest server
func (s *Server) Run(ctx context.Context) error {
	log.Printf("[INFO] start management server on %s", s.Listen)

	handler := http.NewServeMux()
	handler.HandleFunc("/routes", s.routesCtrl())
	handler.Handle("/metrics", promhttp.Handler())
	h := rest.Wrap(handler,
		rest.Recoverer(log.Default()),
		rest.AppInfo("reproxy-mgmt", "umputun", s.Version),
		rest.Ping,
	)

	httpServer := http.Server{
		Addr:              s.Listen,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	go func() {
		<-ctx.Done()
		err := httpServer.Shutdown(context.Background())
		log.Printf("[WARN] mgmt server terminated, %v", err)
	}()

	return httpServer.ListenAndServe()
}

// routesCtrl - GET /routes, returns the list of all routes
func (s *Server) routesCtrl() func(w http.ResponseWriter, r *http.Request) {
	type resp struct {
		Route          string `json:"route,omitempty"`
		Destination    string `json:"destination,omitempty"`
		Server         string `json:"server"`
		MatchType      string `json:"match"`
		Provider       string `json:"provider"`
		AssetsLocation string `json:"assets_location,omitempty"`
		AssetsWebRoot  string `json:"assets_webroot,omitempty"`
		Ping           string `json:"ping,omitempty"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		res := map[string][]resp{}
		for _, mp := range s.Informer.Mappers() {
			res[mp.Server] = append(res[mp.Server], resp{Server: mp.Server, Provider: string(mp.ProviderID), Route: mp.SrcMatch.String(),
				Destination: mp.Dst, MatchType: mp.MatchType.String(), Ping: mp.PingURL})
		}
		if s.AssetsLocation != "" {
			res["*"] = append([]resp{{Server: "*", Provider: "system", MatchType: discovery.MTStatic.String(),
				AssetsLocation: s.AssetsLocation, AssetsWebRoot: s.AssetsWebRoot}}, res["*"]...)
		}
		rest.RenderJSON(w, res)
	}
}
