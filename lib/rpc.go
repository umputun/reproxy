package lib

import (
	"net/http"
)

// Request sent to plugins
type Request struct {
	URL        string
	RemoteAddr string
	Host       string
	Header     http.Header

	Server   string
	Src      string // matches src
	Dst      string // matched destination
	Route    string // proxy route
	Provider string // route provider
}

// HandlerResponse from plugins Handle call
type HandlerResponse struct {
	StatusCode int
	Header     http.Header
}
