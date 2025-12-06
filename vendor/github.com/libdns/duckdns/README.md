Duck DNS for `libdns`
=======================

[![godoc reference](https://img.shields.io/badge/godoc-reference-blue.svg)](https://pkg.go.dev/github.com/libdns/duckdns)

This package implements the [libdns interfaces](https://github.com/libdns/libdns) for [Duck DNS](https://www.duckdns.org/spec.jsp).

## Authenticating

This package uses **API Token authentication**. Refer to the [Duck DNS documentation](https://www.duckdns.org/spec.jsp) for more information.

Start by retrieving your API token from the [table at the top of the account page](https://www.duckdns.org/domains) to be able to make authenticated requests to the API.

NOTE: Duck DNS only supports `A`/`AAAA` and `TXT` records, so it cannot be used for Encrypted ClientHello (ECH), which uses `HTTPS` records.