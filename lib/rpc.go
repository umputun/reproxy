package lib

import (
	"net/http"
)

// Request sent to plugins
type Request struct {
	Method     string
	URL        string
	RemoteAddr string
	Host       string
	Header     http.Header
	Route      string // final destination
	Match      struct {
		Server         string
		Src            string
		Dst            string
		ProviderID     string
		PingURL        string
		MatchType      string
		AssetsLocation string
		AssetsWebRoot  string
	}
}

// Response from plugin's handler call
type Response struct {
	StatusCode         int
	HeadersIn          http.Header
	HeadersOut         http.Header
	OverrideHeadersIn  bool // indicates plugin removing all the original incoming headers
	OverrideHeadersOut bool // indicates plugin removing all the original outgoing headers
}
