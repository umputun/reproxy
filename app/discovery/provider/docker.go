package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

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
	AutoAPI      bool
}

// DockerClient defines interface listing containers and subscribing to events
type DockerClient interface {
	ListContainers() ([]containerInfo, error)
}

// containerInfo is simplified view of container metadata
type containerInfo struct {
	ID     string            `json:"Id"`
	Name   string            `json:"-"`
	State  string            `json:"State"`
	Labels map[string]string `json:"Labels"`
	TS     time.Time         `json:"-"`
	IP     string            `json:"-"`
	Ports  []int             `json:"-"`
}

// Events gets eventsCh with all containers-related docker events events
func (d *Docker) Events(ctx context.Context) (res <-chan discovery.ProviderID) {
	eventsCh := make(chan discovery.ProviderID)
	go d.events(ctx, eventsCh)
	return eventsCh
}

// List all containers and make url mappers
// If AutoAPI enabled all each container and set all params, if not - allow only container with reproxy.* labels
func (d *Docker) List() ([]discovery.URLMapper, error) {
	containers, err := d.listContainers()
	if err != nil {
		return nil, err
	}

	res := make([]discovery.URLMapper, 0, len(containers))
	for _, c := range containers {
		enabled := false
		srcURL := "^/(.*)"
		if d.AutoAPI {
			enabled = true
			srcURL = fmt.Sprintf("^/api/%s/(.*)", c.Name)
		}

		port, err := d.matchedPort(c)
		if err != nil {
			log.Printf("[DEBUG] container %s disabled, %v", c.Name, err)
			continue
		}

		destURL := fmt.Sprintf("http://%s:%d/$1", c.IP, port)
		pingURL := fmt.Sprintf("http://%s:%d/ping", c.IP, port)
		server := "*"
		assetsWebRoot, assetsLocation := "", ""

		// we don't care about value because disabled will be filtered before
		if _, ok := c.Labels["reproxy.enabled"]; ok {
			enabled = true
		}

		if v, ok := c.Labels["reproxy.route"]; ok {
			enabled = true
			srcURL = v
		}

		if v, ok := c.Labels["reproxy.dest"]; ok {
			enabled = true
			destURL = fmt.Sprintf("http://%s:%d%s", c.IP, port, v)
		}

		if v, ok := c.Labels["reproxy.server"]; ok {
			enabled = true
			server = v
		}

		if v, ok := c.Labels["reproxy.ping"]; ok {
			enabled = true
			pingURL = fmt.Sprintf("http://%s:%d%s", c.IP, port, v)
		}

		if v, ok := c.Labels["reproxy.assets"]; ok {
			if ae := strings.Split(v, ":"); len(ae) == 2 {
				enabled = true
				assetsWebRoot = ae[0]
				assetsLocation = ae[1]
			}
		}

		if !enabled {
			log.Printf("[DEBUG] container %s disabled", c.Name)
			continue
		}

		srcRegex, err := regexp.Compile(srcURL)
		if err != nil {
			return nil, errors.Wrapf(err, "invalid src regex %s", srcURL)
		}

		// docker server label may have multiple, comma separated servers
		for _, srv := range strings.Split(server, ",") {
			mp := discovery.URLMapper{Server: strings.TrimSpace(srv), SrcMatch: *srcRegex, Dst: destURL,
				PingURL: pingURL, ProviderID: discovery.PIDocker, MatchType: discovery.MTProxy}

			if assetsWebRoot != "" {
				mp.MatchType = discovery.MTStatic
				mp.AssetsWebRoot = assetsWebRoot
				mp.AssetsLocation = assetsLocation
			}

			res = append(res, mp)
		}
	}

	// sort by len(SrcMatch) to have shorter matches after longer
	// this way we can handle possible conflicts with more detailed match triggered before less detailed

	sort.Slice(res, func(i, j int) bool {
		return len(res[i].SrcMatch.String()) > len(res[j].SrcMatch.String())
	})
	return res, nil
}

func (d *Docker) matchedPort(c containerInfo) (port int, err error) {
	port = c.Ports[0] // by default use the first exposed port
	if v, ok := c.Labels["reproxy.port"]; ok {
		rp, err := strconv.Atoi(v)
		if err != nil {
			return 0, errors.Wrapf(err, "invalid reproxy.port %s", v)
		}
		for _, p := range c.Ports {
			// set port to reproxy.port if matched with one of exposed
			if p == rp {
				port = rp
				break
			}
		}
	}
	return port, nil
}

// changed in tests
var dockerPollingInterval = time.Second * 10

// events starts monitoring changes in running containers and sends refresh
// notification to eventsCh when change(s) are detected. Blocks caller
func (d *Docker) events(ctx context.Context, eventsCh chan<- discovery.ProviderID) error {
	ticker := time.NewTicker(dockerPollingInterval)
	defer ticker.Stop()

	// Keep track of running containers
	saved := make(map[string]containerInfo)

	update := func() {
		containers, err := d.listContainers()
		if err != nil {
			log.Printf("[ERROR] failed to fetch running containers: %s", err)
			return
		}

		refresh := false
		seen := make(map[string]bool)

		for _, c := range containers {
			old, exists := saved[c.ID]

			if !exists || c.IP != old.IP || c.State != old.State || !c.TS.Equal(old.TS) {
				refresh = true
			}

			seen[c.ID] = true
		}

		if len(saved) != len(seen) || refresh {
			log.Printf("[INFO] changes in running containers detected: refreshing routes")
			for k := range saved {
				delete(saved, k)
			}
			for _, c := range containers {
				saved[c.ID] = c
			}
			eventsCh <- discovery.PIDocker
		}
	}

	update() // Refresh immediately
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			update()
		}
	}
}

func (d *Docker) listContainers() (res []containerInfo, err error) {
	containers, err := d.DockerClient.ListContainers()
	if err != nil {
		return nil, errors.Wrap(err, "can't list containers")
	}

	log.Printf("[DEBUG] total containers = %d", len(containers))

	for _, c := range containers {
		if c.State != "running" {
			log.Printf("[DEBUG] skip container %s due to state %s", c.Name, c.State)
			continue
		}

		if discovery.Contains(c.Name, d.Excludes) || strings.EqualFold(c.Name, "reproxy") {
			log.Printf("[DEBUG] container %s excluded", c.Name)
			continue
		}

		if v, ok := c.Labels["reproxy.enabled"]; ok {
			if strings.EqualFold(v, "false") || strings.EqualFold(v, "no") || v == "0" {
				log.Printf("[DEBUG] skip container %s due to reproxy.enabled=%s", c.Name, v)
				continue
			}
		}

		if c.IP == "" {
			log.Printf("[DEBUG] skip container %s, no ip on defined networks", c.Name)
			continue
		}

		if len(c.Ports) == 0 {
			log.Printf("[DEBUG] skip container %s, no exposed ports", c.Name)
			continue
		}

		log.Printf("[DEBUG] running container added, %+v", c)
		res = append(res, c)
	}
	log.Print("[DEBUG] completed list")
	return res, nil
}

type dockerClient struct {
	client  http.Client
	network string // network for IP selection
}

// NewDockerClient constructs docker client for given host and network
func NewDockerClient(host, network string) DockerClient {
	var schemaRegex = regexp.MustCompile("^(?:([a-z0-9]+)://)?(.*)$")
	parts := schemaRegex.FindStringSubmatch(host)
	proto, addr := parts[1], parts[2]
	log.Printf("[DEBUG] Configuring docker client to talk to %s via %s", addr, proto)

	client := http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial(proto, addr)
			},
		},
		Timeout: time.Second * 5,
	}

	return &dockerClient{client, network}
}

func (d *dockerClient) ListContainers() ([]containerInfo, error) {

	const dockerAPIVersion = "v1.41"

	resp, err := d.client.Get(fmt.Sprintf("http://localhost/%s/containers/json", dockerAPIVersion))
	if err != nil {
		return nil, errors.Wrap(err, "failed connection to docker socket")
	}

	defer resp.Body.Close()

	response := []struct {
		containerInfo
		Created         int64
		NetworkSettings struct {
			Networks map[string]struct {
				IPAddress string
			}
		}
		Names        []string
		ExposedPorts []struct{ PrivatePort int } `json:"Ports"`
	}{}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, errors.Wrap(err, "failed to parse response from docker daemon")
	}

	containers := make([]containerInfo, len(response))

	for i, c := range response {
		// fill remaining fields
		c.TS = time.Unix(c.Created, 0)

		for k, v := range c.NetworkSettings.Networks {
			if d.network == "" || k == d.network { // match on network name if defined
				c.IP = v.IPAddress
				break
			}
		}

		c.Name = strings.TrimPrefix(c.Names[0], "/")

		for _, p := range c.ExposedPorts {
			c.Ports = append(c.Ports, p.PrivatePort)
		}

		containers[i] = c.containerInfo
	}

	return containers, nil
}
