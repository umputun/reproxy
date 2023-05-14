package acme

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"time"

	log "github.com/go-pkgz/lgr"

	"golang.org/x/crypto/acme"

	"github.com/umputun/reproxy/app/acme/dnsprovider"
	"github.com/umputun/reproxy/app/dns"
)

var defaultNameservers = []string{
	"google-public-dns-a.google.com",
	"google-public-dns-b.google.com",
}

var acmeV2Enpoint = "https://acme-v02.api.letsencrypt.org/directory"

// DNSChallengeConfig contains configuration for DNS challenge
type DNSChallengeConfig struct {
	Provider        string
	ProviderConfig  string
	Domains         []string
	Nameservers     []string
	Timeout         time.Duration
	PollingInterval time.Duration
	CertPath        string
	KeyPath         string
}

// DNSChallenge represents an ACME DNS challenge
type DNSChallenge struct {
	domains         []string
	nameservers     []string
	client          *acme.Client
	accountKey      *rsa.PrivateKey
	provider        dns.Provider
	order           *acme.Order
	timeout         time.Duration
	pollingInterval time.Duration
	certPath        string
	keyPath         string
}

// NewDNSChallege creates new DNSChallenge
func NewDNSChallege(config DNSChallengeConfig) (*DNSChallenge, error) {
	providerConf := dns.Opts{
		Provider:        config.Provider,
		ConfigPath:      config.ProviderConfig,
		Timeout:         config.Timeout,
		PollingInterval: config.PollingInterval,
	}

	p, err := dnsprovider.NewProvider(providerConf)
	if err != nil {
		return nil, err
	}

	if len(config.Domains) == 0 {
		return nil, fmt.Errorf("no domains (fqdn) specified")
	}

	return &DNSChallenge{provider: p,
		nameservers:     config.Nameservers,
		domains:         config.Domains,
		timeout:         config.Timeout,
		pollingInterval: config.PollingInterval,
		certPath:        config.CertPath,
		keyPath:         config.KeyPath,
	}, nil
}

// PreSolve is called before solving the challenge.
// ACME Order will be created and DNS record will be added.
func (d *DNSChallenge) PreSolve() error {
	ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
	defer cancel()

	if err := d.register(); err != nil {
		return err
	}
	return d.prepareOrder(ctx, d.domains)
}

// waitPropagation blocks until the DNS record is propagated or timeout is reached.
func (d *DNSChallenge) waitPropagation(record dns.Record) error {
	if len(d.domains) == 0 {
		return fmt.Errorf("no domain is provided")
	}

	log.Printf("[INFO] waiting for %s.%s record propagation", record.Host, record.Domain)

	ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
	defer cancel()

	if err := d.provider.WaitUntilPropagated(ctx, record); err != nil {
		log.Printf("[WARN] %v", err)
	}

	if err := d.checkWithNS(ctx, record); err != nil {
		log.Printf("[WARN] nameservers lookup ended with errors: %v", err)
	}

	return nil
}

// Solve is called to accept the challenge and pull the certificate.
func (d *DNSChallenge) Solve() error {
	ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
	defer cancel()

	for _, authzURL := range d.order.AuthzURLs {
		authz, err := d.client.GetAuthorization(ctx, authzURL)
		if err != nil {
			return err
		}

		var chl *acme.Challenge
		for i := range authz.Challenges {
			if authz.Challenges[i].Type == "dns-01" {
				chl = authz.Challenges[i]
				break
			}
		}

		if chl == nil {
			return fmt.Errorf("no DNS-01 challenge found for %v", authz.Identifier.Value)
		}

		record := dns.Record{
			Type:   "TXT",
			Host:   "_acme-challenge",
			Domain: authz.Identifier.Value,
		}

		if record.Value, err = d.client.DNS01ChallengeRecord(chl.Token); err != nil {
			return fmt.Errorf("error by retrieving TXT record value: %v", err)
		}

		err = d.provider.AddRecord(record)
		if err != nil {
			return fmt.Errorf("error by adding TXT record: %s: %v", record.Host+record.Domain, err)
		}

		log.Printf("[INFO] DNS %s record %s.%s added", record.Type, record.Host, record.Domain)

		err = d.waitPropagation(record)
		if err != nil {
			return fmt.Errorf("error by waiting for TXT record propagation: %s: %v", record.Host+record.Domain, err)
		}

		_, err = d.client.Accept(ctx, chl)
		if err != nil {
			return err
		}

		_, err = d.client.WaitAuthorization(ctx, authzURL)
		if err != nil {
			return err
		}

		err = d.provider.RemoveRecord(record)
		if err != nil {
			log.Printf("[WARN] error by removing TXT record: %s: %v", record.Host+record.Domain, err)
		}
	}

	return nil
}

// ObtainCertificate is called to obtain the certificate.
func (d *DNSChallenge) ObtainCertificate() error {
	ctx, cancel := context.WithTimeout(context.Background(), d.timeout)
	defer cancel()

	q := &x509.CertificateRequest{
		DNSNames: d.domains,
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}

	csr, err := x509.CreateCertificateRequest(rand.Reader, q, privateKey)
	if err != nil {
		return err
	}

	ders, _, err := d.client.CreateOrderCert(ctx, d.order.FinalizeURL, csr, false)
	if err != nil {
		return err
	}

	if len(ders) != 1 {
		return fmt.Errorf("expected 1 certificate, got %d", len(ders))
	}

	cert, err := x509.ParseCertificate(ders[0])
	if err != nil {
		return err
	}

	if err = d.writeCertificates(privateKey, cert); err != nil {
		return fmt.Errorf("error by writing certificate: %v", err)
	}

	return nil
}

func getCertificateExpiration(certPath string) (time.Time, error) {
	b, err := os.ReadFile(filepath.Clean(certPath))
	if err != nil {
		return time.Time{}, err
	}

	der, _ := pem.Decode(b)

	cert, err := x509.ParseCertificate(der.Bytes)
	if err != nil {
		return time.Time{}, err
	}

	return cert.NotAfter, nil
}

func (d *DNSChallenge) register() error {
	if d.client != nil {
		return nil
	}

	var err error
	d.accountKey, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}

	client := acme.Client{
		DirectoryURL: acmeV2Enpoint,
		Key:          d.accountKey,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	if _, err := client.Register(ctx, &acme.Account{}, acme.AcceptTOS); err != nil {
		return err
	}

	d.client = &client

	return nil
}

func (d *DNSChallenge) prepareOrder(ctx context.Context, domains []string) error {
	var err error
	authIDs := make([]acme.AuthzID, len(domains))
	for i := range domains {
		authIDs[i] = acme.AuthzID{Type: "dns", Value: domains[i]}
	}

	d.order, err = d.client.AuthorizeOrder(ctx, authIDs)
	if err != nil {
		return err
	}

	return nil
}

func (d *DNSChallenge) checkWithNS(ctx context.Context, record dns.Record) error {
	ticker := time.NewTicker(d.pollingInterval)

	var lastErr error
	nextNameserver := d.getNameserverFn()

	nameserver := nextNameserver()
	if lastErr = dns.LookupTXTRecord(record, nameserver); lastErr == nil {
		log.Printf("[INFO] DNS record %s.%s propagated to nameserver %s", record.Host, record.Domain, nameserver)
		nameserver = nextNameserver()
	}

nsLoop:
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout while checking DNS record propagation. Last error: %v", lastErr)
		case <-ticker.C:
			// record propagated to all nameservers
			if nameserver == "" {
				break nsLoop
			}
			err := dns.LookupTXTRecord(record, nameserver)
			if err == nil {
				log.Printf("[INFO] DNS record %s.%s propagated to nameserver %s", record.Host, record.Domain, nameserver)
				nameserver = nextNameserver()
				continue
			}
			lastErr = err
		}
	}

	return nil
}

func (d *DNSChallenge) writeCertificates(privateKey *rsa.PrivateKey, cert *x509.Certificate) error {
	if privateKey == nil || cert == nil {
		return fmt.Errorf("private key or certificate is nil")
	}

	dir := filepath.Dir(d.keyPath)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return fmt.Errorf("error by creating directory %s: %v", dir, err)
			}
		}
	}

	keyOut, err := os.OpenFile(filepath.Clean(d.keyPath), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	pkb, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return fmt.Errorf("unable to marshal private key: %v", err)
	}
	if err = pem.Encode(keyOut, &pem.Block{Type: "PRIVATE KEY", Bytes: pkb}); err != nil {
		return fmt.Errorf("error by encoding private key: %v", err)
	}
	if err = keyOut.Close(); err != nil {
		return fmt.Errorf("error closing key.pem: %v", err)
	}

	certOut, err := os.Create(d.certPath)
	if err != nil {
		return err
	}

	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}); err != nil {
		return err
	}

	if err := certOut.Close(); err != nil {
		return err
	}

	log.Printf("[INFO] wrote certificate to %s", d.certPath)
	log.Printf("[INFO] wrote private key to %s", d.keyPath)

	return nil
}

func (d *DNSChallenge) getNameserverFn() func() string {
	nameservers := make([]string, 0, len(d.nameservers)+2)
	nameservers = append(nameservers, d.nameservers...)
	nameservers = append(nameservers, defaultNameservers...)

	return func() string {
		if len(nameservers) == 0 {
			return ""
		}

		ns := nameservers[0]
		nameservers = nameservers[1:]
		return ns
	}
}

func getEnvOptionalString(name, defaultValue string) string {
	val := os.Getenv(name)
	if val == "" {
		return defaultValue
	}
	return val
}
