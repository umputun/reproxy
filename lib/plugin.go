package lib

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/rpc"
	"time"

	log "github.com/go-pkgz/lgr"
	"github.com/go-pkgz/repeater"
)

// Plugin provides cancelable rpc server used to run custom plugins
type Plugin struct {
	Name    string   `json:"name"`
	Address string   `json:"address"`
	Methods []string `json:"methods"`
}

// Do register the plugin, send info to reproxy conductor and activate RPC listener.
// On completion unregister from reproxy. Blocking call, should run in goroutine on the caller side
// rvcr is provided struct implemented at least one RPC methods with the signature like this:
// func(req lib.Request, res *lib.Response) (err error)
// see [examples/plugin]() for more info
func (p *Plugin) Do(ctx context.Context, conductor string, rcvr any) (err error) {

	ctxCancel, cancel := context.WithCancel(ctx)
	defer cancel()

	if err = rpc.RegisterName(p.Name, rcvr); err != nil {
		return fmt.Errorf("can't register plugin %s: %w", p.Name, err)
	}
	log.Printf("[INFO] register rpc %s:%s", p.Name, p.Address)

	client := http.Client{Timeout: time.Second}
	time.AfterFunc(time.Millisecond*50, func() {
		// registration http call delayed to let listener time to start
		regErr := repeater.NewDefault(10, time.Millisecond*500).Do(ctx, func() error {
			return p.send(&client, conductor, "POST")
		})
		if regErr != nil {
			log.Printf("[ERROR] can't register with reproxy for %s: %v", p.Name, regErr)
			cancel()
		}
	})

	defer func() {
		if e := p.send(&client, conductor, "DELETE"); e != nil {
			log.Printf("[WARN] can't unregister with reproxy for %s: %v", p.Name, e)
		}
	}()

	return p.listen(ctxCancel)
}

func (p *Plugin) listen(ctx context.Context) error {
	listener, err := net.Listen("tcp", p.Address)
	if err != nil {
		return fmt.Errorf("can't listen on %s: %w", p.Address, err)
	}

	go func() {
		<-ctx.Done()
		if err := listener.Close(); err != nil {
			log.Printf("[WARN] can't lose plugin listener")
		}
	}()

	for {
		log.Printf("[DEBUG] plugin listener for %s:%s activated", p.Name, p.Address)
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return fmt.Errorf("context done: %w", ctx.Err())
			default:
				return fmt.Errorf("accept failed for %s: %w", p.Name, err)
			}
		}
		go rpc.ServeConn(conn)
	}
}

func (p *Plugin) send(client *http.Client, conductor, method string) error {

	if conductor == "" {
		return nil
	}

	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal plugin: %w", err)
	}
	req, err := http.NewRequest(method, conductor, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close() // nolint

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("invalid status %s", resp.Status)
	}
	return nil
}
