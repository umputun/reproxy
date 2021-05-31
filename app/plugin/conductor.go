// Package plugin provides support for RPC plugins with registration server.
// It also implements middleware calling all the registered and alive plugins
package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	log "github.com/go-pkgz/lgr"

	"github.com/umputun/reproxy/lib"
)

//go:generate moq -out dialer_mock.go -fmt goimports . RPCDialer
//go:generate moq -out client_mock.go -fmt goimports . RPCClient

// Conductor accepts registrations from rpc plugins, keeps list of active/current plugins and provides middleware calling all of them.
type Conductor struct {
	Address   string
	RPCDialer RPCDialer

	plugins []Handler
	lock    sync.RWMutex
}

// Handler contains information about a plugin's handler
type Handler struct {
	Address string
	Method  string // full method name for rpc call, i.e. Plugin.Thing
	Alive   bool

	delete bool
	client RPCClient
}

// ConductorCtxtKey used to retrieve conductor from context
type ConductorCtxtKey string

// RPCDialer is a maker interface dialing to rpc server and returning new RPCClient
type RPCDialer interface {
	Dial(network, address string) (RPCClient, error)
}

// RPCDialerFunc is an adapter to allow the use of an ordinary functions as the RPCDialer.
type RPCDialerFunc func(network, address string) (RPCClient, error)

// Dial rpc server
func (f RPCDialerFunc) Dial(network, address string) (RPCClient, error) {
	return f(network, address)
}

// RPCClient defines interface for remote calls
type RPCClient interface {
	Call(serviceMethod string, args interface{}, reply interface{}) error
}

// Run creates and activates http registration server
// TODO: add some basic auth in case if exposed by accident
func (c *Conductor) Run(ctx context.Context) error {
	log.Printf("[INFO] start plugin conductor on %s", c.Address)
	httpServer := &http.Server{
		Addr:              c.Address,
		Handler:           c.registrationHandler(),
		ReadHeaderTimeout: 50 * time.Millisecond,
		WriteTimeout:      50 * time.Millisecond,
		IdleTimeout:       50 * time.Millisecond,
	}

	go func() {
		<-ctx.Done()
		if err := httpServer.Close(); err != nil {
			log.Printf("[ERROR] failed to close plugin registration server, %v", err)
		}
	}()

	// gob.Register(url.URL{})
	return httpServer.ListenAndServe()
}

// Middleware hits all registered, alive-only plugins and modifies the original request accordingly
// Failed plugin calls ignored. Status code from any plugin may stop the chain of calls if not 200. This is needed
// to allow plugins like auth which has to terminate request in some cases.
func (c *Conductor) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		c.lock.RLock()
		for _, p := range c.plugins {
			if !p.Alive {
				continue
			}

			var reply lib.HandlerResponse
			if err := p.client.Call(p.Method, c.makeRequest(r), &reply); err != nil {
				log.Printf("[WARN] failed to invoke plugin handler %s: %v", p.Method, err)
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
			for k, vv := range reply.Header {
				for _, v := range vv {
					w.Header().Add(k, v)
				}
			}
			if reply.StatusCode >= 400 {
				c.lock.RUnlock()
				http.Error(w, http.StatusText(reply.StatusCode), reply.StatusCode)
				return
			}
		}
		c.lock.RUnlock()

		next.ServeHTTP(w, r)
	})
}

// makeRequest creates plugin request from http.Request
// uses context set downstream (by proxyHandler)
func (c *Conductor) makeRequest(r *http.Request) lib.Request {
	ctx := r.Context()
	res := lib.Request{
		URL:        r.URL.String(),
		RemoteAddr: r.RemoteAddr,
		Host:       r.URL.Hostname(),
		Header:     r.Header,
	}

	if v, ok := ctx.Value(ConductorCtxtKey("server")).(string); ok {
		res.Server = v
	}
	if v, ok := ctx.Value(ConductorCtxtKey("src")).(string); ok {
		res.Src = v
	}
	if v, ok := ctx.Value(ConductorCtxtKey("dst")).(string); ok {
		res.Dst = v
	}
	if v, ok := ctx.Value(ConductorCtxtKey("route")).(string); ok {
		res.Route = v
	}
	if v, ok := ctx.Value(ConductorCtxtKey("provider")).(string); ok {
		res.Provider = v
	}

	return res
}

// registrationHandler accept POST or DELETE with lib.Plugin body and register/unregister plugin provider
func (c *Conductor) registrationHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "POST":
			var plugin lib.Plugin
			if err := json.NewDecoder(r.Body).Decode(&plugin); err != nil {
				http.Error(w, "plugin registration failed", http.StatusBadRequest)
				return
			}
			c.locked(func() {
				if err := c.register(plugin); err != nil {
					log.Printf("[WARN] rpc registration failed, %v", err)
					http.Error(w, "rpc registration failed", http.StatusInternalServerError)
					return
				}
			})
		case "DELETE":
			var plugin lib.Plugin
			if err := json.NewDecoder(r.Body).Decode(&plugin); err != nil {
				http.Error(w, "failed to unregister plugin", http.StatusBadRequest)
				return
			}
			c.locked(func() { c.unregister(plugin) })
		default:
			http.Error(w, "invalid request type", http.StatusBadRequest)
		}
	})
}

// register plugin, not thread safe! call should be enclosed with lock
// creates tcp client, retrieves list of handlers (methods) and adds each one with the full method name
func (c *Conductor) register(p lib.Plugin) error {

	var pp []Handler // collect all handlers after registration
	for _, h := range c.plugins {
		if strings.HasPrefix(h.Method, p.Name+".") && h.Address == p.Address { // already registered
			log.Printf("[WARN] plugin %+v already registered", p)
			return nil
		}

		if strings.HasPrefix(h.Method, p.Name+".") && h.Address != p.Address { // registered, but address changed
			log.Printf("[WARN] plugin %+v already registered, but address changed to %s", h, p.Address)
			continue // remove from the collected pp
		}
		pp = append(pp, h)
	}

	client, err := c.RPCDialer.Dial("tcp", p.Address)
	if err != nil {
		return fmt.Errorf("can't reach plugin %+v: %v", p, err)
	}

	for _, l := range p.Methods {
		handler := Handler{client: client, Alive: true, Address: p.Address, Method: p.Name + "." + l}
		pp = append(pp, handler)
		log.Printf("[INFO] register plugin %s:%s, method: %s", p.Name, p.Address, handler.Method)
	}
	c.plugins = pp
	return nil
}

// unregister plugin, not thread safe! call should be enclosed with lock
func (c *Conductor) unregister(p lib.Plugin) {
	log.Printf("[INFO] unregister plugin %s:%s", p.Name, p.Address)
	var res []Handler
	for _, h := range c.plugins {
		if strings.HasPrefix(h.Method, p.Name+".") {
			continue
		}
		res = append(res, h)
	}
	c.plugins = res
}

func (c *Conductor) locked(fn func()) {
	c.lock.Lock()
	fn()
	c.lock.Unlock()
}
