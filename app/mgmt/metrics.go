package mgmt

import (
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

type Metrics struct {
	totalRequests  *prometheus.CounterVec
	responseStatus *prometheus.CounterVec
	httpDuration   *prometheus.HistogramVec
}

func NewMetrics() *Metrics {
	res := &Metrics{}

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

	return res
}

func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		path := r.URL.Path
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

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func NewResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{w, http.StatusOK}
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}
