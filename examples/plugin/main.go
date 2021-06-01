package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/umputun/reproxy/lib"
)

func main() {

	// create demo plugin on port 1234 with two methods: HeaderThing and ErrorThing
	// both called via RPC from reproxy core with fully formed lib.Request
	plugin := lib.Plugin{
		Name:    "TestPlugin",
		Address: "plugin-example:1234",
		Methods: []string{"HeaderThing", "ErrorThing"},
	}
	log.Printf("start demo plugin")
	time.Sleep(time.Second) // allow reproxy container to start
	// Do starts the plugin listener and register with reproxy plugin conductor
	if err := plugin.Do(context.TODO(), "http://reproxy:8081", new(Handler)); err != nil {
		log.Fatal(err)
	}
}

// Handler is an example of middleware handler altering headers and stastus
type Handler struct{}

// HeaderThing adds key:val header to the response
func (h *Handler) HeaderThing(req lib.Request, res *lib.Response) (err error) {
	log.Printf("req: %+v", req)
	res.HeadersOut = http.Header{}
	res.HeadersOut.Add("key", "val")
	res.HeadersIn = http.Header{}
	res.HeadersIn.Add("token", "something")
	res.StatusCode = 200 // each handler has to set status code
	return
}

// ErrorThing returns status 500 on "/fail" url. This terminated processing chain on reproxy side immediately
func (h *Handler) ErrorThing(req lib.Request, res *lib.Response) (err error) {
	log.Printf("req: %+v", req)
	if req.URL == "/fail" {
		res.StatusCode = 500
		return
	}
	res.StatusCode = 200
	return
}
