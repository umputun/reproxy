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

	// Log can be used to set a custom logger. By default, all logs are sent directly
	// to std logger. More logger implementations can be found in package lib/logging.
	Log log.L `json:"-"`
}

// Do register the plugin, send info to reproxy conductor and activate RPC listener.
// On completion unregister from reproxy. Blocking call, should run in goroutine on the caller side
// rvcr is provided struct implemented at least one RPC methods with the signature like this:
// func(req lib.Request, res *lib.Response) (err error)
// see [examples/plugin]() for more info
func (p *Plugin) Do(ctx context.Context, conductor string, rcvr interface{}) (err error) {
	if p.Log == nil {
		p.Log = log.Std
	}

	ctxCancel, cancel := context.WithCancel(ctx)
	defer cancel()

	if err = rpc.RegisterName(p.Name, rcvr); err != nil {
		return fmt.Errorf("can't register plugin %s: %w", p.Name, err)
	}
	p.Log.Logf("[INFO] register rpc %s:%s", p.Name, p.Address)

	client := http.Client{Timeout: time.Second}
	time.AfterFunc(time.Millisecond*50, func() {
		// registration http call delayed to let listener time to start
		err = repeater.NewDefault(10, time.Millisecond*500).Do(ctx, func() error {
			return p.send(&client, conductor, "POST")
		})
		if err != nil {
			p.Log.Logf("[ERROR] can't register with reproxy for %s: %v", p.Name, err)
			cancel()
		}
	})

	defer func() {
		if e := p.send(&client, conductor, "DELETE"); e != nil {
			p.Log.Logf("[WARN] can't unregister with reproxy for %s: %v", p.Name, err)
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
			p.Log.Logf("[WARN] can't lose plugin listener")
		}
	}()

	for {
		p.Log.Logf("[DEBUG] plugin listener for %s:%s activated", p.Name, p.Address)
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
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
		return err
	}
	req, err := http.NewRequest(method, conductor, bytes.NewReader(data))
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("invalid status %s", resp.Status)
	}
	return nil
}
