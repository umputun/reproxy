package provider

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	dc "github.com/fsouza/go-dockerclient"
	log "github.com/go-pkgz/lgr"
	"github.com/pkg/errors"

	"github.com/umputun/reproxy/app/discovery"
)

//go:generate moq -out docker_client_mock.go -skip-ensure -fmt goimports . DockerClient

// Docker provider watches compatible for stop/start changes from containers and maps by
// default from ^/api/%s/(.*) to http://%s:%d/$1, i.e. http://example.com/api/my_container/something
// will be mapped to http://172.17.42.1:8080/something. Ip will be the internal ip of the container and port exposed
// in the Dockerfile.
// Alternatively labels can alter this. reproxy.route sets source route, and reproxy.dest sets the destination.
// Optional reproxy.server enforces match by server name (hostname) and reproxy.ping sets the health check url
type Docker struct {
	DockerClient DockerClient
	Excludes     []string
	Network      string
	AutoAPI      bool
}

// DockerClient defines interface listing containers and subscribing to events
type DockerClient interface {
	ListContainers(opts dc.ListContainersOptions) ([]dc.APIContainers, error)
	AddEventListenerWithOptions(options dc.EventsOptions, listener chan<- *dc.APIEvents) error
}

// containerInfo is simplified docker.APIEvents for containers only
type containerInfo struct {
	ID     string
	Name   string
	TS     time.Time
	Labels map[string]string
	IP     string
	Port   int
}

// Events gets eventsCh with all containers-related docker events events
func (d *Docker) Events(ctx context.Context) (res <-chan discovery.ProviderID) {
	eventsCh := make(chan discovery.ProviderID)
	go func() {
		defer close(eventsCh)
		// loop over to recover from failed events call
		for {
			err := d.events(ctx, d.DockerClient, eventsCh) // publish events to eventsCh in a blocking loop
			if err == context.Canceled || err == context.DeadlineExceeded {
				return
			}
			log.Printf("[WARN] docker events listener failed (restarted), %v", err)
			time.Sleep(1 * time.Second) // prevent busy loop on restart of event listener
		}
	}()
	return eventsCh
}

// List all containers and make url mappers
func (d *Docker) List() ([]discovery.URLMapper, error) {
	containers, err := d.listContainers()
	if err != nil {
		return nil, err
	}

	res := make([]discovery.URLMapper, 0, len(containers))
	for _, c := range containers {
		srcURL := "^/(.*)"
		if d.AutoAPI {
			srcURL = fmt.Sprintf("^/api/%s/(.*)", c.Name)
		}
		destURL := fmt.Sprintf("http://%s:%d/$1", c.IP, c.Port)
		pingURL := fmt.Sprintf("http://%s:%d/ping", c.IP, c.Port)
		server := "*"

		if v, ok := c.Labels["reproxy.route"]; ok {
			srcURL = v
		}
		if v, ok := c.Labels["reproxy.dest"]; ok {
			destURL = fmt.Sprintf("http://%s:%d%s", c.IP, c.Port, v)
		}
		if v, ok := c.Labels["reproxy.server"]; ok {
			server = v
		}
		srcRegex, err := regexp.Compile(srcURL)

		if v, ok := c.Labels["reproxy.ping"]; ok {
			pingURL = fmt.Sprintf("http://%s:%d%s", c.IP, c.Port, v)
		}

		if err != nil {
			return nil, errors.Wrapf(err, "invalid src regex %s", srcURL)
		}

		res = append(res, discovery.URLMapper{Server: server, SrcMatch: *srcRegex, Dst: destURL,
			PingURL: pingURL, ProviderID: discovery.PIDocker})
	}
	return res, nil
}

// activate starts blocking listener for all docker events
// filters everything except "container" type, detects stop/start events and publishes signals to eventsCh
func (d *Docker) events(ctx context.Context, client DockerClient, eventsCh chan discovery.ProviderID) error {
	dockerEventsCh := make(chan *dc.APIEvents)
	err := client.AddEventListenerWithOptions(dc.EventsOptions{
		Filters: map[string][]string{"type": {"container"}, "event": {"start", "die", "destroy", "restart", "pause"}}},
		dockerEventsCh)
	if err != nil {
		return errors.Wrap(err, "can't add even listener")
	}

	eventsCh <- discovery.PIDocker // initial emmit
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-dockerEventsCh:
			if !ok {
				return errors.New("events closed")
			}
			log.Printf("[DEBUG] api event %+v", ev)
			containerName := strings.TrimPrefix(ev.Actor.Attributes["name"], "/")

			if contains(containerName, d.Excludes) {
				log.Printf("[DEBUG] container %s excluded", containerName)
				continue
			}
			log.Printf("[INFO] new docker event: container %s, status %s", containerName, ev.Status)
			eventsCh <- discovery.PIDocker
		}
	}
}

func (d *Docker) listContainers() (res []containerInfo, err error) {

	portExposed := func(c dc.APIContainers) (int, bool) {
		if len(c.Ports) == 0 {
			return 0, false
		}
		return int(c.Ports[0].PrivatePort), true
	}

	containers, err := d.DockerClient.ListContainers(dc.ListContainersOptions{All: false})
	if err != nil {
		return nil, errors.Wrap(err, "can't list containers")
	}
	log.Printf("[DEBUG] total containers = %d", len(containers))

	for _, c := range containers {
		if !contains(c.State, []string{"running"}) {
			log.Printf("[DEBUG] skip container %s due to state %s", c.Names[0], c.State)
			continue
		}
		containerName := strings.TrimPrefix(c.Names[0], "/")
		if contains(containerName, d.Excludes) {
			log.Printf("[DEBUG] container %s excluded", containerName)
			continue
		}

		if v, ok := c.Labels["reproxy.enabled"]; ok {
			if strings.EqualFold(v, "false") || strings.EqualFold(v, "no") {
				log.Printf("[DEBUG] skip container %s due to reproxy.enabled=%s", containerName, v)
				continue
			}
		}

		var ip string
		for k, v := range c.Networks.Networks {
			if d.Network == "" || k == d.Network { // match on network name if defined
				ip = v.IPAddress
				break
			}
		}
		if ip == "" {
			log.Printf("[DEBUG] skip container %s, no ip on %+v", c.Names[0], c.Networks.Networks)
			continue
		}

		port, ok := portExposed(c)
		if !ok {
			log.Printf("[DEBUG] skip container %s, no exposed ports", c.Names[0])
			continue
		}

		ci := containerInfo{
			Name:   containerName,
			ID:     c.ID,
			TS:     time.Unix(c.Created/1000, 0),
			Labels: c.Labels,
			IP:     ip,
			Port:   port,
		}

		log.Printf("[DEBUG] running container added, %+v", ci)
		res = append(res, ci)
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
