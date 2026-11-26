package consulcatalog

import (
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/umputun/reproxy/app/discovery"
)

//go:generate moq -out consul_client_mock.go -skip-ensure -fmt goimports . ConsulClient

// ConsulClient defines interface getting consul services
type ConsulClient interface {
	Get() ([]consulService, error)
}

type consulService struct {
	ServiceID      string   `json:"ServiceID"`
	ServiceName    string   `json:"ServiceName"`
	ServiceTags    []string `json:"ServiceTags"`
	ServiceAddress string   `json:"ServiceAddress"`
	ServicePort    int      `json:"ServicePort"`

	Labels map[string]string `json:"-"`
}

// ConsulCatalog provider periodically gets consul services with tags, started with 'reproxy.'
// It keeps a snapshot of the services' route fields. If the snapshot changes, it sends a signal to the core
// The provider maps services with rules, described in the docker provider documentation
//
// reproxy.route sets source route, and reproxy.dest sets the destination.
// Optional reproxy.server enforces match by server name (hostname) and reproxy.ping sets the health check url
type ConsulCatalog struct {
	client          ConsulClient
	refreshInterval time.Duration
	// count of each service fingerprint in the last snapshot, owned by the watcher; counts keep identical
	// records registered more than once
	list map[string]int
}

// New creates new ConsulCatalog instance
func New(client ConsulClient, checkInterval time.Duration) *ConsulCatalog {
	cc := &ConsulCatalog{
		client:          client,
		refreshInterval: checkInterval,
		list:            make(map[string]int),
	}

	return cc
}

// Events gets eventsCh, which emit services list update events
func (cc *ConsulCatalog) Events(ctx context.Context) (res <-chan discovery.ProviderID) {
	eventsCh := make(chan discovery.ProviderID)
	go func() {
		if err := cc.events(ctx, eventsCh); !errors.Is(err, context.Canceled) {
			log.Printf("[ERROR] unexpected consulcatalog events error: %s", err)
		}
	}()
	return eventsCh
}

func (cc *ConsulCatalog) events(ctx context.Context, eventsCh chan<- discovery.ProviderID) error {
	var err error
	ticker := time.NewTicker(cc.refreshInterval)
	defer ticker.Stop()
	for {
		err = cc.checkUpdates(ctx, eventsCh)
		if err != nil {
			log.Printf("[ERROR] error update consul catalog data, %v", err)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("consul catalog events interrupted: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func (cc *ConsulCatalog) checkUpdates(ctx context.Context, eventsCh chan<- discovery.ProviderID) error {
	services, err := cc.client.Get()
	if err != nil {
		return fmt.Errorf("unable to get services list, %w", err)
	}

	if !cc.serviceListWasChanged(services) {
		return nil
	}

	cc.updateServices(services)

	select {
	case eventsCh <- discovery.PIConsulCatalog:
	case <-ctx.Done(): // don't block on send if discovery is shutting down
	}

	return nil
}

func (cc *ConsulCatalog) serviceListWasChanged(services []consulService) bool {
	return !maps.Equal(cc.list, cc.fingerprints(services))
}

func (cc *ConsulCatalog) updateServices(services []consulService) {
	cc.list = cc.fingerprints(services)
}

// fingerprints counts services by every field List turns into a route, so a changed tag, address or port
// under the same ID reloads the routes. %q keeps label boundaries unambiguous, and fmt sorts map keys.
func (cc *ConsulCatalog) fingerprints(services []consulService) map[string]int {
	res := make(map[string]int, len(services))
	for _, s := range services {
		res[fmt.Sprintf("%q %q %d %q", s.ServiceID, s.ServiceAddress, s.ServicePort, s.Labels)]++
	}
	return res
}

// List all containers and make url mappers
// If AutoAPI enabled all each container and set all params, if not - allow only container with reproxy.* tags
func (cc *ConsulCatalog) List() ([]discovery.URLMapper, error) {
	log.Print("[DEBUG] call consul catalog list")

	services, err := cc.client.Get()
	if err != nil {
		return nil, fmt.Errorf("error get services list, %w", err)
	}

	res := make([]discovery.URLMapper, 0, len(services))

	for _, c := range services {
		enabled := false
		srcURL := "^/(.*)"
		destURL := fmt.Sprintf("http://%s:%d/$1", c.ServiceAddress, c.ServicePort)
		pingURL := fmt.Sprintf("http://%s:%d/ping", c.ServiceAddress, c.ServicePort)
		server := "*"
		var keepHost *bool
		forwardHealthChecks := false
		onlyFrom := []string{}

		if v, ok := c.Labels["reproxy.enabled"]; ok && (v == "true" || v == "yes" || v == "1") {
			enabled = true
		}

		if v, ok := c.Labels["reproxy.route"]; ok {
			enabled = true
			srcURL = v
		}

		if v, ok := c.Labels["reproxy.dest"]; ok {
			enabled = true
			destURL = fmt.Sprintf("http://%s:%d%s", c.ServiceAddress, c.ServicePort, v)
		}

		if v, ok := c.Labels["reproxy.server"]; ok {
			enabled = true
			server = v
		}

		if v, ok := c.Labels["reproxy.remote"]; ok {
			onlyFrom = discovery.ParseOnlyFrom(v)
		}

		authUsers := []string{}
		if v, ok := c.Labels["reproxy.auth"]; ok {
			authUsers = discovery.ParseAuth(v)
		}

		if v, ok := c.Labels["reproxy.ping"]; ok {
			enabled = true
			pingURL = fmt.Sprintf("http://%s:%d%s", c.ServiceAddress, c.ServicePort, v)
		}

		if v, ok := c.Labels["reproxy.keep-host"]; ok {
			enabled = true
			switch v {
			case "true", "yes", "1":
				t := true
				keepHost = &t
			case "false", "no", "0":
				f := false
				keepHost = &f
			default:
				log.Printf("[WARN] invalid value for reproxy.keep-host: %s", v)
			}
		}

		if v, ok := c.Labels["reproxy.forward-health-checks"]; ok {
			switch v {
			case "true", "yes", "1":
				forwardHealthChecks = true
			case "false", "no", "0":
				forwardHealthChecks = false
			default:
				log.Printf("[WARN] invalid value for reproxy.forward-health-checks: %s", v)
			}
		}

		if !enabled {
			log.Printf("[DEBUG] service %s disabled", c.ServiceID)
			continue
		}

		var timeout time.Duration
		if v, ok := c.Labels["reproxy.timeout"]; ok && v != "" {
			dur, perr := time.ParseDuration(v)
			switch {
			case perr != nil:
				log.Printf("[WARN] timeout label value %s is not valid, ignoring", v)
			case dur < 0:
				log.Printf("[WARN] timeout label value %s is negative, ignoring", v)
			default:
				timeout = dur
			}
		}

		throttle := 0
		if v, ok := c.Labels["reproxy.throttle"]; ok && v != "" {
			num, perr := strconv.Atoi(v)
			switch {
			case perr != nil:
				log.Printf("[WARN] throttle label value %s is not valid, ignoring", v)
			case num < 0:
				log.Printf("[WARN] throttle label value %s is negative, ignoring", v)
			default:
				throttle = num
			}
		}

		srcRegex, err := regexp.Compile(srcURL)
		if err != nil {
			return nil, fmt.Errorf("invalid src regex %s: %w", srcURL, err)
		}

		// server label may have multiple, comma separated servers
		for srv := range strings.SplitSeq(server, ",") {
			res = append(res, discovery.URLMapper{Server: strings.TrimSpace(srv), SrcMatch: *srcRegex, Dst: destURL,
				PingURL: pingURL, ProviderID: discovery.PIConsulCatalog, KeepHost: keepHost,
				ForwardHealthChecks: forwardHealthChecks, OnlyFromIPs: onlyFrom, AuthUsers: authUsers,
				Timeout: timeout, Throttle: throttle})
		}
	}

	// sort by len(SrcMatch) to have shorter matches after longer
	// this way we can handle possible conflicts with more detailed match triggered before less detailed

	sort.Slice(res, func(i, j int) bool {
		return len(res[i].SrcMatch.String()) > len(res[j].SrcMatch.String())
	})
	return res, nil
}
