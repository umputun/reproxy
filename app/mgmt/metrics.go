package mgmt

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/umputun/reproxy/app/discovery"
	"github.com/umputun/reproxy/app/plugin"
)

// MetricsConfig holds configuration for metrics collection
type MetricsConfig struct {
	LowCardinality bool // use route pattern instead of raw path for metrics labels
}

// Metrics provides registration and middleware for prometheus
type Metrics struct {
	totalRequests  *prometheus.CounterVec
	responseStatus *prometheus.CounterVec
	httpDuration   *prometheus.HistogramVec
	lowCardinality bool
}

// NewMetrics creates metrics object with all counters registered
func NewMetrics(cfg MetricsConfig) *Metrics {
	res := &Metrics{lowCardinality: cfg.LowCardinality}

	res.totalRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Number of served requests.",
		},
		[]string{"server"},
	)

	res.responseStatus = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "response_status",
			Help: "Status of HTTP responses.",
		},
		[]string{"status"},
	)

	res.httpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_response_time_seconds",
		Help:    "Duration of HTTP requests.",
		Buckets: []float64{0.01, 0.1, 0.5, 1, 2, 3, 5},
	}, []string{"path"})

	prometheus.Unregister(prometheus.NewGoCollector()) //nolint

	if err := prometheus.Register(res.totalRequests); err != nil {
		log.Printf("[WARN] can't register prometheus totalRequests, %v", err)
	}
	if err := prometheus.Register(res.responseStatus); err != nil {
		log.Printf("[WARN] can't register prometheus responseStatus, %v", err)
	}
	if err := prometheus.Register(res.httpDuration); err != nil {
		log.Printf("[WARN] can't register prometheus httpDuration, %v", err)
	}

	return res
}

// Middleware for the primary proxy server to publish all counters and update metrics
func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path

		// use route pattern instead of raw path if low cardinality mode enabled
		if m.lowCardinality {
			path = m.getRoutePattern(r)
		}

		server := r.URL.Hostname()
		if server == "" {
			server = strings.Split(r.Host, ":")[0]
		}

		timer := prometheus.NewTimer(m.httpDuration.WithLabelValues(path))
		rw := NewResponseWriter(w)
		next.ServeHTTP(rw, r)

		statusCode := rw.statusCode
		m.responseStatus.WithLabelValues(strconv.Itoa(statusCode)).Inc()
		m.totalRequests.WithLabelValues(server).Inc()

		timer.ObserveDuration()
	})
}

// getRoutePattern extracts the route pattern from request context.
// Falls back to "[unmatched]" if no match found (e.g., 404 requests).
func (m *Metrics) getRoutePattern(r *http.Request) string {
	if v, ok := r.Context().Value(plugin.CtxMatch).(discovery.MatchedRoute); ok {
		return v.Mapper.SrcMatch.String()
	}
	return "[unmatched]"
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

// NewResponseWriter wraps http.ResponseWriter with stored status code
func NewResponseWriter(w http.ResponseWriter) *responseWriter { //nolint golint
	return &responseWriter{w, http.StatusOK}
}

// WriteHeader wraps http.ResponseWriter and stores status code
func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// Hijack delegate to the original writer if it implements http.Hijacker
func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := rw.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijack not supported")
	}
	conn, buf, err := h.Hijack()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to hijack connection: %w", err)
	}
	return conn, buf, nil
}
