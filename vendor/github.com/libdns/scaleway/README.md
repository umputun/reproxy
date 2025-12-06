Scaleway for [`libdns`](https://github.com/libdns/libdns)
=======================

[![Go Reference](https://pkg.go.dev/badge/test.svg)](https://pkg.go.dev/github.com/libdns/scaleway)

This package implements the [libdns interfaces](https://github.com/libdns/libdns) for [Scaleway](https://scaleway.com), allowing you to manage
DNS records.

## Authenticating

To authenticate you need to supply following Scaleway credentials:

- The Scaleway secret key (aka API key)
- The Scaleway organization ID

## Example

Here's a minimal example of how to get all your DNS records using this `libdns` provider (see `_example/main.go`)

```go
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/libdns/libdns"
	"github.com/libdns/scaleway"
)

func main() {
	secretKey := os.Getenv("SECRET_KEY")
	if secretKey == "" {
		fmt.Printf("SECRET_KEY not set\n")
		return
	}
	organisationID := os.Getenv("ORGANISATION_ID")
	if organisationID == "" {
		fmt.Printf("ORGANISATION_ID not set\n")
		return
	}
	zone := os.Getenv("ZONE")
	if zone == "" {
		fmt.Printf("ZONE not set\n")
		return
	}
	provider := scaleway.Provider{
		SecretKey:      secretKey,
		OrganizationID: organisationID,
	}

	records, err := provider.GetRecords(context.TODO(), zone)
	if err != nil {
		fmt.Printf("ERROR: %s\n", err.Error())
	}

	testName := "libdns-test"
	testId := ""
	for _, record := range records {
		fmt.Printf("%s (.%s): %s, %s\n", record.Name, zone, record.Value, record.Type)
		if record.Name == testName {
			testId = record.ID
		}
	}

	if testId != "" {
		/*
			fmt.Printf("Delete entry for %s (id:%s)\n", testName, testId)
			_, err = provider.DeleteRecords(context.TODO(), zone, []libdns.Record{libdns.Record{
				ID: testId,
			}})
			if err != nil {
				fmt.Printf("ERROR: %s\n", err.Error())
			}
		*/
		// Set only works if we have a record.ID
		fmt.Printf("Replacing entry for %s\n", testName)
		_, err = provider.SetRecords(context.TODO(), zone, []libdns.Record{libdns.Record{
			Type:  "TXT",
			Name:  testName,
			Value: fmt.Sprintf("Replacement test entry created by libdns %s", time.Now()),
			TTL:   time.Duration(30) * time.Second,
			ID:    testId,
		}})
		if err != nil {
			fmt.Printf("ERROR: %s\n", err.Error())
		}
	} else {
		fmt.Printf("Creating new entry for %s\n", testName)
		_, err = provider.AppendRecords(context.TODO(), zone, []libdns.Record{libdns.Record{
			Type:  "TXT",
			Name:  testName,
			Value: fmt.Sprintf("This is a test entry created by libdns %s", time.Now()),
			TTL:   time.Duration(30) * time.Second,
		}})
		if err != nil {
			fmt.Printf("ERROR: %s\n", err.Error())
		}
	}
}
```
