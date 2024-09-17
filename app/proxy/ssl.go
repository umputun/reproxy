package proxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/caddyserver/certmagic"
	log "github.com/go-pkgz/lgr"
	R "github.com/go-pkgz/rest"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
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

	ACMEDirectory string                // URL of the ACME directory to use
	ACMELocation  string                // directory where the obtained certificates are stored
	ACMEEmail     string                // email address to use for the ACME account
	FQDNs         []string              // list of fully qualified domain names to manage certificates for
	DNSProvider   certmagic.DNSProvider // provider to use for DNS-01 challenges
	TTL           time.Duration         // TTL to use when setting DNS records for DNS-01 challenges
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
func (h *Http) httpChallengeRouter(m AutocertManager) http.Handler {
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

//go:generate moq -out dns_provider_mock.go -fmt goimports . dnsProvider

type dnsProvider interface{ certmagic.DNSProvider }

// AutocertManager specifies methods for the automatic ACME certificate manager to implement
type AutocertManager interface {
	GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error)
	HTTPHandler(http.Handler) http.Handler
}

func (h *Http) makeAutocertManager() AutocertManager {
	log.Printf("[DEBUG] autocert manager for domains: %+v, location: %s, email: %q, dns provider: %T",
		h.SSLConfig.FQDNs, h.SSLConfig.ACMELocation, h.SSLConfig.ACMEEmail, h.SSLConfig.DNSProvider)

	mngr := &cmmanager{}

	fqdns := map[string]struct{}{}
	for _, fqdn := range h.SSLConfig.FQDNs {
		fqdns[fqdn] = struct{}{}
	}

	logger := zap.New(zapcore.NewCore(
		zapcore.NewConsoleEncoder(zapcore.EncoderConfig{
			MessageKey:     "msg",
			EncodeDuration: zapcore.StringDurationEncoder,
		}),
		nopSyncer{Writer: log.ToWriter(log.Default(), "[WARN][certmagic]")},
		zap.WarnLevel,
	))

	// certmagic requires to make a configuration template in order to keep up
	// with the changes, for instance, in DNS providers in runtime in cache
	// configuration template itself is required in order to allow cache to invoke
	// certificate renewal at the time when the certificate is about to expire
	magicTmpl := certmagic.Config{
		RenewalWindowRatio: certmagic.DefaultRenewalWindowRatio,
		KeySource:          certmagic.DefaultKeyGenerator,
		Storage:            &certmagic.FileStorage{Path: h.SSLConfig.ACMELocation},
		OnDemand: &certmagic.OnDemandConfig{
			DecisionFunc: func(ctx context.Context, name string) error {
				if _, ok := fqdns[name]; ok {
					return nil
				}
				return fmt.Errorf("not allowed domain %q", name)
			},
		},
		Logger: logger,
	}
	var cache *certmagic.Cache
	cache = certmagic.NewCache(certmagic.CacheOptions{
		GetConfigForCert: func(cert certmagic.Certificate) (*certmagic.Config, error) {
			return certmagic.New(cache, magicTmpl), nil
		},
		Logger: logger,
	})
	magic := certmagic.New(cache, magicTmpl)
	acme := certmagic.NewACMEIssuer(magic, certmagic.ACMEIssuer{
		CA:     h.SSLConfig.ACMEDirectory,
		Email:  h.SSLConfig.ACMEEmail,
		Agreed: true,
		Logger: logger,
	})
	if h.SSLConfig.DNSProvider != nil {
		acme.DNS01Solver = &certmagic.DNS01Solver{
			DNSManager: certmagic.DNSManager{
				DNSProvider: h.SSLConfig.DNSProvider,
				TTL:         h.SSLConfig.TTL,
				Logger:      logger,
				Resolvers:   h.dnsResolvers,
			},
		}
	}
	magic.Issuers = []certmagic.Issuer{acme}

	mngr.magic = magic
	mngr.acme = acme

	return mngr
}

type nopSyncer struct{ io.Writer }

func (nopSyncer) Sync() error { return nil }

type cmmanager struct {
	magic *certmagic.Config
	acme  *certmagic.ACMEIssuer
}

// GetCertificate returns certificate for the autocert manager
func (c cmmanager) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	return c.magic.GetCertificate(hello)
}

// HTTPHandler returns http handler for the autocert manager
func (c cmmanager) HTTPHandler(next http.Handler) http.Handler {
	return c.acme.HTTPChallengeHandler(next)
}

// makeHTTPSAutoCertServer makes https server with autocert mode (LE support)
func (h *Http) makeHTTPSAutocertServer(address string, router http.Handler, m AutocertManager) *http.Server {
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
