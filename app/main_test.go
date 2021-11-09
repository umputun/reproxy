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
	"syscall"
	"testing"
	"time"

	log "github.com/go-pkgz/lgr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/reproxy/lib"
)

func Test_Main(t *testing.T) {

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
		require.NoError(t, e)
	}()

	finished := make(chan struct{})
	go func() {
		main()
		close(finished)
	}()

	// defer cleanup because require check below can fail
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
		assert.NoError(t, err)
		assert.Equal(t, "pong", string(body))
	}
	{
		client := http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/svc1", port))
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, 200, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		assert.NoError(t, err)
		assert.Contains(t, string(body), `"Host": "httpbin.org"`)
	}
	{
		client := http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/svc2/test", port))
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, 200, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		assert.NoError(t, err)
		assert.Contains(t, string(body), `echo echo 123`)
	}
	{
		client := http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/bad", port))
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		assert.NoError(t, err)
		assert.Equal(t, "oh my! 502 - Bad Gateway", string(body))
	}
}

func Test_MainWithSSL(t *testing.T) {
	port := 40000 + int(rand.Int31n(10000))
	os.Args = []string{"test", "--static.enabled",
		"--static.rule=*,/svc1, https://httpbin.org/get,https://feedmaster.umputun.com/ping",
		"--static.rule=*,/svc2/(.*), https://echo.umputun.com/$1,https://feedmaster.umputun.com/ping",
		"--file.enabled", "--file.name=discovery/provider/testdata/config.yml",
		"--dbg", "--logger.enabled", "--logger.stdout", "--logger.file=/tmp/reproxy.log",
		"--listen=127.0.0.1:" + strconv.Itoa(port), "--signature", "--ssl.type=static",
		"--ssl.cert=proxy/testdata/localhost.crt", "--ssl.key=proxy/testdata/localhost.key"}
	defer os.Remove("/tmp/reproxy.log")
	done := make(chan struct{})
	go func() {
		<-done
		e := syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
		require.NoError(t, e)
	}()

	finished := make(chan struct{})
	go func() {
		main()
		close(finished)
	}()

	// defer cleanup because require check below can fail
	defer func() {
		close(done)
		<-finished
	}()

	waitForHTTPServerStart(port)
	time.Sleep(time.Second)

	client := http.Client{
		// allow self-signed certificate
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
		assert.NoError(t, err)
		assert.Equal(t, "pong", string(body))
	}

	{
		resp, err := client.Get(fmt.Sprintf("https://127.0.0.1:%d/svc1", port))
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, 200, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		assert.NoError(t, err)
		assert.Contains(t, string(body), `"Host": "httpbin.org"`)
	}
}

func Test_MainWithPlugin(t *testing.T) {
	rand.Seed(time.Now().UnixNano())
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
		require.NoError(t, e)
	}()

	finished := make(chan struct{})
	go func() {
		main()
		close(finished)
	}()

	// defer cleanup because require check below can fail
	defer func() {
		close(done)
		<-finished
	}()

	waitForHTTPServerStart(proxyPort)

	pluginPort := rand.Intn(10000) + 40000
	plugin := lib.Plugin{Name: "TestPlugin", Address: "127.0.0.1:" + strconv.Itoa(pluginPort), Methods: []string{"HeaderThing", "ErrorThing"}}
	go func() {
		if err := plugin.Do(context.Background(), fmt.Sprintf("http://127.0.0.1:%d", conductorPort), &TestPlugin{}); err != nil {
			require.NotEqual(t, "proxy server closed, http: Server closed", err.Error())
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
		assert.NoError(t, err)
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
			assert.NoError(t, os.Unsetenv("REPROXY_IN_DOCKER"))
			if tt.env != "" {
				assert.NoError(t, os.Setenv("REPROXY_IN_DOCKER", tt.env))
			}
			assert.Equal(t, tt.res, listenAddress(tt.addr, tt.sslType))
		})
	}

}

func Test_redirHTTPPort(t *testing.T) {
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
			assert.NoError(t, os.Unsetenv("REPROXY_IN_DOCKER"))
			if tt.env != "" {
				assert.NoError(t, os.Setenv("REPROXY_IN_DOCKER", tt.env))
			}
			assert.Equal(t, tt.res, redirHTTPPort(tt.port))
		})
	}
}

func Test_sizeParse(t *testing.T) {

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
	// wait for up to 10 seconds for server to start before returning it
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

//nolint
func (h *TestPlugin) HeaderThing(req *lib.Request, res *lib.Response) (err error) {
	log.Printf("req: %+v", req)
	res.HeadersIn = http.Header{}
	res.HeadersIn.Add("inh", "val")
	res.HeadersOut = req.Header
	res.HeadersOut.Add("key1", "val1")
	res.StatusCode = 200
	return nil
}

//nolint
func (h *TestPlugin) ErrorThing(req lib.Request, res *lib.Response) (err error) {
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
	assert.Equal(t, 3, len(res))
	assert.Equal(t, []string{"test:$2y$05$zMxDmK65SjcH2vJQNopVSO/nE8ngVLx65RoETyHpez7yTS/8CLEiW", "test2:$2y$05$TLQqHh6VT4JxysdKGPOlJeSkkMsv.Ku/G45i7ssIm80XuouCrES12", "bad bad"}, res)
}
