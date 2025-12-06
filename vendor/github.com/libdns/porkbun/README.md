Porkbun for [`libdns`](https://github.com/libdns/libdns)
=======================

[![Go Reference](https://pkg.go.dev/badge/test.svg)](https://pkg.go.dev/github.com/libdns/porkbun)

This package implements the [libdns interfaces](https://github.com/libdns/libdns) for Porkbun, allowing you to manage DNS records.

## Usage

[Porkbun API documentation](https://kb.porkbun.com/article/190-getting-started-with-the-porkbun-dns-api) details the process of getting an API key & enable API access for the domain.

An example of usage can be seen in `_test/test.go`.
To run clone the `.env_template` to a file named `.env` and populate with the API key and secret API key.