package dnsprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// Cloudflare implements autocert.DNSManager over Cloudflare API.
type Cloudflare struct {
	Email string
	Key   string
	TTL   time.Duration

	cl   *http.Client
	once sync.Once
}

// String returns the name of the DNS provider.
func (c *Cloudflare) String() string {
	return fmt.Sprintf("cloudflare{email=%s, ttl=%s}", c.Email, c.TTL)
}

// Fulfill adds the DNS record for the domain.
func (c *Cloudflare) Fulfill(ctx context.Context, domain string, record string) error {
	c.once.Do(func() { c.cl = &http.Client{Timeout: 500 * time.Millisecond} })

	reqBody := struct {
		Type    string `json:"type"`
		Name    string `json:"name"`
		Content string `json:"content"`
		TTL     int    `json:"ttl"`
	}{
		Type:    "TXT",
		Name:    "_acme-challenge." + domain + ".",
		Content: record,
		TTL:     int(c.TTL.Seconds()),
	}

	b, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("cloudflare: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.cloudflare.com/client/v4/zones",
		bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("cloudflare: create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Email", c.Email)
	req.Header.Set("X-Auth-Key", c.Key)

	resp, err := c.cl.Do(req)
	if err != nil {
		return fmt.Errorf("cloudflare: do request: %w", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cloudflare: bad status code: %d", resp.StatusCode)
	}

	return nil
}

// Cleanup removes the DNS record for the domain.
func (c *Cloudflare) Cleanup(ctx context.Context, domain string, record string) {
	c.once.Do(func() { c.cl = &http.Client{Timeout: 500 * time.Millisecond} })

	reqBody := struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}{
		Type: "TXT",
		Name: "_acme-challenge." + domain + ".",
	}

	b, err := json.Marshal(reqBody)
	if err != nil {
		log.Printf("[WARN] cloudflare: failed to cleanup dns record: marshal request body %v", err)
		return
	}

	req, err := http.NewRequestWithContext(ctx, "DELETE", "https://api.cloudflare.com/client/v4/zones",
		bytes.NewReader(b))
	if err != nil {
		log.Printf("[WARN] cloudflare: failed to cleanup dns record: make request %v", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Email", c.Email)
	req.Header.Set("X-Auth-Key", c.Key)

	resp, err := c.cl.Do(req)
	if err != nil {
		log.Printf("[WARN] cloudflare: failed to cleanup dns record: do request: %v", err)
		return
	}

	defer resp.Body.Close()
}
