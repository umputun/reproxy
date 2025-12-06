Linode for [`libdns`](https://github.com/libdns/libdns)
=======================

[![Go Reference](https://pkg.go.dev/badge/test.svg)](https://pkg.go.dev/github.com/libdns/linode)

This package implements the [libdns interfaces](https://github.com/libdns/libdns) for Linode, allowing you to manage DNS records.

Requires a Linode API token.

This package was created for use in [HugoKlepsch/caddy-dns_linode](https://github.com/HugoKlepsch/caddy-dns_linode)
or [caddy-dns/linode](https://github.com/caddy-dns/linode).
Both are Caddy plugins for managing DNS records on Linode. 
Caddy uses this package to complete DNS-01 challenges when using Linode.
It may have behaviour tailored to that use case.

# Getting a token

* Go to https://cloud.linode.com/profile/tokens (API Tokens tab)
* Click "Create a Personal Access Token"
* This library requires Read/Write in the "Domains" scope 
* Copy the token. It should be kept private.
* Load it into the `APIToken` member when creating a new `linode.Provider`

# Running Integration Tests

* Requires a Linode API token
* Should be able to run without owning a domain. The test suite tries to operate
on unused domains. It follow the pattern of `libdns-test-<datetime>-<four random hex digits>.example`.
* Set the `LINODE_DNS_PAT` environment variable to your Personal Access Token.
* Run with this command:

```bash
go test -v -tags=integration
````