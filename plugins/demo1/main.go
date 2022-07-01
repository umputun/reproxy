package demo

import (
	"log"
	"net/http"
	"os"

	"github.com/umputun/reproxy/lib"

	"github.com/umputun/reproxy/app/discovery"
)

type Demo struct {
	RespHeaderKey   string
	RespHeaderValue string
}

// InitPlugin будет вызвана в файле, сгенерированном командой go run ./cmd/plugins
func InitPlugin() {
	d := &Demo{
		RespHeaderKey:   "X-Demo-Header",
		RespHeaderValue: "Demo-Value",
	}

	// Пример конфигурирования плагина
	if s := os.Getenv("REPROXY_DEMO_KEY"); s != "" {
		d.RespHeaderKey = s
	}
	if s := os.Getenv("REPROXY_DEMO_VALUE"); s != "" {
		d.RespHeaderValue = s
	}

	// Регистрируем обработчик. Имя плагина будет получено по названию папки, куда плагин склонирован
	// например, если этот файл находится в plugins/demo/main.go - то имя плагина определиться как 'demo'
	lib.Register(d.Handler)
}

func (d *Demo) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		// Если надо, можем получить правила матчинга
		v, ok := r.Context().Value(lib.CtxMatch).(discovery.MatchedRoute)
		if ok {
			log.Printf("[DEBUG] MatchedRoute: %v", v)
		}

		w.Header().Add(d.RespHeaderKey, d.RespHeaderValue)

		next.ServeHTTP(w, r)
	})
}
