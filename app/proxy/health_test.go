package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/reproxy/app/discovery"
	"github.com/umputun/reproxy/app/discovery/provider"
)

func TestHttp_healthHandler(t *testing.T) {
	port := rand.Intn(10000) + 40000
	h := Http{TimeOut: 200 * time.Millisecond, Address: fmt.Sprintf("127.0.0.1:%d", port)}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("req: %v", r)
		w.Header().Add("h1", "v1")
		fmt.Fprintf(w, "response %s", r.URL.String())
	}))

	ps := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("req: %v", r)
		if r.URL.Path == "/123/ping" {
			return
		}
		w.WriteHeader(http.StatusBadRequest)
	}))

	svc := discovery.NewService([]discovery.Provider{
		&provider.Static{Rules: []string{
			"localhost,^/api/(.*)," + ds.URL + "/123/$1," + ps.URL + "/123/ping",
			"127.0.0.1,^/api/(.*)," + ds.URL + "/567/$1," + ps.URL + "/567/ping",
		},
		}})

	go func() {
		_ = svc.Run(context.Background())
	}()

	h.Matcher = svc
	go func() {
		_ = h.Run(ctx)
	}()
	time.Sleep(10 * time.Millisecond)

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
	assert.Equal(t, 1., res["passed"])
	assert.Equal(t, 1., res["failed"])
	assert.Contains(t, res["errors"].([]interface{})[0], "400 Bad Request")
}
