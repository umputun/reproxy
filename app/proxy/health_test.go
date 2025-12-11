package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
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
		assert.True(t, strings.HasSuffix(r.RequestURI, "/ping"))
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
		_ = svc.Run(t.Context())
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
	var mux sync.Mutex
	ps := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.Lock()
		defer mux.Unlock()
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
		_ = svc.Run(t.Context())
	}()

	h.Matcher = svc
	h.Metrics = mgmt.NewMetrics(mgmt.MetricsConfig{})
	go func() {
		_ = h.Run(ctx)
	}()

	// wait for discovery service to load mappers
	time.Sleep(50 * time.Millisecond)

	client := http.Client{}
	var resp *http.Response
	require.Eventually(t, func() bool {
		req, err := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(port)+"/health", http.NoBody)
		if err != nil {
			return false
		}
		resp, err = client.Do(req)
		return err == nil
	}, time.Second, 10*time.Millisecond, "server failed to start")
	defer resp.Body.Close()
	assert.Equal(t, http.StatusExpectationFailed, resp.StatusCode)

	res := map[string]any{}
	err := json.NewDecoder(resp.Body).Decode(&res)
	require.NoError(t, err)
	assert.Equal(t, "failed", res["status"])
	assert.InDelta(t, 4., res["services"], 0.001)
	assert.InDelta(t, 1., res["passed"], 0.001)
	assert.InDelta(t, 2., res["failed"], 0.001)
	assert.Len(t, res["errors"].([]any), 2)
	assert.Contains(t, res["errors"].([]any)[0], "400 Bad Request")
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
		_ = svc.Run(t.Context())
	}()

	h.Matcher = svc
	h.Metrics = mgmt.NewMetrics(mgmt.MetricsConfig{})

	go func() {
		_ = h.Run(ctx)
	}()
	time.Sleep(20 * time.Millisecond)

	client := http.Client{}
	req, err := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(port)+"/ping", http.NoBody)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "pong", string(b))
}
