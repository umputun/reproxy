package lib

import (
	"net/http"
	"regexp"
	"runtime"
)

type contextKey string

// CtxMatch key used to retrieve matching request info from the request context
const CtxMatch = contextKey("match")

type Plugin struct {
	Name    string
	Handler func(http.Handler) http.Handler
}

var plugins []*Plugin
var pluginsNames = map[string]struct{}{}

var re = regexp.MustCompile(`/plugins/(.*).InitPlugin\(`)

func Register(handler func(http.Handler) http.Handler) {
	buf := make([]byte, 1024)

	n := runtime.Stack(buf, true)

	res := re.FindSubmatch(buf[:n])
	if len(res) == 0 {
		panic("Register: can't find plugin name")
	}

	name := string(res[1])

	if _, ok := pluginsNames[name]; ok {
		panic("Duplicate plugin name: " + name)
	}
	pluginsNames[name] = struct{}{}

	plugins = append(plugins, &Plugin{Name: name, Handler: handler})
}

func HasPlugin(name string) bool {
	_, ok := pluginsNames[name]
	return ok
}

func Plugins() []*Plugin {
	return plugins
}
