package proxy

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	log "github.com/go-pkgz/lgr"
	"github.com/libdns/libdns"
	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/umputun/reproxy/app/proxy/acmetest"
)

func TestSSL_Redirect(t *testing.T) {
	p := Http{}

	ts := httptest.NewServer(p.httpToHTTPSRouter())
	defer ts.Close()

	client := http.Client{
		// prevent http redirect
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},

		// allow self-signed certificate
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint
		},
	}

	// check http to https redirect response
	resp, err := client.Get(strings.Replace(ts.URL, "127.0.0.1", "localhost", 1) + "/blah?param=1")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 307, resp.StatusCode)
	assert.Equal(t, "https://localhost:443/blah?param=1", resp.Header.Get("Location"))
}

func TestSSL_ACME_HTTPChallengeRouter(t *testing.T) {
	log.Setup(log.Debug, log.LevelBraces)

	dir, err := os.MkdirTemp("", "acme")
	require.NoError(t, err)
	log.Printf("[DEBUG] acme dir: %s", dir)
	defer os.RemoveAll(dir)

	var tsURL string
	cas := acmetest.NewACMEServer(t,
		acmetest.ModifyRequest(func(r *http.Request) {
			r.URL.Host = strings.TrimPrefix(tsURL, "http://")
		}),
	)

	p := Http{
		SSLConfig: SSLConfig{
			ACMELocation:  dir,
			FQDNs:         []string{"example.com", "localhost"},
			ACMEDirectory: cas.URL(),
		},
	}

	m := p.makeAutocertManager()

	ts := httptest.NewServer(p.httpChallengeRouter(m))
	defer ts.Close()

	tsURL = ts.URL
	client := http.Client{
		// prevent http redirect
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	lh := strings.Replace(ts.URL, "127.0.0.1", "localhost", 1)
	// check http to https redirect response
	resp, err := client.Get(lh + "/blah?param=1")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 307, resp.StatusCode)
	assert.Equal(t, "https://localhost:443/blah?param=1", resp.Header.Get("Location"))

	// acquire new cert from CA and check it with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	certCh := make(chan struct {
		cert *tls.Certificate
		err  error
	})

	go func() {
		cert, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "example.com"})
		certCh <- struct {
			cert *tls.Certificate
			err  error
		}{cert, err}
	}()

	// wait for certificate or timeout
	select {
	case <-ctx.Done():
		t.Fatalf("Certificate acquisition timed out: %v", ctx.Err())
	case result := <-certCh:
		require.NoError(t, result.err)
		assert.NotNil(t, result.cert)
	}
}

func TestSSL_ACME_DNSChallenge(t *testing.T) {
	log.Setup(log.Debug, log.LevelBraces)

	dir, err := os.MkdirTemp("", "acme")
	require.NoError(t, err)
	log.Printf("[DEBUG] acme dir: %s", dir)
	defer os.RemoveAll(dir)

	// use mutex to protect expectedToken from race conditions
	var expectedToken string
	var tokenMutex sync.Mutex

	getToken := func() string {
		tokenMutex.Lock()
		defer tokenMutex.Unlock()
		return expectedToken
	}

	setToken := func(token string) {
		tokenMutex.Lock()
		defer tokenMutex.Unlock()
		expectedToken = token
	}

	cas := acmetest.NewACMEServer(t,
		acmetest.CheckDNS(func(domain string) (exists bool, value string, err error) {
			assert.Equal(t, "example.com", domain)
			return true, getToken(), nil
		}),
	)

	dnsListener, err := net.ListenPacket("udp", ":0")
	require.NoError(t, err)
	dnsPort := strings.TrimPrefix(dnsListener.LocalAddr().String(), "[::]:")

	dnsMock := &dns.Server{
		Addr:       "localhost:" + dnsPort,
		Net:        "udp",
		PacketConn: dnsListener,
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
			msg := &dns.Msg{}
			msg.SetReply(r)

			if len(r.Question) == 0 {
				msg.SetRcode(r, dns.RcodeNameError)
				_ = w.WriteMsg(msg)
				return
			}

			switch r.Question[0].Qtype {
			case dns.TypeSOA:
				msg.Answer = []dns.RR{&dns.SOA{
					Hdr: dns.RR_Header{
						Name:   "example.com.",
						Rrtype: dns.TypeSOA,
						Class:  dns.ClassINET,
						Ttl:    0,
					},
					Ns:   "ns1.example.com.",
					Mbox: "hostmaster.example.com.",
				}}
			case dns.TypeTXT:
				assert.Equal(t, "_acme-challenge.example.com.", r.Question[0].Name)
				msg.Answer = []dns.RR{&dns.TXT{
					Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeTXT, Class: dns.ClassINET, Ttl: 0},
					Txt: []string{getToken()},
				}}
			default:
				msg.SetRcode(r, dns.RcodeNameError)
			}

			_ = w.WriteMsg(msg)
		}),
	}

	// create a channel to ensure DNS server is ready
	dnsReady := make(chan struct{})

	go func() {
		// signal that the DNS server is ready to accept connections
		close(dnsReady)
		// set a timeout for the DNS server
		err := dnsMock.ActivateAndServe()
		if err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
			t.Logf("DNS server error: %v", err)
		}
	}()

	// wait for DNS server to be ready
	<-dnsReady

	// ensure the server is shut down at the end of the test
	defer dnsMock.Shutdown()

	t.Log("dns server started at", dnsMock.Addr)

	p := Http{
		SSLConfig: SSLConfig{
			ACMELocation:  dir,
			FQDNs:         []string{"example.com", "localhost"},
			ACMEDirectory: cas.URL(),
			DNSProvider: &dnsProviderMock{
				AppendRecordsFunc: func(ctx context.Context, zone string, recs []libdns.Record) ([]libdns.Record, error) {
					assert.Equal(t, "example.com.", zone)
					assert.Equal(t, 1, len(recs))
					assert.Equal(t, "_acme-challenge", recs[0].Name)
					assert.Equal(t, "TXT", recs[0].Type)
					assert.NotEmpty(t, recs[0].Value)
					setToken(recs[0].Value)
					return recs, nil
				},
				DeleteRecordsFunc: func(ctx context.Context, zone string, recs []libdns.Record) ([]libdns.Record, error) {
					return recs, nil
				},
			},
		},
		dnsResolvers: []string{dnsMock.Addr},
	}

	m := p.makeAutocertManager()

	// create context with timeout to prevent test from hanging
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// use a channel to handle the certificate acquisition with timeout
	certCh := make(chan struct {
		cert *tls.Certificate
		err  error
	})

	go func() {
		cert, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "example.com"})
		certCh <- struct {
			cert *tls.Certificate
			err  error
		}{cert, err}
	}()

	// wait for certificate or timeout
	select {
	case <-ctx.Done():
		t.Fatalf("Certificate acquisition timed out: %v", ctx.Err())
	case result := <-certCh:
		require.NoError(t, result.err)
		assert.NotNil(t, result.cert)
	}
}
