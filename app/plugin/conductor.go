// Package plugin provides support for RPC plugins with registration server.
// It also implements middleware calling all the registered and alive plugins
package plugin

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/rpc"
	"net/url"
	"strings"
	"sync"
	"time"

	log "github.com/go-pkgz/lgr"

	"github.com/umputun/reproxy/app/discovery"
	"github.com/umputun/reproxy/lib"
)

//go:generate moq -out dialer_mock.go -fmt goimports . RPCDialer
//go:generate moq -out client_mock.go -fmt goimports . RPCClient

// Conductor accepts registrations from rpc plugins, keeps list of active/current plugins and provides middleware calling all of them.
type Conductor struct {
	RPCDialer RPCDialer
	Secret    string        // bearer token required to register or unregister, empty allows anyone
	Timeout   time.Duration // limit for all plugin calls of one request and for each registration dial, 0 for none

	plugins []Handler
	lock    sync.RWMutex
	regLock sync.Mutex // serializes register and unregister, so an unregister can't land inside a dial
}

// Handler contains information about a plugin's handler
type Handler struct {
	Address string
	Method  string // full method name for rpc call, i.e. Plugin.Thing
	Alive   bool
	conn    *pluginConn
}

// maxIdlePluginClients bounds the connections kept open for reuse per plugin. Each call holds its own
// connection, so concurrent calls beyond this number dial extra ones and close them when done.
const maxIdlePluginClients = 16

// pluginConn holds the rpc connections of one registered plugin, shared by its handlers. Each call
// leases a connection exclusively, so a call that runs out of time closes only its own connection
// and never fails the calls of other requests.
type pluginConn struct {
	address string
	dialer  RPCDialer
	lock    sync.Mutex
	idle    []RPCClient
	retired bool // set once the plugin is unregistered or re-registered at another address
}

// conductorCtxtKey used to retrieve conductor from context
type conductorCtxtKey string

// CtxMatch key used to retrieve matching request info from the request context
const CtxMatch = conductorCtxtKey("match")

// RPCDialer is a maker interface dialing to rpc server and returning new RPCClient
type RPCDialer interface {
	Dial(ctx context.Context, network, address string) (RPCClient, error)
}

// RPCDialerFunc is an adapter to allow the use of an ordinary functions as the RPCDialer.
type RPCDialerFunc func(ctx context.Context, network, address string) (RPCClient, error)

// Dial rpc server
func (f RPCDialerFunc) Dial(ctx context.Context, network, address string) (RPCClient, error) {
	return f(ctx, network, address)
}

// RPCClient defines interface for remote calls
type RPCClient interface {
	Call(serviceMethod string, args any, reply any) error
	Close() error
}

// Run serves plugin registrations on ln until ctx is done. The caller binds ln, so a taken address
// fails at startup instead of leaving the proxy running without its plugins.
func (c *Conductor) Run(ctx context.Context, ln net.Listener) error {
	log.Printf("[INFO] start plugin conductor on %s", ln.Addr())
	if c.Secret == "" {
		log.Printf("[WARN] plugin registration has no secret, anyone reaching %s can add or remove plugins", ln.Addr())
	}
	httpServer := &http.Server{
		Handler:           c.registrationHandler(),
		ReadHeaderTimeout: 50 * time.Millisecond,
		WriteTimeout:      c.Timeout + 50*time.Millisecond, // the registration answer follows the plugin dial
		IdleTimeout:       50 * time.Millisecond,
	}

	go func() {
		<-ctx.Done()
		if err := httpServer.Close(); err != nil {
			log.Printf("[ERROR] failed to close plugin registration server, %v", err)
		}
	}()

	if err := httpServer.Serve(ln); err != nil {
		return fmt.Errorf("plugin conductor server failed: %w", err)
	}
	return nil
}

// Middleware hits all registered, alive-only plugins and modifies the original request accordingly.
// A plugin call error terminates the chain with HTTP 500. A reply status code >= 400 also stops the
// chain with that status, to allow plugins like auth which has to terminate the request in some cases.
func (c *Conductor) Middleware(next http.Handler) http.Handler {

	setHeaders := func(src, alt http.Header, overrideHeaders bool) {
		if overrideHeaders {
			for k := range src {
				src.Del(k)
			}
		}
		for k, vv := range alt {
			for _, v := range vv {
				src.Add(k, v)
			}
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		c.lock.RLock()
		alive := append([]Handler(nil), c.plugins...)
		c.lock.RUnlock()

		ctx := r.Context()
		if c.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, c.Timeout)
			defer cancel()
		}

		for _, p := range alive {
			if !p.Alive {
				continue
			}

			var reply lib.Response
			if err := p.conn.call(ctx, p.Method, c.makeRequest(r), &reply); err != nil {
				log.Printf("[WARN] failed to invoke plugin handler %s: %v", p.Method, err)
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}

			setHeaders(r.Header, reply.HeadersIn, reply.OverrideHeadersIn)
			setHeaders(w.Header(), reply.HeadersOut, reply.OverrideHeadersOut)

			if reply.StatusCode >= 400 {
				http.Error(w, http.StatusText(reply.StatusCode), reply.StatusCode)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// makeRequest creates plugin request from http.Request
// uses context set by downstream (by proxyHandler)
func (c *Conductor) makeRequest(r *http.Request) lib.Request {
	ctx := r.Context()
	res := lib.Request{
		// the client's escaping is dropped so plugins check the same path route matching and the upstream see
		URL:        (&url.URL{Path: r.URL.Path, RawQuery: r.URL.RawQuery}).String(),
		RemoteAddr: r.RemoteAddr,
		Host:       discovery.HostName(r.Host),
		Header:     r.Header,
	}

	if v, ok := ctx.Value(CtxMatch).(discovery.MatchedRoute); ok {
		res.Route = v.Destination
		res.Match.MatchType = v.Mapper.MatchType.String()
		res.Match.ProviderID = string(v.Mapper.ProviderID)
		res.Match.Server = v.Mapper.Server
		res.Match.Src = v.Mapper.SrcMatch.String()
		res.Match.Dst = v.Mapper.Dst
		res.Match.PingURL = v.Mapper.PingURL
		res.Match.AssetsLocation = v.Mapper.AssetsLocation
		res.Match.AssetsWebRoot = v.Mapper.AssetsWebRoot
	}

	return res
}

// registrationHandler accept POST or DELETE with lib.Plugin body and register/unregister plugin provider
func (c *Conductor) registrationHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !c.authorized(r) {
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		switch r.Method {
		case "POST":
			var plugin lib.Plugin
			if err := json.NewDecoder(r.Body).Decode(&plugin); err != nil {
				http.Error(w, "plugin registration failed", http.StatusBadRequest)
				return
			}
			if err := c.register(plugin); err != nil {
				log.Printf("[WARN] rpc registration failed, %v", err)
				http.Error(w, "rpc registration failed", http.StatusInternalServerError)
				return
			}
		case "DELETE":
			var plugin lib.Plugin
			if err := json.NewDecoder(r.Body).Decode(&plugin); err != nil {
				http.Error(w, "failed to unregister plugin", http.StatusBadRequest)
				return
			}
			c.unregister(plugin)
		default:
			http.Error(w, "invalid request type", http.StatusBadRequest)
		}
	})
}

// authorized reports whether r carries the registration secret as a bearer token, or no secret is set.
func (c *Conductor) authorized(r *http.Request) bool {
	if c.Secret == "" {
		return true
	}
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && subtle.ConstantTimeCompare([]byte(token), []byte(c.Secret)) == 1
}

// register dials the plugin and adds a handler for each of its methods under the full method name.
// The dial runs without the request lock, so a slow or unreachable plugin address doesn't stall requests.
// It is bounded by Timeout alone, not by the registration request: lib.Plugin retries a POST that
// takes over 1s, and a dial cut short with it would never finish registering a slow plugin.
func (c *Conductor) register(p lib.Plugin) error {
	c.regLock.Lock()
	defer c.regLock.Unlock()
	if c.isRegistered(p) {
		log.Printf("[WARN] plugin %+v already registered", p)
		return nil
	}

	ctx := context.Background()
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	client, err := c.RPCDialer.Dial(ctx, "tcp", p.Address)
	if err != nil {
		return fmt.Errorf("can't reach plugin %+v: %w", p, err)
	}

	conn := &pluginConn{address: p.Address, dialer: c.RPCDialer, idle: []RPCClient{client}}
	var replaced []*pluginConn
	c.locked(func() {
		var pp []Handler //nolint
		for _, h := range c.plugins {
			if !strings.HasPrefix(h.Method, p.Name+".") {
				pp = append(pp, h)
				continue
			}
			log.Printf("[WARN] plugin %+v already registered, but address changed to %s", h, p.Address)
			replaced = append(replaced, h.conn)
		}
		for _, l := range p.Methods {
			handler := Handler{conn: conn, Alive: true, Address: p.Address, Method: p.Name + "." + l}
			pp = append(pp, handler)
			log.Printf("[INFO] register plugin %s, ip: %s, method: %s", p.Name, p.Address, handler.Method)
		}
		if len(p.Methods) == 0 { // no handler owns the connection, so nothing would ever close it
			replaced = append(replaced, conn)
		}
		c.plugins = pp
	})
	c.retireConns(replaced)
	return nil
}

func (c *Conductor) isRegistered(p lib.Plugin) bool {
	c.lock.RLock()
	defer c.lock.RUnlock()
	for _, h := range c.plugins {
		if strings.HasPrefix(h.Method, p.Name+".") && h.Address == p.Address {
			return true
		}
	}
	return false
}

// unregister removes all handlers of the plugin and retires their connections.
func (c *Conductor) unregister(p lib.Plugin) {
	log.Printf("[INFO] unregister plugin %s, ip: %s", p.Name, p.Address)
	c.regLock.Lock()
	defer c.regLock.Unlock()
	var removed []*pluginConn
	c.locked(func() {
		var res []Handler //nolint
		for _, h := range c.plugins {
			if strings.HasPrefix(h.Method, p.Name+".") {
				removed = append(removed, h.conn)
				continue
			}
			res = append(res, h)
		}
		c.plugins = res
	})
	c.retireConns(removed)
}

// retireConns retires each distinct connection set; handlers of one plugin share theirs.
func (c *Conductor) retireConns(conns []*pluginConn) {
	seen := map[*pluginConn]bool{}
	for _, pc := range conns {
		if seen[pc] {
			continue
		}
		seen[pc] = true
		pc.retire()
	}
}

// call runs one plugin method on a leased connection, dialing one when none is idle. Both the dial and
// the call are bounded by ctx; when ctx ends first the leased connection is closed, which unblocks the
// pending call, and the error is returned.
func (pc *pluginConn) call(ctx context.Context, method string, args, reply any) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("plugin call %s: %w", method, err)
	}
	client, err := pc.lease(ctx)
	if err != nil {
		return err
	}

	done := make(chan error, 1)
	go func() { done <- client.Call(method, args, reply) }()
	select {
	case err := <-done:
		var serr rpc.ServerError
		if err == nil || errors.As(err, &serr) { // a method error leaves the connection usable
			pc.release(client)
			return err
		}
		pc.closeClient(client)
		return err
	case <-ctx.Done():
		pc.closeClient(client)
		return fmt.Errorf("plugin call %s: %w", method, ctx.Err())
	}
}

// lease takes an idle connection or dials a new one. A retired plugin is never dialed again.
func (pc *pluginConn) lease(ctx context.Context) (RPCClient, error) {
	pc.lock.Lock()
	if pc.retired {
		pc.lock.Unlock()
		return nil, fmt.Errorf("plugin %s is no longer registered", pc.address)
	}
	if n := len(pc.idle); n > 0 {
		client := pc.idle[n-1]
		pc.idle = pc.idle[:n-1]
		pc.lock.Unlock()
		return client, nil
	}
	pc.lock.Unlock()

	client, err := pc.dialer.Dial(ctx, "tcp", pc.address)
	if err != nil {
		return nil, fmt.Errorf("can't reach plugin %s: %w", pc.address, err)
	}
	pc.lock.Lock()
	retired := pc.retired
	pc.lock.Unlock()
	if retired { // unregistered while dialing
		pc.closeClient(client)
		return nil, fmt.Errorf("plugin %s is no longer registered", pc.address)
	}
	return client, nil
}

// release returns a leased connection for reuse, or closes it when the plugin is retired or enough
// connections are idle already.
func (pc *pluginConn) release(client RPCClient) {
	pc.lock.Lock()
	if !pc.retired && len(pc.idle) < maxIdlePluginClients {
		pc.idle = append(pc.idle, client)
		pc.lock.Unlock()
		return
	}
	pc.lock.Unlock()
	pc.closeClient(client)
}

// retire closes the idle connections and makes leases fail. Leased connections close when released.
func (pc *pluginConn) retire() {
	pc.lock.Lock()
	pc.retired = true
	idle := pc.idle
	pc.idle = nil
	pc.lock.Unlock()
	for _, client := range idle {
		pc.closeClient(client)
	}
}

func (pc *pluginConn) closeClient(client RPCClient) {
	if err := client.Close(); err != nil {
		log.Printf("[DEBUG] failed to close plugin client %s, %v", pc.address, err)
	}
}

func (c *Conductor) locked(fn func()) {
	c.lock.Lock()
	fn()
	c.lock.Unlock()
}
