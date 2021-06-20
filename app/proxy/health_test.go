package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/reproxy/app/discovery"
	"github.com/umputun/reproxy/app/discovery/provider"
	"github.com/umputun/reproxy/app/mgmt"
)

func Test_healthHandlerDeadlock(t *testing.T) {

	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.True(t, strings.HasSuffix(r.RequestURI, "/ping"))
		time.Sleep(time.Millisecond * time.Duration(rand.Intn(5)))
		if rand.Intn(10) == 5 {
			w.WriteHeader(400)
		}
	}))

	rules := make([]string, 0, 90)
	for i := 0; i < cap(rules); i++ {
		rules = append(rules, fmt.Sprintf("*,^/api/(.*),localhost/%d/$1,%s/%d/$1/ping", i, ds.URL, i))
	}

	svc := discovery.NewService([]discovery.Provider{
		&provider.Static{Rules: rules}}, time.Millisecond*10)
	go func() {
		_ = svc.Run(context.Background())
	}()
	time.Sleep(20 * time.Millisecond)

	h := Http{}
	h.Matcher = svc

	rr := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		h.healthHandler(rr, &http.Request{})
		done <- struct{}{}
	}()

	select {
	case <-done:
	case <-time.After(time.Millisecond * 100):
		assert.Fail(t, "deadlock")
	}
}

func TestHttp_healthHandler(t *testing.T) {
	port := rand.Intn(10000) + 40000
	h := Http{Timeouts: Timeouts{ResponseHeader: 200 * time.Millisecond}, Address: fmt.Sprintf("127.0.0.1:%d", port)}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("req: %v", r)
		w.Header().Add("h1", "v1")
		fmt.Fprintf(w, "response %s", r.URL.String())
	}))

	var count int
	ps := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		t.Logf("req: %v", r)
		if r.URL.Path == "/123/ping" {
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))

	svc := discovery.NewService([]discovery.Provider{
		&provider.Static{Rules: []string{
			"localhost,^/api/(.*)," + ds.URL + "/123/$1," + ps.URL + "/123/ping",
			"localhost,^/xyz/(.*)," + ds.URL + "/123/$1," + ps.URL + "/xxx/ping",
			"127.0.0.1,^/api/(.*)," + ds.URL + "/567/$1," + ps.URL + "/567/ping",
			"127.0.0.1,^/api/(.*)," + ds.URL + "/567/$1,",
			"127.0.0.1,^/api/(.*),assets:/567/$1," + ps.URL + "/567/ping",
		},
		}}, time.Millisecond*10)

	go func() {
		_ = svc.Run(context.Background())
	}()

	h.Matcher = svc
	h.Metrics = mgmt.NewMetrics(false, 503)
	go func() {
		_ = h.Run(ctx)
	}()
	time.Sleep(50 * time.Millisecond)

	client := http.Client{}
	req, err := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(port)+"/health", nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusExpectationFailed, resp.StatusCode)

	res := map[string]interface{}{}
	err = json.NewDecoder(resp.Body).Decode(&res)
	require.NoError(t, err)
	assert.Equal(t, "failed", res["status"])
	assert.Equal(t, 4., res["services"])
	assert.Equal(t, 1., res["passed"])
	assert.Equal(t, 2., res["failed"])
	assert.Equal(t, 2, len(res["errors"].([]interface{})))
	assert.Contains(t, res["errors"].([]interface{})[0], "400 Bad Request")
	assert.Equal(t, 3, count, "3 pings for non-assets routes")
}

func TestHttp_pingHandler(t *testing.T) {
	port := rand.Intn(10000) + 40000
	h := Http{Timeouts: Timeouts{ResponseHeader: 200 * time.Millisecond}, Address: fmt.Sprintf("127.0.0.1:%d", port)}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	svc := discovery.NewService([]discovery.Provider{
		&provider.Static{Rules: []string{
			"localhost,^/xyz/(.*),/123/$1,/xxx/ping",
		},
		}}, time.Millisecond*10)

	go func() {
		_ = svc.Run(context.Background())
	}()

	h.Matcher = svc
	h.Metrics = mgmt.NewMetrics(false, 503)

	go func() {
		_ = h.Run(ctx)
	}()
	time.Sleep(20 * time.Millisecond)

	client := http.Client{}
	req, err := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(port)+"/ping", nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	b, err := ioutil.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "pong", string(b))
}
