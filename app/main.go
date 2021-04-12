package main

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	docker "github.com/fsouza/go-dockerclient"
	log "github.com/go-pkgz/lgr"
	"github.com/pkg/errors"
	"github.com/umputun/go-flags"
	"gopkg.in/natefinch/lumberjack.v2"

	"github.com/umputun/reproxy/app/discovery"
	"github.com/umputun/reproxy/app/discovery/provider"
	"github.com/umputun/reproxy/app/proxy"
)

var opts struct {
	Listen       string        `short:"l" long:"listen" env:"LISTEN" default:"127.0.0.1:8080" description:"listen on host:port"`
	TimeOut      time.Duration `short:"t" long:"timeout" env:"TIMEOUT" default:"5s" description:"proxy timeout"`
	MaxSize      int64         `short:"m" long:"max" env:"MAX_SIZE" default:"64000" description:"max response size"`
	GzipEnabled  bool          `short:"g" long:"gzip" env:"GZIP" description:"enable gz compression"`
	ProxyHeaders []string      `short:"x" long:"header" env:"HEADER" description:"proxy headers" env-delim:","`

	SSL struct {
		Type          string   `long:"type" env:"TYPE" description:"ssl (auto) support" choice:"none" choice:"static" choice:"auto" default:"none"` //nolint
		Cert          string   `long:"cert" env:"CERT" description:"path to cert.pem file"`
		Key           string   `long:"key" env:"KEY" description:"path to key.pem file"`
		ACMELocation  string   `long:"acme-location" env:"ACME_LOCATION" description:"dir where certificates will be stored by autocert manager" default:"./var/acme"`
		ACMEEmail     string   `long:"acme-email" env:"ACME_EMAIL" description:"admin email for certificate notifications"`
		RedirHTTPPort int      `long:"http-port" env:"HTTP_PORT" default:"80" description:"http port for redirect to https and acme challenge test"`
		FQDNs         []string `long:"fqdn" env:"ACME_FQDN" env-delim:"," description:"FQDN(s) for ACME certificates"`
	} `group:"ssl" namespace:"ssl" env-namespace:"SSL"`

	Assets struct {
		Location string `short:"a" long:"location" env:"LOCATION" default:"" description:"assets location"`
		WebRoot  string `long:"root" env:"ROOT" default:"/" description:"assets web root"`
	} `group:"assets" namespace:"assets" env-namespace:"ASSETS"`

	Logger struct {
		Enabled    bool   `long:"enabled" env:"ENABLED" description:"enable access and error rotated logs"`
		FileName   string `long:"file" env:"FILE"  default:"access.log" description:"location of access log"`
		MaxSize    int    `long:"max-size" env:"MAX_SIZE" default:"100" description:"maximum size in megabytes before it gets rotated"`
		MaxBackups int    `long:"max-backups" env:"MAX_BACKUPS" default:"10" description:"maximum number of old log files to retain"`
	} `group:"logger" namespace:"logger" env-namespace:"LOGGER"`

	Docker struct {
		Enabled  bool     `long:"enabled" env:"ENABLED" description:"enable docker provider"`
		Host     string   `long:"host" env:"HOST" default:"unix:///var/run/docker.sock" description:"docker host"`
		Network  string   `long:"network" env:"NETWORK" default:"" description:"docker network"`
		Excluded []string `long:"exclude" env:"EXCLUDE" description:"excluded containers" env-delim:","`
	} `group:"docker" namespace:"docker" env-namespace:"DOCKER"`

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

	NoSignature bool `long:"no-signature" env:"NO_SIGNATURE" description:"disable reproxy signature headers"`
	Dbg         bool `long:"dbg" env:"DEBUG" description:"debug mode"`
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
		os.Exit(1)
	}

	setupLog(opts.Dbg)
	catchSignal()

	providers, err := makeProviders()
	if err != nil {
		log.Fatalf("[ERROR] failed to make providers, %v", err)
	}

	svc := discovery.NewService(providers)
	if len(providers) > 0 {
		go func() {
			if e := svc.Run(context.Background()); e != nil {
				log.Fatalf("[ERROR] discovery failed, %v", e)
			}
		}()
	}

	sslConfig, err := makeSSLConfig()
	if err != nil {
		log.Fatalf("[ERROR] failed to make config of ssl server params, %v", err)
	}

	defer func() {
		if x := recover(); x != nil {
			log.Printf("[WARN] run time panic:\n%v", x)
			panic(x)
		}
	}()

	accessLog := makeAccessLogWriter()
	defer func() {
		if err := accessLog.Close(); err != nil {
			log.Printf("[WARN] can't close access log, %v", err)
		}
	}()

	px := &proxy.Http{
		Version:          revision,
		Matcher:          svc,
		Address:          opts.Listen,
		TimeOut:          opts.TimeOut,
		MaxBodySize:      opts.MaxSize,
		AssetsLocation:   opts.Assets.Location,
		AssetsWebRoot:    opts.Assets.WebRoot,
		GzEnabled:        opts.GzipEnabled,
		SSLConfig:        sslConfig,
		ProxyHeaders:     opts.ProxyHeaders,
		AccessLog:        accessLog,
		DisableSignature: opts.NoSignature,
	}
	if err := px.Run(context.Background()); err != nil {
		log.Fatalf("[ERROR] proxy server failed, %v", err) //nolint gocritic
	}
}

func makeProviders() ([]discovery.Provider, error) {
	var res []discovery.Provider

	if opts.File.Enabled {
		res = append(res, &provider.File{
			FileName:      opts.File.Name,
			CheckInterval: opts.File.CheckInterval,
			Delay:         opts.File.Delay,
		})
	}

	if opts.Docker.Enabled {
		client, err := docker.NewClient(opts.Docker.Host)
		if err != nil {
			return nil, errors.Wrapf(err, "failed to make docker client %s", err)
		}
		res = append(res, &provider.Docker{DockerClient: client, Excludes: opts.Docker.Excluded, Network: opts.Docker.Network})
	}

	if opts.Static.Enabled {
		res = append(res, &provider.Static{Rules: opts.Static.Rules})
	}

	if len(res) == 0 && opts.Assets.Location == "" {
		return nil, errors.Errorf("no providers enabled")
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
		config.RedirHTTPPort = opts.SSL.RedirHTTPPort
	case "auto":
		config.SSLMode = proxy.SSLAuto
		config.ACMELocation = opts.SSL.ACMELocation
		config.ACMEEmail = opts.SSL.ACMEEmail
		config.FQDNs = opts.SSL.FQDNs
		config.RedirHTTPPort = opts.SSL.RedirHTTPPort
	}
	return config, err
}

func makeAccessLogWriter() (accessLog io.WriteCloser) {
	if !opts.Logger.Enabled {
		return nopWriteCloser{ioutil.Discard}
	}
	log.Printf("[INFO] logger enabled for %s", opts.Logger.FileName)
	return &lumberjack.Logger{
		Filename:   opts.Logger.FileName,
		MaxSize:    opts.Logger.MaxSize,
		MaxBackups: opts.Logger.MaxBackups,
		Compress:   true,
		LocalTime:  true,
	}
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

func catchSignal() {
	// catch SIGQUIT and print stack traces
	sigChan := make(chan os.Signal)
	go func() {
		for range sigChan {
			log.Print("[INFO] SIGQUIT detected")
			stacktrace := make([]byte, 8192)
			length := runtime.Stack(stacktrace, true)
			if length > 8192 {
				length = 8192
			}
			fmt.Println(string(stacktrace[:length]))
		}
	}()
	signal.Notify(sigChan, syscall.SIGQUIT)
}
