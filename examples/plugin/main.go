package main

import (
	"context"
	"log"

	"github.com/umputun/reproxy/lib"
)

func main() {

	plugin := lib.Plugin{
		Name:    "TestPlugin",
		Address: ":1234",
	}
	log.Printf("start demo plugin")

	if err := plugin.Do(context.TODO(), "", new(Handler)); err != nil {
		log.Fatal(err)
	}
}

type Handler struct{}

func (h *Handler) List(_ lib.Request, res *lib.ListResponse) (err error) {
	res.Methods = append(res.Methods, "HeaderThing")
	res.Methods = append(res.Methods, "ErrorThing")
	return
}

func (h *Handler) HeaderThing(req lib.Request, res *lib.HandlerResponse) (err error) {
	log.Printf("req: %+v", req.HttpReq)
	res.Header = req.HttpReq.Header
	res.Header.Add("key", "val")
	res.StatusCode = 200
	return
}

func (h *Handler) ErrorThing(req lib.Request, res *lib.HandlerResponse) (err error) {
	log.Printf("req: %+v", req.HttpReq)
	if req.HttpReq.URL.Path == "/fail" {
		res.StatusCode = 500
		return
	}

	res.StatusCode = 200
	return
}
