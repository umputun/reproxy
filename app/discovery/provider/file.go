package provider

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"time"

	log "github.com/go-pkgz/lgr"
	"gopkg.in/yaml.v3"

	"github.com/umputun/reproxy/app/discovery"
)

// File implements file-based provider, defined with yaml file
type File struct {
	FileName      string
	CheckInterval time.Duration
	Delay         time.Duration
}

// Events returns channel updating on file change only
func (d *File) Events(ctx context.Context) <-chan discovery.ProviderID {
	res := make(chan discovery.ProviderID)

	// no need to queue multiple events
	trySubmit := func(ch chan discovery.ProviderID) bool {
		select {
		case ch <- discovery.PIFile:
			return true
		default:
			return false
		}
	}

	// check once if config file in place and it is file for real and not a directory
	fi, err := os.Stat(d.FileName)
	if err != nil {
		log.Printf("[WARN] configuration file %s not found", d.FileName)
	}
	if err == nil && fi.IsDir() {
		log.Printf("[WARN] %s is directory but configuration file expected", d.FileName)
	}

	go func() {
		tk := time.NewTicker(d.CheckInterval)
		lastModif := time.Time{}
		for {
			select {
			case <-tk.C:
				fi, err := os.Stat(d.FileName)
				if err != nil {
					continue
				}
				if fi.ModTime() != lastModif {
					// don't react on modification right away
					if fi.ModTime().Sub(lastModif) < d.Delay {
						continue
					}
					log.Printf("[DEBUG] file %s changed, %s -> %s", d.FileName,
						lastModif.Format(time.RFC3339Nano), fi.ModTime().Format(time.RFC3339Nano))
					if trySubmit(res) {
						lastModif = fi.ModTime()
					}
				}
			case <-ctx.Done():
				close(res)
				tk.Stop()
				return
			}
		}
	}()
	return res
}

// List all src dst pairs
func (d *File) List() (res []discovery.URLMapper, err error) {

	var fileConf map[string][]struct {
		SourceRoute   string `yaml:"route"`
		Dest          string `yaml:"dest"`
		Ping          string `yaml:"ping"`
		AssetsEnabled bool   `yaml:"assets"`
		AssetsSPA     bool   `yaml:"spa"`
		KeepHost      *bool  `yaml:"keep-host,omitempty"`
		OnlyFrom      string `yaml:"remote"`
		Auth          string `yaml:"auth"`
	}
	fh, err := os.Open(d.FileName)
	if err != nil {
		return nil, fmt.Errorf("can't open %s: %w", d.FileName, err)
	}
	defer fh.Close() //nolint gosec

	if err = yaml.NewDecoder(fh).Decode(&fileConf); err != nil {
		return nil, fmt.Errorf("can't parse %s: %w", d.FileName, err)
	}
	log.Printf("[DEBUG] file provider %+v", res)

	for srv, fl := range fileConf {
		for _, f := range fl {
			rx, e := regexp.Compile(f.SourceRoute)
			if e != nil {
				return nil, fmt.Errorf("can't parse regex %s: %w", f.SourceRoute, e)
			}
			if srv == "default" {
				srv = "*"
			}
			mapper := discovery.URLMapper{
				Server:      srv,
				SrcMatch:    *rx,
				Dst:         f.Dest,
				PingURL:     f.Ping,
				KeepHost:    f.KeepHost,
				ProviderID:  discovery.PIFile,
				MatchType:   discovery.MTProxy,
				OnlyFromIPs: discovery.ParseOnlyFrom(f.OnlyFrom),
				AuthUsers:   discovery.ParseAuth(f.Auth),
			}
			if f.AssetsEnabled || f.AssetsSPA {
				mapper.MatchType = discovery.MTStatic
				mapper.AssetsSPA = f.AssetsSPA
			}
			res = append(res, mapper)
		}
	}
	sort.Slice(res, func(i, j int) bool {
		return len(res[i].Server) > len(res[j].Server)
	})

	if err = fh.Close(); err != nil {
		return res, fmt.Errorf("failed to close file: %w", err)
	}
	return res, nil
}
