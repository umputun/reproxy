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

	"github.com/umputun/reproxy/app/discovery"
)

//go:generate moq -out docker_client_mock.go -skip-ensure -fmt goimports . DockerClient

// Docker provider watches compatible for stop/start changes from containers and maps by
// default from ^/api/%s/(.*) to http://%s:%d/$1, i.e. http://example.com/api/my_container/something
// will be mapped to http://172.17.42.1:8080/something. Ip will be the internal ip of the container and port exposed
// in the Dockerfile.
// Alternatively labels can alter this. reproxy.route sets source route, and reproxy.dest sets the destination.
// Optional reproxy.server enforces match by server name (hostname) and reproxy.ping sets the health check url
// Labels can be presented multiple times with a numeric suffix to provide multiple matches for a single container
// i.e. reproxy.1.server=example.com, reproxy.1.port=12345 and so on
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

	var res []discovery.URLMapper //nolint:prealloc // we don't know the final size
	for _, c := range containers {
		res = append(res, d.parseContainerInfo(c)...)
	}

	// sort by len(SrcMatch) to have shorter matches after longer
	// this way we can handle possible conflicts with more detailed match triggered before less detailed
	sort.Slice(res, func(i, j int) bool {
		return len(res[i].SrcMatch.String()) > len(res[j].SrcMatch.String())
	})
	return res, nil
}

// parseContainerInfo getting URLMappers for up to 10 routes for 0..9 N (reproxy.N.something)
func (d *Docker) parseContainerInfo(c containerInfo) (res []discovery.URLMapper) {

	for n := 0; n < 9; n++ {
		enabled, explicit := false, false
		srcURL := fmt.Sprintf("^/%s/(.*)", c.Name) // default src is /container-name/(.*)
		if d.APIPrefix != "" {
			prefix := strings.TrimSuffix(strings.TrimPrefix(d.APIPrefix, "/"), "/")
			srcURL = fmt.Sprintf("^/%s/%s/(.*)", prefix, c.Name) // default src with api prefix is /api-prefix/container-name/(.*)
		}

		port, err := d.matchedPort(c, n)
		if err != nil {
			log.Printf("[DEBUG] container %s (route: %d) disabled, %v", c.Name, n, err)
			continue
		}

		// defaults
		destURL, pingURL, server := fmt.Sprintf("http://%s:%d/$1", c.IP, port), fmt.Sprintf("http://%s:%d/ping", c.IP, port), "*"
		assetsWebRoot, assetsLocation := "", ""

		if d.AutoAPI && n == 0 {
			enabled = true
		}

		if _, ok := d.labelN(c.Labels, n, "enabled"); ok {
			enabled, explicit = true, true
		}

		if v, ok := d.labelN(c.Labels, n, "route"); ok {
			enabled, explicit = true, true
			srcURL = v
		}
		if v, ok := d.labelN(c.Labels, n, "dest"); ok {
			enabled, explicit = true, true
			if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
				destURL = v // proxy to http:// and https:// destinations as-is
			} else {
				destURL = fmt.Sprintf("http://%s:%d%s", c.IP, port, v)
			}
		}
		if v, ok := d.labelN(c.Labels, n, "server"); ok {
			enabled = true
			server = v
		}
		if v, ok := d.labelN(c.Labels, n, "ping"); ok {
			enabled = true
			if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
				pingURL = v // if ping is fulle url with http:// or https:// use it as-is
			} else {
				pingURL = fmt.Sprintf("http://%s:%d%s", c.IP, port, v)
			}
		}

		if v, ok := d.labelN(c.Labels, n, "assets"); ok {
			if ae := strings.Split(v, ":"); len(ae) == 2 {
				enabled = true
				assetsWebRoot = ae[0]
				assetsLocation = ae[1]
			}
		}

		// should not set anything, handled on matchedPort level. just use to enable implicitly
		if _, ok := d.labelN(c.Labels, n, "port"); ok {
			enabled = true
		}

		if !enabled {
			log.Printf("[DEBUG] container %s (route: %d) disabled", c.Name, n)
			continue
		}

		srcRegex, err := regexp.Compile(srcURL)
		if err != nil {
			log.Printf("[DEBUG] container %s (route: %d) disabled, invalid src regex: %v", c.Name, n, err)
			continue
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

	return res
}

// matchedPort gets port for route match, default the first exposed port
// if reproxy.N.label found reruns this port only if it is one of exposed by the container
func (d *Docker) matchedPort(c containerInfo, n int) (port int, err error) {
	port = c.Ports[0] // by default use the first exposed port

	if portLabel, ok := d.labelN(c.Labels, n, "port"); ok {
		rp, err := strconv.Atoi(portLabel)
		if err != nil {
			return 0, fmt.Errorf("invalid reproxy port %s: %w", portLabel, err)
		}
		for _, p := range c.Ports {
			// set port to reproxy.N.port if matched with one of exposed
			if p == rp {
				return rp, nil
			}
		}
		return 0, fmt.Errorf("reproxy port %s not exposed", portLabel)
	}
	return port, nil
}

// labelN returns label value from reproxy.N.suffix, i.e. reproxy.1.server
func (d *Docker) labelN(labels map[string]string, n int, suffix string) (result string, ok bool) {
	switch n {
	case 0:
		result, ok = labels["reproxy."+suffix]
		if !ok {
			result, ok = labels["reproxy.0."+suffix]
		}
	default:
		result, ok = labels[fmt.Sprintf("reproxy.%d.%s", n, suffix)]
	}
	return result, ok
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
func NewDockerClient(host, network string) DockerClient {
	var schemaRegex = regexp.MustCompile("^(?:([a-z0-9]+)://)?(.*)$")
	parts := schemaRegex.FindStringSubmatch(host)
	proto, addr := parts[1], parts[2]
	log.Printf("[DEBUG] configuring docker client to talk to %s via %s", addr, proto)

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
