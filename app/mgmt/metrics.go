package mgmt

import (
	"bufio"
	"errors"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics provides registration and middleware for prometheus
type Metrics struct {
	totalRequests           *prometheus.CounterVec
	responseStatus          *prometheus.CounterVec
	httpDuration            *prometheus.HistogramVec
	throttledRequests       *prometheus.CounterVec
	isThrotllingEnabed      bool
	throtlingHttpStatusCode int
}

// NewMetrics create metrics object with all counters registered
func NewMetrics(throttlingConfig *ProxyThrottlingConfig) *Metrics {
	res := &Metrics{
		isThrotllingEnabed:      throttlingConfig.ProxyThrottlingConfig.Enabled,
		throtlingHttpStatusCode: throttlingConfig.HttpStatusCode,
	}

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

	res.throttledRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_throttled",
			Help: "Number of throttled requests.",
		},
		[]string{"server"},
	)

	prometheus.Unregister(prometheus.NewGoCollector())

	if err := prometheus.Register(res.totalRequests); err != nil {
		log.Printf("[WARN] can't register prometheus totalRequests, %v", err)
	}
	if err := prometheus.Register(res.responseStatus); err != nil {
		log.Printf("[WARN] can't register prometheus responseStatus, %v", err)
	}
	if err := prometheus.Register(res.httpDuration); err != nil {
		log.Printf("[WARN] can't register prometheus httpDuration, %v", err)
	}
	if err := prometheus.Register(res.throttledRequests); err != nil {
		log.Printf("[WARN] can't register prometheus throttledRequests, %v", err)
	}

	return res
}

// Middleware for the primary proxy server to publish all counters and update metrics
func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		path := r.URL.Path
		server := r.URL.Hostname()
		if server == "" {
			server = strings.Split(r.Host, ":")[0]
		}

		rw := NewResponseWriter(w)
		timer := prometheus.NewTimer(m.httpDuration.WithLabelValues(path))
		next.ServeHTTP(rw, r)
		timer.ObserveDuration()

		statusCode := rw.statusCode
		m.responseStatus.WithLabelValues(strconv.Itoa(statusCode)).Inc()
		m.totalRequests.WithLabelValues(server).Inc()
		if m.isThrotllingEnabed && statusCode == m.throtlingHttpStatusCode {
			m.throttledRequests.WithLabelValues(server).Inc()
		}
	})
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
	return h.Hijack()
}
