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
)

// Plugin provides cancelable rpc server
type Plugin struct {
	Name    string `json:"name"`
	Address string `json:"address"`
}

// Do register the plugin, send info to reproxy conductor and activate RPC listener.
// On completion unregister from reproxy.
func (p *Plugin) Do(ctx context.Context, conductor string, rcvr interface{}) (err error) {
	if err = rpc.RegisterName(p.Name, rcvr); err != nil {
		return fmt.Errorf("can't register plugin %s: %v", p.Name, err)
	}

	listener, err := net.Listen("tcp", p.Address)
	if err != nil {
		return fmt.Errorf("can't listen on %s: %v", p.Address, err)
	}

	client := http.Client{Timeout: time.Second}
	if err = p.send(client, conductor, "POST"); err != nil {
		return fmt.Errorf("can't register with reproxy for %s: %v", p.Name, err)
	}

	defer func() {
		if e := p.send(client, conductor, "DELETE"); e != nil {
			err = fmt.Errorf("can't unregister with reproxy for %s: %v", p.Name, e)
		}
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return fmt.Errorf("accept failed for %s: %v", p.Name, err)
			}
		}

		go rpc.ServeConn(conn)
	}
}

func (p *Plugin) send(client http.Client, conductor string, method string) error {

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
