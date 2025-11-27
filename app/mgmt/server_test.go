package mgmt

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/reproxy/app/discovery"
)

func TestServer_controllers(t *testing.T) {
	inf := &InformerMock{
		MappersFunc: func() []discovery.URLMapper {
			return []discovery.URLMapper{
				{
					Server: "srv1", MatchType: discovery.MTProxy,
					SrcMatch: *regexp.MustCompile("/api/(.*)"), Dst: "/blah/$1", ProviderID: discovery.PIFile,
					PingURL: "http://example.com/ping",
				},
				{
					Server: "srv2", MatchType: discovery.MTStatic,
					SrcMatch: *regexp.MustCompile("/api2/(.*)"), Dst: "/blah2/$1", ProviderID: discovery.PIDocker,
					PingURL: "http://example.com/ping2",
				},
				{
					Server: "srv2", MatchType: discovery.MTProxy,
					SrcMatch: *regexp.MustCompile("/api3/(.*)"), Dst: "/blah3/$1", ProviderID: discovery.PIDocker,
					PingURL: "http://example.com/ping3",
				},
			}
		},
	}

	port := rand.Intn(10000) + 40000
	srv := Server{Listen: fmt.Sprintf("127.0.0.1:%d", port), Informer: inf,
		AssetsWebRoot: "/static", AssetsLocation: "/www", Metrics: NewMetrics()}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	time.AfterFunc(time.Second, func() {
		cancel()
	})

	done := make(chan struct{})
	go func() {
		srv.Run(ctx)
		t.Logf("server terminated")
		done <- struct{}{}
	}()

	time.Sleep(10 * time.Millisecond)

	client := http.Client{}
	{
		req, err := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(port)+"/ping", http.NoBody)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		t.Logf("%+v", resp.Header)
		assert.Equal(t, "reproxy-mgmt", resp.Header.Get("App-Name"))
	}

	{
		req, err := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(port)+"/routes", http.NoBody)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		data := map[string][]interface{}{}
		err = json.NewDecoder(resp.Body).Decode(&data)
		require.NoError(t, err)

		assert.Len(t, data["srv1"], 1)
		assert.Len(t, data["srv2"], 2)

		assert.Contains(t, fmt.Sprintf("%v", data["srv1"][0]), `destination:/blah/$1`, data["srv1"][0])
		assert.Contains(t, fmt.Sprintf("%v", data["srv1"][0]), `route:/api/(.*)`, data["srv1"][0])
		assert.Contains(t, fmt.Sprintf("%v", data["srv1"][0]), `match:proxy`, data["srv1"][0])
		assert.Contains(t, fmt.Sprintf("%v", data["srv1"][0]), `provider:file`, data["srv1"][0])
		assert.Contains(t, fmt.Sprintf("%v", data["srv1"][0]), `ping:http://example.com/ping`, data["srv1"][0])
	}
	{
		req, err := http.NewRequest("GET", "http://127.0.0.1:"+strconv.Itoa(port)+"/metrics", http.NoBody)
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		t.Logf("%s", string(body))

		assert.Contains(t, string(body), "promhttp_metric_handler_requests_total{code=\"200\"")
		assert.Contains(t, string(body), "promhttp_metric_handler_requests_total counter")
	}
	<-done
}

func TestMetrics_Middleware(t *testing.T) {
	metrics := NewMetrics()

	handler := metrics.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("test response"))
	}))

	t.Run("with host header", func(t *testing.T) {
		req := httptest.NewRequest("GET", "http://example.com/test/path", http.NoBody)
		req.Host = "example.com:8080"
		wr := httptest.NewRecorder()
		handler.ServeHTTP(wr, req)
		assert.Equal(t, http.StatusCreated, wr.Code)
		assert.Equal(t, "test response", wr.Body.String())
	})

	t.Run("with url hostname", func(t *testing.T) {
		req := httptest.NewRequest("POST", "http://api.example.com/api/v1", http.NoBody)
		wr := httptest.NewRecorder()
		handler.ServeHTTP(wr, req)
		assert.Equal(t, http.StatusCreated, wr.Code)
	})

	t.Run("empty host uses request host", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/path", http.NoBody)
		req.Host = "localhost"
		wr := httptest.NewRecorder()
		handler.ServeHTTP(wr, req)
		assert.Equal(t, http.StatusCreated, wr.Code)
	})
}

func TestResponseWriter(t *testing.T) {
	t.Run("default status code", func(t *testing.T) {
		wr := httptest.NewRecorder()
		rw := NewResponseWriter(wr)
		assert.Equal(t, http.StatusOK, rw.statusCode)
	})

	t.Run("write header changes status", func(t *testing.T) {
		wr := httptest.NewRecorder()
		rw := NewResponseWriter(wr)
		rw.WriteHeader(http.StatusNotFound)
		assert.Equal(t, http.StatusNotFound, rw.statusCode)
		assert.Equal(t, http.StatusNotFound, wr.Code)
	})

	t.Run("hijack not supported", func(t *testing.T) {
		wr := httptest.NewRecorder()
		rw := NewResponseWriter(wr)
		conn, buf, err := rw.Hijack()
		assert.Nil(t, conn)
		assert.Nil(t, buf)
		require.Error(t, err)
		assert.Equal(t, "hijack not supported", err.Error())
	})
}

type hijackableResponseWriter struct {
	http.ResponseWriter
	hijacked bool
}

func (h *hijackableResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.hijacked = true
	server, client := net.Pipe()
	go func() { _ = server.Close() }()
	return client, bufio.NewReadWriter(bufio.NewReader(client), bufio.NewWriter(client)), nil
}

func TestResponseWriter_HijackSupported(t *testing.T) {
	hw := &hijackableResponseWriter{ResponseWriter: httptest.NewRecorder()}
	rw := NewResponseWriter(hw)
	conn, buf, err := rw.Hijack()
	require.NoError(t, err)
	assert.NotNil(t, conn)
	assert.NotNil(t, buf)
	assert.True(t, hw.hijacked)
	_ = conn.Close()
}
