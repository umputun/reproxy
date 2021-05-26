package provider

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/reproxy/app/discovery"
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
					Name: "c1", State: "running", IP: "127.0.0.21", Ports: []int{12345},
					Labels: map[string]string{"reproxy.route": "^/api/90/(.*)", "reproxy.dest": "http://example.com/blah/$1",
						"reproxy.server": "example.com", "reproxy.ping": "https://example.com//ping"},
				},
				{
					Name: "c2", State: "running", IP: "127.0.0.3", Ports: []int{12346},
					Labels: map[string]string{"reproxy.enabled": "y"},
				},
				{
					Name: "c3", State: "stopped",
				},
				{
					Name: "c4", State: "running", IP: "127.0.0.2", Ports: []int{12345}, // not enabled
				},
				{
					Name: "c5", State: "running", IP: "127.0.0.122", Ports: []int{2345}, // not enabled
					Labels: map[string]string{"reproxy.enabled": "false"},
				},
			}, nil
		},
	}

	d := Docker{DockerClient: dclient}
	res, err := d.List()
	require.NoError(t, err)
	require.Equal(t, 4, len(res))

	assert.Equal(t, "^/api/123/(.*)", res[0].SrcMatch.String())
	assert.Equal(t, "http://127.0.0.2:12345/blah/$1", res[0].Dst)
	assert.Equal(t, "example.com", res[0].Server)
	assert.Equal(t, "http://127.0.0.2:12345/ping", res[0].PingURL)

	assert.Equal(t, "^/api/90/(.*)", res[1].SrcMatch.String())
	assert.Equal(t, "http://example.com/blah/$1", res[1].Dst)
	assert.Equal(t, "https://example.com//ping", res[1].PingURL)
	assert.Equal(t, "example.com", res[1].Server)

	assert.Equal(t, "^/c2/(.*)", res[2].SrcMatch.String())
	assert.Equal(t, "http://127.0.0.3:12346/$1", res[2].Dst)
	assert.Equal(t, "http://127.0.0.3:12346/ping", res[2].PingURL)
	assert.Equal(t, "*", res[2].Server)

	assert.Equal(t, "^/a/(.*)", res[3].SrcMatch.String())
	assert.Equal(t, "http://127.0.0.2:12348/a/$1", res[3].Dst)
	assert.Equal(t, "http://127.0.0.2:12348/ping", res[3].PingURL)
	assert.Equal(t, "example.com", res[3].Server)
}

func TestDocker_ListMulti(t *testing.T) {
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
					Name: "c2m", State: "running", IP: "127.0.0.3", Ports: []int{7890, 56789},
					Labels: map[string]string{
						"reproxy.enabled": "y",
						"reproxy.1.route": "/api/1/(.*)",
						"reproxy.1.dest":  "/blah/1/$1",
					},
				},
				{
					Name: "c4", State: "running", IP: "127.0.0.12", Ports: []int{12345},
					Labels: map[string]string{"reproxy.port": "12345"},
				},
				{
					Name: "c4", State: "running", IP: "127.0.0.12", Ports: []int{12345},
					Labels: map[string]string{"reproxy.port": "12xx345"}, // bad port
				},
				{
					Name: "c23", State: "running", IP: "127.0.0.3", Ports: []int{12346},
					Labels: map[string]string{"reproxy.enabled": "y", "reproxy.route": "^/api/123/(.***)"}, // bad regex
				},
				{
					Name: "c3", State: "stopped",
				},
				{
					Name: "c4", State: "running", IP: "127.0.0.2", Ports: []int{12345}, // not enabled
				},
				{
					Name: "c4", State: "running", IP: "127.0.0.2", Ports: []int{12345}, // not enabled, port mismatch
					Labels: map[string]string{"reproxy.port": "9999"},
				},
				{
					Name: "c5", State: "running", IP: "127.0.0.122", Ports: []int{2345}, // not enabled
					Labels: map[string]string{"reproxy.enabled": "false"},
				},
			}, nil
		},
	}

	d := Docker{DockerClient: dclient}
	res, err := d.List()
	require.NoError(t, err)
	require.Equal(t, 6, len(res))

	assert.Equal(t, "^/api/123/(.*)", res[0].SrcMatch.String())
	assert.Equal(t, "http://127.0.0.2:12345/blah/$1", res[0].Dst)
	assert.Equal(t, "example.com", res[0].Server)
	assert.Equal(t, "http://127.0.0.2:12345/ping", res[0].PingURL)

	assert.Equal(t, "/api/1/(.*)", res[1].SrcMatch.String())
	assert.Equal(t, "http://127.0.0.3:7890/blah/1/$1", res[1].Dst)
	assert.Equal(t, "http://127.0.0.3:7890/ping", res[1].PingURL)
	assert.Equal(t, "*", res[1].Server)

	assert.Equal(t, "^/c2m/(.*)", res[2].SrcMatch.String())
	assert.Equal(t, "http://127.0.0.3:7890/$1", res[2].Dst)
	assert.Equal(t, "http://127.0.0.3:7890/ping", res[2].PingURL)
	assert.Equal(t, "*", res[2].Server)

	assert.Equal(t, "^/c2/(.*)", res[3].SrcMatch.String())
	assert.Equal(t, "http://127.0.0.3:12346/$1", res[3].Dst)
	assert.Equal(t, "http://127.0.0.3:12346/ping", res[3].PingURL)
	assert.Equal(t, "*", res[3].Server)

	assert.Equal(t, "^/c4/(.*)", res[4].SrcMatch.String())
	assert.Equal(t, "http://127.0.0.12:12345/$1", res[4].Dst)
	assert.Equal(t, "http://127.0.0.12:12345/ping", res[4].PingURL)
	assert.Equal(t, "*", res[4].Server)

	assert.Equal(t, "^/a/(.*)", res[5].SrcMatch.String())
	assert.Equal(t, "http://127.0.0.2:12348/a/$1", res[5].Dst)
	assert.Equal(t, "http://127.0.0.2:12348/ping", res[5].PingURL)
	assert.Equal(t, "example.com", res[5].Server)
}

func TestDocker_ListWithAutoAPI(t *testing.T) {
	dclient := &DockerClientMock{
		ListContainersFunc: func() ([]containerInfo, error) {
			return []containerInfo{
				{
					Name: "c1", State: "running", IP: "127.0.0.2", Ports: []int{1345, 12345},
					Labels: map[string]string{"reproxy.route": "^/api/123/(.*)", "reproxy.dest": "/blah/$1",
						"reproxy.port": "12345", "reproxy.server": "example.com, example2.com", "reproxy.ping": "/ping"},
				},
				{
					Name: "c2", State: "running", IP: "127.0.0.3", Ports: []int{12346},
				},
				{
					Name: "c3", State: "stopped",
				},
				{
					Name: "c4", State: "running", IP: "127.0.0.122", Ports: []int{2345},
					Labels: map[string]string{"reproxy.enabled": "false"},
				},
			}, nil
		},
	}

	d := Docker{DockerClient: dclient, AutoAPI: true, APIPrefix: "/api"}
	res, err := d.List()
	require.NoError(t, err)
	require.Equal(t, 3, len(res))

	assert.Equal(t, "^/api/123/(.*)", res[0].SrcMatch.String())
	assert.Equal(t, "http://127.0.0.2:12345/blah/$1", res[0].Dst)
	assert.Equal(t, "example.com", res[0].Server)
	assert.Equal(t, "http://127.0.0.2:12345/ping", res[0].PingURL)

	assert.Equal(t, "^/api/123/(.*)", res[1].SrcMatch.String())
	assert.Equal(t, "http://127.0.0.2:12345/blah/$1", res[1].Dst)
	assert.Equal(t, "example2.com", res[1].Server)
	assert.Equal(t, "http://127.0.0.2:12345/ping", res[1].PingURL)

	assert.Equal(t, "^/api/c2/(.*)", res[2].SrcMatch.String())
	assert.Equal(t, "http://127.0.0.3:12346/$1", res[2].Dst)
	assert.Equal(t, "http://127.0.0.3:12346/ping", res[2].PingURL)
	assert.Equal(t, "*", res[2].Server)
}

func TestDocker_ListPriority(t *testing.T) {
	dclient := &DockerClientMock{
		ListContainersFunc: func() ([]containerInfo, error) {
			return []containerInfo{
				{
					Name: "c0", State: "running", IP: "127.0.0.2", Ports: []int{12348},
					Labels: map[string]string{"reproxy.route": "^/whoami/(.*)", "reproxy.dest": "/$1",
						"reproxy.server": "example.com", "reproxy.ping": "/ping", "reproxy.assets": "/:/assets/static1"},
				},
			}, nil
		},
	}

	d := Docker{DockerClient: dclient}
	res, err := d.List()
	require.NoError(t, err)
	require.Equal(t, 2, len(res))
	assert.Equal(t, discovery.MTProxy, res[0].MatchType)
	assert.Equal(t, discovery.MTStatic, res[1].MatchType)
}

func TestDocker_refresh(t *testing.T) {
	containers := make(chan []containerInfo)

	d := Docker{
		DockerClient: &DockerClientMock{
			ListContainersFunc: func() ([]containerInfo, error) {
				return <-containers, nil
			},
		},
		RefreshInterval: time.Nanosecond,
	}

	events := make(chan discovery.ProviderID)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	stub := func(id string) containerInfo {
		return containerInfo{ID: id, Name: id, State: "running", IP: "127.0.0." + id, Ports: []int{12345}}
	}

	recv := func() {
		select {
		case <-events:
			return
		case <-time.After(time.Second):
			t.Fatal("No refresh notification was received after 1s")
		}
	}

	go func() {
		if err := d.events(ctx, events); err != context.Canceled {
			log.Fatal(err)
		}
	}()

	// Start some
	containers <- []containerInfo{stub("1"), stub("2")}
	recv()

	// Nothing changed
	containers <- []containerInfo{stub("1"), stub("2")}
	time.Sleep(time.Millisecond)
	assert.Empty(t, events, "unexpected refresh notification")

	// Stopped
	containers <- []containerInfo{stub("1")}
	recv()

	// One changed
	containers <- []containerInfo{
		{ID: "1", Name: "1", State: "running", IP: "127.42.42.42", Ports: []int{12345}},
	}
	recv()

	time.Sleep(time.Millisecond)
	assert.Empty(t, events, "unexpect refresh notification from events channel")
}

func TestDockerClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, `/v1.22/containers/json`, r.URL.Path)

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

	assert.Empty(t, c[1].IP)
}

func TestDockerClient_error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message": "bruh"}`, http.StatusInternalServerError)
	}))

	defer srv.Close()
	addr := fmt.Sprintf("tcp://%s", strings.TrimPrefix(srv.URL, "http://"))

	client := NewDockerClient(addr, "bridge")
	_, err := client.ListContainers()
	require.EqualError(t, err, "unexpected error from docker daemon: bruh")
}

func TestDocker_labelN(t *testing.T) {

	tbl := []struct {
		labels map[string]string
		n      int
		suffix string
		res    string
		ok     bool
	}{
		{map[string]string{}, 0, "port", "", false},
		{map[string]string{"a": "123", "reproxy.port": "9999"}, 0, "port", "9999", true},
		{map[string]string{"a": "123", "reproxy.0.port": "9999"}, 0, "port", "9999", true},
		{map[string]string{"a": "123", "reproxy.1.port": "9999"}, 0, "port", "", false},
		{map[string]string{"a": "123", "reproxy.1.port": "9999"}, 1, "port", "9999", true},
		{map[string]string{"a": "123", "reproxy.1.port": "9999", "reproxy.0.port": "7777"}, 1, "port", "9999", true},
		{map[string]string{"a": "123", "reproxy.1.port": "9999", "reproxy.0.port": "7777"}, 0, "port", "7777", true},
	}

	d := Docker{}
	for i, tt := range tbl {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			res, ok := d.labelN(tt.labels, tt.n, tt.suffix)
			require.Equal(t, tt.ok, ok)
			if !ok {
				return
			}
			assert.Equal(t, tt.res, res)
		})
	}

}
