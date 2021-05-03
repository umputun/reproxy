package main

import (
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"os"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_Main(t *testing.T) {

	port := chooseRandomUnusedPort()
	os.Args = []string{"test", "--static.enabled",
		"--static.rule=*,/svc1, https://httpbin.org/get,https://feedmaster.umputun.com/ping",
		"--static.rule=*,/svc2/(.*), https://echo.umputun.com/$1,https://feedmaster.umputun.com/ping",
		"--file.enabled", "--file.name=discovery/provider/testdata/config.yml",
		"--dbg", "--logger.enabled", "--logger.stdout", "--logger.file=/tmp/reproxy.log",
		"--listen=127.0.0.1:" + strconv.Itoa(port), "--signature",
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
		body, err := ioutil.ReadAll(resp.Body)
		assert.NoError(t, err)
		assert.Equal(t, "pong", string(body))
	}
	{
		client := http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/svc1", port))
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, 200, resp.StatusCode)
		body, err := ioutil.ReadAll(resp.Body)
		assert.NoError(t, err)
		assert.Contains(t, string(body), `"Host": "httpbin.org"`)
	}
	{
		client := http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/svc2/test", port))
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, 200, resp.StatusCode)
		body, err := ioutil.ReadAll(resp.Body)
		assert.NoError(t, err)
		assert.Contains(t, string(body), `echo echo 123`)
	}
	{
		client := http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/bad", port))
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
		body, err := ioutil.ReadAll(resp.Body)
		assert.NoError(t, err)
		assert.Equal(t, "oh my! 502 - Bad Gateway", string(body))
	}
}

func Test_MainWithSSL(t *testing.T) {
	port := chooseRandomUnusedPort()
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
		body, err := ioutil.ReadAll(resp.Body)
		assert.NoError(t, err)
		assert.Equal(t, "pong", string(body))
	}

	{
		resp, err := client.Get(fmt.Sprintf("https://127.0.0.1:%d/svc1", port))
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, 200, resp.StatusCode)
		body, err := ioutil.ReadAll(resp.Body)
		assert.NoError(t, err)
		assert.Contains(t, string(body), `"Host": "httpbin.org"`)
	}
}

func chooseRandomUnusedPort() (port int) {
	for i := 0; i < 10; i++ {
		port = 40000 + int(rand.Int31n(10000))
		if ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port)); err == nil {
			_ = ln.Close()
			break
		}
	}
	return port
}

func waitForHTTPServerStart(port int) {
	// wait for up to 10 seconds for server to start before returning it
	client := http.Client{Timeout: time.Second}
	for i := 0; i < 100; i++ {
		time.Sleep(time.Millisecond * 100)
		if resp, err := client.Get(fmt.Sprintf("http://localhost:%d", port)); err == nil {
			_ = resp.Body.Close()
			return
		}
	}
}

func Test_listenAddress(t *testing.T) {

	tbl := []struct {
		addr string
		env  string
		res  string
	}{
		{"", "1", "0.0.0.0:8080"},
		{"", "0", "127.0.0.1:8080"},
		{"127.0.0.1:8081", "true", "127.0.0.1:8081"},
		{"192.168.1.1:8081", "false", "192.168.1.1:8081"},
		{"127.0.0.1:8080", "0", "127.0.0.1:8080"},
		{"127.0.0.1:8080", "", "127.0.0.1:8080"},
	}

	defer os.Unsetenv("REPROXY_IN_DOCKER")

	for i, tt := range tbl {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			assert.NoError(t, os.Unsetenv("REPROXY_IN_DOCKER"))
			if tt.env != "" {
				assert.NoError(t, os.Setenv("REPROXY_IN_DOCKER", tt.env))
			}
			assert.Equal(t, tt.res, listenAddress(tt.addr))
		})
	}

}
