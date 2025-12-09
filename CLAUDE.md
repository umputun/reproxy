# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Reproxy is a simple edge HTTP(s) reverse proxy supporting multiple providers (docker, static, file, consul catalog). It handles automatic SSL termination with Let's Encrypt, load balancing, health checks, and optional plugin support via RPC.

## Build and Test Commands

```bash
# run all tests
cd app && go test ./...

# run tests with race detection
cd app && go test -race -timeout=60s -count 1 ./...

# run specific test
cd app && go test -run TestName ./...

# run tests with coverage
cd app && go test -cover ./...

# build binary (from repo root)
make build

# build docker image
make docker

# run linter (from repo root)
golangci-lint run
```

## Architecture

### Core Components

- **app/main.go** - CLI entry point with all configuration options (flags, environment variables). Wires together providers, proxy server, and management services.

- **app/discovery/** - Service discovery layer
  - `discovery.go` - `Service` aggregates multiple providers, merges URL mappers, handles health checks
  - `provider/` - Provider implementations (docker, file, static, consul-catalog)
  - `URLMapper` - Core routing rule struct containing server, source regex, destination, health ping URL, match type

- **app/proxy/** - HTTP/HTTPS proxy server
  - `proxy.go` - `Http` struct is the main proxy server, handles both http and https modes
  - `handlers.go` - Middleware handlers (throttling, auth, logging, headers)
  - `ssl.go` - SSL/TLS configuration and ACME (Let's Encrypt) autocert management
  - `health.go` - Health check endpoint handlers
  - `lb_selector.go` - Load balancer strategies (random, failover, roundrobin)
  - `only_from.go` - IP-based access control middleware

- **app/mgmt/** - Management API
  - `server.go` - Exposes `/routes` and `/metrics` endpoints
  - `metrics.go` - Prometheus metrics middleware

- **app/plugin/** - Plugin system
  - `conductor.go` - RPC server for plugin registration and middleware chain

- **lib/** - Plugin development library
  - Used by external plugins to implement custom middleware via RPC

### Request Flow

1. Request hits `proxy.Http.Run()` which sets up middleware chain
2. `matchHandler` middleware matches request to `URLMapper` via `discovery.Service.Match()`
3. Match result stored in request context
4. `proxyHandler` routes to either:
   - `httputil.ReverseProxy` for proxy matches (MTProxy)
   - File server for static matches (MTStatic)
   - Assets handler for default static files

### Provider Priority

Providers are processed in order: static → file → docker → consul-catalog. Earlier providers take precedence for conflicting rules.

### SSL Modes

- `SSLNone` - HTTP only
- `SSLStatic` - User-provided certificates
- `SSLAuto` - ACME/Let's Encrypt automatic certificates with DNS-01 or HTTP-01 challenges

## Testing Patterns

- **Port allocation**: Use `net.Listen("tcp", "127.0.0.1:0")` to get OS-allocated ports instead of random range - avoids port collisions when previous test's port is in TIME_WAIT state
- **httptest cleanup**: Always close `httptest.Server` with `defer ds.Close()` to prevent goroutine leaks between tests ("Fail in goroutine after TestX has completed" errors)
- **Context timeouts**: Context timeouts must be longer than `waitForServer` timeouts to allow proper server startup and cleanup

## Key Patterns

- Configuration via `github.com/umputun/go-flags` with struct tags for CLI/env options
- Logging via `github.com/go-pkgz/lgr`
- Middleware chain built with `github.com/go-pkgz/rest.Wrap()`
- Mocks generated with moq, stored in same package with `_mock.go` suffix
- Tests use testify assertions

## Environment Detection

Reproxy auto-detects docker environment via `REPROXY_IN_DOCKER` env var and adjusts default ports:
- Docker: `0.0.0.0:8080` (http) / `0.0.0.0:8443` (https)
- Non-docker: `127.0.0.1:80` (http) / `127.0.0.1:443` (https)

## Adding DNS-01 Challenge Providers

DNS providers for ACME DNS-01 challenges use the libdns ecosystem (github.com/libdns). To add a new provider:

1. **Import** the libdns provider package in `app/main.go`
2. **Add choice** to `DNS.Type` field tag: `choice:"newprovider"`
3. **Add config struct** inside `DNS struct` with provider-specific fields (usually just `APIToken`)
4. **Add switch case** in `makeSSLConfig()` creating the provider instance
5. **Add test** in `main_test.go` following existing pattern (set opts, call makeSSLConfig, assert NotNil)
6. **Update README** DNS provider section

**Important considerations:**
- libdns providers themselves are tiny (~20-70KB), but some pull in large cloud SDKs (AWS ~5MB, Azure/Google similar)
- "Lightweight" providers (DigitalOcean, Hetzner, Linode) use HTTP APIs with minimal deps
- Check libdns version compatibility - some providers lag behind API changes (e.g., vultr v1.0.0 incompatible with libdns v1.1.x)
