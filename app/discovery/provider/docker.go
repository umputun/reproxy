package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	log "github.com/go-pkgz/lgr"

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
	DockerClient    DockerClient
	Excludes        []string
	AutoAPI         bool
	APIPrefix       string
	RefreshInterval time.Duration
}

// DockerClient defines interface listing containers and subscribing to events
type DockerClient interface {
	ListContainers() ([]containerInfo, error)
}

// containerInfo is simplified view of container metadata
type containerInfo struct {
	ID     string
	Name   string
	State  string
	Labels map[string]string
	TS     time.Time
	IP     string
	Ports  []int
}

// Events gets eventsCh with all containers-related docker events events
func (d *Docker) Events(ctx context.Context) (res <-chan discovery.ProviderID) {
	eventsCh := make(chan discovery.ProviderID)
	go func() {
		if err := d.events(ctx, eventsCh); err != context.Canceled {
			log.Printf("[ERROR] unexpected docker client exit reason: %s", err)
		}
	}()
	return eventsCh
}

// List all containers and make url mappers
// If AutoAPI enabled all each container and set all params, if not - allow only container with reproxy.* labels
func (d *Docker) List() ([]discovery.URLMapper, error) {
	containers, err := d.listContainers(true)
	if err != nil {
		return nil, err
	}

	res := make([]discovery.URLMapper, 0, len(containers))
	for _, c := range containers {
		enabled, explicit := false, false
		srcURL := fmt.Sprintf("^/%s/(.*)", c.Name) // default destination
		if d.APIPrefix != "" {
			prefix := strings.TrimLeft(d.APIPrefix, "/")
			prefix = strings.TrimRight(prefix, "/")
			srcURL = fmt.Sprintf("^/%s/%s/(.*)", prefix, c.Name) // default destination with api prefix
		}

		if d.AutoAPI {
			enabled = true
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
			enabled, explicit = true, true
		}

		if v, ok := c.Labels["reproxy.route"]; ok {
			enabled, explicit = true, true
			srcURL = v
		}

		if v, ok := c.Labels["reproxy.dest"]; ok {
			enabled, explicit = true, true
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

		// should not set anything, handled on matchedPort level. just use to enable implicitly
		if _, ok := c.Labels["reproxy.port"]; ok {
			enabled = true
		}

		if !enabled {
			log.Printf("[DEBUG] container %s disabled", c.Name)
			continue
		}

		srcRegex, err := regexp.Compile(srcURL)
		if err != nil {
			return nil, fmt.Errorf("invalid src regex: %w", err)
		}

		// docker server label may have multiple, comma separated servers
		for _, srv := range strings.Split(server, ",") {
			mp := discovery.URLMapper{Server: strings.TrimSpace(srv), SrcMatch: *srcRegex, Dst: destURL,
				PingURL: pingURL, ProviderID: discovery.PIDocker, MatchType: discovery.MTProxy}

			// for assets we add the second proxy mapping only if explicitly requested
			if assetsWebRoot != "" && explicit {
				mp.MatchType = discovery.MTProxy
				res = append(res, mp)
			}

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
			return 0, fmt.Errorf("invalid reproxy port %s: %w", v, err)
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

// events starts monitoring changes in running containers and sends refresh
// notification to eventsCh when change(s) are detected. Blocks caller
func (d *Docker) events(ctx context.Context, eventsCh chan<- discovery.ProviderID) error {
	ticker := time.NewTicker(d.RefreshInterval)
	defer ticker.Stop()

	// Keep track of running containers
	saved := make(map[string]containerInfo)

	update := func() {
		containers, err := d.listContainers(false)
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

func (d *Docker) listContainers(allowLogging bool) (res []containerInfo, err error) {
	containers, err := d.DockerClient.ListContainers()
	if err != nil {
		return nil, fmt.Errorf("can't list containers: %w", err)
	}

	if allowLogging {
		log.Printf("[DEBUG] total containers = %d", len(containers))
	}

	for _, c := range containers {
		if c.State != "running" {
			if allowLogging {
				log.Printf("[DEBUG] skip container %s due to state %s", c.Name, c.State)
			}
			continue
		}

		if discovery.Contains(c.Name, d.Excludes) || strings.EqualFold(c.Name, "reproxy") {
			if allowLogging {
				log.Printf("[DEBUG] container %s excluded", c.Name)
			}
			continue
		}

		if v, ok := c.Labels["reproxy.enabled"]; ok {
			if strings.EqualFold(v, "false") || strings.EqualFold(v, "no") || v == "0" {
				if allowLogging {
					log.Printf("[DEBUG] skip container %s due to reproxy.enabled=%s", c.Name, v)
				}
				continue
			}
		}

		if c.IP == "" {
			if allowLogging {
				log.Printf("[DEBUG] skip container %s, no ip on defined networks", c.Name)
			}
			continue
		}

		if len(c.Ports) == 0 {
			if allowLogging {
				log.Printf("[DEBUG] skip container %s, no exposed ports", c.Name)
			}
			continue
		}

		if allowLogging {
			log.Printf("[DEBUG] running container added, %+v", c)
		}
		res = append(res, c)
	}
	if allowLogging {
		log.Print("[DEBUG] completed list")
	}
	return res, nil
}

type dockerClient struct {
	client  http.Client
	network string // network for IP selection
}

// NewDockerClient constructs docker client for given host and network
func NewDockerClient(host, network string) (DockerClient, error) {

	uu, err := url.Parse(host)
	if err != nil {
		return nil, fmt.Errorf("can't parse docker url: %w", err)
	}
	addr := uu.Host // for tcp:// it will be host
	if addr == "" { // for unix:// it is path
		addr = uu.Path
	}

	if addr == "" || uu.Scheme == "" {
		return nil, fmt.Errorf("can't get docker address from %s", host)
	}

	log.Printf("[DEBUG] configuring docker client to talk to %s via %s", addr, uu.Scheme)

	client := http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial(uu.Scheme, addr)
			},
		},
		Timeout: time.Second * 5,
	}

	return &dockerClient{client, network}, nil
}

func (d *dockerClient) ListContainers() ([]containerInfo, error) {
	// Minimum API version that returns attached networks
	// docs.docker.com/engine/api/version-history/#v122-api-changes
	const APIVersion = "v1.22"

	resp, err := d.client.Get(fmt.Sprintf("http://localhost/%s/containers/json", APIVersion))
	if err != nil {
		return nil, fmt.Errorf("failed connection to docker socket: %w", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		e := struct {
			Message string `json:"message"`
		}{}

		if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
			return nil, fmt.Errorf("failed to parse error from docker daemon: %w", err)
		}

		return nil, fmt.Errorf("unexpected error from docker daemon: %s", e.Message)
	}

	var response []struct {
		ID              string `json:"Id"`
		Name            string
		State           string
		Labels          map[string]string
		Created         int64
		NetworkSettings struct {
			Networks map[string]struct {
				IPAddress string
			}
		}
		Names []string
		Ports []struct{ PrivatePort int } `json:"Ports"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to parse response from docker daemon: %w", err)
	}

	containers := make([]containerInfo, len(response))

	for i, resp := range response {
		c := containerInfo{}

		c.ID = resp.ID
		c.Name = strings.TrimPrefix(resp.Names[0], "/")
		c.State = resp.State
		c.Labels = resp.Labels
		c.TS = time.Unix(resp.Created, 0)

		for k, v := range resp.NetworkSettings.Networks {
			if d.network == "" || k == d.network { // match on network name if defined
				c.IP = v.IPAddress
				break
			}
		}

		for _, p := range resp.Ports {
			c.Ports = append(c.Ports, p.PrivatePort)
		}

		containers[i] = c
	}

	return containers, nil
}
