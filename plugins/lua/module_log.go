package lua

import (
	log "github.com/go-pkgz/lgr"
	lua "github.com/yuin/gopher-lua"
)

func (c *Conductor) moduleLogLoader(state *lua.LState) int {
	var exports = map[string]lua.LGFunction{
		"debug": c.moduleLogPrint("DEBUG"),
		"info":  c.moduleLogPrint("INFO"),
		"warn":  c.moduleLogPrint("WARN"),
		"error": c.moduleLogPrint("ERROR"),
	}
	mod := state.SetFuncs(state.NewTable(), exports)

	state.Push(mod)
	return 1
}

func (c *Conductor) moduleLogPrint(level string) lua.LGFunction {
	return func(state *lua.LState) int {
		s := "[" + level + "] " + state.Where(1)

		for i := 1; i <= state.GetTop(); i++ {
			s += " " + state.Get(i).String()
		}

		log.Printf(s)

		return 0
	}
}
