package lib

import (
	"net/http"
)

// Request sent to plugins
type Request struct {
	HttpReq  http.Request // the original request
	Server   string       // matched server
	Src      string       // matches src
	Dst      string       // matched destination
	Route    string       // proxy route
	Provider string
}

// HandlerResponse from plugins Handle call
type HandlerResponse struct {
	StatusCode int
	Header     http.Header
}

// ListResponse from plugins List call
type ListResponse struct {
	Methods []string
}
