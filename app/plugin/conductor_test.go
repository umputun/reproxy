package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/rpc"
	"regexp"
	"sync"
	"testing"
	"time"

	log "github.com/go-pkgz/lgr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/reproxy/app/discovery"
	"github.com/umputun/reproxy/lib"
)

func TestConductor_registrationHandler(t *testing.T) {

	rpcClient := &RPCClientMock{
		CloseFunc: func() error { return nil },
		CallFunc: func(serviceMethod string, args any, reply any) error {
			return nil
		},
	}

	dialer := &RPCDialerMock{
		DialFunc: func(_ context.Context, network string, address string) (RPCClient, error) {
			return rpcClient, nil
		},
	}

	c := Conductor{RPCDialer: dialer}
	ts := httptest.NewServer(c.registrationHandler())
	defer ts.Close()

	client := http.Client{Timeout: time.Second}

	{ // register plugin with two methods
		plugin := lib.Plugin{Name: "Test1", Address: "127.0.0.1:0001", Methods: []string{"Mw1", "Mw2"}}
		data, err := json.Marshal(plugin)
		require.NoError(t, err)
		req, err := http.NewRequest("POST", ts.URL, bytes.NewReader(data))
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		assert.Len(t, c.plugins, 2, "two plugins registered")
		assert.Equal(t, "Test1.Mw1", c.plugins[0].Method)
		assert.Equal(t, "127.0.0.1:0001", c.plugins[0].Address)
		assert.True(t, c.plugins[0].Alive)

		assert.Equal(t, "127.0.0.1:0001", c.plugins[1].Address)
		assert.Equal(t, "Test1.Mw2", c.plugins[1].Method)
		assert.True(t, c.plugins[1].Alive)

		assert.Empty(t, rpcClient.CallCalls())
		assert.Len(t, dialer.DialCalls(), 1)
	}

	{ // same registration
		plugin := lib.Plugin{Name: "Test1", Address: "127.0.0.1:0001", Methods: []string{"Mw1", "Mw2"}}
		data, err := json.Marshal(plugin)
		require.NoError(t, err)
		req, err := http.NewRequest("POST", ts.URL, bytes.NewReader(data))
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Len(t, c.plugins, 2, "two plugins registered")
		assert.Empty(t, rpcClient.CallCalls())
		assert.Len(t, dialer.DialCalls(), 1)
	}

	{ // address changed
		plugin := lib.Plugin{Name: "Test1", Address: "127.0.0.2:8002", Methods: []string{"Mw1", "Mw2"}}
		data, err := json.Marshal(plugin)
		require.NoError(t, err)
		req, err := http.NewRequest("POST", ts.URL, bytes.NewReader(data))
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Len(t, c.plugins, 2, "two plugins registered")
		assert.Equal(t, "Test1.Mw1", c.plugins[0].Method)
		assert.Equal(t, "127.0.0.2:8002", c.plugins[0].Address)
		assert.True(t, c.plugins[0].Alive)

		assert.Equal(t, "127.0.0.2:8002", c.plugins[1].Address)
		assert.Equal(t, "Test1.Mw2", c.plugins[1].Method)
		assert.True(t, c.plugins[1].Alive)

		assert.Empty(t, rpcClient.CallCalls())
		assert.Len(t, dialer.DialCalls(), 2)
	}

	{ // address changed
		plugin := lib.Plugin{Name: "Test2", Address: "127.0.0.3:8003", Methods: []string{"Mw11", "Mw12", "Mw13"}}
		data, err := json.Marshal(plugin)
		require.NoError(t, err)
		req, err := http.NewRequest("POST", ts.URL, bytes.NewReader(data))
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Len(t, c.plugins, 2+3, "3 more plugins registered")
		assert.Equal(t, "Test2.Mw11", c.plugins[2].Method)
		assert.Equal(t, "127.0.0.3:8003", c.plugins[2].Address)
		assert.True(t, c.plugins[2].Alive)

		assert.Empty(t, rpcClient.CallCalls())
		assert.Len(t, dialer.DialCalls(), 3)
	}

	{ // bad registration
		req, err := http.NewRequest("POST", ts.URL, bytes.NewBufferString("bas json body"))
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	}

	{ // unsupported registration method
		plugin := lib.Plugin{Name: "Test2", Address: "127.0.0.3:8003", Methods: []string{"Mw11", "Mw12", "Mw13"}}
		data, err := json.Marshal(plugin)
		require.NoError(t, err)
		req, err := http.NewRequest("PUT", ts.URL, bytes.NewReader(data))
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	}

	{ // unregister
		plugin := lib.Plugin{Name: "Test1", Address: "127.0.0.2:8002", Methods: []string{"Mw1", "Mw2"}}
		data, err := json.Marshal(plugin)
		require.NoError(t, err)
		req, err := http.NewRequest("DELETE", ts.URL, bytes.NewReader(data))
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Len(t, c.plugins, 3, "3 plugins left, 2 removed")

		assert.Equal(t, "Test2.Mw11", c.plugins[0].Method)
		assert.Equal(t, "127.0.0.3:8003", c.plugins[0].Address)
		assert.True(t, c.plugins[0].Alive)

		assert.Empty(t, rpcClient.CallCalls())
		assert.Len(t, dialer.DialCalls(), 3)
	}

	{ // bad unregister
		req, err := http.NewRequest("DELETE", ts.URL, bytes.NewBufferString("bad json body"))
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		assert.Len(t, c.plugins, 3, "still 3 plugins left, 2 removed")
	}
}

func TestConductor_registrationHandlerInternalError(t *testing.T) {

	dialer := &RPCDialerMock{
		DialFunc: func(_ context.Context, network string, address string) (RPCClient, error) {
			return nil, errors.New("failed")
		},
	}

	c := Conductor{RPCDialer: dialer}
	ts := httptest.NewServer(c.registrationHandler())
	defer ts.Close()

	client := http.Client{Timeout: time.Second}
	plugin := lib.Plugin{Name: "Test1", Address: "127.0.0.1:0001"}
	data, err := json.Marshal(plugin)
	require.NoError(t, err)
	req, err := http.NewRequest("POST", ts.URL, bytes.NewReader(data))
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestConductor_Middleware(t *testing.T) {

	rpcClient := &RPCClientMock{
		CloseFunc: func() error { return nil },
		CallFunc: func(serviceMethod string, args any, reply any) error {

			if serviceMethod == "Test1.Mw1" {
				req := args.(lib.Request)
				assert.Equal(t, "route123", req.Route)
				assert.Equal(t, "src123", req.Match.Src)
				assert.Equal(t, "dst123", req.Match.Dst)
				assert.Equal(t, "docker", req.Match.ProviderID)
				assert.Equal(t, "server123", req.Match.Server)
				assert.Equal(t, "proxy", req.Match.MatchType)
				assert.Equal(t, "/webroot", req.Match.AssetsWebRoot)
				assert.Equal(t, "loc", req.Match.AssetsLocation)
				log.Printf("rr: %+v", req)
				reply.(*lib.Response).StatusCode = 200
				reply.(*lib.Response).HeadersOut = map[string][]string{}
				reply.(*lib.Response).HeadersOut.Set("k1", "v1")
				reply.(*lib.Response).HeadersIn = map[string][]string{}
				reply.(*lib.Response).HeadersIn.Set("k21", "v21")

			}
			if serviceMethod == "Test1.Mw2" {
				req := args.(lib.Request)
				assert.Equal(t, "route123", req.Route)
				assert.Equal(t, "src123", req.Match.Src)
				assert.Equal(t, "dst123", req.Match.Dst)
				assert.Equal(t, "docker", req.Match.ProviderID)
				assert.Equal(t, "server123", req.Match.Server)
				log.Printf("rr: %+v", req)
				reply.(*lib.Response).StatusCode = 200
				reply.(*lib.Response).HeadersOut = map[string][]string{}
				reply.(*lib.Response).HeadersOut.Set("k11", "v11")
				reply.(*lib.Response).OverrideHeadersOut = true
			}
			if serviceMethod == "Test1.Mw3" {
				t.Fatal("shouldn't be called")
			}
			return nil
		},
	}

	dialer := &RPCDialerMock{
		DialFunc: func(_ context.Context, network string, address string) (RPCClient, error) {
			return rpcClient, nil
		},
	}

	c := Conductor{RPCDialer: dialer}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	go func() {
		c.Run(ctx, ln)
	}()

	client := http.Client{Timeout: time.Second}

	// register plugin with 3 methods
	plugin := lib.Plugin{Name: "Test1", Address: "127.0.0.1:8001", Methods: []string{"Mw1", "Mw2", "Mw3"}}
	data, err := json.Marshal(plugin)
	require.NoError(t, err)
	req, err := http.NewRequest("POST", "http://"+ln.Addr().String(), bytes.NewReader(data))
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Len(t, c.plugins, 3, "3 plugins registered")
	c.plugins[2].Alive = false // set 3rd to dead

	rr, err := http.NewRequest("GET", "http://127.0.0.1", http.NoBody)
	require.NoError(t, err)

	m := discovery.MatchedRoute{
		Destination: "route123",
		Mapper: discovery.URLMapper{
			Server:         "server123",
			ProviderID:     discovery.PIDocker,
			MatchType:      discovery.MTProxy,
			SrcMatch:       *regexp.MustCompile("src123"),
			Dst:            "dst123",
			AssetsWebRoot:  "/webroot",
			AssetsLocation: "loc",
		},
	}
	rr = rr.WithContext(context.WithValue(rr.Context(), CtxMatch, m))
	w := httptest.NewRecorder()
	h := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("k2", "v2")
		w.Write([]byte("something"))
		assert.Equal(t, "v21", r.Header.Get("k21"))
	}))
	h.ServeHTTP(w, rr)
	assert.Equal(t, 200, w.Result().StatusCode)
	assert.Empty(t, w.Result().Header.Get("k1"))
	assert.Equal(t, "v2", w.Result().Header.Get("k2"))
	assert.Equal(t, "v21", rr.Header.Get("k21"))
	assert.Equal(t, "v11", w.Result().Header.Get("k11"))
	t.Logf("req: %+v", rr)
	t.Logf("resp: %+v", w.Result())
}

func TestConductor_MiddlewarePluginBadStatus(t *testing.T) {

	rpcClient := &RPCClientMock{
		CloseFunc: func() error { return nil },
		CallFunc: func(serviceMethod string, args any, reply any) error {
			if serviceMethod == "Test1.Mw1" {
				req := args.(lib.Request)
				assert.Equal(t, "route123", req.Route)
				assert.Equal(t, "src123", req.Match.Src)
				assert.Equal(t, "dst123", req.Match.Dst)
				assert.Equal(t, "docker", req.Match.ProviderID)
				assert.Equal(t, "server123", req.Match.Server)
				log.Printf("rr: %+v", req)
				reply.(*lib.Response).StatusCode = 404
			}
			return nil
		},
	}

	dialer := &RPCDialerMock{
		DialFunc: func(_ context.Context, network string, address string) (RPCClient, error) {
			return rpcClient, nil
		},
	}

	c := Conductor{RPCDialer: dialer}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	go func() {
		c.Run(ctx, ln)
	}()

	client := http.Client{Timeout: time.Second}

	// register plugin with one methods
	plugin := lib.Plugin{Name: "Test1", Address: "127.0.0.1:8001", Methods: []string{"Mw1"}}
	data, err := json.Marshal(plugin)
	require.NoError(t, err)
	req, err := http.NewRequest("POST", "http://"+ln.Addr().String(), bytes.NewReader(data))
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Len(t, c.plugins, 1, "one plugin registered")

	rr, err := http.NewRequest("GET", "http://127.0.0.1", http.NoBody)
	require.NoError(t, err)

	m := discovery.MatchedRoute{
		Destination: "route123",
		Mapper: discovery.URLMapper{
			Server:         "server123",
			ProviderID:     discovery.PIDocker,
			MatchType:      discovery.MTProxy,
			SrcMatch:       *regexp.MustCompile("src123"),
			Dst:            "dst123",
			AssetsWebRoot:  "/webroot",
			AssetsLocation: "loc",
		},
	}
	rr = rr.WithContext(context.WithValue(rr.Context(), CtxMatch, m))
	w := httptest.NewRecorder()
	h := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Failed() // handler not called on plugin middleware error
	}))
	h.ServeHTTP(w, rr)
	assert.Equal(t, 404, w.Result().StatusCode)
	assert.Empty(t, rr.Header.Get("k1")) // header not set by plugin on error
	t.Logf("req: %+v", rr)
	t.Logf("resp: %+v", w.Result())
}

func TestConductor_MiddlewarePluginFailed(t *testing.T) {

	rpcClient := &RPCClientMock{
		CloseFunc: func() error { return nil },
		CallFunc: func(serviceMethod string, args any, reply any) error {
			if serviceMethod == "Test1.Mw1" {
				return errors.New("something failed")
			}
			return nil
		},
	}

	dialer := &RPCDialerMock{
		DialFunc: func(_ context.Context, network string, address string) (RPCClient, error) {
			return rpcClient, nil
		},
	}

	c := Conductor{RPCDialer: dialer}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	go func() {
		c.Run(ctx, ln)
	}()

	client := http.Client{Timeout: time.Second}

	// register plugin with one methods
	plugin := lib.Plugin{Name: "Test1", Address: "127.0.0.1:8001", Methods: []string{"Mw1"}}
	data, err := json.Marshal(plugin)
	require.NoError(t, err)
	req, err := http.NewRequest("POST", "http://"+ln.Addr().String(), bytes.NewReader(data))
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Len(t, c.plugins, 1, "one plugin registered")

	rr, err := http.NewRequest("GET", "http://127.0.0.1", http.NoBody)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	h := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Failed() // handler not called on plugin middleware error
	}))
	h.ServeHTTP(w, rr)
	assert.Equal(t, 500, w.Result().StatusCode)
	assert.Empty(t, rr.Header.Get("k1")) // header not set by plugin on error
	t.Logf("req: %+v", rr)
	t.Logf("resp: %+v", w.Result())
}

func TestConductor_MiddlewarePluginFailedNoLockLeak(t *testing.T) {
	rpcClient := &RPCClientMock{
		CloseFunc: func() error { return nil },
		CallFunc: func(serviceMethod string, args any, reply any) error {
			return errors.New("something failed")
		},
	}

	c := Conductor{plugins: []Handler{{Method: "Test1.Mw1", Alive: true, conn: &pluginConn{idle: []RPCClient{rpcClient}}}}}

	// trigger the call-error path which must release the read lock
	rr, err := http.NewRequest("GET", "http://127.0.0.1", http.NoBody)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	h := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler must not be called on plugin failure")
	}))
	h.ServeHTTP(w, rr)
	assert.Equal(t, 500, w.Result().StatusCode)

	// a write-lock operation must not block on a leaked read lock
	done := make(chan struct{})
	go func() {
		c.locked(func() { c.plugins = nil })
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("write lock blocked, read lock leaked on plugin call-error path")
	}
}

func TestConductor_MiddlewareReleasesLockDuringCall(t *testing.T) {
	callStarted := make(chan struct{})
	release := make(chan struct{})
	rpcClient := &RPCClientMock{
		CloseFunc: func() error { return nil },
		CallFunc: func(serviceMethod string, args any, reply any) error {
			close(callStarted)
			<-release // hold the call open so the test can probe the lock while it's in flight
			return nil
		},
	}

	c := Conductor{plugins: []Handler{{Method: "Test1.Mw1", Alive: true, conn: &pluginConn{idle: []RPCClient{rpcClient}}}}}

	rr, err := http.NewRequest("GET", "http://127.0.0.1", http.NoBody)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	served := make(chan struct{})
	h := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(served) }))
	go h.ServeHTTP(w, rr)

	<-callStarted // plugin RPC is now blocking

	// while the plugin call is in flight, a write-lock op must still proceed - the read lock must
	// have been released before the blocking RPC, not held across it
	locked := make(chan struct{})
	go func() {
		c.locked(func() {})
		close(locked)
	}()
	select {
	case <-locked:
	case <-time.After(2 * time.Second):
		t.Fatal("write lock blocked while plugin call in flight - read lock held across RPC")
	}

	close(release) // let the plugin call finish
	select {
	case <-served:
	case <-time.After(2 * time.Second):
		t.Fatal("middleware did not reach next handler after plugin call returned")
	}
}

func TestConductor_registrationSecret(t *testing.T) {
	dialer := &RPCDialerMock{DialFunc: func(context.Context, string, string) (RPCClient, error) {
		return &RPCClientMock{CloseFunc: func() error { return nil }}, nil
	}}
	c := Conductor{RPCDialer: dialer, Secret: "s3cret"}
	ts := httptest.NewServer(c.registrationHandler())
	defer ts.Close()

	send := func(method, auth string) int {
		data, err := json.Marshal(lib.Plugin{Name: "Test1", Address: "127.0.0.1:0001", Methods: []string{"Mw1"}})
		require.NoError(t, err)
		req, err := http.NewRequest(method, ts.URL, bytes.NewReader(data))
		require.NoError(t, err)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		return resp.StatusCode
	}

	assert.Equal(t, http.StatusUnauthorized, send("POST", ""))
	assert.Equal(t, http.StatusUnauthorized, send("POST", "Bearer wrong"))
	assert.Equal(t, http.StatusUnauthorized, send("POST", "s3cret"))
	assert.Empty(t, dialer.DialCalls())
	assert.Empty(t, c.plugins)

	assert.Equal(t, http.StatusOK, send("POST", "Bearer s3cret"))
	assert.Len(t, c.plugins, 1)

	assert.Equal(t, http.StatusUnauthorized, send("DELETE", ""))
	assert.Len(t, c.plugins, 1)
	assert.Equal(t, http.StatusOK, send("DELETE", "Bearer s3cret"))
	assert.Empty(t, c.plugins)
}

func TestConductor_registrationDialOutsideLock(t *testing.T) {
	dialStarted, release := make(chan struct{}), make(chan struct{})
	dialer := &RPCDialerMock{DialFunc: func(context.Context, string, string) (RPCClient, error) {
		close(dialStarted)
		<-release
		return &RPCClientMock{}, nil
	}}
	next := &RPCClientMock{CallFunc: func(string, any, any) error { return nil }}
	c := Conductor{RPCDialer: dialer, plugins: []Handler{{Method: "Other.Mw", Alive: true, conn: &pluginConn{idle: []RPCClient{next}}}}}

	registered := make(chan error)
	go func() {
		registered <- c.register(lib.Plugin{Name: "Slow", Address: "10.255.255.1:1", Methods: []string{"Mw"}})
	}()
	<-dialStarted

	served := make(chan struct{})
	h := c.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { close(served) }))
	go h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	select {
	case <-served:
	case <-time.After(2 * time.Second):
		t.Fatal("request blocked behind a registration dial")
	}

	close(release)
	require.NoError(t, <-registered)
	assert.Len(t, c.plugins, 2)
}

func TestConductor_registrationConcurrentSamePlugin(t *testing.T) {
	release := make(chan struct{})
	client := &RPCClientMock{CloseFunc: func() error { return nil }}
	dialer := &RPCDialerMock{DialFunc: func(context.Context, string, string) (RPCClient, error) {
		<-release
		return client, nil
	}}
	c := Conductor{RPCDialer: dialer}

	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			assert.NoError(t, c.register(lib.Plugin{Name: "Test1", Address: "127.0.0.1:1", Methods: []string{"Mw1", "Mw2"}}))
		})
	}
	require.Eventually(t, func() bool { return len(dialer.DialCalls()) == 1 }, time.Second, time.Millisecond)
	close(release)
	wg.Wait()

	assert.Len(t, dialer.DialCalls(), 1, "the second registration waits and finds the plugin registered")
	assert.Len(t, c.plugins, 2)
	assert.Empty(t, client.CloseCalls())
}

func TestConductor_unregisterDuringRegistrationDial(t *testing.T) {
	dialStarted, release := make(chan struct{}), make(chan struct{})
	client := &RPCClientMock{CloseFunc: func() error { return nil }}
	dialer := &RPCDialerMock{DialFunc: func(context.Context, string, string) (RPCClient, error) {
		close(dialStarted)
		<-release
		return client, nil
	}}
	c := Conductor{RPCDialer: dialer}
	p := lib.Plugin{Name: "Test1", Address: "127.0.0.1:1", Methods: []string{"Mw1"}}

	registered := make(chan error)
	go func() { registered <- c.register(p) }()
	<-dialStarted

	unregistered := make(chan struct{})
	go func() { c.unregister(p); close(unregistered) }()
	select {
	case <-unregistered:
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-registered)
	<-unregistered

	assert.Empty(t, c.plugins, "an unregister sent during the dial wins")
	assert.Len(t, client.CloseCalls(), 1)
}

func TestConductor_MiddlewarePluginTimeout(t *testing.T) {
	closed, exited := make(chan struct{}), make(chan struct{})
	stuck := &RPCClientMock{
		CallFunc: func(string, any, any) error {
			<-closed
			close(exited)
			return rpc.ErrShutdown
		},
		CloseFunc: func() error { close(closed); return nil },
	}
	healthy := &RPCClientMock{CallFunc: func(_ string, _, reply any) error {
		reply.(*lib.Response).StatusCode = http.StatusOK
		return nil
	}}
	dialer := &RPCDialerMock{DialFunc: func(context.Context, string, string) (RPCClient, error) { return healthy, nil }}
	c := Conductor{Timeout: 100 * time.Millisecond, plugins: []Handler{{Method: "Test1.Mw1", Alive: true,
		conn: &pluginConn{address: "127.0.0.1:1", dialer: dialer, idle: []RPCClient{stuck}}}}}
	h := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))

	start := time.Now()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Less(t, time.Since(start), time.Second)
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("timed out call was not unblocked by closing its client")
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	assert.Equal(t, http.StatusTeapot, w.Code, "next call redials and reaches the handler")
	assert.Len(t, dialer.DialCalls(), 1)
}

func TestConductor_MiddlewareCanceledRequestKeepsOthers(t *testing.T) {
	started, release := make(chan struct{}, 2), make(chan struct{})
	var clients []*RPCClientMock
	var mu sync.Mutex
	dialer := &RPCDialerMock{DialFunc: func(context.Context, string, string) (RPCClient, error) {
		closed := make(chan struct{})
		cl := &RPCClientMock{
			CallFunc: func(_ string, _, reply any) error {
				started <- struct{}{}
				select {
				case <-release:
					reply.(*lib.Response).StatusCode = http.StatusOK
					return nil
				case <-closed:
					return rpc.ErrShutdown
				}
			},
			CloseFunc: func() error { close(closed); return nil },
		}
		mu.Lock()
		clients = append(clients, cl)
		mu.Unlock()
		return cl, nil
	}}
	c := Conductor{Timeout: 5 * time.Second, plugins: []Handler{{Method: "Test1.Mw1", Alive: true,
		conn: &pluginConn{address: "127.0.0.1:1", dialer: dialer}}}}
	h := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))

	first := httptest.NewRecorder()
	firstDone := make(chan struct{})
	go func() {
		h.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
		close(firstDone)
	}()
	<-started

	ctx, cancel := context.WithCancel(context.Background())
	second := httptest.NewRecorder()
	secondDone := make(chan struct{})
	go func() {
		h.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/", http.NoBody).WithContext(ctx))
		close(secondDone)
	}()
	<-started
	cancel()
	<-secondDone
	assert.Equal(t, http.StatusInternalServerError, second.Code)

	close(release)
	<-firstDone
	assert.Equal(t, http.StatusTeapot, first.Code, "a canceled request must not fail another in-flight call")
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, clients, 2)
	assert.Empty(t, clients[0].CloseCalls())
	assert.Len(t, clients[1].CloseCalls(), 1)
}

func TestConductor_MiddlewareDialWithinDeadline(t *testing.T) {
	dialer := &RPCDialerMock{DialFunc: func(ctx context.Context, _, _ string) (RPCClient, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	c := Conductor{Timeout: 100 * time.Millisecond, plugins: []Handler{{Method: "Test1.Mw1", Alive: true,
		conn: &pluginConn{address: "127.0.0.1:1", dialer: dialer}}}}
	h := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))

	start := time.Now()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Less(t, time.Since(start), time.Second)
}

func TestConductor_MiddlewareExpiredContextSkipsCall(t *testing.T) {
	client := &RPCClientMock{}
	c := Conductor{plugins: []Handler{{Method: "Test1.Mw1", Alive: true, conn: &pluginConn{idle: []RPCClient{client}}}}}
	h := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", http.NoBody).WithContext(ctx))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Empty(t, client.CallCalls())
}

func TestPluginConn_retire(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	idle := &RPCClientMock{CloseFunc: func() error { return nil }}
	active := &RPCClientMock{
		CallFunc:  func(string, any, any) error { close(started); <-release; return nil },
		CloseFunc: func() error { return nil },
	}
	dialer := &RPCDialerMock{}
	pc := &pluginConn{address: "127.0.0.1:1", dialer: dialer, idle: []RPCClient{idle, active}}

	called := make(chan error)
	go func() { called <- pc.call(context.Background(), "Test1.Mw1", lib.Request{}, &lib.Response{}) }()
	<-started

	pc.retire()
	assert.Len(t, idle.CloseCalls(), 1)
	assert.Empty(t, active.CloseCalls(), "an active lease finishes its call")

	close(release)
	require.NoError(t, <-called)
	assert.Len(t, active.CloseCalls(), 1, "a lease returned after retirement is closed")

	require.Error(t, pc.call(context.Background(), "Test1.Mw1", lib.Request{}, &lib.Response{}))
	assert.Empty(t, dialer.DialCalls(), "a retired plugin is not dialed again")
}

func TestPluginConn_retireDuringDial(t *testing.T) {
	dialStarted, release := make(chan struct{}), make(chan struct{})
	client := &RPCClientMock{CloseFunc: func() error { return nil }}
	dialer := &RPCDialerMock{DialFunc: func(context.Context, string, string) (RPCClient, error) {
		close(dialStarted)
		<-release
		return client, nil
	}}
	pc := &pluginConn{address: "127.0.0.1:1", dialer: dialer}

	called := make(chan error)
	go func() { called <- pc.call(context.Background(), "Test1.Mw1", lib.Request{}, &lib.Response{}) }()
	<-dialStarted
	pc.retire()
	close(release)

	require.Error(t, <-called)
	assert.Empty(t, client.CallCalls(), "a retired plugin gets no call on a connection dialed before retirement")
	assert.Len(t, client.CloseCalls(), 1)
}

func TestPluginConn_callErrorKeepsOrClosesClient(t *testing.T) {
	tbl := []struct {
		name   string
		err    error
		closed bool
	}{
		{name: "method error keeps the connection", err: rpc.ServerError("bad input"), closed: false},
		{name: "shutdown closes the connection", err: rpc.ErrShutdown, closed: true},
		{name: "transport error closes the connection", err: errors.New("read: connection reset"), closed: true},
	}
	for _, tt := range tbl {
		t.Run(tt.name, func(t *testing.T) {
			failed := false
			client := &RPCClientMock{
				CallFunc: func(string, any, any) error {
					if failed {
						return nil
					}
					failed = true
					return tt.err
				},
				CloseFunc: func() error { return nil },
			}
			fresh := &RPCClientMock{CallFunc: func(string, any, any) error { return nil }}
			dialer := &RPCDialerMock{DialFunc: func(context.Context, string, string) (RPCClient, error) { return fresh, nil }}
			pc := &pluginConn{address: "127.0.0.1:1", dialer: dialer, idle: []RPCClient{client}}

			require.ErrorIs(t, pc.call(context.Background(), "Test1.Mw1", lib.Request{}, &lib.Response{}), tt.err)
			require.NoError(t, pc.call(context.Background(), "Test1.Mw1", lib.Request{}, &lib.Response{}))
			if tt.closed {
				assert.Len(t, client.CloseCalls(), 1)
				assert.Len(t, client.CallCalls(), 1)
				assert.Len(t, dialer.DialCalls(), 1, "the next call dials a fresh connection")
				return
			}
			assert.Empty(t, client.CloseCalls())
			assert.Len(t, client.CallCalls(), 2, "the next call reuses the connection")
			assert.Empty(t, dialer.DialCalls())
		})
	}
}

func TestConductor_registerWithoutMethods(t *testing.T) {
	client := &RPCClientMock{CloseFunc: func() error { return nil }}
	dialer := &RPCDialerMock{DialFunc: func(context.Context, string, string) (RPCClient, error) { return client, nil }}
	c := Conductor{RPCDialer: dialer}

	require.NoError(t, c.register(lib.Plugin{Name: "Test1", Address: "127.0.0.1:1"}))
	assert.Empty(t, c.plugins)
	assert.Len(t, client.CloseCalls(), 1)
}

func TestConductor_reregistrationRetiresOldConn(t *testing.T) {
	var clients []*RPCClientMock
	dialer := &RPCDialerMock{DialFunc: func(context.Context, string, string) (RPCClient, error) {
		cl := &RPCClientMock{CloseFunc: func() error { return nil }}
		clients = append(clients, cl)
		return cl, nil
	}}
	c := Conductor{RPCDialer: dialer}

	require.NoError(t, c.register(lib.Plugin{Name: "Test1", Address: "127.0.0.1:1", Methods: []string{"Mw1", "Mw2"}}))
	require.NoError(t, c.register(lib.Plugin{Name: "Test1", Address: "127.0.0.1:2", Methods: []string{"Mw1"}}))
	require.Len(t, clients, 2)
	assert.Len(t, clients[0].CloseCalls(), 1, "the old address's client is closed")
	assert.Empty(t, clients[1].CloseCalls())

	c.unregister(lib.Plugin{Name: "Test1", Address: "127.0.0.1:2"})
	assert.Len(t, clients[1].CloseCalls(), 1)
	assert.Empty(t, c.plugins)
}

func TestConductor_unregisterMatchesAddress(t *testing.T) {
	var clients []*RPCClientMock
	dialer := &RPCDialerMock{DialFunc: func(context.Context, string, string) (RPCClient, error) {
		cl := &RPCClientMock{CloseFunc: func() error { return nil }}
		clients = append(clients, cl)
		return cl, nil
	}}
	c := Conductor{RPCDialer: dialer}
	old := lib.Plugin{Name: "Auth", Address: "10.0.0.1:9000", Methods: []string{"Check"}}
	replacement := lib.Plugin{Name: "Auth", Address: "10.0.0.2:9000", Methods: []string{"Check"}}

	require.NoError(t, c.register(old))
	require.NoError(t, c.register(replacement))
	require.Len(t, c.plugins, 1)

	c.unregister(old)
	require.Len(t, c.plugins, 1, "the old instance's exit leaves its replacement registered")
	assert.Equal(t, replacement.Address, c.plugins[0].Address)
	assert.Empty(t, clients[1].CloseCalls())

	c.unregister(replacement)
	assert.Empty(t, c.plugins)
	assert.Len(t, clients[1].CloseCalls(), 1)
}

func TestConductor_dottedNamesAreDistinct(t *testing.T) {
	client := &RPCClientMock{CloseFunc: func() error { return nil }}
	dialer := &RPCDialerMock{DialFunc: func(context.Context, string, string) (RPCClient, error) { return client, nil }}
	c := Conductor{RPCDialer: dialer}
	short := lib.Plugin{Name: "Auth", Address: "10.0.0.1:9000", Methods: []string{"Check"}}
	dotted := lib.Plugin{Name: "Auth.V2", Address: "10.0.0.1:9000", Methods: []string{"Check"}}

	require.NoError(t, c.register(dotted))
	require.NoError(t, c.register(short))
	require.Len(t, c.plugins, 2, "a dotted name doesn't make the shorter one registered")

	c.unregister(short)
	require.Len(t, c.plugins, 1)
	assert.Equal(t, "Auth.V2.Check", c.plugins[0].Method)
}

func TestConductor_makeRequest(t *testing.T) {
	tbl := []struct {
		name, target, host, url, server string
	}{
		{name: "escaped path is canonical", target: "/%66ail?a=%20b", host: "example.com", url: "/fail?a=%20b", server: "example.com"},
		{name: "host with port", target: "/x", host: "example.com:8080", url: "/x", server: "example.com"},
		{name: "ipv6 host with port", target: "/x", host: "[::1]:8080", url: "/x", server: "::1"},
		{name: "non-numeric port", target: "/x", host: "ADMIN.example:x", url: "/x", server: "ADMIN.example"},
		{name: "absolute-form target", target: "http://api.example.com/v1", host: "api.example.com", url: "/v1", server: "api.example.com"},
		{name: "absolute-form extra colons", target: "foo://a.wild.example:1:2/v1", url: "/v1", server: "a.wild.example"},
	}

	for _, tt := range tbl {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", tt.target, http.NoBody)
			if tt.host != "" {
				r.Host = tt.host
			}
			r.RemoteAddr = "192.0.2.1:1234"
			req := (&Conductor{}).makeRequest(r)
			assert.Equal(t, tt.url, req.URL)
			assert.Equal(t, tt.server, req.Host)
			assert.Equal(t, "192.0.2.1:1234", req.RemoteAddr)
		})
	}
}
