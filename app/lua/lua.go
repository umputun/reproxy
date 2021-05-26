package lua

import (
	"bytes"
	"fmt"
	"net/http"
	"os"

	log "github.com/go-pkgz/lgr"
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
	scripts []script
}

type script struct {
	filename string
	proto    *lua.LFunction
}

// New creates new Manager instance
func New(scripts []string) (*Manager, error) {
	ss := make([]script, 0, len(scripts))

	for _, filePath := range scripts {
		s, err := loadScript(filePath)
		if err != nil {
			return nil, fmt.Errorf("error read lua script %s, %w", filePath, err)
		}

		ss = append(ss, s)
	}

	// todo: Allow to use with empty scripts list? Or return nil, nil?
	if len(ss) == 0 {
		return nil, fmt.Errorf("you should add at least one lua script, if lua support is enabled")
	}

	return &Manager{
		scripts: ss,
	}, nil
}

func loadScript(filePath string) (script, error) {
	body, err := os.ReadFile(filePath)
	if err != nil {
		return script{}, err
	}

	chunk, err := parse.Parse(bytes.NewReader(body), filePath)
	if err != nil {
		return script{}, err
	}
	proto, err := lua.Compile(chunk, filePath)
	if err != nil {
		return script{}, err
	}

	return script{
		filename: filePath,
		proto: &lua.LFunction{
			IsG:       false,
			Env:       &lua.LTable{},
			Proto:     proto,
			GFunction: nil,
			Upvalues:  make([]*lua.Upvalue, int(proto.NumUpvalues)),
		},
	}, nil
}

// Middleware implements MiddlewareProvider interface
func (mgr *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error

		state := getState()
		defer putState(state)

		state.w = w
		state.r = r

		for _, s := range mgr.scripts {
			s.proto.Env = state.l.Env
			state.l.Push(s.proto)

			err = state.l.PCall(0, lua.MultRet, nil)
			if err != nil {
				log.Printf("[ERROR] error call lua script %s, %v", s.filename, err)
				next.ServeHTTP(w, r)
				return
			}

			if state.canceled {
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}
