package lua

import (
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	lua "github.com/yuin/gopher-lua"

	"github.com/umputun/reproxy/app/discovery"
)

func Test_makeLuaContext(t *testing.T) {
	state := lua.NewState()

	match := discovery.MatchedRoute{
		Destination: "1",
		Alive:       true,
		Mapper: discovery.URLMapper{
			Server:         "2",
			SrcMatch:       *regexp.MustCompile(".*"),
			Dst:            "3",
			ProviderID:     "4",
			PingURL:        "5",
			MatchType:      100,
			RedirectType:   200,
			AssetsLocation: "6",
			AssetsWebRoot:  "7",
			AssetsSPA:      true,
		},
	}

	req := &http.Request{
		Method:     http.MethodGet,
		Proto:      "HTTP/1.1",
		Host:       "localhost:8080",
		RemoteAddr: "foo:1234",
		RequestURI: "/bar/baz",
	}

	ctx := makeLuaContext("plugin.lua", state, nil, req, nil, match)

	tblRoute := ctx.loader.RawGetString("route").(*lua.LTable)

	assert.Equal(t, "1", tblRoute.RawGetString("destination").(lua.LString).String())
	assert.Equal(t, lua.LTrue, tblRoute.RawGetString("alive").(lua.LBool))

	tblMapper := tblRoute.RawGetString("mapper").(*lua.LTable)
	assert.Equal(t, "2", tblMapper.RawGetString("server").(lua.LString).String())
	assert.Equal(t, ".*", tblMapper.RawGetString("srcMatch").(lua.LString).String())
	assert.Equal(t, "3", tblMapper.RawGetString("dst").(lua.LString).String())
	assert.Equal(t, "4", tblMapper.RawGetString("providerID").(lua.LString).String())
	assert.Equal(t, "5", tblMapper.RawGetString("pingURL").(lua.LString).String())
	assert.Equal(t, lua.LNumber(100), tblMapper.RawGetString("matchType").(lua.LNumber))
	assert.Equal(t, lua.LNumber(200), tblMapper.RawGetString("redirectType").(lua.LNumber))
	assert.Equal(t, "6", tblMapper.RawGetString("assetsLocation").(lua.LString).String())
	assert.Equal(t, "7", tblMapper.RawGetString("assetsWebRoot").(lua.LString).String())
	assert.Equal(t, lua.LTrue, tblMapper.RawGetString("assetsSPA").(lua.LBool))

	tblReq := ctx.loader.RawGetString("request").(*lua.LTable)
	assert.Equal(t, "HTTP/1.1", tblReq.RawGetString("proto").(lua.LString).String())
	assert.Equal(t, "GET", tblReq.RawGetString("method").(lua.LString).String())
	assert.Equal(t, "foo:1234", tblReq.RawGetString("remoteAddr").(lua.LString).String())
	assert.Equal(t, "/bar/baz", tblReq.RawGetString("requestURI").(lua.LString).String())
	assert.Equal(t, "localhost:8080", tblReq.RawGetString("host").(lua.LString).String())
}

func Test_readRequestBody(t *testing.T) {
	state := lua.NewState()

	ctx := &luaContext{
		req: state.NewTable(),
		r: &http.Request{
			Body: io.NopCloser(strings.NewReader("foo")),
		},
	}

	v1 := ctx.req.RawGetString("body")
	assert.Equal(t, lua.LNil, v1)

	ctx.readRequestBody(state)

	v2 := ctx.req.RawGetString("body")
	assert.Equal(t, lua.LTString, v2.Type())
	assert.Equal(t, "foo", v2.String())

	vr, errRead := io.ReadAll(ctx.r.Body)
	require.NoError(t, errRead)
	assert.Equal(t, "foo", string(vr))
}

func Test_makeHeader(t *testing.T) {
	state := lua.NewState()
	ctx := &luaContext{}

	h := http.Header{}

	v := ctx.makeHeader(state, h)

	vv, ok := v.(*lua.LTable)
	require.True(t, ok)

	assert.Equal(t, lua.LTFunction, vv.RawGetString("get").Type())
	assert.Equal(t, lua.LTFunction, vv.RawGetString("set").Type())
	assert.Equal(t, lua.LTFunction, vv.RawGetString("add").Type())
	assert.Equal(t, lua.LTFunction, vv.RawGetString("delete").Type())
}

func Test_headerGet_wrong_name(t *testing.T) {
	h := http.Header{}

	var e string

	state := lua.NewState()
	state.Panic = func(state *lua.LState) {
		e = state.Get(2).String()
	}

	f := headerGet(h)

	state.Push(lua.LNumber(100))
	n := f(state)
	assert.Equal(t, 0, n)
	// start space in the message added in lua lib state.go#689
	assert.Equal(t, " header name must be string, got number", e)
}

func Test_headerGet(t *testing.T) {
	h := http.Header{}
	h.Set("a", "b")

	state := lua.NewState()

	f := headerGet(h)

	state.Push(lua.LString("a"))
	n := f(state)
	assert.Equal(t, 1, n)
	assert.Equal(t, "b", state.Get(2).String())
}

func Test_headerDelete_wrong_name(t *testing.T) {
	h := http.Header{}

	var e string

	state := lua.NewState()
	state.Panic = func(state *lua.LState) {
		e = state.Get(2).String()
	}

	f := headerDelete(h)

	state.Push(lua.LNumber(100))
	n := f(state)
	assert.Equal(t, 0, n)
	// start space in the message added in lua lib state.go#689
	assert.Equal(t, " header name must be string, got number", e)
}

func Test_headerDelete(t *testing.T) {
	h := http.Header{}
	h.Set("a", "b")

	state := lua.NewState()

	f := headerDelete(h)

	state.Push(lua.LString("a"))
	n := f(state)
	assert.Equal(t, 0, n)

	assert.Equal(t, "", h.Get("a"))
}

func Test_headerSet_wrong_name(t *testing.T) {
	h := http.Header{}

	var e string

	state := lua.NewState()
	state.Panic = func(state *lua.LState) {
		e = state.Get(2).String()
	}

	f := headerSet(h)

	state.Push(lua.LNumber(100))
	n := f(state)
	assert.Equal(t, 0, n)
	// start space in the message added in lua lib state.go#689
	assert.Equal(t, " header name must be string, got number", e)
}

func Test_headerSet_wrong_value(t *testing.T) {
	h := http.Header{}

	var e string

	state := lua.NewState()
	state.Panic = func(state *lua.LState) {
		e = state.Get(3).String()
	}

	f := headerSet(h)

	state.Push(lua.LString("a"))
	state.Push(lua.LNumber(100))
	n := f(state)
	assert.Equal(t, 0, n)
	// start space in the message added in lua lib state.go#689
	assert.Equal(t, " header value must be string, got number", e)
}

func Test_headerSet(t *testing.T) {
	h := http.Header{}
	h.Set("a", "x")

	state := lua.NewState()

	f := headerSet(h)

	state.Push(lua.LString("a"))
	state.Push(lua.LString("b"))
	n := f(state)
	assert.Equal(t, 0, n)

	v, ok := h["A"]
	require.True(t, ok)
	assert.Equal(t, []string{"b"}, v)
}

func Test_headerAdd_wrong_name(t *testing.T) {
	h := http.Header{}

	var e string

	state := lua.NewState()
	state.Panic = func(state *lua.LState) {
		e = state.Get(2).String()
	}

	f := headerAdd(h)

	state.Push(lua.LNumber(100))
	n := f(state)
	assert.Equal(t, 0, n)
	// start space in the message added in lua lib state.go#689
	assert.Equal(t, " header name must be string, got number", e)
}

func Test_headerAdd_wrong_value(t *testing.T) {
	h := http.Header{}

	var e string

	state := lua.NewState()
	state.Panic = func(state *lua.LState) {
		e = state.Get(3).String()
	}

	f := headerAdd(h)

	state.Push(lua.LString("a"))
	state.Push(lua.LNumber(100))
	n := f(state)
	assert.Equal(t, 0, n)
	// start space in the message added in lua lib state.go#689
	assert.Equal(t, " header value must be string, got number", e)
}

func Test_headerAdd(t *testing.T) {
	h := http.Header{}
	h.Set("a", "x")

	state := lua.NewState()

	f := headerAdd(h)

	state.Push(lua.LString("a"))
	state.Push(lua.LString("b"))
	n := f(state)
	assert.Equal(t, 0, n)

	assert.Equal(t, "x", h.Get("a"))

	v, ok := h["A"]
	require.True(t, ok)
	assert.Equal(t, []string{"x", "b"}, v)
}
