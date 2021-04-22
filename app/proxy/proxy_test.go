package proxy

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"strconv"
	"testing"
	"time"

	R "github.com/go-pkgz/rest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/reproxy/app/discovery"
	"github.com/umputun/reproxy/app/discovery/provider"
	"github.com/umputun/reproxy/app/mgmt"
)

func TestHttp_Do(t *testing.T) {
	port := rand.Intn(10000) + 40000
	h := Http{Timeouts: Timeouts{ResponseHeader: 200 * time.Millisecond}, Address: fmt.Sprintf("127.0.0.1:%d", port),
		AccessLog: io.Discard, Signature: true, ProxyHeaders: []string{"hh1:vv1", "hh2:vv2"}, StdOutEnabled: true}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("req: %v", r)
		w.Header().Add("h1", "v1")
		require.Equal(t, "127.0.0.1", r.Header.Get("X-Real-IP"))
		fmt.Fprintf(w, "response %s", r.URL.String())
	}))

	svc := discovery.NewService([]discovery.Provider{
		&provider.Static{Rules: []string{
			"localhost,^/api/(.*)," + ds.URL + "/123/$1,",
			"127.0.0.1,^/api/(.*)," + ds.URL + "/567/$1,",
		},
		}}, time.Millisecond*10)

	go func() {
		_ = svc.Run(context.Background())
	}()

	time.Sleep(50 * time.Millisecond)
	h.Matcher = svc
	h.Metrics = mgmt.NewMetrics()

	go func() {
		_ = h.Run(ctx)
	}()
	time.Sleep(10 * time.Millisecond)

	client := http.Client{}

	{
		req, err := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(port)+"/api/something", nil)
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
	}

	{
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
	}

	{
		resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/bad/something")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusBadGateway, resp.StatusCode)

	}
}

func TestHttp_DoWithAssets(t *testing.T) {
	port := rand.Intn(10000) + 40000
	h := Http{Timeouts: Timeouts{ResponseHeader: 200 * time.Millisecond}, Address: fmt.Sprintf("127.0.0.1:%d", port),
		AccessLog: io.Discard, AssetsWebRoot: "/static", AssetsLocation: "testdata"}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("req: %v", r)
		w.Header().Add("h1", "v1")
		require.Equal(t, "127.0.0.1", r.Header.Get("X-Real-IP"))
		fmt.Fprintf(w, "response %s", r.URL.String())
	}))

	svc := discovery.NewService([]discovery.Provider{
		&provider.Static{Rules: []string{
			"localhost,^/api/(.*)," + ds.URL + "/123/$1,",
			"127.0.0.1,^/api/(.*)," + ds.URL + "/567/$1,",
		},
		}}, time.Millisecond*10)

	go func() {
		_ = svc.Run(context.Background())
	}()
	time.Sleep(50 * time.Millisecond)
	h.Matcher = svc
	h.Metrics = mgmt.NewMetrics()

	go func() {
		_ = h.Run(ctx)
	}()
	time.Sleep(10 * time.Millisecond)

	client := http.Client{}

	{
		req, err := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(port)+"/api/something", nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		t.Logf("%+v", resp.Header)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "response /567/something", string(body))
		assert.Equal(t, "", resp.Header.Get("App-Name"))
		assert.Equal(t, "v1", resp.Header.Get("h1"))
	}

	{
		resp, err := client.Get("http://localhost:" + strconv.Itoa(port) + "/static/1.html")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		t.Logf("%+v", resp.Header)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "test html", string(body))
		assert.Equal(t, "", resp.Header.Get("App-Name"))
		assert.Equal(t, "", resp.Header.Get("h1"))
	}

}

func TestHttp_DoWithAssetRules(t *testing.T) {
	port := rand.Intn(10000) + 40000
	h := Http{Timeouts: Timeouts{ResponseHeader: 200 * time.Millisecond}, Address: fmt.Sprintf("127.0.0.1:%d", port),
		AccessLog: io.Discard}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("req: %v", r)
		w.Header().Add("h1", "v1")
		require.Equal(t, "127.0.0.1", r.Header.Get("X-Real-IP"))
		fmt.Fprintf(w, "response %s", r.URL.String())
	}))

	svc := discovery.NewService([]discovery.Provider{
		&provider.Static{Rules: []string{
			"localhost,^/api/(.*)," + ds.URL + "/123/$1,",
			"127.0.0.1,^/api/(.*)," + ds.URL + "/567/$1,",
			"*,/web,assets:testdata,",
		},
		}}, time.Millisecond*10)

	go func() {
		_ = svc.Run(context.Background())
	}()
	time.Sleep(50 * time.Millisecond)
	h.Matcher = svc
	h.Metrics = mgmt.NewMetrics()

	go func() {
		_ = h.Run(ctx)
	}()
	time.Sleep(10 * time.Millisecond)

	client := http.Client{}

	{
		req, err := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(port)+"/api/something", nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		t.Logf("%+v", resp.Header)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "response /567/something", string(body))
		assert.Equal(t, "", resp.Header.Get("App-Name"))
		assert.Equal(t, "v1", resp.Header.Get("h1"))
	}

	{
		resp, err := client.Get("http://localhost:" + strconv.Itoa(port) + "/web/1.html")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		t.Logf("%+v", resp.Header)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "test html", string(body))
		assert.Equal(t, "", resp.Header.Get("App-Name"))
		assert.Equal(t, "", resp.Header.Get("h1"))
	}

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
	for i, tt := range tbl {
		tt := tt
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			assert.Equal(t, tt.res, h.toHTTP(tt.addr, tt.port))
		})
	}

}

func TestHttp_cachingHandler(t *testing.T) {

	dir, e := ioutil.TempDir(os.TempDir(), "reproxy")
	require.NoError(t, e)
	e = ioutil.WriteFile(path.Join(dir, "1.html"), []byte("1.htm"), 0600)
	assert.NoError(t, e)
	e = ioutil.WriteFile(path.Join(dir, "2.html"), []byte("2.htm"), 0600)
	assert.NoError(t, e)
	e = ioutil.WriteFile(path.Join(dir, "index.html"), []byte("index.htm"), 0600)
	assert.NoError(t, e)

	defer os.RemoveAll(dir)

	fh, e := R.FileServer("/static", dir)
	require.NoError(t, e)
	h := Http{AssetsCacheDuration: 10 * time.Second, AssetsLocation: dir, AssetsWebRoot: "/static"}
	hh := R.Wrap(fh, h.cachingHandler("/static", dir))
	ts := httptest.NewServer(hh)
	defer ts.Close()
	client := http.Client{Timeout: 599 * time.Second}

	var lastEtag string
	{
		resp, err := client.Get(ts.URL + "/static/1.html")
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		t.Logf("headers: %+v", resp.Header)
		lastEtag = resp.Header.Get("Etag")
	}

	{
		resp, err := client.Get(ts.URL + "/static/1.html")
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		t.Logf("headers: %+v", resp.Header)
		assert.Equal(t, lastEtag, resp.Header.Get("Etag"), "still the same")
	}
	{
		e = os.Chtimes(path.Join(dir, "1.html"), time.Now(), time.Now())
		assert.NoError(t, e)
		resp, err := client.Get(ts.URL + "/static/1.html")
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		t.Logf("headers: %+v", resp.Header)
		assert.NotEqual(t, lastEtag, resp.Header.Get("Etag"), "changed")
	}

	{
		req, err := http.NewRequest("POST", ts.URL+"/static/1.html", nil)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		t.Logf("headers: %+v", resp.Header)
		assert.Equal(t, "", resp.Header.Get("Etag"), "no etag for post")
	}

	{
		resp, err := client.Get(ts.URL + "/static")
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		lastEtag = resp.Header.Get("Etag")
		t.Logf("headers: %+v", resp.Header)
	}
	{
		e = os.Chtimes(path.Join(dir, "index.html"), time.Now(), time.Now())
		assert.NoError(t, e)
		resp, err := client.Get(ts.URL + "/static")
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		t.Logf("headers: %+v", resp.Header)
		assert.NotEqual(t, lastEtag, resp.Header.Get("Etag"), "changed")
	}
}

func TestHttp_cachingHandlerInvalid(t *testing.T) {
	dir := "/tmp/reproxy"
	os.Mkdir("/tmp/reproxy", 0700)
	defer os.RemoveAll("/tmp/reproxy")
	fh, e := R.FileServer("/static", dir)
	require.NoError(t, e)
	h := Http{AssetsCacheDuration: 10 * time.Second, AssetsLocation: dir, AssetsWebRoot: "/static"}
	hh := R.Wrap(fh, h.cachingHandler("/static", dir))
	ts := httptest.NewServer(hh)
	defer ts.Close()
	client := http.Client{Timeout: 599 * time.Second}
	{
		resp, err := client.Get(ts.URL + "/%2e%2e%2f%2e%2e%2f%2e%2e%2f/etc/passwd")
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		etag := resp.Header.Get("Etag")
		t.Logf("headers: %+v", resp.Header)
		assert.Equal(t, `"4a4032899be1b8e4e2c949cae9f94fdf6acc5cfb"`, etag, "for empty key")
	}
}
