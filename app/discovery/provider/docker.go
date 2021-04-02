package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	dclient "github.com/fsouza/go-dockerclient"
	log "github.com/go-pkgz/lgr"
	"github.com/pkg/errors"

	"github.com/umputun/docker-proxy/app/discovery"
)

//go:generate moq -out docker_client_mock.go -skip-ensure -fmt goimports . DockerClient

// Docker emits all changes from all containers states
type Docker struct {
	DockerClient DockerClient
	Excludes     []string
}

// DockerClient defines interface listing containers and subscribing to events
type DockerClient interface {
	ListContainers(opts dclient.ListContainersOptions) ([]dclient.APIContainers, error)
	AddEventListener(listener chan<- *dclient.APIEvents) error
}

// containerInfo is simplified docker.APIEvents for containers only
type containerInfo struct {
	ID     string
	Name   string
	TS     time.Time
	Labels map[string]string
}

var (
	upStatuses   = []string{"start", "restart"}
	downStatuses = []string{"die", "destroy", "stop", "pause"}
)

// Channel gets eventsCh with all containers events
func (d *Docker) Events(ctx context.Context) (res <-chan struct{}) {
	eventsCh := make(chan struct{})
	go func() {
		for {
			err := d.events(ctx, d.DockerClient, eventsCh)
			if err == context.Canceled || err == context.DeadlineExceeded {
				close(eventsCh)
				return
			}
			log.Printf("[WARN] docker events listener failed, restarted, %v", err)
			time.Sleep(100 * time.Millisecond)
		}
	}()
	return eventsCh
}

// List all containers and make url mappers
func (d *Docker) List() ([]discovery.UrlMapper, error) {
	containers, err := d.listContainers()
	if err != nil {
		return nil, err
	}

	var res []discovery.UrlMapper
	for _, c := range containers {
		srcURL := fmt.Sprintf("^/api/%s/(.*)", c.Name)
		destURL := fmt.Sprintf("http://%s:8080/$1", c.Name)
		server := "*"
		if v, ok := c.Labels["dpx.route"]; ok {
			srcURL = v
		}
		if v, ok := c.Labels["dpx.dest"]; ok {
			destURL = fmt.Sprintf("http://%s:8080%s", c.Name, v)
		}
		if v, ok := c.Labels["dpx.server"]; ok {
			server = v
		}
		srcRegex, err := regexp.Compile(srcURL)
		if err != nil {
			return nil, errors.Wrapf(err, "invalid src regex %s", srcURL)
		}

		res = append(res, discovery.UrlMapper{Server: server, SrcMatch: srcRegex, Dst: destURL})
	}
	return res, nil
}

func (d *Docker) ID() discovery.ProviderID { return discovery.PIDocker }

// activate starts blocking listener for all docker events
// filters everything except "container" type, detects stop/start events and publishes signals to eventsCh
func (d *Docker) events(ctx context.Context, client DockerClient, eventsCh chan struct{}) error {
	dockerEventsCh := make(chan *dclient.APIEvents)
	if err := client.AddEventListener(dockerEventsCh); err != nil {
		return errors.Wrap(err, "can't add even listener")
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-dockerEventsCh:
			if !ok {
				return errors.New("events closed")
			}
			if ev.Type != "container" {
				continue
			}
			if !contains(ev.Status, upStatuses) && !contains(ev.Status, downStatuses) {
				continue
			}
			log.Printf("[DEBUG] api event %+v", ev)
			containerName := strings.TrimPrefix(ev.Actor.Attributes["name"], "/")

			if contains(containerName, d.Excludes) {
				log.Printf("[DEBUG] container %s excluded", containerName)
				continue
			}
			log.Printf("[INFO] new event %+v", ev)
			eventsCh <- struct{}{}
		}
	}
}

func (d *Docker) listContainers() (res []containerInfo, err error) {

	containers, err := d.DockerClient.ListContainers(dclient.ListContainersOptions{All: false})
	if err != nil {
		return nil, errors.Wrap(err, "can't list containers")
	}
	log.Printf("[DEBUG] total containers = %d", len(containers))

	for _, c := range containers {
		if !contains(c.Status, upStatuses) {
			continue
		}
		containerName := strings.TrimPrefix(c.Names[0], "/")
		if contains(containerName, d.Excludes) {
			log.Printf("[DEBUG] container %s excluded", containerName)
			continue
		}
		event := containerInfo{
			Name:   containerName,
			ID:     c.ID,
			TS:     time.Unix(c.Created/1000, 0),
			Labels: c.Labels,
		}
		log.Printf("[DEBUG] running container added, %+v", event)
		res = append(res, event)
	}
	log.Print("[DEBUG] completed list")
	return res, nil
}

func contains(e string, s []string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}
