# Namecheap for [`libdns`](https://github.com/libdns/libdns)

[![Go Reference](https://pkg.go.dev/badge/test.svg)](https://pkg.go.dev/github.com/libdns/namecheap)

This package implements the [libdns interfaces](https://github.com/libdns/libdns) for namecheap. This is a community project. You should first test that it works for your use case.

## Usage

See [namecheap api docs](https://www.namecheap.com/support/api/intro/) for details on how to get setup with using the namecheap API.

Once you have an API Key and have whitelisted your client IP, you can begin using this library.

## Testing

Unit tests are run with go tooling and gofmt should be run prior to submitting patches.

```shell
go test -race ./internal/namecheap/...
```

```shell
go fmt ./...
```

There are simple integrations test under `./internal/testing` that can be used for testing with this library and serves as an exmpale for usage. **DO NOT** use this test for production as it will delete your existing records. You can pass in your credentials through command line flags:

```shell
go test -tags=integration ./internal/testing/... -api-key <your_api_key> -username <your_username> -domain example.com.
```

By default the sandbox URL is used but you can also pass the production endpint with the `-endpoint <url>` flag.
