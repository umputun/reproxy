package demo

import (
	"net/http"
	"os"
)

type Demo struct {
	RespHeaderKey   string
	RespHeaderValue string
}

var plugin = &Demo{
	RespHeaderKey:   "X-Demo-Header",
	RespHeaderValue: "Demo-Value",
}

func InitPlugin() func(http.Handler) http.Handler {
	// Пример конфигурирования плагина
	if s := os.Getenv("REPROXY_DEMO_KEY"); s != "" {
		plugin.RespHeaderKey = s
	}
	if s := os.Getenv("REPROXY_DEMO_VALUE"); s != "" {
		plugin.RespHeaderValue = s
	}

	return plugin.Handler
}

func (d *Demo) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		// Если требуется, можем получить MatchedRoute из контекста
		//v, ok := r.Context().Value(discovery.CtxMatch).(discovery.MatchedRoute)

		w.Header().Add(d.RespHeaderKey, d.RespHeaderValue)

		next.ServeHTTP(w, r)
	})
}
