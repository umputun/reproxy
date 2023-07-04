package lua

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	lua "github.com/yuin/gopher-lua"
)

func createTempFile(t *testing.T, content string) string {
	f, errCreate := os.CreateTemp("", "script.lua")
	require.NoError(t, errCreate)

	_, errWrite := f.WriteString(content)
	require.NoError(t, errWrite)

	errClose := f.Close()
	require.NoError(t, errClose)

	return f.Name()
}

func TestConductor_Add_error_read(t *testing.T) {
	c := &Conductor{}

	filename := "script.lua"

	err := c.Add(filename)
	require.Error(t, err)
	assert.Equal(t, "error read file, open script.lua: no such file or directory", err.Error())
}

func TestConductor_Add_parse_error(t *testing.T) {
	c := &Conductor{}

	filename := createTempFile(t, "bad code")

	err := c.Add(filename)
	require.Error(t, err)

	assert.True(t, strings.HasPrefix(err.Error(), "parse error, "))
	assert.True(t, strings.HasSuffix(err.Error(), "line:1(column:8) near 'code':   parse error\n"))
}

func TestConductor_Add_compile_error(t *testing.T) {
	c := &Conductor{}

	// define 300 local variables, which is more than allowed by lua lib in compile.go#13
	var content string
	for i := 0; i < 300; i++ {
		content += "local i" + strconv.Itoa(i) + " = 0\n"
	}

	filename := createTempFile(t, content)

	err := c.Add(filename)
	require.Error(t, err)

	assert.True(t, strings.HasPrefix(err.Error(), "compile error, "))
	assert.True(t, strings.HasSuffix(err.Error(), "too many local variables"))
}

func TestConductor_Add_error_execute(t *testing.T) {
	c := &Conductor{}

	filename := createTempFile(t, "a = b + c")

	err := c.Add(filename)
	require.Error(t, err)

	assert.True(t, strings.HasPrefix(err.Error(), "error execute lua plugin, "))
	assert.True(t, strings.HasSuffix(err.Error(), "cannot perform add operation between nil and nil"))
}

func TestConductor_Add_return_not_a_function(t *testing.T) {
	c := &Conductor{}

	filename := createTempFile(t, "return 10")

	err := c.Add(filename)
	require.Error(t, err)

	assert.Equal(t, "lua plugin must return a function, got number", err.Error())
}

func TestConductor(t *testing.T) {
	c := &Conductor{}

	filename := createTempFile(t, "return function(context) end")

	err := c.Add(filename)
	require.NoError(t, err)
	assert.Equal(t, 1, len(c.handlers))
}

func Test_getResponseStatusAndBody_wrong_status(t *testing.T) {
	type testCase struct {
		name           string
		argStatus      lua.LValue
		argBody        lua.LValue
		wantStatus     int
		wantBody       string
		wantError      bool
		wantErrorValue string
	}

	testCases := []testCase{
		{
			name:           "status wrong type",
			argStatus:      lua.LString("foo"),
			wantError:      true,
			wantErrorValue: "response statusCode must be a number or nil, got string",
		},
		{
			name:           "status wrong value, less than 100",
			argStatus:      lua.LNumber(99),
			wantError:      true,
			wantErrorValue: "response statusCode must be a 3-digit number, got 99",
		},
		{
			name:           "status wrong value, more than 999",
			argStatus:      lua.LNumber(1000),
			wantError:      true,
			wantErrorValue: "response statusCode must be a 3-digit number, got 1000",
		},
		{
			name:           "body wrong type",
			argStatus:      nil,
			argBody:        lua.LNumber(100),
			wantStatus:     0,
			wantBody:       "",
			wantError:      true,
			wantErrorValue: "response body must be a string or nil, got number",
		},
		{
			name:           "default values",
			argStatus:      nil,
			argBody:        nil,
			wantStatus:     200,
			wantBody:       "",
			wantError:      false,
			wantErrorValue: "",
		},
		{
			name:           "with status and body",
			argStatus:      lua.LNumber(400),
			argBody:        lua.LString("foo"),
			wantStatus:     400,
			wantBody:       "foo",
			wantError:      false,
			wantErrorValue: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			state := lua.NewState()
			ctx := &luaContext{
				resp: state.NewTable(),
			}
			if tc.argStatus != nil {
				ctx.resp.RawSetString("statusCode", tc.argStatus)
			}
			if tc.argBody != nil {
				ctx.resp.RawSetString("body", tc.argBody)
			}

			status, body, err := ctx.getResponseStatusAndBody()
			if err != nil && !tc.wantError {
				t.Fatalf("want no error, but got error %q", err.Error())
			}
			if err == nil && tc.wantError {
				t.Fatalf("want error %q, but got no error", tc.wantErrorValue)
			}
			if err != nil && tc.wantError && tc.wantErrorValue != err.Error() {
				t.Fatalf("want error %q, but got error %q", tc.wantErrorValue, err.Error())
			}
			if status != tc.wantStatus {
				t.Fatalf("want status %d, but got status %d", tc.wantStatus, status)
			}
			if body != tc.wantBody {
				t.Fatalf("want body %s, but got body %s", tc.wantBody, body)
			}
		})
	}
}
