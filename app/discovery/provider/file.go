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
				Server:     srv,
				SrcMatch:   *rx,
				Dst:        f.Dest,
				PingURL:    f.Ping,
				ProviderID: discovery.PIFile,
				MatchType:  discovery.MTProxy,
			}
			if f.AssetsEnabled {
				mapper.MatchType = discovery.MTStatic
			}
			res = append(res, mapper)
		}
	}
	sort.Slice(res, func(i, j int) bool {
		if res[i].Server == res[j].Server {
			return res[i].SrcMatch.String() < res[j].SrcMatch.String()
		}
		return res[i].Server < res[j].Server
	})

	err = fh.Close()
	return res, err
}
