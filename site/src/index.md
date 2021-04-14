---
title: Reproxy
subtitle: Simple Reverse Proxy
description: Reproxy is a minimalistic system acting as an edge server / reverse proxy for your infrastructure. It provides the only essential functionality with no bells and whistles. Setup is very straightforward and not much to configure.
---

# {{ title }}

{{ description }}

- Automatic SSL termination with <a href="https://letsencrypt.org/" rel="nofollow noopener noreferrer" target="_blank">Let's Encrypt</a>
- Support of user-provided SSL certificates
- Simple but flexible proxy rules
- Static, command line proxy rules provider
- Dynamic, file-based proxy rules provider
- Docker provider with an automatic discovery
- Optional traffic compression
- User-defined limits and timeouts
- Single binary distribution
- Docker container distribution
- Built-in static assets server
- [Open source](https://github.com/umputun/reproxy)

## Install

- for a binary distribution pick the proper file in the [release section](https://github.com/umputun/reproxy/releases)
- docker container available on [Docker Hub](https://hub.docker.com/r/umputun/reproxy) as well as on [Github Container Registry](ghcr.io/umputun/reproxy).

Latest stable version has `:vX.Y.Z` tag (with `:latest` alias) and the current master has `:master` tag.

## Providers

User can sets multiple providers at the same time.

_See examples of various providers in [examples](https://github.com/umputun/reproxy/tree/master/examples)_

### Static

This is the simplest provider defining all mapping rules directly in the command line (or environment). Multiple rules supported.
Each rule is 3 or 4 comma-separated elements `server,sourceurl,destination,[ping-url]`. For example:

- `*,^/api/(.*),https://api.example.com/$1,` - proxy all request to any host/server with `/api` prefix to `https://api.example.com`
- `example.com,/foo/bar,https://api.example.com/zzz,https://api.example.com/ping` - proxy all requests to `example.com` and with `/foo/bar` url to `https://api.example.com/zzz`. Uses `https://api.example.com/ping` for the health check

The last (4th) element defines an optional ping url used for health reporting. I.e.`*,^/api/(.*),https://api.example.com/$1,https://api.example.com/ping`. See [Health check](https://github.com/umputun/reproxy#ping-and-health-checks) section for more details.

### File

`reproxy --file.enabled --file.name=config.yml`

Example of `config.yml`:

```yaml
default: # the same as * (catch-all) server
  - { route: '^/api/svc1/(.*)', dest: 'http://127.0.0.1:8080/blah1/$1' }
  - {
      route: '/api/svc3/xyz',
      dest: 'http://127.0.0.3:8080/blah3/xyz',
      'ping': 'http://127.0.0.3:8080/ping',
    }
srv.example.com:
  - { route: '^/api/svc2/(.*)', dest: 'http://127.0.0.2:8080/blah2/$1/abc' }
```

This is a dynamic provider and file change will be applied automatically.

### Docker

Docker provider supports a fully automatic discovery (with `--docker.auto`) with no extra configuration and by default redirects all requests like `https://server/<container_name>/(.*)` to the internal IP of the given container and the exposed port. Only active (running) containers will be detected.

This default can be changed with labels:

- `reproxy.server` - server (hostname) to match. Also can be a list of comma-separated servers.
- `reproxy.route` - source route (location)
- `reproxy.dest` - destination path. Note: this is not full url, but just the path which will be appended to container's ip:port
- `reproxy.port` - destination port for the discovered container
- `reproxy.ping` - ping path for the destination container.
- `reproxy.enabled` - enable (`yes`, `true`, `1`) or disable (`no`, `false`, `0`) container from reproxy destinations.

Pls note: without `--docker.auto` the destination container has to have at least one of `reproxy.*` labels to be considered as a potential destination.

With `--docker.auto`, all containers with exposed port will be considered as routing destinations. There are 3 ways to restrict it:

- Exclude some containers explicitly with `--docker.exclude`, i.e. `--docker.exclude=c1 --docker.exclude=c2 ...`
- Allow only a particular docker network with `--docker.network`
- Set the label `reproxy.enabled=false` or `reproxy.enabled=no` or `reproxy.enabled=0`

This is a dynamic provider and any change in container's status will be applied automatically.

## SSL support

SSL mode (by default none) can be set to `auto` (ACME/LE certificates), `static` (existing certificate) or `none`. If `auto` turned on SSL certificate will be issued automatically for all discovered server names. User can override it by setting `--ssl.fqdn` value(s)

## Logging

By default no request log generated. This can be turned on by setting `--logger.enabled`. The log (auto-rotated) has [Apache Combined Log Format](http://httpd.apache.org/docs/2.2/logs.html#combined)

## Assets Server

User may turn assets server on (off by default) to serve static files. As long as `--assets.location` set it will treat every non-proxied request under `assets.root` as a request for static files.

Assets server can be used without any proxy providers. In this mode reproxy acts as a simple web server for a static context.

## More options

- `--gzip` enables gizp compression for responses.
- `--max=N` allows to set the maximum size of request (default 64k)
- `--header` sets extra header(s) added to each proxied request
- `--timeout.*` various timeouts for both server and proxy transport. See `timeout` section in [All Application Options](https://github.com/umputun/reproxy#all-application-options)

## Ping and health checks

reproxy provides 2 endpoints for this purpose:

- `/ping` responds with `pong` and indicates what reproxy up and running
- `/health` returns `200 OK` status if all destination servers responded to their ping request with `200` or `417 Expectation Failed` if any of servers responded with non-200 code. It also returns json body with details about passed/failed services.

## All Application Options

```
  -l, --listen=                     listen on host:port (default: 127.0.0.1:8080) [$LISTEN]
  -m, --max=                        max response size (default: 64000) [$MAX_SIZE]
  -g, --gzip                        enable gz compression [$GZIP]
  -x, --header=                     proxy headers [$HEADER]
      --signature                   enable reproxy signature headers [$SIGNATURE]
      --dbg                         debug mode [$DEBUG]

ssl:
      --ssl.type=[none|static|auto] ssl (auto) support (default: none) [$SSL_TYPE]
      --ssl.cert=                   path to cert.pem file [$SSL_CERT]
      --ssl.key=                    path to key.pem file [$SSL_KEY]
      --ssl.acme-location=          dir where certificates will be stored by autocert manager (default: ./var/acme) [$SSL_ACME_LOCATION]
      --ssl.acme-email=             admin email for certificate notifications [$SSL_ACME_EMAIL]
      --ssl.http-port=              http port for redirect to https and acme challenge test (default: 80) [$SSL_HTTP_PORT]
      --ssl.fqdn=                   FQDN(s) for ACME certificates [$SSL_ACME_FQDN]

assets:
  -a, --assets.location=            assets location [$ASSETS_LOCATION]
      --assets.root=                assets web root (default: /) [$ASSETS_ROOT]

logger:
      --logger.stdout               enable stdout logging [$LOGGER_STDOUT]
      --logger.enabled              enable access and error rotated logs [$LOGGER_ENABLED]
      --logger.file=                location of access log (default: access.log) [$LOGGER_FILE]
      --logger.max-size=            maximum size in megabytes before it gets rotated (default: 100) [$LOGGER_MAX_SIZE]
      --logger.max-backups=         maximum number of old log files to retain (default: 10) [$LOGGER_MAX_BACKUPS]

docker:
      --docker.enabled              enable docker provider [$DOCKER_ENABLED]
      --docker.host=                docker host (default: unix:///var/run/docker.sock) [$DOCKER_HOST]
      --docker.network=             docker network [$DOCKER_NETWORK]
      --docker.exclude=             excluded containers [$DOCKER_EXCLUDE]
      --docker.auto                 enable automatic routing (without labels) [$DOCKER_AUTO]

file:
      --file.enabled                enable file provider [$FILE_ENABLED]
      --file.name=                  file name (default: reproxy.yml) [$FILE_NAME]
      --file.interval=              file check interval (default: 3s) [$FILE_INTERVAL]
      --file.delay=                 file event delay (default: 500ms) [$FILE_DELAY]

static:
      --static.enabled              enable static provider [$STATIC_ENABLED]
      --static.rule=                routing rules [$STATIC_RULES]

timeout:
      --timeout.read-header=        read header server timeout (default: 5s) [$TIMEOUT_READ_HEADER]
      --timeout.write=              write server timeout (default: 30s) [$TIMEOUT_WRITE]
      --timeout.idle=               idle server timeout (default: 30s) [$TIMEOUT_IDLE]
      --timeout.dial=               dial transport timeout (default: 30s) [$TIMEOUT_DIAL]
      --timeout.keep-alive=         keep-alive transport timeout (default: 30s) [$TIMEOUT_KEEP_ALIVE]
      --timeout.resp-header=        response header transport timeout (default: 5s) [$TIMEOUT_RESP_HEADER]
      --timeout.idle-conn=          idle connection transport timeout (default: 90s) [$TIMEOUT_IDLE_CONN]
      --timeout.tls=                TLS hanshake transport timeout (default: 10s) [$TIMEOUT_TLS]
      --timeout.continue=           expect continue transport timeout (default: 1s) [$TIMEOUT_CONTINUE]

Help Options:
  -h, --help                        Show this help message

```

## Status

The project is under active development and may have breaking changes till `v1` released.
