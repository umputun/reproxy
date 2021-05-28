package lua

import (
	"bytes"
	"fmt"
	log "github.com/go-pkgz/lgr"
	"net/http"
	"os"
	"sync"

	lua "github.com/yuin/gopher-lua"
	"github.com/yuin/gopher-lua/parse"
)

/*

LUA API:

reproxy.request.uri                         string
reproxy.request.host                        string
reproxy.request.method                      string
reproxy.request.headers.has(name, value)    bool
reproxy.request.headers.del(name)           void
reproxy.request.headers.add(name, value)    void
reproxy.request.headers.get(name)           string

reproxy.response.headers.add(name, value)   void
reproxy.response.headers.set(name, value)   void

reproxy.log.debug(format[, ...args])        void
reproxy.log.info(format[, ...args])         void
reproxy.log.warn(format[, ...args])         void
reproxy.log.error(format[, ...args])        void

reproxy.stop(status_code[, response_body])  void

*/

// Manager represents LUA script manager
// It loading scripts and provide MiddlewareProvider interface
type Manager struct {
	scripts map[string]*sync.Pool
}

// New creates new Manager instance
func New(scripts []string) (*Manager, error) {
	m := &Manager{
		scripts: map[string]*sync.Pool{},
	}

	for _, filePath := range scripts {
		body, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("error read script, %w", err)
		}

		chunk, err := parse.Parse(bytes.NewReader(body), filePath)
		if err != nil {
			return nil, fmt.Errorf("error parse lua script, %w", err)
		}
		proto, err := lua.Compile(chunk, filePath)
		if err != nil {
			return nil, fmt.Errorf("error compile lua script, %w", err)
		}

		f := func(filePath string, proto *lua.FunctionProto) func() interface{} {
			return func() interface{} {
				return newState(filePath, proto)
			}
		}

		m.scripts[filePath] = &sync.Pool{New: f(filePath, proto)}

		log.Printf("[DEBUG] add lua script: %s", filePath)
	}

	return m, nil
}

// Middleware implements MiddlewareProvider interface
func (mgr *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error

		for _, pool := range mgr.scripts {
			st := pool.Get().(*state)

			err = st.run(w, r)
			if err != nil {
				log.Printf("[ERROR] error call lua script %s, %v", st.filePath, err)
				next.ServeHTTP(w, r)
				st.reset()
				pool.Put(st)
				return
			}

			if st.canceled {
				st.reset()
				pool.Put(st)
				return
			}

			st.reset()
			pool.Put(st)
		}

		next.ServeHTTP(w, r)
	})
}
