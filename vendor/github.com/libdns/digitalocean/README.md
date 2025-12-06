# DigitalOcean for `libdns`

[![godoc reference](https://img.shields.io/badge/godoc-reference-blue.svg)](https://pkg.go.dev/github.com/libdns/digitalocean)


This package implements the libdns interfaces for the [DigitalOcean API](https://developers.digitalocean.com/documentation/v2/#domains) (using the Go implementation from: https://github.com/digitalocean/godo)

## Authenticating

To authenticate you need to supply a DigitalOcean API token.

## Example

Here's a minimal example of how to get all your DNS records using this `libdns` provider (see `_example/main.go`)

```go
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/libdns/digitalocean"
	"github.com/libdns/libdns"
)

func main() {
	token := os.Getenv("DO_AUTH_TOKEN")
	if token == "" {
		fmt.Printf("DO_AUTH_TOKEN not set\n")
		return
	}
	zone := os.Getenv("ZONE")
	if zone == "" {
		fmt.Printf("ZONE not set\n")
		return
	}
	// NOTE: when `DELETE_ENTRIES` is set to `1`, the script will delete the created entries
	deleteEntries := os.Getenv("DELETE_ENTRIES") == "1"
	provider := digitalocean.Provider{APIToken: token}

	records, err := provider.GetRecords(context.TODO(), zone)
	if err != nil {
		fmt.Printf("ERROR: %s\n", err.Error())
	}

	txtTestName := "libdns-test-txt"
	txtTestId := ""
	aTestName := "libdns-test-a"
	aTestId := ""
	for _, record := range records {
		fmt.Printf("%s (.%s): %s, %s\n", record.RR().Name, zone, record.RR().Data, record.RR().Type)
		if record.RR().Name == txtTestName {
			txtTestId = record.(digitalocean.DNS).ID
		} else if record.RR().Name == aTestName {
			aTestId = record.(digitalocean.DNS).ID
		}
	}

	if txtTestId != "" && aTestId != "" {
		if deleteEntries {
			fmt.Printf("Delete entry for %s (id:%s)\n", txtTestName, txtTestId)
			_, err = provider.DeleteRecords(context.TODO(), zone, []libdns.Record{digitalocean.DNS{ID: txtTestId}})
			if err != nil {
				fmt.Printf("ERROR: %s\n", err.Error())
			}
			fmt.Printf("Delete entry for %s (id:%s)\n", aTestName, aTestId)
			_, err = provider.DeleteRecords(context.TODO(), zone, []libdns.Record{digitalocean.DNS{ID: aTestId}})
			if err != nil {
				fmt.Printf("ERROR: %s\n", err.Error())
			}
		} else {
			fmt.Printf("Replacing entry for %s\n", txtTestName)
			_, err = provider.SetRecords(context.TODO(), zone, []libdns.Record{digitalocean.DNS{
				Record: libdns.RR{
					Type: "TXT",
					Name: txtTestName,
					Data: fmt.Sprintf("Replacement test entry created by libdns %s", time.Now()),
					TTL:  time.Duration(30) * time.Second,
				},
				ID: txtTestId,
			}})
			if err != nil {
				fmt.Printf("ERROR: %s\n", err.Error())
			}
			fmt.Printf("Replacing entry for %s\n", aTestName)
			_, err = provider.SetRecords(context.TODO(), zone, []libdns.Record{digitalocean.DNS{
				Record: libdns.RR{
					Type: "A",
					Name: aTestName,
					Data: "127.0.0.1",
					TTL:  time.Duration(30) * time.Second,
				},
				ID: aTestId,
			}})
			if err != nil {
				fmt.Printf("ERROR: %s\n", err.Error())
			}
		}
	} else {
		fmt.Printf("Creating new entry for %s\n", txtTestName)
		_, err = provider.AppendRecords(context.TODO(), zone, []libdns.Record{libdns.RR{
			Type: "TXT",
			Name: txtTestName,
			Data: fmt.Sprintf("This is a test entry created by libdns %s", time.Now()),
			TTL:  time.Duration(30) * time.Second,
		}})
		if err != nil {
			fmt.Printf("ERROR: %s\n", err.Error())
		}
		fmt.Printf("Creating new entry for %s\n", aTestName)
		_, err = provider.AppendRecords(context.TODO(), zone, []libdns.Record{libdns.RR{
			Type: "A",
			Name: aTestName,
			Data: "127.0.0.1",
			TTL:  time.Duration(30) * time.Second,
		}})
		if err != nil {
			fmt.Printf("ERROR: %s\n", err.Error())
		}
	}
}
```
