package autocert

import (
	"cmp"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/caddyserver/certmagic"
	log "github.com/go-pkgz/lgr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Manager specifies methods for the automatic ACME certificate manager to implement
type Manager interface {
	GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error)
	HTTPHandler(http.Handler) http.Handler
}

// Config specifies parameters for the ACME certificate manager
type Config struct {
	ACMELocation string                // directory where the obtained certificates are stored
	ACMEEmail    string                // email address to use for the ACME account
	FQDNs        []string              // list of fully qualified domain names to manage certificates for
	DNSProvider  certmagic.DNSProvider // provider to use for DNS-01 challenges
	TTL          time.Duration         // TTL to use when setting DNS records for DNS-01 challenges
}

// Certmagic is a wrapper for certmagic.Config and certmagic.ACMEIssuer,
// to implement the Manager interface
type Certmagic struct {
	magic *certmagic.Config
	acme  *certmagic.ACMEIssuer
}

// NewCertmagic creates a new ACME certificate manager.
func NewCertmagic(cfg Config) Manager {
	mngr := Certmagic{}

	fqdns := map[string]struct{}{}
	for _, fqdn := range cfg.FQDNs {
		fqdns[fqdn] = struct{}{}
	}

	logger := zap.New(zapcore.NewCore(
		zapcore.NewConsoleEncoder(zap.NewProductionEncoderConfig()),
		nopSyncer{Writer: log.ToWriter(log.Default(), "[DEBUG][certmagic]")},
		zap.DebugLevel,
	))

	// certmagic requires to make a configuration template in order to keep up
	// with the changes, for instance, in DNS providers in runtime in cache
	// configuration template itself is required in order to allow cache to invoke
	// certificate renewal at the time when the certificate is about to expire
	magicTmpl := certmagic.Config{
		RenewalWindowRatio: certmagic.DefaultRenewalWindowRatio,
		KeySource:          certmagic.DefaultKeyGenerator,
		Storage:            &certmagic.FileStorage{Path: cfg.ACMELocation},
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
		// OS env used in tests
		CA:     cmp.Or(os.Getenv("TEST_ACME_CA"), certmagic.LetsEncryptProductionCA),
		Email:  cfg.ACMEEmail,
		Agreed: true,
		Logger: logger,
	})
	if cfg.DNSProvider != nil {
		acme.DNS01Solver = &certmagic.DNS01Solver{
			DNSManager: certmagic.DNSManager{
				DNSProvider: cfg.DNSProvider,
				TTL:         cfg.TTL,
				Logger:      logger,
			},
		}
	}
	magic.Issuers = []certmagic.Issuer{acme}

	mngr.magic = magic
	mngr.acme = acme

	return mngr
}

// GetCertificate is a hook to get a certificate for a given ClientHelloInfo.
func (m Certmagic) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	return m.magic.GetCertificate(hello)
}

// HTTPHandler returns an http.Handler that handles ACME HTTP challenges.
func (m Certmagic) HTTPHandler(next http.Handler) http.Handler {
	return m.acme.HTTPChallengeHandler(next)
}

type nopSyncer struct{ io.Writer }

func (nopSyncer) Sync() error { return nil }
