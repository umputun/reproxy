package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/alecthomas/units"
	log "github.com/go-pkgz/lgr"
	"github.com/umputun/go-flags"
	"gopkg.in/natefinch/lumberjack.v2"

	"github.com/umputun/reproxy/app/discovery"
	"github.com/umputun/reproxy/app/discovery/provider"
	"github.com/umputun/reproxy/app/discovery/provider/consulcatalog"
	"github.com/umputun/reproxy/app/mgmt"
	"github.com/umputun/reproxy/app/proxy"
)

var opts struct {
	Listen       string   `short:"l" long:"listen" env:"LISTEN" description:"listen on host:port (default: 0.0.0.0:8080/8443 under docker, 127.0.0.1:80/443 without)"`
	MaxSize      string   `short:"m" long:"max" env:"MAX_SIZE" default:"64K" description:"max request size"`
	GzipEnabled  bool     `short:"g" long:"gzip" env:"GZIP" description:"enable gz compression"`
	ProxyHeaders []string `short:"x" long:"header" env:"HEADER" description:"proxy headers" env-delim:","`

	SSL struct {
		Type          string   `long:"type" env:"TYPE" description:"ssl (auto) support" choice:"none" choice:"static" choice:"auto" default:"none"` //nolint
		Cert          string   `long:"cert" env:"CERT" description:"path to cert.pem file"`
		Key           string   `long:"key" env:"KEY" description:"path to key.pem file"`
		ACMELocation  string   `long:"acme-location" env:"ACME_LOCATION" description:"dir where certificates will be stored by autocert manager" default:"./var/acme"`
		ACMEEmail     string   `long:"acme-email" env:"ACME_EMAIL" description:"admin email for certificate notifications"`
		RedirHTTPPort int      `long:"http-port" env:"HTTP_PORT" description:"http port for redirect to https and acme challenge test (default: 8080 under docker, 80 without)"`
		FQDNs         []string `long:"fqdn" env:"ACME_FQDN" env-delim:"," description:"FQDN(s) for ACME certificates"`
	} `group:"ssl" namespace:"ssl" env-namespace:"SSL"`

	Assets struct {
		Location     string   `short:"a" long:"location" env:"LOCATION" default:"" description:"assets location"`
		WebRoot      string   `long:"root" env:"ROOT" default:"/" description:"assets web root"`
		CacheControl []string `long:"cache" env:"CACHE" description:"cache duration for assets" env-delim:","`
	} `group:"assets" namespace:"assets" env-namespace:"ASSETS"`

	Logger struct {
		StdOut     bool   `long:"stdout" env:"STDOUT" description:"enable stdout logging"`
		Enabled    bool   `long:"enabled" env:"ENABLED" description:"enable access and error rotated logs"`
		FileName   string `long:"file" env:"FILE"  default:"access.log" description:"location of access log"`
		MaxSize    string `long:"max-size" env:"MAX_SIZE" default:"100M" description:"maximum size before it gets rotated"`
		MaxBackups int    `long:"max-backups" env:"MAX_BACKUPS" default:"10" description:"maximum number of old log files to retain"`
	} `group:"logger" namespace:"logger" env-namespace:"LOGGER"`

	Docker struct {
		Enabled   bool     `long:"enabled" env:"ENABLED" description:"enable docker provider"`
		Host      string   `long:"host" env:"HOST" default:"unix:///var/run/docker.sock" description:"docker host"`
		Network   string   `long:"network" env:"NETWORK" default:"" description:"docker network"`
		Excluded  []string `long:"exclude" env:"EXCLUDE" description:"excluded containers" env-delim:","`
		AutoAPI   bool     `long:"auto" env:"AUTO" description:"enable automatic routing (without labels)"`
		APIPrefix string   `long:"prefix" env:"PREFIX" description:"prefix for docker source routes"`
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
		Enabled bool   `long:"enabled" env:"ENABLED" description:"enable management API"`
		Listen  string `long:"listen" env:"LISTEN" default:"0.0.0.0:8081" description:"listen on host:port"`
	} `group:"mgmt" namespace:"mgmt" env-namespace:"MGMT"`

	ErrorReport struct {
		Enabled  bool   `long:"enabled" env:"ENABLED" description:"enable html errors reporting"`
		Template string `long:"template" env:"TEMPLATE" description:"error message template file"`
	} `group:"error" namespace:"error" env-namespace:"ERROR"`

	Signature bool `long:"signature" env:"SIGNATURE" description:"enable reproxy signature headers"`
	Dbg       bool `long:"dbg" env:"DEBUG" description:"debug mode"`
}

var revision = "unknown"

func main() {
	fmt.Printf("reproxy %s\n", revision)

	p := flags.NewParser(&opts, flags.PrintErrors|flags.PassDoubleDash|flags.HelpFlag)
	p.SubcommandsOptional = true
	if _, err := p.Parse(); err != nil {
		if err.(*flags.Error).Type != flags.ErrHelp {
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
		if x := recover(); x != nil {
			log.Printf("[WARN] run time panic:\n%v", x)
			panic(x)
		}

		// catch signal and invoke graceful termination
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		<-stop
		log.Printf("[WARN] interrupt signal")
		cancel()
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

	sslConfig, sslErr := makeSSLConfig()
	if sslErr != nil {
		return fmt.Errorf("failed to make config of ssl server params: %w", sslErr)
	}

	accessLog, alErr := makeAccessLogWriter()
	if alErr != nil {
		return fmt.Errorf("failed to access log: %w", sslErr)
	}

	defer func() {
		if logErr := accessLog.Close(); logErr != nil {
			log.Printf("[WARN] can't close access log, %v", logErr)
		}
	}()

	metrics := mgmt.NewMetrics()
	go func() {
		mgSrv := mgmt.Server{
			Listen:         opts.Management.Listen,
			Informer:       svc,
			AssetsLocation: opts.Assets.Location,
			AssetsWebRoot:  opts.Assets.WebRoot,
			Version:        revision,
			Metrics:        metrics,
		}
		if opts.Management.Enabled {
			if mgErr := mgSrv.Run(ctx); err != nil {
				log.Printf("[WARN] management service failed, %v", mgErr)
			}
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

	px := &proxy.Http{
		Version:        revision,
		Matcher:        svc,
		Address:        addr,
		MaxBodySize:    int64(maxBodySize),
		AssetsLocation: opts.Assets.Location,
		AssetsWebRoot:  opts.Assets.WebRoot,
		CacheControl:   cacheControl,
		GzEnabled:      opts.GzipEnabled,
		SSLConfig:      sslConfig,
		ProxyHeaders:   opts.ProxyHeaders,
		AccessLog:      accessLog,
		StdOutEnabled:  opts.Logger.StdOut,
		Signature:      opts.Signature,
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
		Metrics:  metrics,
		Reporter: errReporter,
	}

	err = px.Run(ctx)
	if err != nil && err == http.ErrServerClosed {
		log.Printf("[WARN] proxy server closed, %v", err) //nolint gocritic
		return nil
	}
	return err
}

// make all providers. the order is matter, defines which provider will have priority in case of conflicting rules
// static first, file second and docker the last one
func makeProviders() ([]discovery.Provider, error) {
	var res []discovery.Provider

	if opts.Static.Enabled {
		var msgs []string
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
		client := provider.NewDockerClient(opts.Docker.Host, opts.Docker.Network)

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
	case "auto":
		config.SSLMode = proxy.SSLAuto
		config.ACMELocation = opts.SSL.ACMELocation
		config.ACMEEmail = opts.SSL.ACMEEmail
		config.FQDNs = opts.SSL.FQDNs
		config.RedirHTTPPort = redirHTTPPort(opts.SSL.RedirHTTPPort)
	}
	return config, err
}

func makeErrorReporter() (proxy.Reporter, error) {
	result := &proxy.ErrorReporter{
		Nice: opts.ErrorReport.Enabled,
	}
	if opts.ErrorReport.Template != "" {
		data, err := ioutil.ReadFile(opts.ErrorReport.Template)
		if err != nil {
			return nil, fmt.Errorf("failed to load error html template from %s, %w", opts.ErrorReport.Template, err)
		}
		result.Template = string(data)
	}
	return result, nil
}

func makeAccessLogWriter() (accessLog io.WriteCloser, err error) {
	if !opts.Logger.Enabled {
		return nopWriteCloser{ioutil.Discard}, nil
	}

	maxSize, perr := sizeParse(opts.Logger.MaxSize)
	if perr != nil {
		return nil, fmt.Errorf("can't parse logger MaxSize: %w", perr)
	}

	maxSize /= 1048576

	log.Printf("[INFO] logger enabled for %s, max size %dM", opts.Logger.FileName, maxSize)
	return &lumberjack.Logger{
		Filename:   opts.Logger.FileName,
		MaxSize:    int(maxSize), // in MB
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

func sizeParse(inp string) (units.Base2Bytes, error) {
	if inp == "" {
		return 0, errors.New("empty value")
	}

	size, err := units.ParseBase2Bytes(inp)

	return size, err
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
