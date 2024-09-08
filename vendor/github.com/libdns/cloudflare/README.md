Cloudflare for `libdns`
=======================

[![godoc reference](https://img.shields.io/badge/godoc-reference-blue.svg)](https://pkg.go.dev/github.com/libdns/cloudflare)

This package implements the [libdns interfaces](https://github.com/libdns/libdns) for [Cloudflare](https://www.cloudflare.com).

## Authenticating

This package supports API **token** authentication.

You will need to create a token with the following permissions:

- Zone / Zone / Read
- Zone / DNS / Edit

The first permission is needed to get the zone ID, and the second permission is obviously necessary to edit the DNS records. If you're only using the `GetRecords()` method, you can change the second permission to Read to guarantee no changes will be made.

To clarify, do NOT use API keys, which are globally-scoped:

![Don't use API keys](https://user-images.githubusercontent.com/1128849/81196485-556aca00-8f7c-11ea-9e13-c6a8a966f689.png)

DO use scoped API tokens:

![Don't use API keys](https://user-images.githubusercontent.com/1128849/81196503-5c91d800-8f7c-11ea-93cc-ad7d73420fab.png)
