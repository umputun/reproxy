package proxy

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"

	log "github.com/go-pkgz/lgr"
	R "github.com/go-pkgz/rest"
	"github.com/umputun/reproxy/app/proxy/autocert"
)

// sslMode defines ssl mode for rest server
type sslMode int8

const (
	// SSLNone defines to run http server only
	SSLNone sslMode = iota

	// SSLStatic defines to run both https and http server. Redirect http to https
	SSLStatic

	// SSLAuto defines to run both https and http server. Redirect http to https. Https server with autocert support
	SSLAuto
)

// SSLConfig holds all ssl params for rest server
type SSLConfig struct {
	SSLMode       sslMode
	Cert          string
	Key           string
	RedirHTTPPort int

	autocert.Config
}

// httpToHTTPSRouter creates new router which does redirect from http to https server
// with default middlewares. Used in 'static' ssl mode.
func (h *Http) httpToHTTPSRouter() http.Handler {
	log.Printf("[DEBUG] create https-to-http redirect routes")
	return R.Wrap(h.redirectHandler(), R.Recoverer(log.Default()))
}

// httpChallengeRouter creates new router which performs ACME "http-01" challenge response
// with default middlewares. This part is necessary to obtain certificate from LE.
// If it receives not a acme challenge it performs redirect to https server.
// Used in 'auto' ssl mode.
func (h *Http) httpChallengeRouter(m autocert.Manager) http.Handler {
	log.Printf("[DEBUG] create http-challenge routes")
	return R.Wrap(m.HTTPHandler(h.redirectHandler()), R.Recoverer(log.Default()))
}

func (h *Http) redirectHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server := strings.Split(r.Host, ":")[0]
		newURL := fmt.Sprintf("https://%s:443%s", server, r.URL.Path)
		if r.URL.RawQuery != "" {
			newURL += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, newURL, http.StatusTemporaryRedirect)
	})
}

func (h *Http) makeAutocertManager() autocert.Manager {
	log.Printf("[DEBUG] autocert manager for domains: %+v, location: %s, email: %q, dns provider: %T",
		h.SSLConfig.FQDNs, h.SSLConfig.ACMELocation, h.SSLConfig.ACMEEmail, h.SSLConfig.DNSProvider)
	return autocert.NewCertmagic(h.SSLConfig.Config)
}

// makeHTTPSAutoCertServer makes https server with autocert mode (LE support)
func (h *Http) makeHTTPSAutocertServer(address string, router http.Handler, m autocert.Manager) *http.Server {
	server := h.makeHTTPServer(address, router)
	cfg := h.makeTLSConfig()
	cfg.GetCertificate = m.GetCertificate
	server.TLSConfig = cfg
	return server
}

// makeHTTPSServer makes https server for static mode
func (h *Http) makeHTTPSServer(address string, router http.Handler) *http.Server {
	server := h.makeHTTPServer(address, router)
	server.TLSConfig = h.makeTLSConfig()
	return server
}

func (h *Http) makeTLSConfig() *tls.Config {
	return &tls.Config{
		PreferServerCipherSuites: true,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
		},
		MinVersion: tls.VersionTLS12,
		CurvePreferences: []tls.CurveID{
			tls.CurveP256,
			tls.X25519,
			tls.CurveP384,
		},
	}
}
