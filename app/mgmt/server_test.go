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
	"github.com/umputun/reproxy/app/plugin"
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
		AssetsWebRoot: "/static", AssetsLocation: "/www", Metrics: NewMetrics(MetricsConfig{})}
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

		data := map[string][]any{}
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
	metrics := NewMetrics(MetricsConfig{})

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

func TestMetrics_LowCardinality(t *testing.T) {
	t.Run("low cardinality uses route pattern when match in context", func(t *testing.T) {
		metrics := NewMetrics(MetricsConfig{LowCardinality: true})

		var capturedPath string
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// capture the path that would be used for metrics by calling getRoutePattern
			capturedPath = metrics.getRoutePattern(r)
			w.WriteHeader(http.StatusOK)
		})

		wrapped := metrics.Middleware(handler)

		// create request with matched route in context
		req := httptest.NewRequest("GET", "http://example.com/api/users/123", http.NoBody)
		matchedRoute := discovery.MatchedRoute{
			Mapper: discovery.URLMapper{
				SrcMatch: *regexp.MustCompile(`^/api/users/(.*)`),
			},
		}
		ctx := context.WithValue(req.Context(), plugin.CtxMatch, matchedRoute)
		req = req.WithContext(ctx)

		wr := httptest.NewRecorder()
		wrapped.ServeHTTP(wr, req)

		assert.Equal(t, http.StatusOK, wr.Code)
		assert.Equal(t, `^/api/users/(.*)`, capturedPath)
	})

	t.Run("low cardinality falls back to unmatched when no context", func(t *testing.T) {
		metrics := NewMetrics(MetricsConfig{LowCardinality: true})

		var capturedPath string
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedPath = metrics.getRoutePattern(r)
			w.WriteHeader(http.StatusNotFound)
		})

		wrapped := metrics.Middleware(handler)

		// request without matched route in context
		req := httptest.NewRequest("GET", "http://example.com/unknown/path", http.NoBody)
		wr := httptest.NewRecorder()
		wrapped.ServeHTTP(wr, req)

		assert.Equal(t, http.StatusNotFound, wr.Code)
		assert.Equal(t, "[unmatched]", capturedPath)
	})

	t.Run("default mode uses raw path", func(t *testing.T) {
		metrics := NewMetrics(MetricsConfig{LowCardinality: false})

		handler := metrics.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		// even with matched route in context, should use raw path
		req := httptest.NewRequest("GET", "http://example.com/api/users/123", http.NoBody)
		matchedRoute := discovery.MatchedRoute{
			Mapper: discovery.URLMapper{
				SrcMatch: *regexp.MustCompile(`^/api/users/(.*)`),
			},
		}
		ctx := context.WithValue(req.Context(), plugin.CtxMatch, matchedRoute)
		req = req.WithContext(ctx)

		wr := httptest.NewRecorder()
		handler.ServeHTTP(wr, req)

		assert.Equal(t, http.StatusOK, wr.Code)
		// in default mode, raw path is used (verified by the middleware behavior)
		// we can't easily capture the exact label value, but we verify no panic occurs
	})
}

func TestMetrics_getRoutePattern(t *testing.T) {
	metrics := &Metrics{lowCardinality: true}

	t.Run("returns route pattern when match in context", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", http.NoBody)
		matchedRoute := discovery.MatchedRoute{
			Mapper: discovery.URLMapper{
				SrcMatch: *regexp.MustCompile(`^/test/(.*)`),
			},
		}
		ctx := context.WithValue(req.Context(), plugin.CtxMatch, matchedRoute)
		req = req.WithContext(ctx)

		result := metrics.getRoutePattern(req)
		assert.Equal(t, `^/test/(.*)`, result)
	})

	t.Run("returns unmatched when no context value", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", http.NoBody)
		result := metrics.getRoutePattern(req)
		assert.Equal(t, "[unmatched]", result)
	})

	t.Run("returns unmatched when context value is wrong type", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/test", http.NoBody)
		ctx := context.WithValue(req.Context(), plugin.CtxMatch, "wrong type")
		req = req.WithContext(ctx)

		result := metrics.getRoutePattern(req)
		assert.Equal(t, "[unmatched]", result)
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
