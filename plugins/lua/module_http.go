package lua

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"
)

const (
	defaultHTTPTimeout = time.Second * 30
)

func (c *Conductor) moduleHTTPLoader(state *lua.LState) int {
	var exports = map[string]lua.LGFunction{
		"get":     c.httpGet,
		"post":    c.httpPost,
		"request": c.httpRequest,
	}

	mod := state.SetFuncs(state.NewTable(), exports)
	mod.RawSetString("MethodGet", lua.LString(http.MethodGet))
	mod.RawSetString("MethodHead", lua.LString(http.MethodHead))
	mod.RawSetString("MethodPost", lua.LString(http.MethodPost))
	mod.RawSetString("MethodPut", lua.LString(http.MethodPut))
	mod.RawSetString("MethodPatch", lua.LString(http.MethodPatch))
	mod.RawSetString("MethodDelete", lua.LString(http.MethodDelete))
	mod.RawSetString("MethodConnect", lua.LString(http.MethodConnect))
	mod.RawSetString("MethodOptions", lua.LString(http.MethodOptions))
	mod.RawSetString("MethodTrace", lua.LString(http.MethodTrace))

	state.Push(mod)
	return 1
}

func (c *Conductor) httpRequest(state *lua.LState) int {
	var requestBody io.Reader = http.NoBody
	headers := http.Header{}
	timeout := defaultHTTPTimeout

	method := state.Get(1)
	if method.Type() != lua.LTString {
		state.RaiseError("method must be a string")
		return 0
	}

	requestURL := state.Get(2)
	if requestURL.Type() != lua.LTString {
		state.RaiseError("request url must be a string")
		return 0
	}

	argOptions := state.Get(3)
	if argOptions.Type() != lua.LTNil {
		if argOptions.Type() != lua.LTTable {
			state.RaiseError("options must be a table")
			return 0
		}

		optionsTable := argOptions.(*lua.LTable)
		optionTimeout := optionsTable.RawGetString("timeout")
		if optionTimeout.Type() != lua.LTNil {
			if optionTimeout.Type() != lua.LTString {
				state.RaiseError("option 'timeout' must be a string")
				return 0
			}
			var errTimeout error
			timeout, errTimeout = time.ParseDuration(optionTimeout.String())
			if errTimeout != nil {
				state.RaiseError("error parse option 'timeout' to go duration format, %s", errTimeout.Error())
				return 0
			}
		}
		optionBody := optionsTable.RawGetString("body")
		if optionBody.Type() != lua.LTNil {
			if optionBody.Type() != lua.LTString {
				state.RaiseError("option 'body' must be a string")
				return 0
			}
			requestBody = strings.NewReader(optionBody.String())
		}
		optionHeaders := optionsTable.RawGetString("headers")
		if optionHeaders.Type() != lua.LTNil {
			if optionHeaders.Type() != lua.LTTable {
				state.RaiseError("option 'headers' must be a table")
				return 0
			}
			optionHeaders.(*lua.LTable).ForEach(func(key lua.LValue, value lua.LValue) {
				if key.Type() != lua.LTString || value.Type() != lua.LTString {
					state.RaiseError("invalid option header, key and value must be a string")
					return
				}
				headers.Add(key.String(), value.String())
			})
		}
	}

	tbl, err := c.httpRawRequest(method.String(), requestURL.String(), requestBody, headers, timeout)
	if err != nil {
		state.Push(lua.LNil)
		state.Push(lua.LString(err.Error()))
		return 2
	}

	state.Push(tbl)

	return 1
}

func (c *Conductor) httpPost(state *lua.LState) int {
	headers := http.Header{}

	requestURL := state.Get(1)
	if requestURL.Type() != lua.LTString {
		state.RaiseError("request url must be a string")
		return 0
	}

	contentType := state.Get(2)
	if contentType.Type() != lua.LTString {
		state.RaiseError("content type must be a string")
		return 0
	}

	body := state.Get(3)
	if body.Type() != lua.LTString {
		state.RaiseError("body must be a string")
		return 0
	}
	headers.Add("content-type", contentType.String())

	tbl, err := c.httpRawRequest(http.MethodPost, requestURL.String(), strings.NewReader(body.String()), headers, defaultHTTPTimeout)
	if err != nil {
		state.Push(lua.LNil)
		state.Push(lua.LString(err.Error()))
		return 2
	}

	state.Push(tbl)

	return 1
}

func (c *Conductor) httpGet(state *lua.LState) int {
	requestURL := state.Get(1)
	if requestURL.Type() != lua.LTString {
		state.RaiseError("request url must be a string")
		return 0
	}

	tbl, err := c.httpRawRequest(http.MethodGet, requestURL.String(), http.NoBody, http.Header{}, defaultHTTPTimeout)
	if err != nil {
		state.Push(lua.LNil)
		state.Push(lua.LString(err.Error()))
		return 2
	}

	state.Push(tbl)

	return 1
}

func (c *Conductor) httpRawRequest(method, requestURL string, requestBody io.Reader, headers http.Header, timeout time.Duration) (*lua.LTable, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, errCreateRequest := http.NewRequestWithContext(ctx, method, requestURL, requestBody)
	if errCreateRequest != nil {
		return nil, fmt.Errorf("error create request, %w", errCreateRequest)
	}
	req.Header = headers

	resp, errDo := c.httpClient.Do(req)
	if errDo != nil {
		return nil, fmt.Errorf("error do request, %w", errDo)
	}
	defer resp.Body.Close() // nolint

	respBody, errReadRespBody := io.ReadAll(resp.Body)
	if errReadRespBody != nil {
		return nil, fmt.Errorf("error read response body, %w", errReadRespBody)
	}

	tbl := &lua.LTable{}
	tbl.RawSetString("code", lua.LNumber(resp.StatusCode))
	tbl.RawSetString("body", lua.LString(respBody))
	h := &lua.LTable{}
	for key, values := range headers {
		v := &lua.LTable{}
		for _, value := range values {
			v.Append(lua.LString(value))
		}
		h.RawSetString(key, v)
	}
	tbl.RawSetString("headers", h)
	return tbl, nil
}
