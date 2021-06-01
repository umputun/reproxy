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
)

// Plugin provides cancelable rpc server used to run custom plugins
type Plugin struct {
	Name    string   `json:"name"`
	Address string   `json:"address"`
	Methods []string `json:"methods"`
}

// Do register the plugin, send info to reproxy conductor and activate RPC listener.
// On completion unregister from reproxy. Blocking call, should run in goroutine on the caller side
// rvcr is provided struct implemented at least one RPC methods with teh signature leike this:
// func(req lib.Request, res *lib.Response) (err error)
// see [examples/plugin]() for more info
func (p *Plugin) Do(ctx context.Context, conductor string, rcvr interface{}) (err error) {
	if err = rpc.RegisterName(p.Name, rcvr); err != nil {
		return fmt.Errorf("can't register plugin %s: %v", p.Name, err)
	}
	log.Printf("[INFO] register rpc %s:%s", p.Name, p.Address)

	client := http.Client{Timeout: time.Second}
	time.AfterFunc(time.Millisecond*10, func() {
		// registration http call delayed to let listener time to start
		if err = p.send(&client, conductor, "POST"); err != nil {
			err = fmt.Errorf("can't register with reproxy for %s: %v", p.Name, err)
		}
	})

	defer func() {
		if e := p.send(&client, conductor, "DELETE"); e != nil {
			err = fmt.Errorf("can't unregister with reproxy for %s: %v", p.Name, e)
		}
	}()

	listener, err := net.Listen("tcp", p.Address)
	if err != nil {
		return fmt.Errorf("can't listen on %s: %v", p.Address, err)
	}

	for {
		log.Printf("[DEBUG] plugin listener for %s:%s activated", p.Name, p.Address)
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				return fmt.Errorf("accept failed for %s: %v", p.Name, err)
			}
		}

		go rpc.ServeConn(conn)
	}

}

func (p *Plugin) send(client *http.Client, conductor string, method string) error {

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
