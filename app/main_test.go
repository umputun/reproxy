package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	log "github.com/go-pkgz/lgr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/reproxy/app/proxy"
	"github.com/umputun/reproxy/lib"
)

var setupLoggerOnce sync.Once

func setupLogger() {
	setupLoggerOnce.Do(func() {
		log.Setup(log.Debug, log.CallerFile, log.CallerFunc, log.Msec, log.LevelBraces)
	})
}

func Test_Main(t *testing.T) {
	setupLogger()

	port := 40000 + int(rand.Int31n(10000))
	os.Args = []string{"test", "--static.enabled",
		"--static.rule=*,/svc1, https://httpbin.org/get,https://feedmaster.umputun.com/ping",
		"--static.rule=*,/svc2/(.*), https://echo.umputun.com/$1,https://feedmaster.umputun.com/ping",
		"--file.enabled", "--file.name=discovery/provider/testdata/config.yml",
		"--dbg", "--logger.enabled", "--logger.stdout", "--logger.file=/tmp/reproxy.log",
		"--listen=127.0.0.1:" + strconv.Itoa(port), "--signature", "--mgmt.enabled",
		"--error.enabled", "--error.template=proxy/testdata/errtmpl.html",
	}
	defer os.Remove("/tmp/reproxy.log")
	done := make(chan struct{})
	go func() {
		<-done
		e := syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		assert.NoError(t, e)
	}()

	finished := make(chan struct{})
	go func() {
		main()
		close(finished)
	}()

	defer func() {
		close(done)
		<-finished
	}()

	waitForHTTPServerStart(port)
	time.Sleep(time.Second)

	{
		resp, err := http.Get(fmt.Sprintf("http://localhost:%d/ping", port))
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, 200, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "pong", string(body))
	}
	{
		client := http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/svc1", port))
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, 200, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), `"Host": "httpbin.org"`)
	}
	{
		client := http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/svc2/test", port))
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, 200, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), `echo echo 123`)
	}
	{
		client := http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/bad", port))
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "oh my! 502 - Bad Gateway", string(body))
	}
}

func Test_MainWithSSL(t *testing.T) {
	setupLogger()

	port := 40000 + int(rand.Int31n(10000))
	httpPort := 50000 + int(rand.Int31n(10000)) // use a high port for HTTP redirect
	os.Args = []string{"test", "--static.enabled",
		"--static.rule=*,/svc1, https://httpbin.org/get,https://feedmaster.umputun.com/ping",
		"--static.rule=*,/svc2/(.*), https://echo.umputun.com/$1,https://feedmaster.umputun.com/ping",
		"--file.enabled", "--file.name=discovery/provider/testdata/config.yml",
		"--dbg", "--logger.enabled", "--logger.stdout", "--logger.file=/tmp/reproxy.log",
		"--listen=127.0.0.1:" + strconv.Itoa(port), "--signature", "--ssl.type=static",
		"--ssl.cert=proxy/testdata/localhost.crt", "--ssl.key=proxy/testdata/localhost.key",
		"--ssl.http-port=" + strconv.Itoa(httpPort)}
	defer os.Remove("/tmp/reproxy.log")
	done := make(chan struct{})
	go func() {
		<-done
		e := syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		assert.NoError(t, e)
	}()

	finished := make(chan struct{})
	go func() {
		main()
		close(finished)
	}()

	defer func() {
		close(done)
		<-finished
	}()

	waitForHTTPServerStart(port)
	time.Sleep(time.Second)

	client := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		Timeout: 10 * time.Second,
	}
	{
		resp, err := client.Get(fmt.Sprintf("https://127.0.0.1:%d/ping", port))
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, 200, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "pong", string(body))
	}

	{
		resp, err := client.Get(fmt.Sprintf("https://127.0.0.1:%d/svc1", port))
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, 200, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), `"Host": "httpbin.org"`)
	}
}

func Test_MainWithPlugin(t *testing.T) {
	setupLogger()

	proxyPort := rand.Intn(10000) + 40000
	conductorPort := rand.Intn(10000) + 40000
	os.Args = []string{"test", "--static.enabled",
		"--static.rule=*,/svc1, https://httpbin.org/get,https://feedmaster.umputun.com/ping",
		"--static.rule=*,/svc2/(.*), https://echo.umputun.com/$1,https://feedmaster.umputun.com/ping",
		"--file.enabled", "--file.name=discovery/provider/testdata/config.yml",
		"--dbg", "--logger.enabled", "--logger.stdout", "--logger.file=/tmp/reproxy.log",
		"--listen=127.0.0.1:" + strconv.Itoa(proxyPort), "--signature", "--error.enabled",
		"--header=hh1:vv1",
		"--plugin.enabled", "--plugin.listen=127.0.0.1:" + strconv.Itoa(conductorPort),
	}
	defer os.Remove("/tmp/reproxy.log")
	done := make(chan struct{})
	go func() {
		<-done
		e := syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		assert.NoError(t, e)
	}()

	finished := make(chan struct{})
	go func() {
		main()
		close(finished)
	}()

	defer func() {
		close(done)
		<-finished
	}()

	waitForHTTPServerStart(proxyPort)

	pluginPort := rand.Intn(10000) + 40000
	plugin := lib.Plugin{Name: "TestPlugin", Address: "127.0.0.1:" + strconv.Itoa(pluginPort), Methods: []string{"HeaderThing", "ErrorThing"}}
	go func() {
		if err := plugin.Do(context.Background(), fmt.Sprintf("http://127.0.0.1:%d", conductorPort), &TestPlugin{}); err != nil {
			assert.NotEqual(t, "proxy server closed, http: Server closed", err.Error())
		}
	}()

	time.Sleep(time.Second)

	client := http.Client{Timeout: 10 * time.Second}
	{
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/svc1", proxyPort))
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, 200, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		t.Logf("body: %s", string(body))
		assert.Contains(t, string(body), `"Host": "httpbin.org"`)
		assert.Contains(t, string(body), `"Inh": "val"`)
		assert.Equal(t, "val1", resp.Header.Get("key1"))
		assert.Equal(t, "val2", resp.Header.Get("key2"))
	}
	{
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/fail", proxyPort))
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, 500, resp.StatusCode)
	}
}

func Test_listenAddress(t *testing.T) {
	setupLogger()

	tbl := []struct {
		addr    string
		sslType string
		env     string
		res     string
	}{
		{"", "none", "1", "0.0.0.0:8080"},
		{"", "none", "0", "127.0.0.1:80"},
		{"", "auto", "false", "127.0.0.1:443"},
		{"", "auto", "true", "0.0.0.0:8443"},
		{"127.0.0.1:8081", "none", "true", "127.0.0.1:8081"},
		{"192.168.1.1:8081", "none", "false", "192.168.1.1:8081"},
		{"127.0.0.1:8080", "none", "0", "127.0.0.1:8080"},
		{"127.0.0.1:8443", "auto", "true", "127.0.0.1:8443"},
	}

	defer os.Unsetenv("REPROXY_IN_DOCKER")

	for i, tt := range tbl {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			require.NoError(t, os.Unsetenv("REPROXY_IN_DOCKER"))
			if tt.env != "" {
				require.NoError(t, os.Setenv("REPROXY_IN_DOCKER", tt.env))
			}
			assert.Equal(t, tt.res, listenAddress(tt.addr, tt.sslType))
		})
	}

}

func Test_redirHTTPPort(t *testing.T) {
	setupLogger()

	tbl := []struct {
		port int
		env  string
		res  int
	}{
		{0, "1", 8080},
		{0, "0", 80},
		{0, "true", 8080},
		{0, "false", 80},
		{1234, "true", 1234},
		{1234, "false", 1234},
	}

	defer os.Unsetenv("REPROXY_IN_DOCKER")

	for i, tt := range tbl {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			require.NoError(t, os.Unsetenv("REPROXY_IN_DOCKER"))
			if tt.env != "" {
				require.NoError(t, os.Setenv("REPROXY_IN_DOCKER", tt.env))
			}
			assert.Equal(t, tt.res, redirHTTPPort(tt.port))
		})
	}
}

func Test_sizeParse(t *testing.T) {
	setupLogger()

	tbl := []struct {
		inp string
		res uint64
		err bool
	}{
		{"1000", 1000, false},
		{"0", 0, false},
		{"", 0, true},
		{"10K", 10240, false},
		{"1k", 1024, false},
		{"14m", 14 * 1024 * 1024, false},
		{"7G", 7 * 1024 * 1024 * 1024, false},
		{"170g", 170 * 1024 * 1024 * 1024, false},
		{"17T", 17 * 1024 * 1024 * 1024 * 1024, false},
		{"123aT", 0, true},
		{"123a", 0, true},
		{"123.45", 0, true},
	}

	for i, tt := range tbl {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			res, err := sizeParse(tt.inp)
			if tt.err {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.res, res)
		})
	}
}

func waitForHTTPServerStart(port int) {
	client := http.Client{Timeout: time.Second}
	for i := 0; i < 100; i++ {
		time.Sleep(time.Millisecond * 100)
		if resp, err := client.Get(fmt.Sprintf("http://localhost:%d/ping", port)); err == nil {
			_ = resp.Body.Close()
			return
		}
	}
}

type TestPlugin struct{}

func (h *TestPlugin) HeaderThing(req *lib.Request, res *lib.Response) error { //nolint:unparam // doesn't fail in tests
	log.Printf("req: %+v", req)
	res.HeadersIn = http.Header{}
	res.HeadersIn.Add("inh", "val")
	res.HeadersOut = req.Header
	res.HeadersOut.Add("key1", "val1")
	res.StatusCode = 200
	return nil
}

func (h *TestPlugin) ErrorThing(req lib.Request, res *lib.Response) error { //nolint:unparam // doesn't fail in tests
	log.Printf("req: %+v", req)
	if req.URL == "/fail" {
		res.StatusCode = 500
		return nil
	}
	res.HeadersOut = req.Header
	res.HeadersOut.Add("key2", "val2")
	res.StatusCode = 200
	return nil
}

func Test_splitAtCommas(t *testing.T) {
	setupLogger()

	tbl := []struct {
		inp string
		res []string
	}{
		{"a string", []string{"a string"}},
		{"vv1, vv2, vv3", []string{"vv1", "vv2", "vv3"}},
		{`"vv1, blah", vv2, vv3`, []string{"vv1, blah", "vv2", "vv3"}},
		{
			`Access-Control-Allow-Headers:"DNT,X-CustomHeader,Keep-Alive,User-Agent,X-Requested-With,If-Modified-Since,Cache-Control,Content-Type",header123:val, foo:"bar1,bar2"`,
			[]string{"Access-Control-Allow-Headers:\"DNT,X-CustomHeader,Keep-Alive,User-Agent,X-Requested-With,If-Modified-Since,Cache-Control,Content-Type\"", "header123:val", "foo:\"bar1,bar2\""},
		},
		{"", []string{}},
	}

	for i, tt := range tbl {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			assert.Equal(t, tt.res, splitAtCommas(tt.inp))
		})
	}

}

func Test_makeBasicAuth(t *testing.T) {
	setupLogger()

	pf := `test:$2y$05$zMxDmK65SjcH2vJQNopVSO/nE8ngVLx65RoETyHpez7yTS/8CLEiW
		test2:$2y$05$TLQqHh6VT4JxysdKGPOlJeSkkMsv.Ku/G45i7ssIm80XuouCrES12
		bad bad`

	fh, err := os.CreateTemp(os.TempDir(), "reproxy_auth_*")
	require.NoError(t, err)
	defer fh.Close()

	n, err := fh.WriteString(pf)
	require.NoError(t, err)
	require.Equal(t, len(pf), n)

	res, err := makeBasicAuth(fh.Name())
	require.NoError(t, err)
	assert.Len(t, res, 3)
	assert.Equal(t, []string{"test:$2y$05$zMxDmK65SjcH2vJQNopVSO/nE8ngVLx65RoETyHpez7yTS/8CLEiW", "test2:$2y$05$TLQqHh6VT4JxysdKGPOlJeSkkMsv.Ku/G45i7ssIm80XuouCrES12", "bad bad"}, res)
}

func Test_makeSSLConfig(t *testing.T) {
	setupLogger()

	t.Run("ssl type none", func(t *testing.T) {
		opts.SSL.Type = "none"
		cfg, err := makeSSLConfig()
		require.NoError(t, err)
		assert.Equal(t, proxy.SSLNone, cfg.SSLMode)
	})

	t.Run("ssl type static without cert", func(t *testing.T) {
		opts.SSL.Type = "static"
		opts.SSL.Cert = ""
		opts.SSL.Key = "some.key"
		_, err := makeSSLConfig()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "path to cert.pem is required")
	})

	t.Run("ssl type static without key", func(t *testing.T) {
		opts.SSL.Type = "static"
		opts.SSL.Cert = "some.crt"
		opts.SSL.Key = ""
		_, err := makeSSLConfig()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "path to key.pem is required")
	})

	t.Run("ssl type static valid", func(t *testing.T) {
		opts.SSL.Type = "static"
		opts.SSL.Cert = "proxy/testdata/localhost.crt"
		opts.SSL.Key = "proxy/testdata/localhost.key"
		opts.SSL.RedirHTTPPort = 8080
		cfg, err := makeSSLConfig()
		require.NoError(t, err)
		assert.Equal(t, proxy.SSLStatic, cfg.SSLMode)
		assert.Equal(t, "proxy/testdata/localhost.crt", cfg.Cert)
		assert.Equal(t, "proxy/testdata/localhost.key", cfg.Key)
		assert.Equal(t, 8080, cfg.RedirHTTPPort)
	})

	t.Run("ssl type auto", func(t *testing.T) {
		opts.SSL.Type = "auto"
		opts.SSL.ACMEDirectory = "https://acme.example.com"
		opts.SSL.ACMELocation = "/var/acme"
		opts.SSL.ACMEEmail = "admin@example.com"
		opts.SSL.FQDNs = []string{"example.com", "www.example.com"}
		opts.SSL.DNS.Type = "none"
		cfg, err := makeSSLConfig()
		require.NoError(t, err)
		assert.Equal(t, proxy.SSLAuto, cfg.SSLMode)
		assert.Equal(t, "https://acme.example.com", cfg.ACMEDirectory)
		assert.Equal(t, "/var/acme", cfg.ACMELocation)
		assert.Equal(t, "admin@example.com", cfg.ACMEEmail)
		assert.Equal(t, []string{"example.com", "www.example.com"}, cfg.FQDNs)
		assert.Nil(t, cfg.DNSProvider)
	})

	t.Run("ssl type auto with cloudflare dns", func(t *testing.T) {
		opts.SSL.Type = "auto"
		opts.SSL.DNS.Type = "cloudflare"
		opts.SSL.DNS.Cloudflare.APIToken = "test-token"
		cfg, err := makeSSLConfig()
		require.NoError(t, err)
		assert.NotNil(t, cfg.DNSProvider)
	})

	t.Run("ssl type auto with route53 dns", func(t *testing.T) {
		opts.SSL.Type = "auto"
		opts.SSL.DNS.Type = "route53"
		opts.SSL.DNS.Route53.Region = "us-east-1"
		cfg, err := makeSSLConfig()
		require.NoError(t, err)
		assert.NotNil(t, cfg.DNSProvider)
	})

	t.Run("ssl type auto with gandi dns", func(t *testing.T) {
		opts.SSL.Type = "auto"
		opts.SSL.DNS.Type = "gandi"
		opts.SSL.DNS.Gandi.BearerToken = "test-token"
		cfg, err := makeSSLConfig()
		require.NoError(t, err)
		assert.NotNil(t, cfg.DNSProvider)
	})

	t.Run("ssl type auto with digitalocean dns", func(t *testing.T) {
		opts.SSL.Type = "auto"
		opts.SSL.DNS.Type = "digitalocean"
		opts.SSL.DNS.DigitalOcean.APIToken = "test-token"
		cfg, err := makeSSLConfig()
		require.NoError(t, err)
		assert.NotNil(t, cfg.DNSProvider)
	})

	t.Run("ssl type auto with hetzner dns", func(t *testing.T) {
		opts.SSL.Type = "auto"
		opts.SSL.DNS.Type = "hetzner"
		opts.SSL.DNS.Hetzner.APIToken = "test-token"
		cfg, err := makeSSLConfig()
		require.NoError(t, err)
		assert.NotNil(t, cfg.DNSProvider)
	})

	t.Run("ssl type auto with linode dns", func(t *testing.T) {
		opts.SSL.Type = "auto"
		opts.SSL.DNS.Type = "linode"
		opts.SSL.DNS.Linode.APIToken = "test-token"
		cfg, err := makeSSLConfig()
		require.NoError(t, err)
		assert.NotNil(t, cfg.DNSProvider)
	})

	t.Run("ssl type auto with godaddy dns", func(t *testing.T) {
		opts.SSL.Type = "auto"
		opts.SSL.DNS.Type = "godaddy"
		opts.SSL.DNS.GoDaddy.APIToken = "test-token"
		cfg, err := makeSSLConfig()
		require.NoError(t, err)
		assert.NotNil(t, cfg.DNSProvider)
	})

	t.Run("ssl type auto with namecheap dns", func(t *testing.T) {
		opts.SSL.Type = "auto"
		opts.SSL.DNS.Type = "namecheap"
		opts.SSL.DNS.Namecheap.APIKey = "test-key"
		opts.SSL.DNS.Namecheap.User = "test-user"
		cfg, err := makeSSLConfig()
		require.NoError(t, err)
		assert.NotNil(t, cfg.DNSProvider)
	})

	t.Run("ssl type auto with scaleway dns", func(t *testing.T) {
		opts.SSL.Type = "auto"
		opts.SSL.DNS.Type = "scaleway"
		opts.SSL.DNS.Scaleway.SecretKey = "test-key"
		opts.SSL.DNS.Scaleway.OrganizationID = "test-org"
		cfg, err := makeSSLConfig()
		require.NoError(t, err)
		assert.NotNil(t, cfg.DNSProvider)
	})

	t.Run("ssl type auto with porkbun dns", func(t *testing.T) {
		opts.SSL.Type = "auto"
		opts.SSL.DNS.Type = "porkbun"
		opts.SSL.DNS.Porkbun.APIKey = "test-key"
		opts.SSL.DNS.Porkbun.APISecretKey = "test-secret"
		cfg, err := makeSSLConfig()
		require.NoError(t, err)
		assert.NotNil(t, cfg.DNSProvider)
	})

	t.Run("ssl type auto with dnsimple dns", func(t *testing.T) {
		opts.SSL.Type = "auto"
		opts.SSL.DNS.Type = "dnsimple"
		opts.SSL.DNS.DNSimple.APIAccessToken = "test-token"
		opts.SSL.DNS.DNSimple.AccountID = "test-account"
		cfg, err := makeSSLConfig()
		require.NoError(t, err)
		assert.NotNil(t, cfg.DNSProvider)
	})

	t.Run("ssl type auto with duckdns dns", func(t *testing.T) {
		opts.SSL.Type = "auto"
		opts.SSL.DNS.Type = "duckdns"
		opts.SSL.DNS.DuckDNS.APIToken = "test-token"
		cfg, err := makeSSLConfig()
		require.NoError(t, err)
		assert.NotNil(t, cfg.DNSProvider)
	})

	t.Run("ssl type invalid", func(t *testing.T) {
		opts.SSL.Type = "invalid"
		_, err := makeSSLConfig()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid value")
	})

	// reset to default
	opts.SSL.Type = "none"
	opts.SSL.DNS.Type = "none"
}

func Test_makeLBSelector(t *testing.T) {
	setupLogger()

	t.Run("random selector", func(t *testing.T) {
		opts.LBType = "random"
		sel := makeLBSelector()
		assert.IsType(t, &proxy.RandomSelector{}, sel)
	})

	t.Run("failover selector", func(t *testing.T) {
		opts.LBType = "failover"
		sel := makeLBSelector()
		assert.IsType(t, &proxy.FailoverSelector{}, sel)
	})

	t.Run("roundrobin selector", func(t *testing.T) {
		opts.LBType = "roundrobin"
		sel := makeLBSelector()
		assert.IsType(t, &proxy.RoundRobinSelector{}, sel)
	})

	t.Run("default selector", func(t *testing.T) {
		opts.LBType = "unknown"
		sel := makeLBSelector()
		assert.IsType(t, &proxy.FailoverSelector{}, sel)
	})

	// reset
	opts.LBType = "random"
}

func Test_fqdns(t *testing.T) {
	setupLogger()

	tbl := []struct {
		inp []string
		res []string
	}{
		{[]string{"example.com"}, []string{"example.com"}},
		{[]string{" example.com "}, []string{"example.com"}},
		{[]string{"  example.com  ", " www.example.com\t"}, []string{"example.com", "www.example.com"}},
		{[]string{}, nil},
		{nil, nil},
	}

	for i, tt := range tbl {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			result := fqdns(tt.inp)
			assert.Equal(t, tt.res, result)
		})
	}
}
