package lua

import (
	"time"

	lua "github.com/yuin/gopher-lua"
)

func (c *Conductor) moduleKVLoader(state *lua.LState) int {
	var exports = map[string]lua.LGFunction{
		"set":    c.moduleKVSet,
		"get":    c.moduleKVGet,
		"delete": c.moduleKVDelete,
		"update": c.moduleKVUpdate,
	}
	mod := state.SetFuncs(state.NewTable(), exports)

	state.Push(mod)
	return 1
}

func (c *Conductor) moduleKVSet(state *lua.LState) int {
	var tm time.Duration

	key := state.Get(1)
	if key.Type() != lua.LTString {
		state.RaiseError("key must be a string, got %s", key.Type().String())
		return 0
	}

	value := state.Get(2)
	if value.Type() != lua.LTString {
		state.RaiseError("value must be a string, got %s", value.Type().String())
		return 0
	}

	timeout := state.Get(3)
	if timeout.Type() != lua.LTNil {
		if timeout.Type() != lua.LTString {
			state.RaiseError("timeout must be a string, got %s", timeout.Type().String())
			return 0
		}

		t, errT := time.ParseDuration(timeout.String())
		if errT != nil {
			state.RaiseError("timeout must be go duration string format, %s", errT.Error())
			return 0
		}
		tm = t
	}

	err := c.kv.Set(key.String(), value.String(), tm)
	if err != nil {
		state.Push(lua.LString(err.Error()))
		return 1
	}

	return 0
}

func (c *Conductor) moduleKVUpdate(state *lua.LState) int {
	key := state.Get(1)
	if key.Type() != lua.LTString {
		state.RaiseError("key must be a string, got %s", key.Type().String())
		return 0
	}

	value := state.Get(2)
	if value.Type() != lua.LTString {
		state.RaiseError("value must be a string, got %s", value.Type().String())
		return 0
	}

	err := c.kv.Update(key.String(), value.String())
	if err != nil {
		state.Push(lua.LString(err.Error()))
		return 1
	}

	return 0
}

func (c *Conductor) moduleKVGet(state *lua.LState) int {
	key := state.Get(1)
	if key.Type() != lua.LTString {
		state.RaiseError("key must be a string, got %s", key.Type().String())
		return 0
	}

	v, err := c.kv.Get(key.String())
	if err != nil {
		state.Push(lua.LNil)
		state.Push(lua.LString(err.Error()))
		return 2
	}

	state.Push(lua.LString(v))

	return 1
}

func (c *Conductor) moduleKVDelete(state *lua.LState) int {
	key := state.Get(1)
	if key.Type() != lua.LTString {
		state.RaiseError("key must be a string, got %s", key.Type().String())
		return 0
	}

	err := c.kv.Delete(key.String())
	if err != nil {
		state.Push(lua.LString(err.Error()))
		return 1
	}

	return 0
}
