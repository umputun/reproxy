package lua

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	R "github.com/go-pkgz/rest"
	lua "github.com/yuin/gopher-lua"
	"github.com/yuin/gopher-lua/parse"
)

type kv interface {
	Set(key, value string, timeout time.Duration) error
	Update(key, value string) error
	Delete(key string) error
	Get(key string) (string, error)
}

type httpClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type contextKey string

const (
	// CtxMatch is a context key for match
	CtxMatch = contextKey("match")
)

// New creates new conductor
func New(kv kv, httpClient httpClient) *Conductor {
	c := &Conductor{
		kv:         kv,
		httpClient: httpClient,
	}

	return c
}

// Conductor is a lua plugin conductor
type Conductor struct {
	kv         kv
	handlers   []func(handler http.Handler) http.Handler
	httpClient httpClient
}

// Middleware returns middleware for lua plugins
func (c *Conductor) Middleware(next http.Handler) http.Handler {
	return R.Wrap(next, c.handlers...)
}

// Add adds lua plugin
func (c *Conductor) Add(filename string) error {
	data, errRead := os.ReadFile(filename) // nolint
	if errRead != nil {
		return fmt.Errorf("error read file, %w", errRead)
	}

	chunk, errParse := parse.Parse(bytes.NewReader(data), filename)
	if errParse != nil {
		return fmt.Errorf("parse error, %w", errParse)
	}

	proto, errCompile := lua.Compile(chunk, filename)
	if errCompile != nil {
		return fmt.Errorf("compile error, %w", errCompile)
	}

	state, errFunc := c.prepareLuaState()
	defer state.Close()

	lFunc := state.NewFunctionFromProto(proto)
	state.Push(lFunc)
	errDo := state.PCall(0, lua.MultRet, errFunc)
	if errDo != nil {
		return fmt.Errorf("error execute lua plugin, %w", errDo)
	}

	f := state.Get(1)
	if f.Type() != lua.LTFunction {
		return fmt.Errorf("lua plugin must return a function, got %s", f.Type().String())
	}
	c.handlers = append(c.handlers, c.handler(filename, f))

	return nil
}

func (c *Conductor) prepareLuaState() (*lua.LState, *lua.LFunction) {
	state := lua.NewState()

	state.PreloadModule("log", c.moduleLogLoader)
	state.PreloadModule("http", c.moduleHTTPLoader)
	state.PreloadModule("kv", c.moduleKVLoader)

	// prevent print lua stacktrace to stdout on error
	errFunc := state.NewFunction(func(_ *lua.LState) int { return 1 })

	return state, errFunc
}

func (c *Conductor) handler(filename string, f lua.LValue) func(handler http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			state, errFunc := c.prepareLuaState()
			defer state.Close()

			state.SetContext(r.Context())

			luaCtx := makeLuaContext(filename, state, w, r, next, r.Context().Value(CtxMatch))

			state.Push(f)
			state.Push(luaCtx.loader)
			errDoHook := state.PCall(1, lua.MultRet, errFunc)
			if errDoHook != nil {
				log.Printf("[WARN] error execute lua plugin %q, %v", filename, errDoHook)
				if !luaCtx.nextIsCalled {
					next.ServeHTTP(w, r)
					return
				}
			}

			if !luaCtx.captureResponse && luaCtx.nextIsCalled {
				return
			}

			for k := range w.Header() {
				w.Header().Del(k)
			}

			for k, vv := range luaCtx.responseHeader {
				for _, v := range vv {
					w.Header().Add(k, v)
				}
			}

			statusCode, body, errGetResponseData := luaCtx.getResponseStatusAndBody()
			if errGetResponseData != nil {
				log.Printf("[ERROR] error get response status code and body, %v", errGetResponseData)
				return
			}

			w.Header().Set("content-length", strconv.Itoa(len(body)))
			w.WriteHeader(statusCode)
			w.Write([]byte(body)) // nolint
		})
	}
}

func (ctx *luaContext) getResponseStatusAndBody() (statusCode int, body string, err error) {
	statusCode = 200
	body = ""

	statusCodeV := ctx.resp.RawGetString("statusCode")
	if statusCodeV.Type() != lua.LTNil {
		if statusCodeV.Type() != lua.LTNumber {
			return 0, "", fmt.Errorf("response statusCode must be a number or nil, got %s", statusCodeV.Type().String())
		}
		statusCode = int(statusCodeV.(lua.LNumber))
	}

	if statusCode < 100 || statusCode > 999 {
		return 0, "", fmt.Errorf("response statusCode must be a 3-digit number, got %d", statusCode)
	}

	bodyV := ctx.resp.RawGetString("body")
	if bodyV.Type() != lua.LTNil {
		if bodyV.Type() != lua.LTString {
			return 0, "", fmt.Errorf("response body must be a string or nil, got %s", bodyV.Type().String())
		}
		body = bodyV.String()
	}

	return statusCode, body, nil
}
