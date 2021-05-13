package consulcatalog

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClient(t *testing.T) {
	cl := NewClient("demo//", &http.Client{})
	assert.IsType(t, &consulClient{}, cl)
}

func TestClient_getServiceNames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		assert.Equal(t, "/v1/catalog/services", req.RequestURI)
		rw.Write([]byte(`{"s1":[],"s2":["baz","wow"],"s3":["bar","reproxy.enabled","foo"],"s4":["reproxy.enabled"]}`))
	}))

	cl := &consulClient{
		address:    server.URL,
		httpClient: server.Client(),
	}

	names, err := cl.getServiceNames()
	require.NoError(t, err)
	require.Equal(t, 2, len(names))
	assert.Equal(t, names[0], "s3")
	assert.Equal(t, names[1], "s4")
}

func TestClient_getServiceNames_error_send_request(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
	}))
	server.Close()

	cl := &consulClient{
		address:    server.URL,
		httpClient: server.Client(),
	}

	_, err := cl.getServiceNames()
	require.Error(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), "error send request to consul, Get "))
}

func TestClient_getServiceNames_bad_status_code(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(400)
	}))

	cl := &consulClient{
		address:    server.URL,
		httpClient: server.Client(),
	}

	_, err := cl.getServiceNames()
	require.Error(t, err)
	assert.Equal(t, "unexpected response status code 400", err.Error())
}

func TestClient_getServiceNames_wrong_answer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.Write([]byte(`bad json`))
	}))

	cl := &consulClient{
		address:    server.URL,
		httpClient: server.Client(),
	}

	_, err := cl.getServiceNames()
	require.Error(t, err)
	assert.Equal(t, "error unmarshal consul response, invalid character 'b' looking for beginning of value", err.Error())
}

func TestClient_getServices(t *testing.T) {
	body := `[
{"ServiceID":"s1","ServiceName":"n1","ServiceTags":[],"ServiceAddress":"a1","ServicePort":1000},
{"ServiceID":"s2","ServiceName":"n2","ServiceTags":["reproxy.enabled","foo"],"ServiceAddress":"a2","ServicePort":2000},
{"ServiceID":"s3","ServiceName":"n3","ServiceTags":["reproxy.foo","reproxy.a=1","reproxy.b=bar","reproxy.baz=","reproxy.=bad"],"ServiceAddress":"a3","ServicePort":3000}
]`

	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		assert.Equal(t, "/v1/catalog/service/service1", req.RequestURI)
		rw.Write([]byte(body))
	}))

	cl := &consulClient{
		address:    server.URL,
		httpClient: server.Client(),
	}

	services, err := cl.getServices("service1")
	require.NoError(t, err)
	require.Equal(t, 3, len(services))

	assert.Equal(t, "s1", services[0].ServiceID)
	assert.Equal(t, "n1", services[0].ServiceName)
	assert.Equal(t, "a1", services[0].ServiceAddress)
	assert.Equal(t, 1000, services[0].ServicePort)
	assert.Equal(t, 0, len(services[0].Labels))

	var v string
	var ok bool

	assert.Equal(t, "s2", services[1].ServiceID)
	assert.Equal(t, "n2", services[1].ServiceName)
	assert.Equal(t, "a2", services[1].ServiceAddress)
	assert.Equal(t, 2000, services[1].ServicePort)
	assert.Equal(t, 1, len(services[1].Labels))
	v, ok = services[1].Labels["reproxy.enabled"]
	assert.True(t, ok)
	assert.Equal(t, "", v)

	assert.Equal(t, "s3", services[2].ServiceID)
	assert.Equal(t, "n3", services[2].ServiceName)
	assert.Equal(t, "a3", services[2].ServiceAddress)
	assert.Equal(t, 3000, services[2].ServicePort)
	assert.Equal(t, 5, len(services[2].Labels))
	v, ok = services[2].Labels["reproxy.foo"]
	assert.True(t, ok)
	assert.Equal(t, "", v)
	v, ok = services[2].Labels["reproxy.a"]
	assert.True(t, ok)
	assert.Equal(t, "1", v)
	v, ok = services[2].Labels["reproxy.b"]
	assert.True(t, ok)
	assert.Equal(t, "bar", v)
	v, ok = services[2].Labels["reproxy.baz"]
	assert.True(t, ok)
	assert.Equal(t, "", v)
	v, ok = services[2].Labels["reproxy.=bad"]
	assert.True(t, ok)
	assert.Equal(t, "", v)
}

func TestClient_getServices_request_error(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
	}))
	server.Close()

	cl := &consulClient{
		address:    server.URL,
		httpClient: server.Client(),
	}

	_, err := cl.getServices("service1")
	require.Error(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), "error send request to consul, Get "))
}

func TestClient_getServices_bad_status_code(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.WriteHeader(400)
	}))

	cl := &consulClient{
		address:    server.URL,
		httpClient: server.Client(),
	}

	_, err := cl.getServices("service1")
	require.Error(t, err)
	assert.Equal(t, "unexpected response status code 400", err.Error())
}

func TestClient_getServices_wrong_answer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		rw.Write([]byte("bad json"))
	}))

	cl := &consulClient{
		address:    server.URL,
		httpClient: server.Client(),
	}

	_, err := cl.getServices("service1")
	require.Error(t, err)
	assert.Equal(t, "error unmarshal consul response, invalid character 'b' looking for beginning of value", err.Error())
}

func TestClient_Get(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if req.RequestURI == "/v1/catalog/services" {
			rw.Write([]byte(`{"s1":["reproxy.enabled"]}`))
			return
		}
		if req.RequestURI == "/v1/catalog/service/s1" {
			rw.Write([]byte(`[{"ServiceID":"s1","ServiceName":"n1","ServiceTags":["reproxy.enabled"],"ServiceAddress":"a1","ServicePort":1000}]`))
			return
		}
		panic("unexpected " + req.RequestURI)
	}))

	cl := &consulClient{
		address:    server.URL,
		httpClient: server.Client(),
	}

	res, err := cl.Get()
	require.NoError(t, err)
	require.Equal(t, 1, len(res))

	assert.Equal(t, "s1", res[0].ServiceID)
	assert.Equal(t, "n1", res[0].ServiceName)
	assert.Equal(t, "a1", res[0].ServiceAddress)
	assert.Equal(t, 1000, res[0].ServicePort)
}

func TestClient_Get_error_get_names(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if req.RequestURI == "/v1/catalog/services" {
			rw.WriteHeader(400)
			return
		}
		if req.RequestURI == "/v1/catalog/service/s1" {
			rw.Write([]byte(`[{"ServiceID":"s1","ServiceName":"n1","ServiceTags":["reproxy.enabled"],"ServiceAddress":"a1","ServicePort":1000}]`))
			return
		}
		panic("unexpected " + req.RequestURI)
	}))

	cl := &consulClient{
		address:    server.URL,
		httpClient: server.Client(),
	}

	_, err := cl.Get()
	require.Error(t, err)
	assert.Equal(t, "error get service names, unexpected response status code 400", err.Error())
}

func TestClient_Get_error_get_services(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if req.RequestURI == "/v1/catalog/services" {

			return
		}
		if req.RequestURI == "/v1/catalog/service/s1" {
			rw.WriteHeader(400)
			return
		}
		panic("unexpected " + req.RequestURI)
	}))

	cl := &consulClient{
		address:    server.URL,
		httpClient: server.Client(),
	}

	_, err := cl.Get()
	require.Error(t, err)
	assert.Equal(t, "error get service names, error unmarshal consul response, EOF", err.Error())
}
