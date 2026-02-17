package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/rpc"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	log "github.com/go-pkgz/lgr"
	"github.com/libdns/cloudflare"
	"github.com/libdns/digitalocean"
	"github.com/libdns/dnsimple"
	"github.com/libdns/duckdns"
	"github.com/libdns/gandi"
	"github.com/libdns/godaddy"
	"github.com/libdns/hetzner"
	"github.com/libdns/linode"
	"github.com/libdns/namecheap"
	"github.com/libdns/porkbun"
	"github.com/libdns/route53"
	"github.com/libdns/scaleway"
	"github.com/umputun/go-flags"
	"gopkg.in/natefinch/lumberjack.v2"

	"github.com/umputun/reproxy/app/discovery"
	"github.com/umputun/reproxy/app/discovery/provider"
	"github.com/umputun/reproxy/app/discovery/provider/consulcatalog"
	"github.com/umputun/reproxy/app/mgmt"
	"github.com/umputun/reproxy/app/plugin"
	"github.com/umputun/reproxy/app/proxy"
)

var opts struct {
	Listen              string   `short:"l" long:"listen" env:"LISTEN" description:"listen on host:port (default: 0.0.0.0:8080/8443 under docker, 127.0.0.1:80/443 without)"`
	MaxSize             string   `short:"m" long:"max" env:"MAX_SIZE" default:"64K" description:"max request size"`
	GzipEnabled         bool     `short:"g" long:"gzip" env:"GZIP" description:"enable gz compression"`
	ProxyHeaders        []string `short:"x" long:"header" description:"outgoing proxy headers to add"` // env HEADER split in code to allow , inside ""
	DropHeaders         []string `long:"drop-header" env:"DROP_HEADERS" description:"incoming headers to drop" env-delim:","`
	AuthBasicHtpasswd   string   `long:"basic-htpasswd" env:"BASIC_HTPASSWD" description:"htpasswd file for basic auth"`
	RemoteLookupHeaders bool     `long:"remote-lookup-headers" env:"REMOTE_LOOKUP_HEADERS" description:"enable remote lookup headers"`
	LBType              string   `long:"lb-type" env:"LB_TYPE" description:"load balancer type" choice:"random" choice:"failover" choice:"roundrobin" default:"random"` // nolint
	Insecure            bool     `long:"insecure" env:"INSECURE" description:"skip SSL certificate verification for the destination host"`
	KeepHost            bool     `long:"keep-host" env:"KEEP_HOST" description:"pass the Host header from the client as-is, instead of rewriting it"`

	SSL struct {
		Type           string   `long:"type" env:"TYPE" description:"ssl (auto) support" choice:"none" choice:"static" choice:"auto" default:"none"` // nolint
		Cert           string   `long:"cert" env:"CERT" description:"path to cert.pem file"`
		Key            string   `long:"key" env:"KEY" description:"path to key.pem file"`
		ACMEDirectory  string   `long:"acme-directory" env:"ACME_DIRECTORY" description:"ACME directory to use" default:"https://acme-v02.api.letsencrypt.org/directory"`
		ACMELocation   string   `long:"acme-location" env:"ACME_LOCATION" description:"dir where certificates will be stored by autocert manager" default:"./var/acme"`
		ACMEEmail      string   `long:"acme-email" env:"ACME_EMAIL" description:"admin email for certificate notifications"`
		RedirHTTPPort  int      `long:"http-port" env:"HTTP_PORT" description:"http port for redirect to https and acme challenge test (default: 8080 under docker, 80 without)"`
		NoHTTPRedirect bool     `long:"no-redirect" env:"NO_REDIRECT" description:"disable http to https redirect"`
		FQDNs          []string `long:"fqdn" env:"ACME_FQDN" env-delim:"," description:"FQDN(s) for ACME certificates"`
		DNS           struct {
			Type       string        `long:"type" env:"TYPE" description:"DNS provider type" choice:"none" choice:"cloudflare" choice:"route53" choice:"gandi" choice:"digitalocean" choice:"hetzner" choice:"linode" choice:"godaddy" choice:"namecheap" choice:"scaleway" choice:"porkbun" choice:"dnsimple" choice:"duckdns" default:"none"` // nolint
			TTL        time.Duration `long:"ttl" env:"TTL" default:"2m" description:"DNS record TTL"`
			Cloudflare struct {
				APIToken string `long:"api-token" env:"API_TOKEN" description:"cloudflare api token"`
			} `group:"cloudflare" namespace:"cloudflare" env-namespace:"CLOUDFLARE"`
			Route53 struct {
				Region          string `long:"region" env:"REGION" description:"AWS region"`
				Profile         string `long:"profile" env:"PROFILE" description:"AWS profile"`
				AccessKeyID     string `long:"access-key-id" env:"ACCESS_KEY_ID" description:"AWS access key id"`
				SecretAccessKey string `long:"secret-access-key" env:"SECRET_ACCESS_KEY" description:"AWS secret access key"`
				SessionToken    string `long:"session-token" env:"SESSION_TOKEN" description:"AWS session token"`
				HostedZoneID    string `long:"hosted-zone-id" env:"HOSTED_ZONE_ID" description:"AWS hosted zone id"`
			} `group:"route53" namespace:"route53" env-namespace:"ROUTE53"`
			Gandi struct {
				BearerToken string `long:"bearer-token" env:"BEARER_TOKEN" description:"gandi bearer token"`
			} `group:"gandi" namespace:"gandi" env-namespace:"GANDI"`
			DigitalOcean struct {
				APIToken string `long:"api-token" env:"API_TOKEN" description:"digitalocean api token"`
			} `group:"digitalocean" namespace:"digitalocean" env-namespace:"DIGITALOCEAN"`
			Hetzner struct {
				APIToken string `long:"api-token" env:"API_TOKEN" description:"hetzner api token"`
			} `group:"hetzner" namespace:"hetzner" env-namespace:"HETZNER"`
			Linode struct {
				APIToken string `long:"api-token" env:"API_TOKEN" description:"linode api token"`
			} `group:"linode" namespace:"linode" env-namespace:"LINODE"`
			GoDaddy struct {
				APIToken string `long:"api-token" env:"API_TOKEN" description:"godaddy api token"`
			} `group:"godaddy" namespace:"godaddy" env-namespace:"GODADDY"`
			Namecheap struct {
				APIKey      string `long:"api-key" env:"API_KEY" description:"namecheap api key"`
				User        string `long:"user" env:"USER" description:"namecheap api user"`
				ClientIP    string `long:"client-ip" env:"CLIENT_IP" description:"namecheap client ip (auto-discovered if not set)"`
				APIEndpoint string `long:"api-endpoint" env:"API_ENDPOINT" description:"namecheap api endpoint (production or sandbox)"`
			} `group:"namecheap" namespace:"namecheap" env-namespace:"NAMECHEAP"`
			Scaleway struct {
				SecretKey      string `long:"secret-key" env:"SECRET_KEY" description:"scaleway secret key"`
				OrganizationID string `long:"organization-id" env:"ORGANIZATION_ID" description:"scaleway organization id"`
			} `group:"scaleway" namespace:"scaleway" env-namespace:"SCALEWAY"`
			Porkbun struct {
				APIKey       string `long:"api-key" env:"API_KEY" description:"porkbun api key"`
				APISecretKey string `long:"api-secret-key" env:"API_SECRET_KEY" description:"porkbun api secret key"`
			} `group:"porkbun" namespace:"porkbun" env-namespace:"PORKBUN"`
			DNSimple struct {
				APIAccessToken string `long:"api-access-token" env:"API_ACCESS_TOKEN" description:"dnsimple api access token"`
				AccountID      string `long:"account-id" env:"ACCOUNT_ID" description:"dnsimple account id"`
			} `group:"dnsimple" namespace:"dnsimple" env-namespace:"DNSIMPLE"`
			DuckDNS struct {
				APIToken string `long:"api-token" env:"API_TOKEN" description:"duckdns api token"`
			} `group:"duckdns" namespace:"duckdns" env-namespace:"DUCKDNS"`
		} `group:"dns" namespace:"dns" env-namespace:"DNS"`
	} `group:"ssl" namespace:"ssl" env-namespace:"SSL"`

	Assets struct {
		Location     string   `short:"a" long:"location" env:"LOCATION" default:"" description:"assets location"`
		WebRoot      string   `long:"root" env:"ROOT" default:"/" description:"assets web root"`
		SPA          bool     `long:"spa" env:"SPA" description:"spa treatment for assets"`
		CacheControl []string `long:"cache" env:"CACHE" description:"cache duration for assets" env-delim:","`
		NotFound     string   `long:"not-found" env:"NOT_FOUND" description:"path to file to serve on 404, relative to location"`
	} `group:"assets" namespace:"assets" env-namespace:"ASSETS"`

	Logger struct {
		StdOut     bool   `long:"stdout" env:"STDOUT" description:"enable stdout logging"`
		Enabled    bool   `long:"enabled" env:"ENABLED" description:"enable access and error rotated logs"`
		FileName   string `long:"file" env:"FILE"  default:"access.log" description:"location of access log"`
		MaxSize    string `long:"max-size" env:"MAX_SIZE" default:"100M" description:"maximum size before it gets rotated"`
		MaxBackups int    `long:"max-backups" env:"MAX_BACKUPS" default:"10" description:"maximum number of old log files to retain"`
	} `group:"logger" namespace:"logger" env-namespace:"LOGGER"`

	Docker struct {
		Enabled    bool     `long:"enabled" env:"ENABLED" description:"enable docker provider"`
		Host       string   `long:"host" env:"HOST" default:"unix:///var/run/docker.sock" description:"docker host"`
		Network    string   `long:"network" env:"NETWORK" default:"" description:"docker network"`
		Excluded   []string `long:"exclude" env:"EXCLUDE" description:"excluded containers" env-delim:","`
		AutoAPI    bool     `long:"auto" env:"AUTO" description:"enable automatic routing (without labels)"`
		APIPrefix  string   `long:"prefix" env:"PREFIX" description:"prefix for docker source routes"`
		APIVersion string   `long:"api-version" env:"API_VERSION" default:"1.24" description:"docker API version"`
	} `group:"docker" namespace:"docker" env-namespace:"DOCKER"`

	ConsulCatalog struct {
		Enabled       bool          `long:"enabled" env:"ENABLED" description:"enable consul catalog provider"`
		Address       string        `long:"address" env:"ADDRESS" default:"http://127.0.0.1:8500" description:"consul address"`
		CheckInterval time.Duration `long:"interval" env:"INTERVAL" default:"1s" description:"consul catalog check interval"`
	} `group:"consul-catalog" namespace:"consul-catalog" env-namespace:"CONSUL_CATALOG"`

	File struct {
		Enabled       bool          `long:"enabled" env:"ENABLED" description:"enable file provider"`
		Name          string        `long:"name" env:"NAME" default:"reproxy.yml" description:"file name"`
		CheckInterval time.Duration `long:"interval" env:"INTERVAL" default:"3s" description:"file check interval"`
		Delay         time.Duration `long:"delay" env:"DELAY" default:"500ms" description:"file event delay"`
	} `group:"file" namespace:"file" env-namespace:"FILE"`

	Static struct {
		Enabled bool     `long:"enabled" env:"ENABLED" description:"enable static provider"`
		Rules   []string `long:"rule" env:"RULES" description:"routing rules" env-delim:";"`
	} `group:"static" namespace:"static" env-namespace:"STATIC"`

	Timeouts struct {
		ReadHeader     time.Duration `long:"read-header" env:"READ_HEADER" default:"5s"  description:"read header server timeout"`
		Write          time.Duration `long:"write" env:"WRITE" default:"30s" description:"write server timeout"`
		Idle           time.Duration `long:"idle" env:"IDLE" default:"30s" description:"idle server timeout"`
		Dial           time.Duration `long:"dial" env:"DIAL" default:"30s" description:"dial transport timeout"`
		KeepAlive      time.Duration `long:"keep-alive" env:"KEEP_ALIVE" default:"30s"  description:"keep-alive transport timeout"`
		ResponseHeader time.Duration `long:"resp-header" env:"RESP_HEADER" default:"5s"  description:"response header transport timeout"`
		IdleConn       time.Duration `long:"idle-conn" env:"IDLE_CONN" default:"90s"  description:"idle connection transport timeout"`
		TLSHandshake   time.Duration `long:"tls" env:"TLS" default:"10s" description:"TLS hanshake transport timeout"`
		ExpectContinue time.Duration `long:"continue" env:"CONTINUE" default:"1s" description:"expect continue transport timeout"`
	} `group:"timeout" namespace:"timeout" env-namespace:"TIMEOUT"`

	Management struct {
		Enabled        bool   `long:"enabled" env:"ENABLED" description:"enable management API"`
		Listen         string `long:"listen" env:"LISTEN" default:"0.0.0.0:8081" description:"listen on host:port"`
		LowCardinality bool   `long:"low-cardinality" env:"LOW_CARDINALITY" description:"use route patterns instead of raw paths for metrics labels"`
	} `group:"mgmt" namespace:"mgmt" env-namespace:"MGMT"`

	ErrorReport struct {
		Enabled  bool   `long:"enabled" env:"ENABLED" description:"enable html errors reporting"`
		Template string `long:"template" env:"TEMPLATE" description:"error message template file"`
	} `group:"error" namespace:"error" env-namespace:"ERROR"`

	HealthCheck struct {
		Enabled  bool          `long:"enabled" env:"ENABLED" description:"enable automatic health-check"`
		Interval time.Duration `long:"interval" env:"INTERVAL" default:"300s" description:"automatic health-check interval"`
	} `group:"health-check" namespace:"health-check" env-namespace:"HEALTH_CHECK"`

	Throttle struct {
		System int `long:"system" env:"SYSTEM" default:"0" description:"throttle overall activity'"`
		User   int `long:"user" env:"USER"  default:"0" description:"limit req/sec per user and per proxy destination"`
	} `group:"throttle" namespace:"throttle" env-namespace:"THROTTLE"`

	Upstream struct {
		MaxIdleConns    int `long:"max-idle-conns" env:"MAX_IDLE_CONNS" default:"100" description:"max idle connections total"`
		MaxConnsPerHost int `long:"max-conns" env:"MAX_CONNS" default:"0" description:"max connections per upstream host (0=unlimited)"`
	} `group:"upstream" namespace:"upstream" env-namespace:"UPSTREAM"`

	Plugin struct {
		Enabled bool   `long:"enabled" env:"ENABLED" description:"enable plugin support"`
		Listen  string `long:"listen" env:"LISTEN" default:"127.0.0.1:8081" description:"registration listen on host:port"`
	} `group:"plugin" namespace:"plugin" env-namespace:"PLUGIN"`

	Signature bool `long:"signature" env:"SIGNATURE" description:"enable reproxy signature headers"`
	Dbg       bool `long:"dbg" env:"DEBUG" description:"debug mode"`
}

var revision = "unknown"

func main() {
	if os.Getenv("GO_FLAGS_COMPLETION") == "" {
		fmt.Printf("reproxy %s\n", revision)
	}

	p := flags.NewParser(&opts, flags.PrintErrors|flags.PassDoubleDash|flags.HelpFlag)
	p.SubcommandsOptional = true
	if _, err := p.Parse(); err != nil {
		var flagsErr *flags.Error
		if errors.As(err, &flagsErr) && flagsErr.Type != flags.ErrHelp {
			log.Printf("[ERROR] cli error: %v", err)
		}
		os.Exit(2)
	}

	setupLog(opts.Dbg)

	log.Printf("[DEBUG] options: %+v", opts)

	err := run()
	if err != nil {
		log.Fatalf("[ERROR] proxy server failed, %v", err)
	}
}

func run() error {
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		// catch signal and invoke graceful termination
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		<-stop
		log.Printf("[WARN] interrupt signal")
		cancel()
	}()

	defer func() {
		// handle panic
		if x := recover(); x != nil {
			log.Printf("[WARN] run time panic:\n%v", x)
			panic(x)
		}
	}()

	providers, err := makeProviders()
	if err != nil {
		return fmt.Errorf("failed to make providers: %w", err)
	}

	svc := discovery.NewService(providers, time.Second)
	if len(providers) > 0 {
		go func() {
			if e := svc.Run(context.Background()); e != nil {
				log.Printf("[WARN] discovery failed, %v", e)
			}
		}()
	}
	if opts.HealthCheck.Enabled {
		svc.ScheduleHealthCheck(context.Background(), opts.HealthCheck.Interval)
	}

	sslConfig, sslErr := makeSSLConfig()
	if sslErr != nil {
		return fmt.Errorf("failed to make config of ssl server params: %w", sslErr)
	}

	accessLog, alErr := makeAccessLogWriter()
	if alErr != nil {
		return fmt.Errorf("failed to access log: %w", alErr)
	}

	defer func() {
		if logErr := accessLog.Close(); logErr != nil {
			log.Printf("[WARN] can't close access log, %v", logErr)
		}
	}()

	cacheControl, err := proxy.MakeCacheControl(opts.Assets.CacheControl)
	if err != nil {
		return fmt.Errorf("failed to make cache control: %w", err)
	}

	errReporter, err := makeErrorReporter()
	if err != nil {
		return fmt.Errorf("failed to make error reporter: %w", err)
	}

	addr := listenAddress(opts.Listen, opts.SSL.Type)
	log.Printf("[DEBUG] listen address %s", addr)

	maxBodySize, perr := sizeParse(opts.MaxSize)
	if perr != nil {
		return fmt.Errorf("failed to convert MaxSize: %w", err)
	}

	proxyHeaders := opts.ProxyHeaders
	if len(proxyHeaders) == 0 {
		proxyHeaders = splitAtCommas(os.Getenv("HEADER")) // env value may have comma inside "", parsed separately
	}

	basicAuthAllowed, baErr := makeBasicAuth(opts.AuthBasicHtpasswd)
	if baErr != nil {
		return fmt.Errorf("failed to load basic auth: %w", baErr)
	}

	px := &proxy.Http{
		Version:        revision,
		Matcher:        svc,
		Address:        addr,
		MaxBodySize:    int64(maxBodySize), //nolint
		AssetsLocation: opts.Assets.Location,
		AssetsWebRoot:  opts.Assets.WebRoot,
		Assets404:      opts.Assets.NotFound,
		AssetsSPA:      opts.Assets.SPA,
		CacheControl:   cacheControl,
		GzEnabled:      opts.GzipEnabled,
		SSLConfig:      sslConfig,
		Insecure:       opts.Insecure,
		ProxyHeaders:   proxyHeaders,
		DropHeader:     opts.DropHeaders,
		AccessLog:      accessLog,
		StdOutEnabled:  opts.Logger.StdOut,
		Signature:      opts.Signature,
		LBSelector:     makeLBSelector(),
		Timeouts: proxy.Timeouts{
			ReadHeader:     opts.Timeouts.ReadHeader,
			Write:          opts.Timeouts.Write,
			Idle:           opts.Timeouts.Idle,
			Dial:           opts.Timeouts.Dial,
			KeepAlive:      opts.Timeouts.KeepAlive,
			IdleConn:       opts.Timeouts.IdleConn,
			TLSHandshake:   opts.Timeouts.TLSHandshake,
			ExpectContinue: opts.Timeouts.ExpectContinue,
			ResponseHeader: opts.Timeouts.ResponseHeader,
		},
		Metrics:                 makeMetrics(ctx, svc),
		Reporter:                errReporter,
		PluginConductor:         makePluginConductor(ctx),
		ThrottleSystem:          opts.Throttle.System * 3,
		ThrottleUser:            opts.Throttle.User,
		BasicAuthEnabled:        len(basicAuthAllowed) > 0,
		BasicAuthAllowed:        basicAuthAllowed,
		KeepHost:                opts.KeepHost,
		OnlyFrom:                makeOnlyFromMiddleware(),
		UpstreamMaxIdleConns:    opts.Upstream.MaxIdleConns,
		UpstreamMaxConnsPerHost: opts.Upstream.MaxConnsPerHost,
	}

	err = px.Run(ctx)
	if err != nil && errors.Is(err, http.ErrServerClosed) {
		log.Printf("[WARN] proxy server closed, %v", err) // nolint gocritic
		return nil
	}
	if err != nil {
		return fmt.Errorf("proxy server failed: %w", err)
	}
	return nil
}

// makeBasicAuth returns a list of allowed basic auth users and password hashes.
// if no htpasswd file is specified, an empty list is returned.
func makeBasicAuth(htpasswdFile string) ([]string, error) {
	var basicAuthAllowed []string
	if htpasswdFile != "" {
		data, err := os.ReadFile(htpasswdFile) //nolint:gosec //read file with opts passed path
		if err != nil {
			return nil, fmt.Errorf("failed to read htpasswd file %s: %w", htpasswdFile, err)
		}
		basicAuthAllowed = strings.Split(string(data), "\n")
		for i, v := range basicAuthAllowed {
			basicAuthAllowed[i] = strings.TrimSpace(v)
			basicAuthAllowed[i] = strings.ReplaceAll(basicAuthAllowed[i], "\t", "")
		}
	}
	return basicAuthAllowed, nil
}

// make all providers. the order is matter, defines which provider will have priority in case of conflicting rules
// static first, file second and docker the last one
func makeProviders() ([]discovery.Provider, error) {
	var res []discovery.Provider

	if opts.Static.Enabled {
		msgs := make([]string, 0, len(opts.Static.Rules))
		for _, rule := range opts.Static.Rules {
			msgs = append(msgs, "\""+rule+"\"")
		}
		log.Printf("[DEBUG] inject static rules: %s", strings.Join(msgs, " "))
		res = append(res, &provider.Static{Rules: opts.Static.Rules})
	}

	if opts.File.Enabled {
		res = append(res, &provider.File{
			FileName:      opts.File.Name,
			CheckInterval: opts.File.CheckInterval,
			Delay:         opts.File.Delay,
		})
	}

	if opts.Docker.Enabled {
		client := provider.NewDockerClient(opts.Docker.Host, opts.Docker.Network, opts.Docker.APIVersion)

		if opts.Docker.AutoAPI {
			log.Printf("[INFO] auto-api enabled for docker")
		}

		const refreshInterval = time.Second * 10 // seems like a reasonable default

		res = append(res, &provider.Docker{DockerClient: client, Excludes: opts.Docker.Excluded,
			AutoAPI: opts.Docker.AutoAPI, APIPrefix: opts.Docker.APIPrefix, RefreshInterval: refreshInterval})
	}

	if opts.ConsulCatalog.Enabled {
		client := consulcatalog.NewClient(opts.ConsulCatalog.Address, http.DefaultClient)
		res = append(res, consulcatalog.New(client, opts.ConsulCatalog.CheckInterval))
	}

	if len(res) == 0 && opts.Assets.Location == "" {
		return nil, errors.New("no providers enabled")
	}
	return res, nil
}

func makePluginConductor(ctx context.Context) proxy.MiddlewareProvider {
	if !opts.Plugin.Enabled {
		return nil
	}

	conductor := &plugin.Conductor{
		Address: opts.Plugin.Listen,
		RPCDialer: plugin.RPCDialerFunc(func(_, address string) (plugin.RPCClient, error) {
			return rpc.Dial("tcp", address)
		}),
	}
	go func() {
		if err := conductor.Run(ctx); err != nil {
			log.Printf("[WARN] plugin conductor error, %v", err)
		}
	}()
	return conductor
}

func makeMetrics(ctx context.Context, informer mgmt.Informer) proxy.MiddlewareProvider {
	if !opts.Management.Enabled {
		return nil
	}
	metrics := mgmt.NewMetrics(mgmt.MetricsConfig{
		LowCardinality: opts.Management.LowCardinality,
	})
	go func() {
		mgSrv := mgmt.Server{
			Listen:         opts.Management.Listen,
			Informer:       informer,
			AssetsLocation: opts.Assets.Location,
			AssetsWebRoot:  opts.Assets.WebRoot,
			Version:        revision,
		}
		if err := mgSrv.Run(ctx); err != nil {
			log.Printf("[WARN] management service failed, %v", err)
		}
	}()
	return metrics
}

func makeSSLConfig() (config proxy.SSLConfig, err error) {
	switch opts.SSL.Type {
	case "none":
		config.SSLMode = proxy.SSLNone
	case "static":
		if opts.SSL.Cert == "" {
			return config, errors.New("path to cert.pem is required")
		}
		if opts.SSL.Key == "" {
			return config, errors.New("path to key.pem is required")
		}
		config.SSLMode = proxy.SSLStatic
		config.Cert = opts.SSL.Cert
		config.Key = opts.SSL.Key
		config.RedirHTTPPort = redirHTTPPort(opts.SSL.RedirHTTPPort)
		config.NoHTTPRedirect = opts.SSL.NoHTTPRedirect
	case "auto":
		config.SSLMode = proxy.SSLAuto
		config.ACMEDirectory = opts.SSL.ACMEDirectory
		config.ACMELocation = opts.SSL.ACMELocation
		config.ACMEEmail = opts.SSL.ACMEEmail
		config.FQDNs = fqdns(opts.SSL.FQDNs)
		config.RedirHTTPPort = redirHTTPPort(opts.SSL.RedirHTTPPort)
		config.NoHTTPRedirect = opts.SSL.NoHTTPRedirect
		config.TTL = opts.SSL.DNS.TTL
		switch opts.SSL.DNS.Type {
		case "cloudflare":
			config.DNSProvider = &cloudflare.Provider{APIToken: opts.SSL.DNS.Cloudflare.APIToken}
		case "route53":
			config.DNSProvider = &route53.Provider{
				Region:             opts.SSL.DNS.Route53.Region,
				Profile:            opts.SSL.DNS.Route53.Profile,
				AccessKeyId:        opts.SSL.DNS.Route53.AccessKeyID,
				SecretAccessKey:    opts.SSL.DNS.Route53.SecretAccessKey,
				SessionToken:       opts.SSL.DNS.Route53.SessionToken,
				HostedZoneID:       opts.SSL.DNS.Route53.HostedZoneID,
				WaitForRoute53Sync: true,
			}
		case "gandi":
			config.DNSProvider = &gandi.Provider{
				BearerToken: opts.SSL.DNS.Gandi.BearerToken,
			}
		case "digitalocean":
			config.DNSProvider = &digitalocean.Provider{APIToken: opts.SSL.DNS.DigitalOcean.APIToken}
		case "hetzner":
			config.DNSProvider = &hetzner.Provider{AuthAPIToken: opts.SSL.DNS.Hetzner.APIToken}
		case "linode":
			config.DNSProvider = &linode.Provider{APIToken: opts.SSL.DNS.Linode.APIToken}
		case "godaddy":
			config.DNSProvider = &godaddy.Provider{APIToken: opts.SSL.DNS.GoDaddy.APIToken}
		case "namecheap":
			config.DNSProvider = &namecheap.Provider{
				APIKey:      opts.SSL.DNS.Namecheap.APIKey,
				User:        opts.SSL.DNS.Namecheap.User,
				ClientIP:    opts.SSL.DNS.Namecheap.ClientIP,
				APIEndpoint: opts.SSL.DNS.Namecheap.APIEndpoint,
			}
		case "scaleway":
			config.DNSProvider = &scaleway.Provider{
				SecretKey:      opts.SSL.DNS.Scaleway.SecretKey,
				OrganizationID: opts.SSL.DNS.Scaleway.OrganizationID,
			}
		case "porkbun":
			config.DNSProvider = &porkbun.Provider{
				APIKey:       opts.SSL.DNS.Porkbun.APIKey,
				APISecretKey: opts.SSL.DNS.Porkbun.APISecretKey,
			}
		case "dnsimple":
			config.DNSProvider = &dnsimple.Provider{
				APIAccessToken: opts.SSL.DNS.DNSimple.APIAccessToken,
				AccountID:      opts.SSL.DNS.DNSimple.AccountID,
			}
		case "duckdns":
			config.DNSProvider = &duckdns.Provider{APIToken: opts.SSL.DNS.DuckDNS.APIToken}
		}
	default:
		return config, fmt.Errorf("invalid value %q for SSL_TYPE, allowed values are: none, static or auto", opts.SSL.Type)
	}
	return config, err
}

func makeLBSelector() proxy.LBSelector {
	switch opts.LBType {
	case "random":
		return &proxy.RandomSelector{}
	case "failover":
		return &proxy.FailoverSelector{}
	case "roundrobin":
		return &proxy.RoundRobinSelector{}
	default:
		return &proxy.FailoverSelector{}
	}
}

func makeOnlyFromMiddleware() *proxy.OnlyFrom {
	if opts.RemoteLookupHeaders {
		return proxy.NewOnlyFrom(proxy.OFRealIP, proxy.OFForwarded, proxy.OFRemoteAddr)
	}
	return proxy.NewOnlyFrom(proxy.OFRemoteAddr)
}

func makeErrorReporter() (proxy.Reporter, error) {
	result := &proxy.ErrorReporter{
		Nice: opts.ErrorReport.Enabled,
	}
	if opts.ErrorReport.Template != "" {
		data, err := os.ReadFile(opts.ErrorReport.Template)
		if err != nil {
			return nil, fmt.Errorf("failed to load error html template from %s, %w", opts.ErrorReport.Template, err)
		}
		result.Template = string(data)
	}
	return result, nil
}

func makeAccessLogWriter() (accessLog io.WriteCloser, err error) {
	if !opts.Logger.Enabled {
		return nopWriteCloser{io.Discard}, nil
	}

	maxSize, perr := sizeParse(opts.Logger.MaxSize)
	if perr != nil {
		return nil, fmt.Errorf("can't parse logger MaxSize: %w", perr)
	}

	maxSize /= 1048576

	log.Printf("[INFO] logger enabled for %s, max size %dM", opts.Logger.FileName, maxSize)
	return &lumberjack.Logger{
		Filename:   opts.Logger.FileName,
		MaxSize:    int(maxSize), //nolint in MB
		MaxBackups: opts.Logger.MaxBackups,
		Compress:   true,
		LocalTime:  true,
	}, nil
}

// listenAddress sets default to 127.0.0.0:8080/80443 and, if detected REPROXY_IN_DOCKER env, to 0.0.0.0:80/443
func listenAddress(addr, sslType string) string {

	// don't set default if any opts.Listen address defined by user
	if addr != "" {
		return addr
	}

	// http, set default to 8080 in docker, 80 without
	if sslType == "none" {
		if v, ok := os.LookupEnv("REPROXY_IN_DOCKER"); ok && (v == "1" || v == "true") {
			return "0.0.0.0:8080"
		}
		return "127.0.0.1:80"
	}

	// https, set default to 8443 in docker, 443 without
	if v, ok := os.LookupEnv("REPROXY_IN_DOCKER"); ok && (v == "1" || v == "true") {
		return "0.0.0.0:8443"
	}
	return "127.0.0.1:443"
}

func redirHTTPPort(port int) int {
	// don't set default if any ssl.http-port defined by user
	if port != 0 {
		return port
	}
	if v, ok := os.LookupEnv("REPROXY_IN_DOCKER"); ok && (v == "1" || v == "true") {
		return 8080
	}
	return 80
}

// fqdns cleans space suffixes and prefixes which can sneak in from docker compose
func fqdns(inp []string) (res []string) {
	for _, v := range inp {
		res = append(res, strings.TrimSpace(v))
	}
	return res
}

func sizeParse(inp string) (uint64, error) {
	if inp == "" {
		return 0, errors.New("empty value")
	}
	for i, sfx := range []string{"k", "m", "g", "t"} {
		if strings.HasSuffix(inp, strings.ToUpper(sfx)) || strings.HasSuffix(inp, strings.ToLower(sfx)) {
			val, err := strconv.Atoi(inp[:len(inp)-1])
			if err != nil {
				return 0, fmt.Errorf("can't parse %s: %w", inp, err)
			}
			return uint64(float64(val) * math.Pow(float64(1024), float64(i+1))), nil
		}
	}
	res, err := strconv.ParseUint(inp, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse size %s: %w", inp, err)
	}
	return res, nil
}

// splitAtCommas split s at commas, ignoring commas in strings.
// Eliminate leading and trailing dbl quotes in each element only if both presented
// based on https://stackoverflow.com/a/59318708
func splitAtCommas(s string) []string {

	cleanup := func(s string) string {
		if s == "" {
			return s
		}
		res := strings.TrimSpace(s)
		if res[0] == '"' && res[len(res)-1] == '"' {
			res = strings.TrimPrefix(res, `"`)
			res = strings.TrimSuffix(res, `"`)
		}
		return res
	}

	var res []string
	var beg int
	var inString bool

	for i := 0; i < len(s); i++ {
		if s[i] == ',' && !inString {
			res = append(res, cleanup(s[beg:i]))
			beg = i + 1
			continue
		}

		if s[i] == '"' {
			if !inString {
				inString = true
			} else if i > 0 && s[i-1] != '\\' { // also allow \"
				inString = false
			}
		}
	}
	res = append(res, cleanup(s[beg:]))
	if len(res) == 1 && res[0] == "" {
		return []string{}
	}
	return res
}

type nopWriteCloser struct{ io.Writer }

func (n nopWriteCloser) Close() error { return nil }

func setupLog(dbg bool) {
	if dbg {
		log.Setup(log.Debug, log.CallerFile, log.CallerFunc, log.Msec, log.LevelBraces)
		return
	}
	log.Setup(log.Msec, log.LevelBraces)
}
