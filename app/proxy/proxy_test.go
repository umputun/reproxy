package proxy

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/umputun/docker-proxy/app/discovery"
	"github.com/umputun/docker-proxy/app/discovery/provider"
)

func TestHttp_Do(t *testing.T) {
	port := rand.Intn(10000) + 40000
	h := Http{TimeOut: 200 * time.Millisecond, Address: fmt.Sprintf("127.0.0.1:%d", port)}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("req: %v", r)
		w.Header().Add("h1", "v1")
		fmt.Fprintf(w, "response %s", r.URL.String())
	}))

	svc := discovery.NewService([]discovery.Provider{
		&provider.Static{Rules: []string{
			"localhost,^/api/(.*)," + ds.URL + "/123/$1",
			"127.0.0.1,^/api/(.*)," + ds.URL + "/567/$1",
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

	{
		resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/api/something")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		t.Logf("%+v", resp.Header)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, "response /567/something", string(body))
		assert.Equal(t, "dpx", resp.Header.Get("App-Name"))
		assert.Equal(t, "v1", resp.Header.Get("h1"))
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
		assert.Equal(t, "dpx", resp.Header.Get("App-Name"))
		assert.Equal(t, "v1", resp.Header.Get("h1"))
	}

	{
		resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/bad/something")
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusBadGateway, resp.StatusCode)

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
			assert.Equal(t, tt.res, h.toHttp(tt.addr, tt.port))
		})
	}

}
