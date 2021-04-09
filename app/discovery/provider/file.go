package provider

import (
	"context"
	"os"
	"regexp"
	"sort"
	"time"

	log "github.com/go-pkgz/lgr"
	"github.com/pkg/errors"
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
func (d *File) Events(ctx context.Context) <-chan struct{} {
	res := make(chan struct{})

	// no need to queue multiple events
	trySubmit := func(ch chan struct{}) {
		select {
		case ch <- struct{}{}:
		default:
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
					lastModif = fi.ModTime()
					trySubmit(res)
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
		SourceRoute string `yaml:"route"`
		Dest        string `yaml:"dest"`
		Ping        string `yaml:"ping"`
	}
	fh, err := os.Open(d.FileName)
	if err != nil {
		return nil, errors.Wrapf(err, "can't open %s", d.FileName)
	}
	defer fh.Close() //nolint gosec

	if err = yaml.NewDecoder(fh).Decode(&fileConf); err != nil {
		return nil, errors.Wrapf(err, "can't parse %s", d.FileName)
	}
	log.Printf("[DEBUG] file provider %+v", res)

	for srv, fl := range fileConf {
		for _, f := range fl {
			rx, e := regexp.Compile(f.SourceRoute)
			if e != nil {
				return nil, errors.Wrapf(e, "can't parse regex %s", f.SourceRoute)
			}
			if srv == "default" {
				srv = "*"
			}
			mapper := discovery.URLMapper{Server: srv, SrcMatch: *rx, Dst: f.Dest, PingURL: f.Ping}
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

// ID returns providers id
func (d *File) ID() discovery.ProviderID { return discovery.PIFile }
