package provider

import (
	"bufio"
	"context"
	"os"
	"regexp"
	"strings"
	"time"

	log "github.com/go-pkgz/lgr"
	"github.com/pkg/errors"

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

	// no need to queue multiple events or wait
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
	fh, err := os.Open(d.FileName)
	if err != nil {
		return nil, errors.Wrapf(err, "can't open %s", d.FileName)
	}
	defer fh.Close()

	s := bufio.NewScanner(fh)
	for s.Scan() {
		line := s.Text()
		elems := strings.Fields(line)
		if len(elems) != 2 {
			continue
		}
		rx, err := regexp.Compile(elems[0])
		if err != nil {
			return nil, errors.Wrapf(err, "can't parse regex %s", elems[0])
		}
		res = append(res, discovery.UrlMapper{SrcMatch: rx, Dst: elems[1]})
	}
	return res, s.Err()
}

func (d *File) ID() string { return "file" }
