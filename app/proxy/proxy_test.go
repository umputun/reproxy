package proxy

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/reproxy/app/discovery"
	"github.com/umputun/reproxy/app/discovery/provider"
	"github.com/umputun/reproxy/app/mgmt"
)

func TestHttp_Do(t *testing.T) {
	port, releasePort := getFreePort(t)
	h := Http{Timeouts: Timeouts{ResponseHeader: 200 * time.Millisecond}, Address: fmt.Sprintf("127.0.0.1:%d", port),
		AccessLog: io.Discard, Signature: true, ProxyHeaders: []string{"hh1:vv1", "hh2:vv2"}, StdOutEnabled: true,
		Reporter: &ErrorReporter{Nice: true}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("req: %v", r)
		w.Header().Add("h1", "v1")
		assert.Equal(t, "127.0.0.1", r.Header.Get("X-Real-IP"))
		assert.Equal(t, "127.0.0.1", r.Header.Get("X-Forwarded-For"))
		assert.Empty(t, r.Header.Get("X-Forwarded-Proto")) // ssl auto only
		assert.Empty(t, r.Header.Get("X-Forwarded-Port"))
		assert.NotEmpty(t, r.Header.Get("X-Forwarded-URL"), "X-Forwarded-URL header must be set")
		fmt.Fprintf(w, "response %s", r.URL.String())
	}))
	defer ds.Close()

	svc := discovery.NewService([]discovery.Provider{
		&provider.Static{Rules: []string{
			"localhost,^/api/(.*)," + ds.URL + "/123/$1,",
			"127.0.0.1,^/api/(.*)," + ds.URL + "/567/$1,",
		},
		}}, time.Millisecond*10)

	go func() {
		_ = svc.Run(t.Context())
	}()

	time.Sleep(50 * time.Millisecond)
	h.Matcher = svc
	h.Metrics = mgmt.NewMetrics(mgmt.MetricsConfig{})

	releasePort()
	go func() {
		_ = h.Run(ctx)
	}()

	client := http.Client{}

	// wait for server to be ready
	require.Eventually(t, func() bool {
		resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/ping")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 10*time.Millisecond, "server failed to start")

	t.Run("to 127.0.0.1, good", func(t *testing.T) {
		req, err := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(port)+"/api/something?xxx=yyy", http.NoBody)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		t.Logf("%+v", resp.Header)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "response /567/something?xxx=yyy", string(body))
		assert.Equal(t, "reproxy", resp.Header.Get("App-Name"))
		assert.Equal(t, "v1", resp.Header.Get("h1"))
		assert.Equal(t, "vv1", resp.Header.Get("hh1"))
		assert.Equal(t, "vv2", resp.Header.Get("hh2"))
	})

	t.Run("to localhost, good", func(t *testing.T) {
		resp, err := client.Get("http://localhost:" + strconv.Itoa(port) + "/api/something")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		t.Logf("%+v", resp.Header)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "response /123/something", string(body))
		assert.Equal(t, "reproxy", resp.Header.Get("App-Name"))
		assert.Equal(t, "v1", resp.Header.Get("h1"))
	})

	t.Run("bad gateway", func(t *testing.T) {
		resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/bad/something")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
		b, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Contains(t, string(b), "Sorry for the inconvenience")
		assert.Equal(t, "text/html; charset=utf-8", resp.Header.Get("Content-Type"))
	})

	t.Run("url encode", func(t *testing.T) {
		resp, err := client.Get("http://localhost:" + strconv.Itoa(port) + "/api/test%20%25%20and%20&,%20and%20other%20characters%20@%28%29%5E%21")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		t.Logf("%+v", resp.Header)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "response /123/test%20%25%20and%20&,%20and%20other%20characters%20@%28%29%5E%21", string(body))
		assert.Equal(t, "reproxy", resp.Header.Get("App-Name"))
		assert.Equal(t, "v1", resp.Header.Get("h1"))
	})

	t.Run("unparsable query passed as is", func(t *testing.T) {
		resp, err := client.Get("http://localhost:" + strconv.Itoa(port) + "/api/something?a=1;b=2&c=%zz")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "response /123/something?a=1;b=2&c=%zz", string(body))
	})
}

func TestHttp_DoWithSSL(t *testing.T) {
	port, releasePort := getFreePort(t)
	h := Http{Timeouts: Timeouts{ResponseHeader: 200 * time.Millisecond}, Address: fmt.Sprintf("localhost:%d", port),
		AccessLog: io.Discard, Signature: true, ProxyHeaders: []string{"hh1:vv1", "hh2:vv2"}, StdOutEnabled: true,
		Reporter:  &ErrorReporter{Nice: true},
		SSLConfig: SSLConfig{SSLMode: SSLStatic, Cert: "testdata/localhost.crt", Key: "testdata/localhost.key"}, Insecure: true,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ds := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("req: %v", r)
		w.Header().Add("h1", "v1")
		assert.Equal(t, "127.0.0.1", r.Header.Get("X-Real-IP"))
		assert.Equal(t, "127.0.0.1", r.Header.Get("X-Forwarded-For"))
		assert.Equal(t, "https", r.Header.Get("X-Forwarded-Proto")) // ssl auto only
		assert.Equal(t, "443", r.Header.Get("X-Forwarded-Port"))
		fmt.Fprintf(w, "response %s", r.URL.String())
	}))

	svc := discovery.NewService([]discovery.Provider{
		&provider.Static{Rules: []string{
			"localhost,^/api/(.*)," + strings.Replace(ds.URL, "127.0.0.1", "localhost", 1) + "/123/$1,",
			"127.0.0.1,^/api/(.*)," + strings.Replace(ds.URL, "127.0.0.1", "localhost", 1) + "/567/$1,",
		},
		}}, time.Millisecond*10)

	go func() {
		_ = svc.Run(t.Context())
	}()

	time.Sleep(50 * time.Millisecond)
	h.Matcher = svc
	h.Metrics = mgmt.NewMetrics(mgmt.MetricsConfig{})

	releasePort()
	go func() {
		_ = h.Run(ctx)
	}()

	client := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	// wait for server to be ready
	require.Eventually(t, func() bool {
		resp, err := client.Get("https://localhost:" + strconv.Itoa(port) + "/ping")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 10*time.Millisecond, "server failed to start")

	t.Run("to localhost, good", func(t *testing.T) {
		req, err := http.NewRequest("GET", "https://localhost:"+strconv.Itoa(port)+"/api/something", http.NoBody)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		t.Logf("%+v", resp.Header)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "response /123/something", string(body))
		assert.Equal(t, "reproxy", resp.Header.Get("App-Name"))
		assert.Equal(t, "v1", resp.Header.Get("h1"))
		assert.Equal(t, "vv1", resp.Header.Get("hh1"))
		assert.Equal(t, "vv2", resp.Header.Get("hh2"))
	})

	t.Run("to localhost, request with X-Forwarded-Proto and X-Forwarded-Port", func(t *testing.T) {
		req, err := http.NewRequest("GET", "https://localhost:"+strconv.Itoa(port)+"/api/something", http.NoBody)
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("X-Forwarded-Port", "443")
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		t.Logf("%+v", resp.Header)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "response /123/something", string(body))
		assert.Equal(t, "reproxy", resp.Header.Get("App-Name"))
		assert.Equal(t, "v1", resp.Header.Get("h1"))
		assert.Equal(t, "vv1", resp.Header.Get("hh1"))
		assert.Equal(t, "vv2", resp.Header.Get("hh2"))
	})

	t.Run("to 127.0.0.1", func(t *testing.T) {
		req, err := http.NewRequest("GET", "https://127.0.0.1:"+strconv.Itoa(port)+"/api/something", http.NoBody)
		req.Header.Set("X-Forwarded-Proto", "https")
		req.Header.Set("X-Forwarded-Port", "443")
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		t.Logf("%+v", resp.Header)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "response /567/something", string(body))
		assert.Equal(t, "reproxy", resp.Header.Get("App-Name"))
		assert.Equal(t, "v1", resp.Header.Get("h1"))
		assert.Equal(t, "vv1", resp.Header.Get("hh1"))
		assert.Equal(t, "vv2", resp.Header.Get("hh2"))
	})

	t.Run("forwarded headers nominated in Connection", func(t *testing.T) {
		req, err := http.NewRequest("GET", "https://localhost:"+strconv.Itoa(port)+"/api/something", http.NoBody)
		require.NoError(t, err)
		req.Header.Set("Connection", "X-Real-IP, X-Forwarded-Proto, X-Forwarded-Port")
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

func TestHttp_DoWithAssets(t *testing.T) {
	port, releasePort := getFreePort(t)
	cc := NewCacheControl(time.Hour * 12)
	h := Http{Timeouts: Timeouts{ResponseHeader: 200 * time.Millisecond}, Address: fmt.Sprintf("127.0.0.1:%d", port),
		AccessLog: io.Discard, AssetsWebRoot: "/static", AssetsLocation: "testdata", CacheControl: cc, Reporter: &ErrorReporter{Nice: false}}
	ctx, cancel := context.WithTimeout(context.Background(), 1000*time.Millisecond)
	defer cancel()

	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("req: %v", r)
		w.Header().Add("h1", "v1")
		assert.Equal(t, "127.0.0.1", r.Header.Get("X-Real-IP"))
		fmt.Fprintf(w, "response %s", r.URL.String())
	}))
	defer ds.Close()

	svc := discovery.NewService([]discovery.Provider{
		&provider.Static{Rules: []string{
			"localhost,^/api/(.*)," + ds.URL + "/123/$1,",
			"127.0.0.1,^/api/(.*)," + ds.URL + "/567/$1,",
		},
		}}, time.Millisecond*10)

	go func() {
		_ = svc.Run(t.Context())
	}()
	time.Sleep(50 * time.Millisecond)
	h.Matcher = svc
	h.Metrics = mgmt.NewMetrics(mgmt.MetricsConfig{})

	releasePort()
	go func() {
		_ = h.Run(ctx)
	}()
	waitForServer(t, fmt.Sprintf("127.0.0.1:%d", port))

	client := http.Client{}

	t.Run("api call", func(t *testing.T) {
		req, err := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(port)+"/api/something", http.NoBody)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		t.Logf("%+v", resp.Header)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "response /567/something", string(body))
		assert.Empty(t, resp.Header.Get("App-Method"))
		assert.Equal(t, "v1", resp.Header.Get("h1"))
	})

	t.Run("static call, good", func(t *testing.T) {
		resp, err := client.Get("http://localhost:" + strconv.Itoa(port) + "/static/1.html")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		t.Logf("%+v", resp.Header)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "test html", string(body))
		assert.Empty(t, resp.Header.Get("App-Method"))
		assert.Empty(t, resp.Header.Get("h1"))
		assert.Equal(t, "public, max-age=43200", resp.Header.Get("Cache-Control"))
	})

	t.Run("static call, bad", func(t *testing.T) {
		resp, err := client.Get("http://localhost:" + strconv.Itoa(port) + "/static/bad.html")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "404 page not found\n", string(body))
	})

	t.Run("bad url", func(t *testing.T) {
		resp, err := client.Get("http://localhost:" + strconv.Itoa(port) + "/svcbad")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), "Server error")
		assert.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))
	})
}

func TestHttp_DoWithAssetsCustom404(t *testing.T) {
	port, releasePort := getFreePort(t)
	cc := NewCacheControl(time.Hour * 12)
	h := Http{Timeouts: Timeouts{ResponseHeader: 200 * time.Millisecond}, Address: fmt.Sprintf("127.0.0.1:%d", port),
		AccessLog: io.Discard, AssetsWebRoot: "/static", AssetsLocation: "testdata", Assets404: "404.html",
		CacheControl: cc, Reporter: &ErrorReporter{Nice: false}}
	ctx, cancel := context.WithTimeout(context.Background(), 1000*time.Millisecond)
	defer cancel()

	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("req: %v", r)
		w.Header().Add("h1", "v1")
		assert.Equal(t, "127.0.0.1", r.Header.Get("X-Real-IP"))
		fmt.Fprintf(w, "response %s", r.URL.String())
	}))
	defer ds.Close()

	svc := discovery.NewService([]discovery.Provider{
		&provider.Static{Rules: []string{
			"localhost,^/api/(.*)," + ds.URL + "/123/$1,",
			"127.0.0.1,^/api/(.*)," + ds.URL + "/567/$1,",
		},
		}}, time.Millisecond*10)

	go func() {
		_ = svc.Run(t.Context())
	}()
	time.Sleep(50 * time.Millisecond)
	h.Matcher = svc
	h.Metrics = mgmt.NewMetrics(mgmt.MetricsConfig{})

	releasePort()
	go func() {
		_ = h.Run(ctx)
	}()
	waitForServer(t, fmt.Sprintf("127.0.0.1:%d", port))

	client := http.Client{}

	t.Run("api call, found", func(t *testing.T) {
		req, err := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(port)+"/api/something", http.NoBody)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		t.Logf("%+v", resp.Header)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "response /567/something", string(body))
		assert.Empty(t, resp.Header.Get("App-Method"))
		assert.Equal(t, "v1", resp.Header.Get("h1"))
	})

	t.Run("static call, found", func(t *testing.T) {
		resp, err := client.Get("http://localhost:" + strconv.Itoa(port) + "/static/1.html")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		t.Logf("%+v", resp.Header)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "test html", string(body))
		assert.Empty(t, resp.Header.Get("App-Method"))
		assert.Empty(t, resp.Header.Get("h1"))
		assert.Equal(t, "public, max-age=43200", resp.Header.Get("Cache-Control"))
	})

	t.Run("static call, not found", func(t *testing.T) {
		resp, err := client.Get("http://localhost:" + strconv.Itoa(port) + "/static/bad.html")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "not found! blah blah blah\nthere is no spoon", string(body))
		t.Logf("%+v", resp.Header)
		assert.Equal(t, "text/html; charset=utf-8", resp.Header.Get("Content-Type"))
	})

	t.Run("another static call, not found", func(t *testing.T) {
		resp, err := client.Get("http://localhost:" + strconv.Itoa(port) + "/static/bad2.html")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "not found! blah blah blah\nthere is no spoon", string(body))
		t.Logf("%+v", resp.Header)
		assert.Equal(t, "text/html; charset=utf-8", resp.Header.Get("Content-Type"))
	})
}

func TestHttp_DoWithSpaAssets(t *testing.T) {
	port, releasePort := getFreePort(t)
	cc := NewCacheControl(time.Hour * 12)
	h := Http{Timeouts: Timeouts{ResponseHeader: 200 * time.Millisecond}, Address: fmt.Sprintf("127.0.0.1:%d", port),
		AccessLog: io.Discard, AssetsWebRoot: "/static", AssetsLocation: "testdata", AssetsSPA: true,
		CacheControl: cc, Reporter: &ErrorReporter{Nice: false}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("req: %v", r)
		w.Header().Add("h1", "v1")
		assert.Equal(t, "127.0.0.1", r.Header.Get("X-Real-IP"))
		fmt.Fprintf(w, "response %s", r.URL.String())
	}))
	defer ds.Close()

	svc := discovery.NewService([]discovery.Provider{
		&provider.Static{Rules: []string{
			"localhost,^/api/(.*)," + ds.URL + "/123/$1,",
			"127.0.0.1,^/api/(.*)," + ds.URL + "/567/$1,",
		},
		}}, time.Millisecond*10)

	go func() {
		_ = svc.Run(t.Context())
	}()
	time.Sleep(50 * time.Millisecond)
	h.Matcher = svc
	h.Metrics = mgmt.NewMetrics(mgmt.MetricsConfig{})

	releasePort()
	go func() {
		_ = h.Run(ctx)
	}()

	client := http.Client{}

	// wait for server to be ready
	require.Eventually(t, func() bool {
		resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/ping")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 10*time.Millisecond, "server failed to start")

	t.Run("api call, good", func(t *testing.T) {
		req, err := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(port)+"/api/something", http.NoBody)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		t.Logf("%+v", resp.Header)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "response /567/something", string(body))
		assert.Empty(t, resp.Header.Get("App-Method"))
		assert.Equal(t, "v1", resp.Header.Get("h1"))
	})

	t.Run("static call, good", func(t *testing.T) {
		resp, err := client.Get("http://localhost:" + strconv.Itoa(port) + "/static/1.html")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		t.Logf("%+v", resp.Header)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "test html", string(body))
		assert.Empty(t, resp.Header.Get("App-Method"))
		assert.Empty(t, resp.Header.Get("h1"))
		assert.Equal(t, "public, max-age=43200", resp.Header.Get("Cache-Control"))
	})

	t.Run("static call, not found server index", func(t *testing.T) {
		resp, err := client.Get("http://localhost:" + strconv.Itoa(port) + "/static/bad.html")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		t.Logf("%+v", resp.Header)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "index html", string(body))
		assert.Empty(t, resp.Header.Get("App-Method"))
		assert.Empty(t, resp.Header.Get("h1"))
		assert.Equal(t, "public, max-age=43200", resp.Header.Get("Cache-Control"))
	})

	t.Run("static call, bad url", func(t *testing.T) {
		resp, err := client.Get("http://localhost:" + strconv.Itoa(port) + "/svcbad")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Contains(t, string(body), "Server error")
		assert.Equal(t, "text/plain; charset=utf-8", resp.Header.Get("Content-Type"))
	})
}

func TestHttp_DoWithAssetRules(t *testing.T) {
	port, releasePort := getFreePort(t)
	cc := NewCacheControl(time.Hour * 12)
	h := Http{Timeouts: Timeouts{ResponseHeader: 200 * time.Millisecond}, Address: fmt.Sprintf("127.0.0.1:%d", port),
		AccessLog: io.Discard, CacheControl: cc, Reporter: &ErrorReporter{}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("req: %v", r)
		w.Header().Add("h1", "v1")
		assert.Equal(t, "127.0.0.1", r.Header.Get("X-Real-IP"))
		fmt.Fprintf(w, "response %s", r.URL.String())
	}))
	defer ds.Close()

	svc := discovery.NewService([]discovery.Provider{
		&provider.Static{Rules: []string{
			"localhost,^/api/(.*)," + ds.URL + "/123/$1,",
			"127.0.0.1,^/api/(.*)," + ds.URL + "/567/$1,",
			"*,/web,assets:testdata,",
			"*,/web2,spa:testdata,",
		},
		}}, time.Millisecond*10)

	go func() {
		_ = svc.Run(t.Context())
	}()
	time.Sleep(150 * time.Millisecond)

	h.Matcher = svc
	h.Metrics = mgmt.NewMetrics(mgmt.MetricsConfig{})

	releasePort()
	go func() {
		_ = h.Run(ctx)
	}()
	waitForServer(t, fmt.Sprintf("127.0.0.1:%d", port))

	client := http.Client{}

	t.Run("web2 spa not found page", func(t *testing.T) {
		resp, err := client.Get("http://localhost:" + strconv.Itoa(port) + "/web2/nop.html")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		t.Logf("%+v", resp.Header)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "index html", string(body))
		assert.Empty(t, resp.Header.Get("App-Method"))
		assert.Empty(t, resp.Header.Get("h1"))
		assert.Equal(t, "public, max-age=43200", resp.Header.Get("Cache-Control"))
	})

	t.Run("api call on 127.0.0.1", func(t *testing.T) {
		req, err := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(port)+"/api/something", http.NoBody)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		t.Logf("%+v", resp.Header)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "response /567/something", string(body))
		assert.Empty(t, resp.Header.Get("App-Method"))
		assert.Equal(t, "v1", resp.Header.Get("h1"))
	})

	t.Run("web call on localhost", func(t *testing.T) {
		resp, err := client.Get("http://localhost:" + strconv.Itoa(port) + "/web/1.html")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		t.Logf("%+v", resp.Header)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "test html", string(body))
		assert.Empty(t, resp.Header.Get("App-Method"))
		assert.Empty(t, resp.Header.Get("h1"))
		assert.Equal(t, "public, max-age=43200", resp.Header.Get("Cache-Control"))
	})

	t.Run("web call on localhost, not found", func(t *testing.T) {
		resp, err := client.Get("http://localhost:" + strconv.Itoa(port) + "/web/nop.html")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}

func TestHttp_DoWithRedirects(t *testing.T) {
	port, releasePort := getFreePort(t)
	cc := NewCacheControl(time.Hour * 12)
	h := Http{Timeouts: Timeouts{ResponseHeader: 200 * time.Millisecond}, Address: fmt.Sprintf("127.0.0.1:%d", port),
		AccessLog: io.Discard, CacheControl: cc, Reporter: &ErrorReporter{}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	svc := discovery.NewService([]discovery.Provider{
		&provider.Static{Rules: []string{
			"localhost,^/api/(.*),@perm http://example.com/123/$1,",
			"127.0.0.1,^/api/(.*),@302 http://example.com/567/$1,",
		},
		}}, time.Millisecond*10)

	go func() {
		_ = svc.Run(t.Context())
	}()
	time.Sleep(50 * time.Millisecond)
	h.Matcher = svc
	h.Metrics = mgmt.NewMetrics(mgmt.MetricsConfig{})

	releasePort()
	go func() {
		_ = h.Run(ctx)
	}()

	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	waitForServer(t, fmt.Sprintf("127.0.0.1:%d", port))

	t.Run("localhost to example.com", func(t *testing.T) {
		req, err := http.NewRequest("GET", "http://localhost:"+strconv.Itoa(port)+"/api/something", http.NoBody)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusMovedPermanently, resp.StatusCode)
		t.Logf("%+v", resp.Header)
		assert.Equal(t, "http://example.com/123/something", resp.Header.Get("Location"))
	})

	t.Run("127.0.0.1 to example.com", func(t *testing.T) {
		req, err := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(port)+"/api/something", http.NoBody)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusFound, resp.StatusCode)
		t.Logf("%+v", resp.Header)
		assert.Equal(t, "http://example.com/567/something", resp.Header.Get("Location"))
	})
}

func TestHttp_DoLimitedReq(t *testing.T) {
	port, releasePort := getFreePort(t)
	h := Http{Timeouts: Timeouts{ResponseHeader: 200 * time.Millisecond}, Address: fmt.Sprintf("127.0.0.1:%d", port),
		AccessLog: io.Discard, Signature: true, ProxyHeaders: []string{"hh1:vv1", "hh2:vv2"}, StdOutEnabled: true,
		Reporter: &ErrorReporter{Nice: true}, MaxBodySize: 10}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("req: %v", r)
		w.Header().Add("h1", "v1")
		assert.Equal(t, "127.0.0.1", r.Header.Get("X-Real-IP"))
		fmt.Fprintf(w, "response %s", r.URL.String())
	}))
	defer ds.Close()

	svc := discovery.NewService([]discovery.Provider{
		&provider.Static{Rules: []string{
			"localhost,^/api/(.*)," + ds.URL + "/123/$1,",
			"127.0.0.1,^/api/(.*)," + ds.URL + "/567/$1,",
		},
		}}, time.Millisecond*10)

	go func() {
		_ = svc.Run(t.Context())
	}()

	time.Sleep(50 * time.Millisecond)
	h.Matcher, h.Metrics = svc, mgmt.NewMetrics(mgmt.MetricsConfig{})

	releasePort()
	go func() {
		_ = h.Run(ctx)
	}()
	waitForServer(t, fmt.Sprintf("127.0.0.1:%d", port))

	client := http.Client{}

	t.Run("allowed request size", func(t *testing.T) {
		req, err := http.NewRequest("POST", "http://127.0.0.1:"+strconv.Itoa(port)+"/api/something", bytes.NewBufferString("abcdefg"))
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		t.Logf("%+v", resp.Header)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "response /567/something", string(body))
		assert.Equal(t, "reproxy", resp.Header.Get("App-Name"))
		assert.Equal(t, "v1", resp.Header.Get("h1"))
		assert.Equal(t, "vv1", resp.Header.Get("hh1"))
		assert.Equal(t, "vv2", resp.Header.Get("hh2"))
	})

	t.Run("request size too large", func(t *testing.T) {
		req, err := http.NewRequest("POST", "http://127.0.0.1:"+strconv.Itoa(port)+"/api/something", bytes.NewBufferString("abcdefg1234567"))
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
	})
}

func TestHttp_health(t *testing.T) {
	port, releasePort := getFreePort(t)
	h := Http{Timeouts: Timeouts{ResponseHeader: 200 * time.Millisecond}, Address: fmt.Sprintf("127.0.0.1:%d", port),
		AccessLog: io.Discard, Signature: true, ProxyHeaders: []string{"hh1:vv1", "hh2:vv2"}, StdOutEnabled: true,
		Reporter: &ErrorReporter{Nice: true}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("req: %v", r)
		w.Header().Add("h1", "v1")
		assert.Equal(t, "127.0.0.1", r.Header.Get("X-Real-IP"))
		fmt.Fprintf(w, "response %s", r.URL.String())
	}))
	defer ds.Close()

	svc := discovery.NewService([]discovery.Provider{
		&provider.Static{Rules: []string{
			"localhost,^/api/(.*)," + ds.URL + "/123/$1,",
			"127.0.0.1,^/api/(.*)," + ds.URL + "/567/$1,",
			"*,/web,spa:testdata,",
		},
		}}, time.Millisecond*10)

	go func() {
		_ = svc.Run(t.Context())
	}()

	time.Sleep(50 * time.Millisecond)
	h.Matcher, h.Metrics = svc, mgmt.NewMetrics(mgmt.MetricsConfig{})

	releasePort()
	go func() {
		_ = h.Run(ctx)
	}()
	waitForServer(t, fmt.Sprintf("127.0.0.1:%d", port))

	client := http.Client{}

	// api call
	req, err := http.NewRequest("POST", "http://127.0.0.1:"+strconv.Itoa(port)+"/api/something", bytes.NewBufferString("abcdefg"))
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	t.Logf("%+v", resp.Header)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "response /567/something", string(body))
	assert.Equal(t, "reproxy", resp.Header.Get("App-Name"))
	assert.Equal(t, "v1", resp.Header.Get("h1"))
	assert.Equal(t, "vv1", resp.Header.Get("hh1"))
	assert.Equal(t, "vv2", resp.Header.Get("hh2"))

	// health check
	req, err = http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(port)+"/health", http.NoBody)
	require.NoError(t, err)
	resp, err = client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"status": "ok", "services": 2}`, string(body))
}

func TestHttp_withBasicAuth(t *testing.T) {
	port, releasePort := getFreePort(t)
	h := Http{Timeouts: Timeouts{ResponseHeader: 200 * time.Millisecond}, Address: fmt.Sprintf("127.0.0.1:%d", port),
		AccessLog: io.Discard, Signature: true, ProxyHeaders: []string{"hh1:vv1", "hh2:vv2"}, StdOutEnabled: true,
		Reporter: &ErrorReporter{Nice: true}, BasicAuthEnabled: true, BasicAuthAllowed: []string{
			"test:$2y$05$zMxDmK65SjcH2vJQNopVSO/nE8ngVLx65RoETyHpez7yTS/8CLEiW",
			"test2:$2y$05$TLQqHh6VT4JxysdKGPOlJeSkkMsv.Ku/G45i7ssIm80XuouCrES12",
		}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("req: %v", r)
		w.Header().Add("h1", "v1")
		assert.Equal(t, "127.0.0.1", r.Header.Get("X-Real-IP"))
		fmt.Fprintf(w, "response %s", r.URL.String())
	}))
	defer ds.Close()

	svc := discovery.NewService([]discovery.Provider{
		&provider.Static{Rules: []string{
			"localhost,^/api/(.*)," + ds.URL + "/123/$1,",
			"127.0.0.1,^/api/(.*)," + ds.URL + "/567/$1,",
			"*,/web,spa:testdata,",
		},
		}}, time.Millisecond*10)

	go func() {
		_ = svc.Run(t.Context())
	}()

	time.Sleep(50 * time.Millisecond)
	h.Matcher, h.Metrics = svc, mgmt.NewMetrics(mgmt.MetricsConfig{})

	releasePort()
	go func() {
		_ = h.Run(ctx)
	}()

	// wait for server to be ready
	client := http.Client{Timeout: 100 * time.Millisecond}
	require.Eventually(t, func() bool {
		_, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/")
		return err == nil
	}, 5*time.Second, 10*time.Millisecond, "server did not start")

	t.Run("no auth", func(t *testing.T) {
		req, err := http.NewRequest("POST", "http://127.0.0.1:"+strconv.Itoa(port)+"/api/something", bytes.NewBufferString("abcdefg"))
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("bad auth", func(t *testing.T) {
		req, err := http.NewRequest("POST", "http://127.0.0.1:"+strconv.Itoa(port)+"/api/something", bytes.NewBufferString("abcdefg"))
		req.SetBasicAuth("test", "badpasswd")
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("good auth", func(t *testing.T) {
		req, err := http.NewRequest("POST", "http://127.0.0.1:"+strconv.Itoa(port)+"/api/something", bytes.NewBufferString("abcdefg"))
		req.SetBasicAuth("test", "passwd")
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("good auth 2", func(t *testing.T) {
		req, err := http.NewRequest("POST", "http://127.0.0.1:"+strconv.Itoa(port)+"/api/something", bytes.NewBufferString("abcdefg"))
		req.SetBasicAuth("test2", "passwd2")
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

func TestHttp_toHttp(t *testing.T) {

	tbl := []struct {
		addr string
		port int
		res  string
	}{
		{"localhost:1234", 80, "localhost:80"},
		{"m.example.com:443", 8080, "m.example.com:8080"},
		{"192.168.1.1:1443", 8080, "192.168.1.1:8080"},
	}

	h := Http{}
	for _, tt := range tbl {
		t.Run(tt.addr, func(t *testing.T) {
			assert.Equal(t, tt.res, h.toHTTP(tt.addr, tt.port))
		})
	}
}

func TestHttp_isAssetRequest(t *testing.T) {
	tbl := []struct {
		req            string
		assetsLocation string
		assetsWebRoot  string
		res            bool
	}{
		{"/static/123.html", "/tmp", "/static", true},
		{"/static/123.html", "/tmp", "/static/", true},
		{"/static", "/tmp", "/static", true},
		{"/static/", "/tmp", "/static", true},
		{"/bad/", "/tmp", "/static", false},
		{"/static/", "", "/static", false},
		{"/static/", "/tmp", "", false},
	}

	for _, tt := range tbl {
		t.Run(tt.req, func(t *testing.T) {
			h := Http{AssetsLocation: tt.assetsLocation, AssetsWebRoot: tt.assetsWebRoot}
			r, err := http.NewRequest("GET", tt.req, http.NoBody)
			require.NoError(t, err)
			assert.Equal(t, tt.res, h.isAssetRequest(r))
		})
	}

}

func TestHttp_matchHandler(t *testing.T) {
	tbl := []struct {
		name    string
		matches discovery.Matches
		res     string
		ok      bool
	}{
		{
			name: "all alive destinations",
			matches: discovery.Matches{MatchType: discovery.MTProxy, Routes: []discovery.MatchedRoute{
				{Destination: "dest1", Alive: true},
				{Destination: "dest2", Alive: true},
				{Destination: "dest3", Alive: true},
			}},
			res: "dest1", ok: true,
		},

		{
			name: "second alive destination",
			matches: discovery.Matches{MatchType: discovery.MTProxy, Routes: []discovery.MatchedRoute{
				{Destination: "dest1", Alive: false},
				{Destination: "dest2", Alive: true},
				{Destination: "dest3", Alive: false},
			}},
			res: "dest2", ok: true,
		},
		{
			name: "one dead destination",
			matches: discovery.Matches{MatchType: discovery.MTProxy, Routes: []discovery.MatchedRoute{
				{Destination: "dest1", Alive: false},
				{Destination: "dest2", Alive: true},
				{Destination: "dest3", Alive: true},
			}},
			res: "dest2", ok: true,
		},
		{
			name: "last alive destination",
			matches: discovery.Matches{MatchType: discovery.MTProxy, Routes: []discovery.MatchedRoute{
				{Destination: "dest1", Alive: false},
				{Destination: "dest2", Alive: false},
				{Destination: "dest3", Alive: true},
			}},
			res: "dest3", ok: true,
		},
		{
			name: "all dead destinations",
			matches: discovery.Matches{MatchType: discovery.MTProxy, Routes: []discovery.MatchedRoute{
				{Destination: "dest1", Alive: false},
				{Destination: "dest2", Alive: false},
				{Destination: "dest3", Alive: false},
			}},
			res: "", ok: false,
		},
		{
			name:    "no destinations",
			matches: discovery.Matches{MatchType: discovery.MTProxy, Routes: []discovery.MatchedRoute{}}, res: "", ok: false,
		},
	}

	var count atomic.Int32
	matcherMock := &MatcherMock{
		MatchFunc: func(srv string, src string) discovery.Matches {
			return tbl[count.Load()].matches
		},
	}

	for _, tt := range tbl {
		t.Run(tt.name, func(t *testing.T) {
			h := Http{Matcher: matcherMock, LBSelector: &FailoverSelector{}}
			handler := h.matchHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Logf("req: %+v", r)
				t.Logf("dst: %v", r.Context().Value(ctxURL))

				v := r.Context().Value(ctxURL)
				if v == nil {
					assert.False(t, tt.ok)
					return
				}
				assert.Equal(t, tt.res, v.(*url.URL).String())
			}))

			req := httptest.NewRequest("GET", "http://example.com", http.NoBody)
			wr := httptest.NewRecorder()
			handler.ServeHTTP(wr, req)
			assert.Equal(t, http.StatusOK, wr.Code)
			count.Add(1)
		})
	}
}

func TestHttp_matchHandlerInvalidHost(t *testing.T) {
	tbl := []struct {
		name, host string
		code       int
		matched    bool
	}{
		{name: "valid host", host: "aaaa.example.com", code: http.StatusOK, matched: true},
		{name: "valid host with port", host: "aaaa.example.com:8080", code: http.StatusOK, matched: true},
		{name: "path and fragment in host", host: "localhost/admin#.example.com", code: http.StatusBadRequest},
		{name: "braces in host", host: "1${host.1}.example.com", code: http.StatusBadRequest},
		{name: "userinfo in host", host: "user@evil.com", code: http.StatusBadRequest},
		{name: "space in host", host: "a b.example.com", code: http.StatusBadRequest},
		{name: "percent escapes in host", host: "%2e%2e%2fadmin.example.com", code: http.StatusBadRequest},
	}

	for _, tt := range tbl {
		t.Run(tt.name, func(t *testing.T) {
			matched := false
			matcherMock := &MatcherMock{MatchFunc: func(srv, src string) discovery.Matches {
				matched = true
				return discovery.Matches{}
			}}
			h := Http{Matcher: matcherMock, LBSelector: &FailoverSelector{}, Reporter: &ErrorReporter{Nice: false}}
			handler := h.matchHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

			req := httptest.NewRequest("GET", "/x", http.NoBody)
			req.Host = tt.host
			wr := httptest.NewRecorder()
			handler.ServeHTTP(wr, req)
			assert.Equal(t, tt.code, wr.Code)
			assert.Equal(t, tt.matched, matched)
		})
	}
}

func TestHttp_matchHandlerUnsafePath(t *testing.T) {
	tbl := []struct {
		name, target string
		code         int
		matched      bool
	}{
		{name: "root", target: "/", code: http.StatusOK, matched: true},
		{name: "plain path", target: "/api/x", code: http.StatusOK, matched: true},
		{name: "trailing slash", target: "/api/x/", code: http.StatusOK, matched: true},
		{name: "encoded slash inside a segment", target: "/api/a%2fb", code: http.StatusOK, matched: true},
		{name: "dots inside a name", target: "/api/..x/.well-known", code: http.StatusOK, matched: true},
		{name: "path parameter on a name", target: "/api/x;v=1/y", code: http.StatusOK, matched: true},
		{name: "path parameter as last segment", target: "/app/;jsessionid=1", code: http.StatusOK, matched: true},
		{name: "encoded backslash inside a name", target: "/api/a%5cb", code: http.StatusOK, matched: true},
		{name: "parent with path parameter", target: "/api/public/..;/private/x", code: http.StatusBadRequest},
		{name: "current with path parameter", target: "/api/public/.;v=1/x", code: http.StatusBadRequest},
		{name: "empty segment with path parameter", target: "/api/;/private/x", code: http.StatusBadRequest},
		{name: "parent with encoded backslash", target: "/api/public/..%5cprivate/x", code: http.StatusBadRequest},
		{name: "double encoded backslash", target: "/api/%5c%5cprivate/x", code: http.StatusBadRequest},
		{name: "parent segment", target: "/api/public/../private/x", code: http.StatusBadRequest},
		{name: "current segment", target: "/api/./x", code: http.StatusBadRequest},
		{name: "encoded parent and slash", target: "/api/public/%2e%2e%2fprivate/x", code: http.StatusBadRequest},
		{name: "parent as last segment", target: "/api/%2e%2e", code: http.StatusBadRequest},
		{name: "double slash", target: "/api//private/x", code: http.StatusBadRequest},
		{name: "encoded slash next to a slash", target: "/api/%2fprivate/x", code: http.StatusBadRequest},
		{name: "leading double slash", target: "//api/x", code: http.StatusBadRequest},
		{name: "trailing double slash", target: "/api/x//", code: http.StatusBadRequest},
	}

	for _, tt := range tbl {
		t.Run(tt.name, func(t *testing.T) {
			matched := false
			matcherMock := &MatcherMock{MatchFunc: func(srv, src string) discovery.Matches {
				matched = true
				return discovery.Matches{}
			}}
			h := Http{Matcher: matcherMock, LBSelector: &FailoverSelector{}, Reporter: &ErrorReporter{Nice: false}}
			handler := h.matchHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

			req := httptest.NewRequest("GET", tt.target, http.NoBody)
			wr := httptest.NewRecorder()
			handler.ServeHTTP(wr, req)
			assert.Equal(t, tt.code, wr.Code)
			assert.Equal(t, tt.matched, matched)
		})
	}
}

func TestHttp_discoveredServers(t *testing.T) {
	calls := 0
	m := &MatcherMock{ServersFunc: func() []string {
		defer func() { calls++ }()
		switch calls {
		case 0, 1, 2, 3, 4:
			return []string{}
		case 5:
			return []string{"s1", "s2"}
		case 6, 7:
			return []string{"s1", "s2", "s3"}
		default:
			t.Fatalf("shoudn't be called %d times", calls)
			return nil
		}
	}}

	h := Http{Matcher: m}

	res := h.discoveredServers(context.Background(), time.Millisecond)
	assert.Equal(t, []string{"s1", "s2", "s3"}, res)
}

func TestHttp_UpstreamConfig(t *testing.T) {
	port, releasePort := getFreePort(t)

	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "response %s", r.URL.String())
	}))
	defer ds.Close()

	svc := discovery.NewService([]discovery.Provider{
		&provider.Static{Rules: []string{
			"localhost,^/api/(.*)," + ds.URL + "/test/$1,",
		}},
	}, time.Millisecond*10)

	go func() {
		_ = svc.Run(t.Context())
	}()
	time.Sleep(50 * time.Millisecond)

	t.Run("with default upstream values", func(t *testing.T) {
		h := Http{
			Timeouts:                Timeouts{ResponseHeader: 200 * time.Millisecond},
			Address:                 fmt.Sprintf("127.0.0.1:%d", port),
			AccessLog:               io.Discard,
			Matcher:                 svc,
			Metrics:                 mgmt.NewMetrics(mgmt.MetricsConfig{}),
			Reporter:                &ErrorReporter{Nice: true},
			UpstreamMaxIdleConns:    100, // default value
			UpstreamMaxConnsPerHost: 0,   // unlimited, default
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		releasePort()
		go func() {
			_ = h.Run(ctx)
		}()
		waitForServer(t, fmt.Sprintf("127.0.0.1:%d", port))

		resp, err := http.Get("http://localhost:" + strconv.Itoa(port) + "/api/something")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "response /test/something", string(body))
	})

	t.Run("with custom upstream values", func(t *testing.T) {
		port2, releasePort2 := getFreePort(t)
		h := Http{
			Timeouts:                Timeouts{ResponseHeader: 200 * time.Millisecond},
			Address:                 fmt.Sprintf("127.0.0.1:%d", port2),
			AccessLog:               io.Discard,
			Matcher:                 svc,
			Metrics:                 mgmt.NewMetrics(mgmt.MetricsConfig{}),
			Reporter:                &ErrorReporter{Nice: true},
			UpstreamMaxIdleConns:    50,
			UpstreamMaxConnsPerHost: 10,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		releasePort2()
		go func() {
			_ = h.Run(ctx)
		}()
		waitForServer(t, fmt.Sprintf("127.0.0.1:%d", port2))

		resp, err := http.Get("http://localhost:" + strconv.Itoa(port2) + "/api/something")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "response /test/something", string(body))
	})
}

// waitForServer waits until the server at the given address is ready to accept connections
func waitForServer(t *testing.T, addr string) {
	t.Helper()
	require.Eventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err != nil {
			return false
		}
		conn.Close()
		return true
	}, 5*time.Second, 50*time.Millisecond, "server at %s did not become ready", addr)
}

// getFreePort reserves an available tcp port on 127.0.0.1 and keeps the listener
// open so no other listener created during test setup (e.g. httptest.NewServer)
// cannot grab the same port. call the returned release func immediately before
// binding a server to the port; the listener is also closed on test cleanup.
func getFreePort(t *testing.T) (port int, release func()) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port = l.Addr().(*net.TCPAddr).Port
	release = func() { _ = l.Close() }
	t.Cleanup(release)
	return port, release
}

// TestHttp_withPerRouteAuth_DefaultInit tests per-route auth works correctly.
// the test verifies that per-route auth works correctly with the default setup.
func TestHttp_withPerRouteAuth_DefaultInit(t *testing.T) {
	port, releasePort := getFreePort(t)
	authHash := "$2y$05$zMxDmK65SjcH2vJQNopVSO/nE8ngVLx65RoETyHpez7yTS/8CLEiW" // passwd

	h := Http{
		Timeouts:  Timeouts{ResponseHeader: 200 * time.Millisecond},
		Address:   fmt.Sprintf("127.0.0.1:%d", port),
		AccessLog: io.Discard,
		Reporter:  &ErrorReporter{Nice: true},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "response %s", r.URL.String())
	}))
	defer ds.Close()

	mockProvider := &mockAuthProvider{
		mappers: []discovery.URLMapper{
			{
				Server:    "*",
				SrcMatch:  *regexp.MustCompile("^/secure/(.*)"),
				Dst:       ds.URL + "/secure/$1",
				MatchType: discovery.MTProxy,
				AuthUsers: []string{"test:" + authHash},
			},
		},
	}

	svc := discovery.NewService([]discovery.Provider{mockProvider}, time.Millisecond*10)
	go func() {
		_ = svc.Run(context.Background())
	}()
	time.Sleep(50 * time.Millisecond)

	h.Matcher, h.Metrics = svc, mgmt.NewMetrics(mgmt.MetricsConfig{})

	releasePort()
	go func() {
		_ = h.Run(ctx)
	}()
	waitForServer(t, fmt.Sprintf("127.0.0.1:%d", port))

	client := http.Client{Timeout: 100 * time.Millisecond}

	// this MUST return 401 - if it returns 200, we have a security bypass bug
	t.Run("secure route without auth must return 401", func(t *testing.T) {
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/secure/test", port))
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "route with AuthUsers should require auth")
	})

	t.Run("secure route with correct auth returns 200", func(t *testing.T) {
		req, err := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/secure/test", port), http.NoBody)
		require.NoError(t, err)
		req.SetBasicAuth("test", "passwd")
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

func TestHttp_withPerRouteAuth(t *testing.T) {
	port, releasePort := getFreePort(t)
	authHash := "$2y$05$zMxDmK65SjcH2vJQNopVSO/nE8ngVLx65RoETyHpez7yTS/8CLEiW" // passwd

	h := Http{
		Timeouts:  Timeouts{ResponseHeader: 200 * time.Millisecond},
		Address:   fmt.Sprintf("127.0.0.1:%d", port),
		AccessLog: io.Discard,
		Reporter:  &ErrorReporter{Nice: true},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "response %s", r.URL.String())
	}))
	defer ds.Close()

	// create a mock provider that returns routes with AuthUsers
	mockProvider := &mockAuthProvider{
		mappers: []discovery.URLMapper{
			{
				Server:    "*",
				SrcMatch:  *regexp.MustCompile("^/secure/(.*)"),
				Dst:       ds.URL + "/secure/$1",
				MatchType: discovery.MTProxy,
				AuthUsers: []string{"test:" + authHash},
			},
			{
				Server:    "*",
				SrcMatch:  *regexp.MustCompile("^/public/(.*)"),
				Dst:       ds.URL + "/public/$1",
				MatchType: discovery.MTProxy,
				AuthUsers: []string{}, // no auth required
			},
		},
	}

	svc := discovery.NewService([]discovery.Provider{mockProvider}, time.Millisecond*10)
	go func() {
		_ = svc.Run(context.Background())
	}()
	time.Sleep(50 * time.Millisecond)

	h.Matcher, h.Metrics = svc, mgmt.NewMetrics(mgmt.MetricsConfig{})

	releasePort()
	go func() {
		_ = h.Run(ctx)
	}()
	waitForServer(t, fmt.Sprintf("127.0.0.1:%d", port))

	client := http.Client{Timeout: 100 * time.Millisecond}

	t.Run("secure route without auth returns 401", func(t *testing.T) {
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/secure/test", port))
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		assert.Equal(t, `Basic realm="Restricted"`, resp.Header.Get("WWW-Authenticate"))
	})

	t.Run("secure route with bad auth returns 401", func(t *testing.T) {
		req, err := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/secure/test", port), http.NoBody)
		require.NoError(t, err)
		req.SetBasicAuth("test", "wrongpassword")
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	t.Run("secure route with good auth returns 200", func(t *testing.T) {
		req, err := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/secure/test", port), http.NoBody)
		require.NoError(t, err)
		req.SetBasicAuth("test", "passwd")
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		body, _ := io.ReadAll(resp.Body)
		assert.Contains(t, string(body), "response /secure/test")
	})

	t.Run("public route without auth returns 200", func(t *testing.T) {
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/public/test", port))
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		body, _ := io.ReadAll(resp.Body)
		assert.Contains(t, string(body), "response /public/test")
	})
}

func TestHttp_EncodedPathRoutePolicy(t *testing.T) {
	port, releasePort := getFreePort(t)
	authHash := "$2y$05$zMxDmK65SjcH2vJQNopVSO/nE8ngVLx65RoETyHpez7yTS/8CLEiW" // passwd
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, r.URL.EscapedPath())
	}))
	defer upstream.Close()

	p := &mockAuthProvider{mappers: []discovery.URLMapper{
		{
			Server: "*", SrcMatch: *regexp.MustCompile("^/api/private/(.*)"), Dst: upstream.URL + "/private/$1",
			MatchType: discovery.MTProxy, AuthUsers: []string{"test:" + authHash},
		},
		{
			Server: "*", SrcMatch: *regexp.MustCompile("^/api/remote/(.*)"), Dst: upstream.URL + "/remote/$1",
			MatchType: discovery.MTProxy, OnlyFromIPs: []string{"192.0.2.1"},
		},
		{
			Server: "*", SrcMatch: *regexp.MustCompile("^/api/(.*)"), Dst: upstream.URL + "/$1",
			MatchType: discovery.MTProxy,
		},
	}}

	svc := discovery.NewService([]discovery.Provider{p}, 10*time.Millisecond)
	go func() { _ = svc.Run(ctx) }()
	require.Eventually(t, func() bool { return len(svc.Mappers()) == 3 }, time.Second, 10*time.Millisecond)

	h := Http{
		Timeouts: Timeouts{ResponseHeader: 200 * time.Millisecond}, Address: fmt.Sprintf("127.0.0.1:%d", port),
		AccessLog: io.Discard, Reporter: &ErrorReporter{Nice: true}, Matcher: svc,
		Metrics: mgmt.NewMetrics(mgmt.MetricsConfig{}), OnlyFrom: NewOnlyFrom(nil),
	}
	releasePort()
	go func() { _ = h.Run(ctx) }()
	waitForServer(t, fmt.Sprintf("127.0.0.1:%d", port))

	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantBody   string
	}{
		{name: "encoded slash uppercase uses auth route", path: "/api/private%2Fsecret", wantStatus: http.StatusUnauthorized},
		{name: "encoded slash lowercase uses auth route", path: "/api/private%2fsecret", wantStatus: http.StatusUnauthorized},
		{name: "encoded unreserved uses auth route", path: "/api/%70rivate/secret", wantStatus: http.StatusUnauthorized},
		{name: "encoded slash uses remote allow-list route", path: "/api/remote%2Fsecret", wantStatus: http.StatusForbidden},
		{name: "space and percent pass through", path: "/api/te%20st%25value", wantStatus: http.StatusOK, wantBody: "/te%20st%25value"},
	}

	client := http.Client{Timeout: time.Second}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", port, tt.path), http.NoBody)
			require.NoError(t, err)
			resp, err := client.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			assert.Equal(t, tt.wantStatus, resp.StatusCode)
			if tt.wantBody != "" {
				body, readErr := io.ReadAll(resp.Body)
				require.NoError(t, readErr)
				assert.Equal(t, tt.wantBody, string(body))
			}
		})
	}
}

func TestHttp_withGlobalAndPerRouteAuth(t *testing.T) {
	port, releasePort := getFreePort(t)
	globalHash := "$2y$05$zMxDmK65SjcH2vJQNopVSO/nE8ngVLx65RoETyHpez7yTS/8CLEiW" // passwd
	routeHash := "$2y$05$TLQqHh6VT4JxysdKGPOlJeSkkMsv.Ku/G45i7ssIm80XuouCrES12"  // passwd2

	h := Http{
		Timeouts:         Timeouts{ResponseHeader: 200 * time.Millisecond},
		Address:          fmt.Sprintf("127.0.0.1:%d", port),
		AccessLog:        io.Discard,
		Reporter:         &ErrorReporter{Nice: true},
		BasicAuthEnabled: true,
		BasicAuthAllowed: []string{"globaluser:" + globalHash},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "response %s", r.URL.String())
	}))
	defer ds.Close()

	mockProvider := &mockAuthProvider{
		mappers: []discovery.URLMapper{
			{
				Server:    "*",
				SrcMatch:  *regexp.MustCompile("^/secure/(.*)"),
				Dst:       ds.URL + "/secure/$1",
				MatchType: discovery.MTProxy,
				AuthUsers: []string{"routeuser:" + routeHash}, // per-route auth
			},
			{
				Server:    "*",
				SrcMatch:  *regexp.MustCompile("^/global/(.*)"),
				Dst:       ds.URL + "/global/$1",
				MatchType: discovery.MTProxy,
				AuthUsers: []string{}, // no per-route auth, uses global
			},
		},
	}

	svc := discovery.NewService([]discovery.Provider{mockProvider}, time.Millisecond*10)
	go func() {
		_ = svc.Run(context.Background())
	}()
	time.Sleep(50 * time.Millisecond)

	h.Matcher, h.Metrics = svc, mgmt.NewMetrics(mgmt.MetricsConfig{})

	releasePort()
	go func() {
		_ = h.Run(ctx)
	}()
	waitForServer(t, fmt.Sprintf("127.0.0.1:%d", port))

	client := http.Client{Timeout: 100 * time.Millisecond}

	t.Run("route with per-route auth ignores global auth", func(t *testing.T) {
		// global creds should NOT work on per-route auth route
		req, err := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/secure/test", port), http.NoBody)
		require.NoError(t, err)
		req.SetBasicAuth("globaluser", "passwd")
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "global creds should not work on per-route auth route")
	})

	t.Run("route with per-route auth accepts route-specific creds", func(t *testing.T) {
		req, err := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/secure/test", port), http.NoBody)
		require.NoError(t, err)
		req.SetBasicAuth("routeuser", "passwd2")
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("route without per-route auth uses global auth", func(t *testing.T) {
		req, err := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/global/test", port), http.NoBody)
		require.NoError(t, err)
		req.SetBasicAuth("globaluser", "passwd")
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("route without per-route auth rejects route-specific creds", func(t *testing.T) {
		req, err := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/global/test", port), http.NoBody)
		require.NoError(t, err)
		req.SetBasicAuth("routeuser", "passwd2")
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "route creds should not work on global auth route")
	})
}

// mockAuthProvider is a test provider that returns pre-configured URLMappers with AuthUsers
type mockAuthProvider struct {
	mappers []discovery.URLMapper
}

func (m *mockAuthProvider) Events(_ context.Context) <-chan discovery.ProviderID {
	res := make(chan discovery.ProviderID, 1)
	res <- discovery.PIStatic
	return res
}

func (m *mockAuthProvider) List() ([]discovery.URLMapper, error) { return m.mappers, nil }

func TestHttp_PerRouteTimeoutAndThrottle(t *testing.T) {
	// upstream that sleeps with context-awareness — used to verify per-route timeout cancels in-flight requests
	slowUpstream := func(sleep time.Duration) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(sleep):
			}
			fmt.Fprintf(w, "ok %s", r.URL.Path)
		}))
	}
	fastUpstream := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "ok %s", r.URL.Path)
		}))
	}

	timeoutUpstream := slowUpstream(500 * time.Millisecond) // exceeds 200ms per-route timeout
	defer timeoutUpstream.Close()
	throttleUpstream := fastUpstream()
	defer throttleUpstream.Close()
	bothUpstream := fastUpstream()
	defer bothUpstream.Close()
	controlUpstream := slowUpstream(250 * time.Millisecond) // slow but under global timeout
	defer controlUpstream.Close()

	tmplBytes, err := os.ReadFile("testdata/per_route.yml")
	require.NoError(t, err)
	yaml := strings.NewReplacer(
		"__TIMEOUT_URL__", timeoutUpstream.URL,
		"__THROTTLE_URL__", throttleUpstream.URL,
		"__BOTH_URL__", bothUpstream.URL,
		"__CONTROL_URL__", controlUpstream.URL,
	).Replace(string(tmplBytes))
	cfgPath := filepath.Join(t.TempDir(), "per_route.yml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(yaml), 0o600))

	port, releasePort := getFreePort(t)
	h := Http{
		Timeouts:  Timeouts{ResponseHeader: 2 * time.Second, Write: 5 * time.Second, ReadHeader: 5 * time.Second},
		Address:   fmt.Sprintf("127.0.0.1:%d", port),
		AccessLog: io.Discard,
		Reporter:  &ErrorReporter{Nice: true},
	}

	svc := discovery.NewService([]discovery.Provider{
		&provider.File{FileName: cfgPath, CheckInterval: 20 * time.Millisecond, Delay: 10 * time.Millisecond},
	}, time.Millisecond*10)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	go func() { _ = svc.Run(ctx) }()

	h.Matcher, h.Metrics = svc, mgmt.NewMetrics(mgmt.MetricsConfig{})

	releasePort()
	go func() { _ = h.Run(ctx) }()
	waitForServer(t, fmt.Sprintf("127.0.0.1:%d", port))

	// fresh conn per request — a per-route timeout fire leaves Go's http.Server tracking
	// the conn as broken; keep-alive reuse would carry that state into unrelated sub-tests
	client := http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{DisableKeepAlives: true},
	}
	baseURL := "http://127.0.0.1:" + strconv.Itoa(port)

	// wait for the file provider to ingest the fixture and the proxy to start matching
	require.Eventually(t, func() bool {
		resp, err := client.Get(baseURL + "/control/probe")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 25*time.Millisecond, "routes never became matchable")

	t.Run("timeout returns 502 quickly", func(t *testing.T) {
		start := time.Now()
		resp, err := client.Get(baseURL + "/timeout/x")
		require.NoError(t, err)
		defer resp.Body.Close()
		elapsed := time.Since(start)
		assert.Equal(t, http.StatusBadGateway, resp.StatusCode, "per-route timeout should produce 502")
		assert.Less(t, elapsed, 450*time.Millisecond, "timeout must fire well before upstream's 500ms sleep")
	})

	t.Run("control route without per-route timeout completes normally", func(t *testing.T) {
		// upstream sleeps 250ms which would exceed a 200ms per-route timeout if it had one,
		// but the control route has no per-route timeout so it succeeds under the 5s global write timeout
		start := time.Now()
		resp, err := client.Get(baseURL + "/control/x")
		require.NoError(t, err)
		defer resp.Body.Close()
		elapsed := time.Since(start)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "ok /x", string(body))
		assert.GreaterOrEqual(t, elapsed, 250*time.Millisecond, "control route should run to completion")
	})

	t.Run("throttle blocks third rapid request", func(t *testing.T) {
		statuses := make([]int, 3)
		for i := range statuses {
			resp, err := client.Get(baseURL + "/throttle/x")
			require.NoError(t, err)
			statuses[i] = resp.StatusCode
			resp.Body.Close()
		}
		assert.Equal(t, http.StatusOK, statuses[0])
		assert.Equal(t, http.StatusOK, statuses[1])
		assert.Equal(t, http.StatusTooManyRequests, statuses[2], "third rapid request should hit per-route throttle")
	})

	t.Run("control route accepts many rapid requests", func(t *testing.T) {
		// control route has no per-route throttle; with no global throttle set, all rapid requests pass
		for range 3 {
			resp, err := client.Get(baseURL + "/control/x")
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			resp.Body.Close()
		}
	})

	t.Run("chain ordering: throttle 429 arrives within timeout window", func(t *testing.T) {
		// /both has timeout: 50ms and throttle: 1 — first request consumes the bucket,
		// second exceeds throttle and must return 429 quickly, not stall on a slow upstream
		resp1, err := client.Get(baseURL + "/both/x")
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp1.StatusCode)
		resp1.Body.Close()

		start := time.Now()
		resp2, err := client.Get(baseURL + "/both/x")
		require.NoError(t, err)
		elapsed := time.Since(start)
		defer resp2.Body.Close()
		assert.Equal(t, http.StatusTooManyRequests, resp2.StatusCode, "second rapid request should be throttled")
		assert.Less(t, elapsed, 200*time.Millisecond, "429 must arrive well within sane bound regardless of upstream latency")
	})
}

func TestHttp_DoWithGzip(t *testing.T) {
	mp3 := make([]byte, 100_000)
	_, err := rand.Read(mp3)
	require.NoError(t, err)
	txt := []byte(strings.Repeat("compressible text line\n", 4000))
	var gzTxt bytes.Buffer
	gw := gzip.NewWriter(&gzTxt)
	_, err = gw.Write(txt)
	require.NoError(t, err)
	require.NoError(t, gw.Close())

	var lastAccept atomic.Value
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseSSE := func() { releaseOnce.Do(func() { close(release) }) }

	mux := http.NewServeMux()
	serveFile := func(name, ctype string, data []byte) {
		mux.HandleFunc("/"+name, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", ctype)
			w.Header().Set("ETag", `"`+name+`-v1"`)
			http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
		})
	}
	serveFile("a.mp3", "audio/mpeg", mp3)
	serveFile("a.txt", "text/plain; charset=utf-8", txt)
	mux.HandleFunc("/pregz", func(w http.ResponseWriter, r *http.Request) {
		lastAccept.Store(r.Header.Get("Accept-Encoding"))
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			_, _ = w.Write(txt)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(gzTxt.Bytes())
	})
	mux.HandleFunc("/sse", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: one\n\n"))
		_ = http.NewResponseController(w).Flush()
		<-release
		_, _ = w.Write([]byte("data: two\n\n"))
	})
	mux.HandleFunc("/upgrade", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "echo", r.Header.Get("Upgrade"))
		conn, brw, err := http.NewResponseController(w).Hijack()
		if !assert.NoError(t, err) {
			return
		}
		defer conn.Close()
		_, _ = brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: echo\r\n\r\n")
		_ = brw.Flush()
		line, _ := brw.ReadString('\n')
		_, _ = brw.WriteString("echo " + line)
		_ = brw.Flush()
	})
	ds := httptest.NewServer(mux)
	defer ds.Close()
	defer releaseSSE()

	port, releasePort := getFreePort(t)
	h := Http{Timeouts: Timeouts{ResponseHeader: time.Second}, Address: fmt.Sprintf("127.0.0.1:%d", port), AccessLog: io.Discard,
		AssetsWebRoot: "/static", AssetsLocation: "testdata", GzEnabled: true, GzExclude: testGzipExclude, Reporter: &ErrorReporter{},
		CacheControl: NewCacheControl(time.Hour)}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	svc := discovery.NewService([]discovery.Provider{
		&provider.Static{Rules: []string{"127.0.0.1,^/up/(.*)," + ds.URL + "/$1,"}},
	}, time.Millisecond*10)
	go func() { _ = svc.Run(t.Context()) }()
	time.Sleep(50 * time.Millisecond)
	h.Matcher = svc
	h.Metrics = mgmt.NewMetrics(mgmt.MetricsConfig{})
	releasePort()
	go func() { _ = h.Run(ctx) }()
	waitForServer(t, fmt.Sprintf("127.0.0.1:%d", port))
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	rawClient := &http.Client{Transport: &http.Transport{DisableCompression: true}, Timeout: 5 * time.Second}
	get := func(t *testing.T, path string, hdrs map[string]string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest("GET", baseURL+path, http.NoBody)
		require.NoError(t, err)
		for k, v := range hdrs {
			req.Header.Set(k, v)
		}
		resp, err := rawClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp, body
	}

	t.Run("excluded media served plain with ranges", func(t *testing.T) {
		resp, body := get(t, "/up/a.mp3", map[string]string{"Accept-Encoding": "gzip, deflate"})
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Empty(t, resp.Header.Get("Content-Encoding"))
		assert.Equal(t, "100000", resp.Header.Get("Content-Length"))
		assert.Equal(t, "bytes", resp.Header.Get("Accept-Ranges"))
		assert.Equal(t, `"a.mp3-v1"`, resp.Header.Get("ETag"))
		assert.Equal(t, mp3, body)
	})

	t.Run("range on excluded media", func(t *testing.T) {
		resp, body := get(t, "/up/a.mp3", map[string]string{"Accept-Encoding": "gzip", "Range": "bytes=1000-1999"})
		assert.Equal(t, http.StatusPartialContent, resp.StatusCode)
		assert.Empty(t, resp.Header.Get("Content-Encoding"))
		assert.Equal(t, "bytes 1000-1999/100000", resp.Header.Get("Content-Range"))
		assert.Equal(t, mp3[1000:2000], body)
	})

	t.Run("range on compressible type", func(t *testing.T) {
		resp, body := get(t, "/up/a.txt", map[string]string{"Accept-Encoding": "gzip", "Range": "bytes=10-99"})
		assert.Equal(t, http.StatusPartialContent, resp.StatusCode)
		assert.Empty(t, resp.Header.Get("Content-Encoding"))
		assert.Equal(t, fmt.Sprintf("bytes 10-99/%d", len(txt)), resp.Header.Get("Content-Range"))
		assert.Equal(t, txt[10:100], body)
	})

	t.Run("multipart range on compressible type", func(t *testing.T) {
		resp, body := get(t, "/up/a.txt", map[string]string{"Accept-Encoding": "gzip", "Range": "bytes=0-9,20-29"})
		assert.Equal(t, http.StatusPartialContent, resp.StatusCode)
		assert.Empty(t, resp.Header.Get("Content-Encoding"))
		assert.Contains(t, resp.Header.Get("Content-Type"), "multipart/byteranges")
		assert.Contains(t, string(body), string(txt[20:30]))
	})

	t.Run("compressible type compressed with weak etag and no ranges", func(t *testing.T) {
		resp, body := get(t, "/up/a.txt", map[string]string{"Accept-Encoding": "gzip"})
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
		assert.Empty(t, resp.Header.Get("Accept-Ranges"))
		assert.Equal(t, strconv.Itoa(len(body)), resp.Header.Get("Content-Length"))
		assert.Equal(t, `W/"a.txt-v1"`, resp.Header.Get("ETag"))
		assert.Equal(t, []string{"Accept-Encoding"}, resp.Header.Values("Vary"))
		assert.Equal(t, string(txt), decodeBody(t, "gzip", body))
	})

	t.Run("if-range with weak etag returns full response", func(t *testing.T) {
		resp, body := get(t, "/up/a.txt", map[string]string{"Accept-Encoding": "gzip", "Range": "bytes=10-99", "If-Range": `W/"a.txt-v1"`})
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
		assert.Equal(t, string(txt), decodeBody(t, "gzip", body))
	})

	t.Run("client without gzip gets plain", func(t *testing.T) {
		resp, body := get(t, "/up/a.txt", nil)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Empty(t, resp.Header.Get("Content-Encoding"))
		assert.Equal(t, "bytes", resp.Header.Get("Accept-Ranges"))
		assert.Equal(t, txt, body)
	})

	t.Run("upstream compressed response passes through once", func(t *testing.T) {
		resp, body := get(t, "/up/pregz", map[string]string{"Accept-Encoding": "gzip"})
		assert.Equal(t, "gzip", lastAccept.Load())
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
		assert.Empty(t, resp.Header.Get("Content-Type"))
		assert.Equal(t, gzTxt.Bytes(), body)
	})

	t.Run("upstream gzip decoded for client without gzip", func(t *testing.T) {
		resp, body := get(t, "/up/pregz", nil)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Empty(t, resp.Header.Get("Content-Encoding"))
		assert.Equal(t, txt, body)
	})

	t.Run("event stream flushed through compression", func(t *testing.T) {
		req, err := http.NewRequest("GET", baseURL+"/up/sse", http.NoBody)
		require.NoError(t, err)
		req.Header.Set("Accept-Encoding", "gzip")
		resp, err := rawClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
		zr, err := gzip.NewReader(resp.Body)
		require.NoError(t, err)
		first := make([]byte, len("data: one\n\n"))
		_, err = io.ReadFull(zr, first)
		require.NoError(t, err)
		assert.Equal(t, "data: one\n\n", string(first))
		releaseSSE()
		rest, err := io.ReadAll(zr)
		require.NoError(t, err)
		assert.Equal(t, "data: two\n\n", string(rest))
	})

	t.Run("upgrade bypasses compression", func(t *testing.T) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
		require.NoError(t, err)
		defer conn.Close()
		require.NoError(t, conn.SetDeadline(time.Now().Add(5*time.Second)))
		_, err = fmt.Fprintf(conn, "GET /up/upgrade HTTP/1.1\r\nHost: 127.0.0.1:%d\r\nConnection: Upgrade\r\nUpgrade: echo\r\n"+
			"Accept-Encoding: gzip\r\n\r\n", port)
		require.NoError(t, err)
		br := bufio.NewReader(conn)
		resp, err := http.ReadResponse(br, nil)
		require.NoError(t, err)
		assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
		assert.Empty(t, resp.Header.Get("Content-Encoding"))
		_, err = conn.Write([]byte("ping\n"))
		require.NoError(t, err)
		line, err := br.ReadString('\n')
		require.NoError(t, err)
		assert.Equal(t, "echo ping\n", line)
	})

	t.Run("static asset compressed and range plain", func(t *testing.T) {
		resp, body := get(t, "/static/1.html", map[string]string{"Accept-Encoding": "gzip"})
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
		assert.Equal(t, "test html", decodeBody(t, "gzip", body))

		resp, body = get(t, "/static/1.html", map[string]string{"Accept-Encoding": "gzip", "Range": "bytes=0-3"})
		assert.Equal(t, http.StatusPartialContent, resp.StatusCode)
		assert.Empty(t, resp.Header.Get("Content-Encoding"))
		assert.Equal(t, "test", string(body))
	})
}

func TestHttp_UpstreamAbortReachesClient(t *testing.T) {
	// regression: the recoverer swallowed http.ErrAbortHandler and ended a truncated body as a complete response
	var release atomic.Pointer[chan struct{}]
	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, brw, err := http.NewResponseController(w).Hijack()
		if !assert.NoError(t, err) {
			return
		}
		defer conn.Close()
		chunk := strings.Repeat("partial body line\n", 500)
		_, _ = fmt.Fprintf(brw, "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nTransfer-Encoding: chunked\r\n\r\n%x\r\n%s\r\n",
			len(chunk), chunk)
		_ = brw.Flush()
		<-*release.Load()
	}))
	defer ds.Close()

	port, releasePort := getFreePort(t)
	h := Http{Timeouts: Timeouts{ResponseHeader: time.Second}, Address: fmt.Sprintf("127.0.0.1:%d", port), AccessLog: io.Discard,
		GzEnabled: true, GzExclude: testGzipExclude, Reporter: &ErrorReporter{}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	svc := discovery.NewService([]discovery.Provider{
		&provider.Static{Rules: []string{"127.0.0.1,^/up/(.*)," + ds.URL + "/$1,"}},
	}, time.Millisecond*10)
	go func() { _ = svc.Run(t.Context()) }()
	time.Sleep(50 * time.Millisecond)
	h.Matcher = svc
	h.Metrics = mgmt.NewMetrics(mgmt.MetricsConfig{})
	releasePort()
	go func() { _ = h.Run(ctx) }()
	waitForServer(t, fmt.Sprintf("127.0.0.1:%d", port))

	tbl := []struct {
		name     string
		accept   string
		encoding string
	}{
		{name: "compressed", accept: "gzip", encoding: "gzip"},
		{name: "plain", accept: "identity", encoding: ""},
	}
	for _, tt := range tbl {
		t.Run(tt.name, func(t *testing.T) {
			ch := make(chan struct{})
			var releaseOnce sync.Once
			releaseUpstream := func() { releaseOnce.Do(func() { close(ch) }) }
			release.Store(&ch)
			defer releaseUpstream()

			req, err := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/up/drop", port), http.NoBody)
			require.NoError(t, err)
			req.Header.Set("Accept-Encoding", tt.accept)
			resp, err := (&http.Client{Transport: &http.Transport{DisableCompression: true}, Timeout: 5 * time.Second}).Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, tt.encoding, resp.Header.Get("Content-Encoding"))

			var rd io.Reader = resp.Body
			if tt.encoding == "gzip" {
				zr, zerr := gzip.NewReader(resp.Body)
				require.NoError(t, zerr)
				rd = zr
			}
			prefix := make([]byte, 100)
			_, err = io.ReadFull(rd, prefix)
			require.NoError(t, err)
			assert.Equal(t, strings.Repeat("partial body line\n", 6)[:100], string(prefix))

			releaseUpstream()
			rest, err := io.ReadAll(rd)
			require.ErrorIs(t, err, io.ErrUnexpectedEOF, "client must see the truncation")
			assert.NotContains(t, string(rest), "Internal Server Error")
		})
	}
}

func TestHttp_ForwardedHeaders(t *testing.T) {
	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "xri=%s|xff=%s|xfh=%s|xfp=%s|xfport=%s|fwd=%s", r.Header.Get("X-Real-IP"),
			strings.Join(r.Header.Values("X-Forwarded-For"), ";"), strings.Join(r.Header.Values("X-Forwarded-Host"), ";"),
			r.Header.Get("X-Forwarded-Proto"), r.Header.Get("X-Forwarded-Port"), r.Header.Get("Forwarded"))
	}))
	defer ds.Close()

	svc := discovery.NewService([]discovery.Provider{&provider.Static{Rules: []string{"*,^/api/(.*)," + ds.URL + "/$1,"}}},
		time.Millisecond*10)
	go func() { _ = svc.Run(t.Context()) }()
	time.Sleep(50 * time.Millisecond)

	client := http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	startProxy := func(t *testing.T, trusted TrustedProxies, ssl bool) (baseURL, host string) {
		port, releasePort := getFreePort(t)
		h := Http{Timeouts: Timeouts{ResponseHeader: 200 * time.Millisecond}, Address: fmt.Sprintf("127.0.0.1:%d", port),
			AccessLog: io.Discard, Reporter: &ErrorReporter{Nice: true}, Matcher: svc,
			Metrics: mgmt.NewMetrics(mgmt.MetricsConfig{}), TrustedProxies: trusted}
		scheme := "http"
		if ssl {
			h.SSLConfig = SSLConfig{SSLMode: SSLStatic, Cert: "testdata/localhost.crt", Key: "testdata/localhost.key",
				NoHTTPRedirect: true}
			scheme = "https"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		t.Cleanup(cancel)
		releasePort()
		go func() { _ = h.Run(ctx) }()
		host = "127.0.0.1:" + strconv.Itoa(port)
		require.Eventually(t, func() bool {
			resp, err := client.Get(scheme + "://" + host + "/ping")
			if err != nil {
				return false
			}
			resp.Body.Close()
			return resp.StatusCode == http.StatusOK
		}, 5*time.Second, 10*time.Millisecond, "server failed to start")
		return scheme + "://" + host, host
	}

	type testCase struct {
		name    string
		headers map[string][]string
		want    string
	}
	runCases := func(t *testing.T, baseURL string, tbl []testCase) {
		for _, tt := range tbl {
			t.Run(tt.name, func(t *testing.T) {
				req, err := http.NewRequest("GET", baseURL+"/api/something", http.NoBody)
				require.NoError(t, err)
				for k, vals := range tt.headers {
					for _, v := range vals {
						req.Header.Add(k, v)
					}
				}
				resp, err := client.Do(req)
				require.NoError(t, err)
				defer resp.Body.Close()
				require.Equal(t, http.StatusOK, resp.StatusCode)
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				assert.Equal(t, tt.want, string(body))
			})
		}
	}

	t.Run("untrusted client", func(t *testing.T) {
		baseURL, host := startProxy(t, nil, false)
		runCases(t, baseURL, []testCase{
			{name: "no forwarded headers", want: "xri=127.0.0.1|xff=127.0.0.1|xfh=" + host + "|xfp=|xfport=|fwd="},
			{name: "spoofed xff", headers: map[string][]string{"X-Forwarded-For": {"6.6.6.6"}},
				want: "xri=127.0.0.1|xff=6.6.6.6, 127.0.0.1|xfh=" + host + "|xfp=|xfport=|fwd="},
			{name: "spoofed xff in two fields", headers: map[string][]string{"X-Forwarded-For": {"6.6.6.6", "7.7.7.7"}},
				want: "xri=127.0.0.1|xff=6.6.6.6, 7.7.7.7, 127.0.0.1|xfh=" + host + "|xfp=|xfport=|fwd="},
			{name: "spoofed x-real-ip", headers: map[string][]string{"X-Real-Ip": {"7.7.7.7"}},
				want: "xri=127.0.0.1|xff=127.0.0.1|xfh=" + host + "|xfp=|xfport=|fwd="},
			{name: "spoofed host, proto, port and forwarded", headers: map[string][]string{"X-Forwarded-Host": {"evil.example.com"},
				"X-Forwarded-Proto": {"https"}, "X-Forwarded-Port": {"443"}, "Forwarded": {"for=6.6.6.6"}},
				want: "xri=127.0.0.1|xff=127.0.0.1|xfh=" + host + "|xfp=|xfport=|fwd="},
			{name: "forwarded headers nominated in Connection",
				headers: map[string][]string{"Connection": {"X-Real-IP, X-Forwarded-Host, X-Forwarded-For"}},
				want:    "xri=127.0.0.1|xff=127.0.0.1|xfh=" + host + "|xfp=|xfport=|fwd="},
			{name: "spoofed xff nominated in Connection", headers: map[string][]string{"X-Forwarded-For": {"6.6.6.6"},
				"Connection": {"X-Forwarded-For"}},
				want: "xri=127.0.0.1|xff=127.0.0.1|xfh=" + host + "|xfp=|xfport=|fwd="},
		})
	})

	t.Run("untrusted client, tls", func(t *testing.T) {
		baseURL, host := startProxy(t, nil, true)
		runCases(t, baseURL, []testCase{
			{name: "no forwarded headers", want: "xri=127.0.0.1|xff=127.0.0.1|xfh=" + host + "|xfp=https|xfport=443|fwd="},
			{name: "spoofed proto and port", headers: map[string][]string{"X-Forwarded-Proto": {"http"}, "X-Forwarded-Port": {"80"}},
				want: "xri=127.0.0.1|xff=127.0.0.1|xfh=" + host + "|xfp=https|xfport=443|fwd="},
			{name: "forwarded headers nominated in Connection",
				headers: map[string][]string{"Connection": {"X-Real-IP, X-Forwarded-Proto, X-Forwarded-Port"}},
				want:    "xri=127.0.0.1|xff=127.0.0.1|xfh=" + host + "|xfp=https|xfport=443|fwd="},
		})
	})

	trusted, err := ParseTrustedProxies([]string{"127.0.0.1"})
	require.NoError(t, err)

	t.Run("trusted proxy", func(t *testing.T) {
		baseURL, host := startProxy(t, trusted, false)
		runCases(t, baseURL, []testCase{
			{name: "no forwarded headers", want: "xri=127.0.0.1|xff=127.0.0.1|xfh=" + host + "|xfp=|xfport=|fwd="},
			{name: "client from xff", headers: map[string][]string{"X-Forwarded-For": {"6.6.6.6, 203.0.113.9"}},
				want: "xri=203.0.113.9|xff=6.6.6.6, 203.0.113.9, 127.0.0.1|xfh=" + host + "|xfp=|xfport=|fwd="},
			{name: "host, proto, port and forwarded kept", headers: map[string][]string{"X-Forwarded-Host": {"front.example.com"},
				"X-Forwarded-Proto": {"https"}, "X-Forwarded-Port": {"443"}, "Forwarded": {"for=203.0.113.9"}},
				want: "xri=127.0.0.1|xff=127.0.0.1|xfh=front.example.com;" + host + "|xfp=https|xfport=443|fwd=for=203.0.113.9"},
			{name: "headers nominated in Connection not kept", headers: map[string][]string{"X-Forwarded-For": {"203.0.113.9"},
				"X-Forwarded-Host": {"front.example.com"}, "Connection": {"X-Forwarded-Host, X-Forwarded-For"}},
				want: "xri=203.0.113.9|xff=127.0.0.1|xfh=" + host + "|xfp=|xfport=|fwd="},
		})
	})

	t.Run("trusted proxy, tls", func(t *testing.T) {
		baseURL, host := startProxy(t, trusted, true)
		runCases(t, baseURL, []testCase{
			{name: "no forwarded headers", want: "xri=127.0.0.1|xff=127.0.0.1|xfh=" + host + "|xfp=https|xfport=443|fwd="},
			{name: "proto and port kept", headers: map[string][]string{"X-Forwarded-Proto": {"http"}, "X-Forwarded-Port": {"8080"}},
				want: "xri=127.0.0.1|xff=127.0.0.1|xfh=" + host + "|xfp=http|xfport=8080|fwd="},
		})
	})
}

func TestHttp_setXRealIP(t *testing.T) {
	tbl := []struct {
		name       string
		remoteAddr string
		realIP     string
		want       string
	}{
		{name: "peer overrides client header", remoteAddr: "203.0.113.9:1234", realIP: "7.7.7.7", want: "203.0.113.9"},
		{name: "zoned peer overrides client header", remoteAddr: "[fe80::1%en0]:1234", realIP: "7.7.7.7", want: "fe80::1"},
		{name: "unparsable peer drops client header", remoteAddr: "garbage", realIP: "7.7.7.7", want: ""},
	}

	for _, tt := range tbl {
		t.Run(tt.name, func(t *testing.T) {
			h := Http{}
			in := httptest.NewRequest(http.MethodGet, "http://example.com/", http.NoBody)
			in.RemoteAddr = tt.remoteAddr
			out := in.Clone(t.Context())
			out.Header.Set("X-Real-IP", tt.realIP)
			h.setXRealIP(in, out)
			assert.Equal(t, tt.want, out.Header.Get("X-Real-IP"))
		})
	}
}
