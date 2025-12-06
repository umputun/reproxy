package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
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
		CallFunc: func(serviceMethod string, args any, reply any) error {
			return nil
		},
	}

	dialer := &RPCDialerMock{
		DialFunc: func(network string, address string) (RPCClient, error) {
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
		DialFunc: func(network string, address string) (RPCClient, error) {
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
		DialFunc: func(network string, address string) (RPCClient, error) {
			return rpcClient, nil
		},
	}

	c := Conductor{RPCDialer: dialer, Address: "127.0.0.1:50100"}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	go func() {
		c.Run(ctx)
	}()
	time.Sleep(time.Millisecond * 50)

	client := http.Client{Timeout: time.Second}

	// register plugin with 3 methods
	plugin := lib.Plugin{Name: "Test1", Address: "127.0.0.1:8001", Methods: []string{"Mw1", "Mw2", "Mw3"}}
	data, err := json.Marshal(plugin)
	require.NoError(t, err)
	req, err := http.NewRequest("POST", "http://127.0.0.1:50100", bytes.NewReader(data))
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
		DialFunc: func(network string, address string) (RPCClient, error) {
			return rpcClient, nil
		},
	}

	port := rand.Intn(30000)
	c := Conductor{RPCDialer: dialer, Address: "127.0.0.1:" + strconv.Itoa(30000+port)}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	go func() {
		c.Run(ctx)
	}()
	time.Sleep(time.Millisecond * 150)

	client := http.Client{Timeout: time.Second}

	// register plugin with one methods
	plugin := lib.Plugin{Name: "Test1", Address: "127.0.0.1:8001", Methods: []string{"Mw1"}}
	data, err := json.Marshal(plugin)
	require.NoError(t, err)
	req, err := http.NewRequest("POST", "http://127.0.0.1:"+strconv.Itoa(30000+port), bytes.NewReader(data))
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
		CallFunc: func(serviceMethod string, args any, reply any) error {
			if serviceMethod == "Test1.Mw1" {
				return errors.New("something failed")
			}
			return nil
		},
	}

	dialer := &RPCDialerMock{
		DialFunc: func(network string, address string) (RPCClient, error) {
			return rpcClient, nil
		},
	}

	c := Conductor{RPCDialer: dialer, Address: "127.0.0.1:50100"}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	go func() {
		c.Run(ctx)
	}()
	time.Sleep(time.Millisecond * 250)

	client := http.Client{Timeout: time.Second}

	// register plugin with one methods
	plugin := lib.Plugin{Name: "Test1", Address: "127.0.0.1:8001", Methods: []string{"Mw1"}}
	data, err := json.Marshal(plugin)
	require.NoError(t, err)
	req, err := http.NewRequest("POST", "http://127.0.0.1:50100", bytes.NewReader(data))
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
