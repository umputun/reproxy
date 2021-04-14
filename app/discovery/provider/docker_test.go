package provider

import (
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDocker_List(t *testing.T) {
	dclient := &DockerClientMock{
		ListContainersFunc: func() ([]containerInfo, error) {
			return []containerInfo{
				{
					Name: "c0", State: "running", IP: "127.0.0.2", Ports: []int{12348},
					Labels: map[string]string{"reproxy.route": "^/a/(.*)", "reproxy.dest": "/a/$1",
						"reproxy.server": "example.com", "reproxy.ping": "/ping"},
				},
				{
					Name: "c1", State: "running", IP: "127.0.0.2", Ports: []int{12345},
					Labels: map[string]string{"reproxy.route": "^/api/123/(.*)", "reproxy.dest": "/blah/$1",
						"reproxy.server": "example.com", "reproxy.ping": "/ping"},
				},
				{
					Name: "c2", State: "running", IP: "127.0.0.3", Ports: []int{12346},
					Labels: map[string]string{"reproxy.enabled": "y"},
				},
				{
					Name: "c3", State: "stopped",
				},
				{
					Name: "c4", State: "running", IP: "127.0.0.2", Ports: []int{12345},
				},
				{
					Name: "c5", State: "running", IP: "127.0.0.122", Ports: []int{2345},
					Labels: map[string]string{"reproxy.enabled": "false"},
				},
			}, nil
		},
	}

	d := Docker{DockerClient: dclient}
	res, err := d.List()
	require.NoError(t, err)
	require.Equal(t, 3, len(res))

	assert.Equal(t, "^/api/123/(.*)", res[0].SrcMatch.String())
	assert.Equal(t, "http://127.0.0.2:12345/blah/$1", res[0].Dst)
	assert.Equal(t, "example.com", res[0].Server)
	assert.Equal(t, "http://127.0.0.2:12345/ping", res[0].PingURL)

	assert.Equal(t, "^/a/(.*)", res[1].SrcMatch.String())
	assert.Equal(t, "http://127.0.0.2:12348/a/$1", res[1].Dst)
	assert.Equal(t, "http://127.0.0.2:12348/ping", res[1].PingURL)
	assert.Equal(t, "example.com", res[1].Server)

	assert.Equal(t, "^/(.*)", res[2].SrcMatch.String())
	assert.Equal(t, "http://127.0.0.3:12346/$1", res[2].Dst)
	assert.Equal(t, "http://127.0.0.3:12346/ping", res[2].PingURL)
	assert.Equal(t, "*", res[2].Server)
}

func TestDockerClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1.41/containers/json", r.URL.Path)

		// obtained using curl --unix-socket /var/run/docker.sock http://localhost/v1.41/containers/json
		resp, err := ioutil.ReadFile("testdata/containers.json")
		require.NoError(t, err)
		w.Write(resp)
	}))

	defer srv.Close()
	addr := fmt.Sprintf("tcp://%s", strings.TrimPrefix(srv.URL, "http://"))

	client := NewDockerClient(addr, "bridge")
	c, err := client.ListContainers()
	require.NoError(t, err, "unexpected error while listing containers")

	assert.Len(t, c, 2)

	assert.NotEmpty(t, c[0].ID)
	assert.Equal(t, "nginx", c[0].Name)
	assert.Equal(t, "running", c[0].State)
	assert.Equal(t, "172.17.0.3", c[0].IP)
	assert.Equal(t, "y", c[0].Labels["reproxy.enabled"])
	assert.Equal(t, []int{80}, c[0].Ports)
	assert.Equal(t, time.Unix(1618417435, 0), c[0].TS)

	assert.Equal(t, "", c[1].IP)
}
