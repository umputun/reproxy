package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
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
	port := getFreePort(t)
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
	h.Metrics = mgmt.NewMetrics()

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
}

func TestHttp_DoWithSSL(t *testing.T) {
	port := getFreePort(t)
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
	h.Metrics = mgmt.NewMetrics()

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
}

func TestHttp_DoWithAssets(t *testing.T) {
	port := getFreePort(t)
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
	h.Metrics = mgmt.NewMetrics()

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
	port := getFreePort(t)
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
	h.Metrics = mgmt.NewMetrics()

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
	port := getFreePort(t)
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
	h.Metrics = mgmt.NewMetrics()

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
	port := getFreePort(t)
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
	h.Metrics = mgmt.NewMetrics()

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
	port := getFreePort(t)
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
	h.Metrics = mgmt.NewMetrics()

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
	port := getFreePort(t)
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
	h.Matcher, h.Metrics = svc, mgmt.NewMetrics()

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
	port := getFreePort(t)
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
	h.Matcher, h.Metrics = svc, mgmt.NewMetrics()

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
	port := getFreePort(t)
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
	h.Matcher, h.Metrics = svc, mgmt.NewMetrics()

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

	var count int32
	matcherMock := &MatcherMock{
		MatchFunc: func(srv string, src string) discovery.Matches {
			return tbl[atomic.LoadInt32(&count)].matches
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
			atomic.AddInt32(&count, 1)
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
	port := getFreePort(t)

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
			Metrics:                 mgmt.NewMetrics(),
			Reporter:                &ErrorReporter{Nice: true},
			UpstreamMaxIdleConns:    100, // default value
			UpstreamMaxConnsPerHost: 0,   // unlimited, default
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

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
		port2 := getFreePort(t)
		h := Http{
			Timeouts:                Timeouts{ResponseHeader: 200 * time.Millisecond},
			Address:                 fmt.Sprintf("127.0.0.1:%d", port2),
			AccessLog:               io.Discard,
			Matcher:                 svc,
			Metrics:                 mgmt.NewMetrics(),
			Reporter:                &ErrorReporter{Nice: true},
			UpstreamMaxIdleConns:    50,
			UpstreamMaxConnsPerHost: 10,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

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

// getFreePort returns a free TCP port allocated by the OS
func getFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// TestHttp_withPerRouteAuth_DefaultInit tests per-route auth when PerRouteAuth is NOT explicitly set.
// this simulates the production scenario where main.go doesn't initialize PerRouteAuth.
// the test verifies that per-route auth still works correctly with default initialization.
func TestHttp_withPerRouteAuth_DefaultInit(t *testing.T) {
	port := getFreePort(t)
	authHash := "$2y$05$zMxDmK65SjcH2vJQNopVSO/nE8ngVLx65RoETyHpez7yTS/8CLEiW" // passwd

	// NOTE: PerRouteAuth is NOT set - this is how main.go currently initializes Http
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

	h.Matcher, h.Metrics = svc, mgmt.NewMetrics()

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
	port := getFreePort(t)
	// test password hash for "secret123" generated with htpasswd -nbB test secret123
	authHash := "$2y$05$zMxDmK65SjcH2vJQNopVSO/nE8ngVLx65RoETyHpez7yTS/8CLEiW" // passwd

	h := Http{
		Timeouts:     Timeouts{ResponseHeader: 200 * time.Millisecond},
		Address:      fmt.Sprintf("127.0.0.1:%d", port),
		AccessLog:    io.Discard,
		Reporter:     &ErrorReporter{Nice: true},
		PerRouteAuth: NewPerRouteAuth(),
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

	h.Matcher, h.Metrics = svc, mgmt.NewMetrics()

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

func TestHttp_withGlobalAndPerRouteAuth(t *testing.T) {
	port := getFreePort(t)
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

	h.Matcher, h.Metrics = svc, mgmt.NewMetrics()

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
