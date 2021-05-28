package lua

import (
	log "github.com/go-pkgz/lgr"
	lua "github.com/yuin/gopher-lua"
	"net/http"
	"strconv"
)

type state struct {
	r        *http.Request
	w        http.ResponseWriter
	canceled bool

	filePath string
	l        *lua.LState
	proto    *lua.LFunction
}

func newState(filePath string, proto *lua.FunctionProto) *state {
	st := &state{
		l:        lua.NewState(),
		filePath: filePath,
	}

	mt := st.l.NewTable()

	st.l.SetField(mt, "request", st.createRequest())
	st.l.SetField(mt, "response", st.createResponse())
	st.l.SetField(mt, "log", st.createLog())
	st.l.SetField(mt, "stop", st.l.NewFunction(st.stop))

	st.l.SetGlobal("reproxy", mt)

	st.proto = st.l.NewFunctionFromProto(proto)

	return st
}

func (st *state) run(w http.ResponseWriter, r *http.Request) error {
	st.w = w
	st.r = r

	st.l.Push(st.proto)
	return st.l.PCall(0, lua.MultRet, nil)
}

func (st *state) reset() {
	st.r = nil
	st.w = nil
	st.canceled = false
}

func (st *state) stop(state *lua.LState) int {
	st.canceled = true

	code, err := strconv.Atoi(state.Get(1).String())
	if err != nil {
		log.Printf("[ERROR] error convert status code to int, %v", err)
		return 0
	}
	st.w.WriteHeader(code)

	body := state.Get(2)
	if body.Type() != lua.LTNil {
		_, err = st.w.Write([]byte(body.String()))
		if err != nil {
			log.Printf("[ERROR] error write to response, %v", err)
		}
	}

	return 0
}

func (st *state) createLog() *lua.LTable {
	t := &lua.LTable{}

	t.RawSetString("debug", st.l.NewFunction(st.sendToLog("DEBUG")))
	t.RawSetString("info", st.l.NewFunction(st.sendToLog("INFO")))
	t.RawSetString("warn", st.l.NewFunction(st.sendToLog("WARN")))
	t.RawSetString("error", st.l.NewFunction(st.sendToLog("ERROR")))

	return t
}

func (st *state) sendToLog(level string) lua.LGFunction {
	return func(state *lua.LState) int {
		n := state.GetTop()
		if n < 1 {
			log.Printf("[ERROR] expect at least one argument")
			return 0
		}

		format := state.Get(1).String()

		var args []interface{}

		for i := 2; i <= n; i++ {
			v := state.Get(i)
			switch v.Type() {
			case lua.LTNumber:
				j, err := strconv.ParseFloat(v.String(), 64)
				if err != nil {
					log.Printf("[ERROR] error parse float %s, %s", v.String(), err)
					continue
				}
				args = append(args, j)
			case lua.LTString:
				args = append(args, v.String())
			case lua.LTBool:
				args = append(args, v.String() == "true")
			default:
				args = append(args, v.String())
			}
		}

		log.Printf("["+level+"] "+format, args...)

		return 0
	}
}

func (st *state) createResponse() *lua.LTable {
	t := &lua.LTable{}

	h := &lua.LTable{}
	h.RawSetString("add", st.l.NewFunction(st.addResponseHeader))
	h.RawSetString("set", st.l.NewFunction(st.setResponseHeader))
	t.RawSetString("headers", h)

	return t
}

func (st *state) setResponseHeader(state *lua.LState) int {
	name := state.Get(1)
	if name.Type() != lua.LTString {
		log.Printf("[ERROR] error get header name")
		return 0
	}
	value := state.Get(2)
	if value.Type() != lua.LTString {
		log.Printf("[ERROR] error get header value")
		return 0
	}

	st.w.Header().Set(name.String(), value.String())

	return 0
}

func (st *state) addResponseHeader(state *lua.LState) int {
	name := state.Get(1)
	if name.Type() != lua.LTString {
		log.Printf("[ERROR] error get header name")
		return 0
	}
	value := state.Get(2)
	if value.Type() != lua.LTString {
		log.Printf("[ERROR] error get header value")
		return 0
	}

	st.w.Header().Add(name.String(), value.String())

	return 0
}

func (st *state) createRequest() *lua.LTable {
	t := &lua.LTable{}
	t.RawSetString("uri", st.l.NewFunction(st.getRequestURI))
	t.RawSetString("host", st.l.NewFunction(st.getRequestHost))
	t.RawSetString("method", st.l.NewFunction(st.getRequestMethod))

	h := &lua.LTable{}

	h.RawSetString("has", st.l.NewFunction(st.hasRequestHeader))
	h.RawSetString("del", st.l.NewFunction(st.deleteRequestHeader))
	h.RawSetString("add", st.l.NewFunction(st.addRequestHeader))
	h.RawSetString("get", st.l.NewFunction(st.getRequestHeader))

	t.RawSetString("headers", h)

	return t
}

func (st *state) getRequestURI(state *lua.LState) int {
	state.Push(lua.LString(st.r.RequestURI))
	return 1
}

func (st *state) getRequestHost(state *lua.LState) int {
	state.Push(lua.LString(st.r.Host))
	return 1
}

func (st *state) getRequestMethod(state *lua.LState) int {
	state.Push(lua.LString(st.r.Method))
	return 1
}

func (st *state) addRequestHeader(state *lua.LState) int {
	name := state.Get(1)
	if name.Type() != lua.LTString {
		log.Printf("[ERROR] error get header name")
		return 0
	}
	value := state.Get(2)
	if value.Type() != lua.LTString {
		log.Printf("[ERROR] error get header value")
		return 0
	}

	st.r.Header.Add(name.String(), value.String())

	return 0

}

func (st *state) getRequestHeader(state *lua.LState) int {
	name := state.Get(1)
	if name.Type() != lua.LTString {
		log.Printf("[ERROR] error get header name")
		return 0
	}

	value := st.r.Header.Get(name.String())
	state.Push(lua.LString(value))

	return 1
}

func (st *state) deleteRequestHeader(state *lua.LState) int {
	name := state.Get(1)
	if name.Type() != lua.LTString {
		log.Printf("[ERROR] error get header name")
		return 0
	}

	st.r.Header.Del(name.String())

	return 0

}

func (st *state) hasRequestHeader(state *lua.LState) int {
	name := state.Get(1)
	if name.Type() != lua.LTString {
		log.Printf("[ERROR] error get header name")
		return 0
	}
	value := state.Get(2)
	if value.Type() != lua.LTString {
		log.Printf("[ERROR] error get header value")
		return 0
	}

	state.Push(lua.LBool(st.r.Header.Get(name.String()) == value.String()))

	return 1
}
