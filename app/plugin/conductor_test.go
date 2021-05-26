package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	log "github.com/go-pkgz/lgr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/reproxy/lib"
)

// func TestHandler_Execute(t *testing.T) {
// 	client, err := rpc.Dial("tcp", "localhost:1234")
// 	if err != nil {
// 		log.Fatal("dialing:", err)
// 	}
//
// 	var reply lib.HandlerResponse
//
// 	req, _ := http.NewRequest("POST", "https://exmple.com", nil)
// 	req.Header.Set("inp", "blah")
// 	err = client.Call("TestPlugin.Execute", lib.Request{HttpReq: *req}, &reply)
// 	require.NoError(t, err)
// 	log.Print(reply)
//
// 	var list lib.ListResponse
// 	err = client.Call("TestPlugin.List", lib.Request{}, &list)
// 	require.NoError(t, err)
// 	log.Print(list)
// }

func TestConductor_registrationHandler(t *testing.T) {

	rpcClient := &RPCClientMock{
		CallFunc: func(serviceMethod string, args interface{}, reply interface{}) error {
			if serviceMethod == "Test1.List" {
				reply.(*lib.ListResponse).Methods = []string{"Mw1", "Mw2"}
			}
			if serviceMethod == "Test2.List" {
				reply.(*lib.ListResponse).Methods = []string{"Mw11", "Mw12", "Mw13"}
			}
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
		plugin := lib.Plugin{Name: "Test1", Address: "127.0.0.1:0001"}
		data, err := json.Marshal(plugin)
		require.NoError(t, err)
		req, err := http.NewRequest("POST", ts.URL, bytes.NewReader(data))
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		assert.Equal(t, 2, len(c.plugins), "two plugins registered")
		assert.Equal(t, "Test1.Mw1", c.plugins[0].Method)
		assert.Equal(t, "127.0.0.1:0001", c.plugins[0].Address)
		assert.Equal(t, true, c.plugins[0].Alive)

		assert.Equal(t, "127.0.0.1:0001", c.plugins[1].Address)
		assert.Equal(t, "Test1.Mw2", c.plugins[1].Method)
		assert.Equal(t, true, c.plugins[1].Alive)

		assert.Equal(t, 1, len(rpcClient.CallCalls()))
		assert.Equal(t, 1, len(dialer.DialCalls()))
	}

	{ // same registration
		plugin := lib.Plugin{Name: "Test1", Address: "127.0.0.1:0001"}
		data, err := json.Marshal(plugin)
		require.NoError(t, err)
		req, err := http.NewRequest("POST", ts.URL, bytes.NewReader(data))
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, 2, len(c.plugins), "two plugins registered")
		assert.Equal(t, 1, len(rpcClient.CallCalls()))
		assert.Equal(t, 1, len(dialer.DialCalls()))
	}

	{ // address changed
		plugin := lib.Plugin{Name: "Test1", Address: "127.0.0.2:8002"}
		data, err := json.Marshal(plugin)
		require.NoError(t, err)
		req, err := http.NewRequest("POST", ts.URL, bytes.NewReader(data))
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, 2, len(c.plugins), "two plugins registered")
		assert.Equal(t, "Test1.Mw1", c.plugins[0].Method)
		assert.Equal(t, "127.0.0.2:8002", c.plugins[0].Address)
		assert.Equal(t, true, c.plugins[0].Alive)

		assert.Equal(t, "127.0.0.2:8002", c.plugins[1].Address)
		assert.Equal(t, "Test1.Mw2", c.plugins[1].Method)
		assert.Equal(t, true, c.plugins[1].Alive)

		assert.Equal(t, 2, len(rpcClient.CallCalls()))
		assert.Equal(t, 2, len(dialer.DialCalls()))
	}

	{ // address changed
		plugin := lib.Plugin{Name: "Test2", Address: "127.0.0.3:8003"}
		data, err := json.Marshal(plugin)
		require.NoError(t, err)
		req, err := http.NewRequest("POST", ts.URL, bytes.NewReader(data))
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, 2+3, len(c.plugins), "3 more plugins registered")
		assert.Equal(t, "Test2.Mw11", c.plugins[2].Method)
		assert.Equal(t, "127.0.0.3:8003", c.plugins[2].Address)
		assert.Equal(t, true, c.plugins[2].Alive)

		assert.Equal(t, 3, len(rpcClient.CallCalls()))
		assert.Equal(t, 3, len(dialer.DialCalls()))
	}

	{ // unregister
		plugin := lib.Plugin{Name: "Test1", Address: "127.0.0.2:8002"}
		data, err := json.Marshal(plugin)
		require.NoError(t, err)
		req, err := http.NewRequest("DELETE", ts.URL, bytes.NewReader(data))
		require.NoError(t, err)
		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, 3, len(c.plugins), "3 plugins left, 2 removed")

		assert.Equal(t, "Test2.Mw11", c.plugins[0].Method)
		assert.Equal(t, "127.0.0.3:8003", c.plugins[0].Address)
		assert.Equal(t, true, c.plugins[0].Alive)

		assert.Equal(t, 3, len(rpcClient.CallCalls()))
		assert.Equal(t, 3, len(dialer.DialCalls()))
	}
}

func TestConductor_registrationHandlerError(t *testing.T) {

	rpcClient := &RPCClientMock{
		CallFunc: func(serviceMethod string, args interface{}, reply interface{}) error {
			return errors.New("failed")
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
		CallFunc: func(serviceMethod string, args interface{}, reply interface{}) error {
			if serviceMethod == "Test1.List" {
				reply.(*lib.ListResponse).Methods = []string{"Mw1", "Mw2"}
			}
			if serviceMethod == "Test2.List" {
				reply.(*lib.ListResponse).Methods = []string{"Mw11", "Mw12", "Mw13"}
			}

			if serviceMethod == "Test1.Mw1" {
				req := args.(lib.Request)
				assert.Equal(t, "route123", req.Route)
				assert.Equal(t, "src123", req.Src)
				assert.Equal(t, "dst123", req.Dst)
				assert.Equal(t, "route123", req.Route)
				assert.Equal(t, "provider123", req.Provider)
				assert.Equal(t, "server123", req.Server)
				log.Printf("rr: %+v", req)
				reply.(*lib.HandlerResponse).StatusCode = 300
				reply.(*lib.HandlerResponse).Header = map[string][]string{}
				reply.(*lib.HandlerResponse).Header.Set("k1", "v1")
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

	// register plugin with two methods
	plugin := lib.Plugin{Name: "Test1", Address: "127.0.0.1:8001"}
	data, err := json.Marshal(plugin)
	require.NoError(t, err)
	req, err := http.NewRequest("POST", "http://127.0.0.1:50100", bytes.NewReader(data))
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 2, len(c.plugins), "two plugins registered")

	rr, err := http.NewRequest("GET", "http://127.0.0.1", nil)
	require.NoError(t, err)

	rr = rr.WithContext(context.WithValue(rr.Context(), ConductorCtxtKey("route"), "route123"))
	rr = rr.WithContext(context.WithValue(rr.Context(), ConductorCtxtKey("src"), "src123"))
	rr = rr.WithContext(context.WithValue(rr.Context(), ConductorCtxtKey("dst"), "dst123"))
	rr = rr.WithContext(context.WithValue(rr.Context(), ConductorCtxtKey("route"), "route123"))
	rr = rr.WithContext(context.WithValue(rr.Context(), ConductorCtxtKey("provider"), "provider123"))
	rr = rr.WithContext(context.WithValue(rr.Context(), ConductorCtxtKey("server"), "server123"))
	w := httptest.NewRecorder()
	h := c.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	h.ServeHTTP(w, rr)
	assert.Equal(t, 300, w.Result().StatusCode)
	assert.Equal(t, "v1", rr.Header.Get("k1"))
	t.Logf("%+v", rr)
}
