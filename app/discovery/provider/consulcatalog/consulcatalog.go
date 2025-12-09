package consulcatalog

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"sort"
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
// It stores service list IDs in the internal storage. If service list was changed, it send signal to the core
// The provider maps services with rules, described in the docker provider documentation
//
// reproxy.route sets source route, and reproxy.dest sets the destination.
// Optional reproxy.server enforces match by server name (hostname) and reproxy.ping sets the health check url
type ConsulCatalog struct {
	client          ConsulClient
	refreshInterval time.Duration
	// current services list with ServiceID as map key
	list map[string]struct{}
}

// New creates new ConsulCatalog instance
func New(client ConsulClient, checkInterval time.Duration) *ConsulCatalog {
	cc := &ConsulCatalog{
		client:          client,
		refreshInterval: checkInterval,
		list:            make(map[string]struct{}),
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
		err = cc.checkUpdates(eventsCh)
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

func (cc *ConsulCatalog) checkUpdates(eventsCh chan<- discovery.ProviderID) error {
	services, err := cc.client.Get()
	if err != nil {
		return fmt.Errorf("unable to get services list, %w", err)
	}

	if !cc.serviceListWasChanged(services) {
		return nil
	}

	cc.updateServices(services)

	eventsCh <- discovery.PIConsulCatalog

	return nil
}

func (cc *ConsulCatalog) serviceListWasChanged(services []consulService) bool {
	if len(services) != len(cc.list) {
		return true
	}

	for _, s := range services {
		if _, ok := cc.list[s.ServiceID]; !ok {
			return true
		}
	}

	return false
}

func (cc *ConsulCatalog) updateServices(services []consulService) {
	for key := range cc.list {
		delete(cc.list, key)
	}
	for _, s := range services {
		cc.list[s.ServiceID] = struct{}{}
	}
}

// List all containers and make url mappers
// If AutoAPI enabled all each container and set all params, if not - allow only container with reproxy.* tags
func (cc *ConsulCatalog) List() ([]discovery.URLMapper, error) {
	log.Print("[DEBUG] call consul catalog list")

	res := make([]discovery.URLMapper, 0, len(cc.list))

	services, err := cc.client.Get()
	if err != nil {
		return nil, fmt.Errorf("error get services list, %w", err)
	}

	for _, c := range services {
		enabled := false
		srcURL := "^/(.*)"
		destURL := fmt.Sprintf("http://%s:%d/$1", c.ServiceAddress, c.ServicePort)
		pingURL := fmt.Sprintf("http://%s:%d/ping", c.ServiceAddress, c.ServicePort)
		server := "*"
		var keepHost *bool
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

		if !enabled {
			log.Printf("[DEBUG] service %s disabled", c.ServiceID)
			continue
		}

		srcRegex, err := regexp.Compile(srcURL)
		if err != nil {
			return nil, fmt.Errorf("invalid src regex %s: %w", srcURL, err)
		}

		// server label may have multiple, comma separated servers
		for srv := range strings.SplitSeq(server, ",") {
			res = append(res, discovery.URLMapper{Server: strings.TrimSpace(srv), SrcMatch: *srcRegex, Dst: destURL,
				PingURL: pingURL, ProviderID: discovery.PIConsulCatalog, KeepHost: keepHost, OnlyFromIPs: onlyFrom, AuthUsers: authUsers})
		}
	}

	// sort by len(SrcMatch) to have shorter matches after longer
	// this way we can handle possible conflicts with more detailed match triggered before less detailed

	sort.Slice(res, func(i, j int) bool {
		return len(res[i].SrcMatch.String()) > len(res[j].SrcMatch.String())
	})
	return res, nil
}
