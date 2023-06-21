package lua

import (
	"bytes"
	"io"
	"net/http"

	"github.com/umputun/reproxy/app/discovery"
	lua "github.com/yuin/gopher-lua"
)

type luaContext struct {
	filename string

	loader *lua.LTable
	req    *lua.LTable
	resp   *lua.LTable

	captureResponse bool
	nextIsCalled    bool
	nextFunc        http.Handler

	r              *http.Request
	responseHeader http.Header
}

func makeLuaContext(filename string, state *lua.LState, w http.ResponseWriter, r *http.Request, next http.Handler, routeData any) *luaContext {
	ctx := &luaContext{
		r:              r,
		nextFunc:       next,
		filename:       filename,
		responseHeader: http.Header{},
		loader:         state.NewTable(),
		req:            state.NewTable(),
		resp:           state.NewTable(),
	}

	routeTable := state.NewTable()
	routeMapperTable := state.NewTable()
	routeTable.RawSetString("mapper", routeMapperTable)
	ctx.loader.RawSetString("route", routeTable)
	ctx.loader.RawSetString("request", ctx.req)
	ctx.loader.RawSetString("response", ctx.resp)
	ctx.loader.RawSetString("next", state.NewFunction(ctx.next(w)))

	// request
	ctx.req.RawSetString("method", lua.LString(ctx.r.Method))
	ctx.req.RawSetString("remoteAddr", lua.LString(ctx.r.RemoteAddr))
	ctx.req.RawSetString("requestURI", lua.LString(ctx.r.RequestURI))
	ctx.req.RawSetString("host", lua.LString(ctx.r.Host))
	ctx.req.RawSetString("readBody", state.NewFunction(ctx.readRequestBody))
	ctx.req.RawSetString("header", ctx.makeHeader(state, ctx.r.Header))

	// response
	ctx.resp.RawSetString("header", ctx.makeHeader(state, ctx.responseHeader))

	// route
	if v, ok := routeData.(discovery.MatchedRoute); ok {
		routeTable.RawSetString("destination", lua.LString(v.Destination))
		routeTable.RawSetString("alive", lua.LBool(v.Alive))

		routeMapperTable.RawSetString("server", lua.LString(v.Mapper.Server))
		routeMapperTable.RawSetString("srcMatch", lua.LString(v.Mapper.SrcMatch.String()))
		routeMapperTable.RawSetString("dst", lua.LString(v.Mapper.Dst))
		routeMapperTable.RawSetString("providerID", lua.LString(v.Mapper.ProviderID))
		routeMapperTable.RawSetString("pingURL", lua.LString(v.Mapper.PingURL))
		routeMapperTable.RawSetString("matchType", lua.LNumber(v.Mapper.MatchType))
		routeMapperTable.RawSetString("redirectType", lua.LNumber(v.Mapper.RedirectType))
		routeMapperTable.RawSetString("assetsLocation", lua.LString(v.Mapper.AssetsLocation))
		routeMapperTable.RawSetString("assetsWebRoot", lua.LString(v.Mapper.AssetsWebRoot))
		routeMapperTable.RawSetString("assetsSPA", lua.LBool(v.Mapper.AssetsSPA))
	}

	return ctx
}

func (ctx *luaContext) readRequestBody(state *lua.LState) int {
	b, err := io.ReadAll(ctx.r.Body)
	if err != nil {
		state.Push(lua.LString(err.Error()))
		return 1
	}

	ctx.r.Body = io.NopCloser(bytes.NewReader(b))

	ctx.req.RawSetString("body", lua.LString(b))

	return 0
}

func (ctx *luaContext) makeHeader(state *lua.LState, h http.Header) lua.LValue {
	t := state.NewTable()

	t.RawSetString("get", state.NewFunction(headerGet(h)))
	t.RawSetString("set", state.NewFunction(headerSet(h)))
	t.RawSetString("add", state.NewFunction(headerAdd(h)))
	t.RawSetString("delete", state.NewFunction(headerDelete(h)))

	return t
}

func headerGet(h http.Header) lua.LGFunction {
	return func(state *lua.LState) int {
		name := state.CheckString(1)
		state.Push(lua.LString(h.Get(name)))
		return 1
	}
}

func headerDelete(h http.Header) lua.LGFunction {
	return func(state *lua.LState) int {
		name := state.CheckString(1)
		h.Del(name)
		return 0
	}
}

func headerSet(h http.Header) lua.LGFunction {
	return func(state *lua.LState) int {
		name := state.CheckString(1)
		value := state.CheckString(2)
		h.Set(name, value)
		return 0
	}
}

func headerAdd(h http.Header) lua.LGFunction {
	return func(state *lua.LState) int {
		name := state.CheckString(1)
		value := state.CheckString(2)
		h.Add(name, value)
		return 0
	}
}
