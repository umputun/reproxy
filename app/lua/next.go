package lua

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/yuin/gopher-lua"
)

func (ctx *luaContext) next(w http.ResponseWriter) func(state *lua.LState) int {
	return func(state *lua.LState) int {
		if ctx.nextIsCalled {
			log.Printf("[WARN] next already called, lua plugin %s", ctx.filename)
			return 0
		}

		ctx.nextIsCalled = true

		if opts, ok := state.Get(1).(*lua.LTable); ok {
			if v := opts.RawGetString("captureResponse"); v.Type() == lua.LTBool {
				ctx.captureResponse = lua.LVAsBool(v)
			}
		}

		reqBody := ctx.req.RawGetString("body")
		if reqBody.Type() == lua.LTString {
			ctx.r.ContentLength = int64(len(reqBody.String()))
			ctx.r.Body = io.NopCloser(strings.NewReader(reqBody.String()))
		}

		if !ctx.captureResponse {
			ctx.nextFunc.ServeHTTP(w, ctx.r)
			return 0
		}

		ww := httptest.NewRecorder()
		for k, vv := range w.Header() {
			for _, v := range vv {
				ww.Header().Add(k, v)
			}
		}

		ctx.nextFunc.ServeHTTP(ww, ctx.r)

		ctx.resp.RawSetString("statusCode", lua.LNumber(ww.Code))
		ctx.resp.RawSetString("body", lua.LString(ww.Body.Bytes()))
		for k, vv := range ww.Header() {
			for _, v := range vv {
				ctx.responseHeader.Add(k, v)
			}
		}

		return 0
	}
}
