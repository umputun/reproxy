package provider

import (
	"context"
	"os"
	"regexp"
	"time"

	log "github.com/go-pkgz/lgr"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v3"

	"github.com/umputun/docker-proxy/app/discovery"
)

// File implements file-based provider
// Each line contains src:dst pairs, i.e. ^/api/svc1/(.*) http://127.0.0:8080/blah/$1
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
func (d *File) List() (res []discovery.UrlMapper, err error) {

	var fileConf []struct {
		SourceServer string `yaml:"server"`
		SourceRoute  string `yaml:"route"`
		Dest         string `yaml:"dest"`
	}

	fh, err := os.Open(d.FileName)
	if err != nil {
		return nil, errors.Wrapf(err, "can't open %s", d.FileName)
	}
	defer fh.Close()

	if err = yaml.NewDecoder(fh).Decode(&fileConf); err != nil {
		return nil, errors.Wrapf(err, "can't parse %s", d.FileName)
	}
	log.Printf("[DEBUG] file provider %+v", res)

	for _, f := range fileConf {
		rx, err := regexp.Compile(f.SourceRoute)
		if err != nil {
			return nil, errors.Wrapf(err, "can't parse regex %s", f.SourceRoute)
		}
		res = append(res, discovery.UrlMapper{Server: f.SourceServer, SrcMatch: rx, Dst: f.Dest})
	}
	return res, nil
}

func (d *File) ID() discovery.ProviderID { return discovery.PIFile }
