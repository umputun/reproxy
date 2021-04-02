package provider

import (
	"context"
	"testing"
	"time"

	dclient "github.com/fsouza/go-dockerclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDocker_List(t *testing.T) {
	dc := &DockerClientMock{
		ListContainersFunc: func(opts dclient.ListContainersOptions) ([]dclient.APIContainers, error) {
			return []dclient.APIContainers{
				{Names: []string{"c1"}, Status: "start",
					Labels: map[string]string{"dpx.route": "^/api/123/(.*)", "dpx.dest": "/blah/$1", "dpx.server": "example.com"}},
				{Names: []string{"c2"}, Status: "start"},
				{Names: []string{"c3"}, Status: "stop"},
			}, nil
		},
	}

	d := Docker{DockerClient: dc}
	res, err := d.List()
	require.NoError(t, err)
	assert.Equal(t, 2, len(res))

	assert.Equal(t, "^/api/123/(.*)", res[0].SrcMatch.String())
	assert.Equal(t, "http://c1:8080/blah/$1", res[0].Dst)
	assert.Equal(t, "example.com", res[0].Server)

	assert.Equal(t, "^/api/c2/(.*)", res[1].SrcMatch.String())
	assert.Equal(t, "http://c2:8080/$1", res[1].Dst)
	assert.Equal(t, "*", res[1].Server)

}

func TestDocker_Events(t *testing.T) {
	dc := &DockerClientMock{
		AddEventListenerFunc: func(listener chan<- *dclient.APIEvents) error {
			go func() {
				time.Sleep(30 * time.Millisecond)
				listener <- &dclient.APIEvents{Type: "container", Status: "start",
					Actor: dclient.APIActor{Attributes: map[string]string{"name": "/c1"}}}
				time.Sleep(30 * time.Millisecond)
				listener <- &dclient.APIEvents{Type: "bad", Status: "start",
					Actor: dclient.APIActor{Attributes: map[string]string{"name": "/c2"}}}
			}()
			return nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	d := Docker{DockerClient: dc}
	ch := d.Events(ctx)

	events := 0
	for range ch {
		t.Log("event")
		events++
	}
	assert.Equal(t, 1, events)
}
