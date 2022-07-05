package plugins

import (
	"net/http"
)

type contextKey string

const (
	CtxMatch = contextKey("match")
)

type Plugin struct {
	Name    string
	Handler func(next http.Handler) http.Handler
}

var plugins []Plugin
var pluginsNames = map[string]struct{}{}

func HasPlugin(name string) bool {
	_, ok := pluginsNames[name]
	return ok
}

func Plugins() []Plugin {
	return plugins
}

func registerPlugin(p Plugin) {
	if _, ok := pluginsNames[p.Name]; ok {
		panic("duplicate plugin name: " + p.Name)
	}
	pluginsNames[p.Name] = struct{}{}
	plugins = append(plugins, p)
}
