package proxy

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	log "github.com/go-pkgz/lgr"
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

	// acquire new cert from CA and check it
	cert, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "example.com"})
	require.NoError(t, err)
	assert.NotNil(t, cert)
}
