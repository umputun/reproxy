<div align="center">
  <img class="logo" src="https://raw.githubusercontent.com/umputun/reproxy/master/site/src/logo-bg.svg" width="355px" height="142px" alt="Reproxy | Simple Reverse Proxy"/>
</div>

Reproxy is a simple edge HTTP(s) server / reverse proxy supporting various providers (docker, static, file, consul catalog). One or more providers supply information about the requested server, requested URL, destination URL, and health check URL. It is distributed as a single binary or as a docker container.

- Automatic SSL termination with <a href="https://letsencrypt.org/" rel="nofollow noopener noreferrer" target="_blank">Let's Encrypt</a>
- Support of user-provided SSL certificates
- Simple but flexible proxy rules
- Static, command-line proxy rules provider
- Dynamic, file-based proxy rules provider
- Docker provider with an automatic discovery
- Consul Catalog provider with discovery by service tags
- Support of multiple (virtual) hosts
- Optional traffic compression
- User-defined limits and timeouts
- Single binary distribution
- Docker container distribution
- Built-in static assets server
- Management server with routes info and prometheus metrics

---

[![build](https://github.com/umputun/reproxy/actions/workflows/ci.yml/badge.svg)](https://github.com/umputun/reproxy/actions/workflows/ci.yml)&nbsp;[![Coverage Status](https://coveralls.io/repos/github/umputun/reproxy/badge.svg?branch=master)](https://coveralls.io/github/umputun/reproxy?branch=master)&nbsp;[![Go Report Card](https://goreportcard.com/badge/github.com/umputun/reproxy)](https://goreportcard.com/report/github.com/umputun/reproxy)&nbsp;[![Docker Automated build](https://img.shields.io/docker/automated/jrottenberg/ffmpeg.svg)](https://hub.docker.com/repository/docker/umputun/reproxy)

Server (host) can be set as FQDN, i.e. `s.example.com` or `*` (catch all). Requested url can be regex, for example `^/api/(.*)` and destination url may have regex matched groups in, i.e. `http://d.example.com:8080/$1`. For the example above `http://s.example.com/api/something?foo=bar` will be proxied to `http://d.example.com:8080/something?foo=bar`.

For convenience, requests with the trailing `/` and without regex groups expanded to `/(.*)`, and destinations in those cases expanded to `/$1`. I.e. `/api/` -> `http://127.0.0.1/service` will be translated to `^/api/(.*)` -> `http://127.0.0.1/service/$1`

Both HTTP and HTTPS supported. For HTTPS, static certificate can be used as well as automated ACME (Let's Encrypt) certificates. Optional assets server can be used to serve static files. Starting reproxy requires at least one provider defined. The rest of parameters are strictly optional and have sane default.

Examples:
 - with a static provider: `reproxy --static.enabled --static.rule="example.com/api/(.*),https://api.example.com/$1"`
 - with an automatic docker discovery: `reproxy --docker.enabled --docker.auto`

## Install

- for a binary distribution pick the proper file in the [release section](https://github.com/umputun/reproxy/releases)
- docker container available on [Docker Hub](https://hub.docker.com/r/umputun/reproxy) as well as on [Github Container Registry](https://github.com/users/umputun/packages/container/reproxy/versions). I.e. `docker pull umputun/reproxy` or `docker pull ghcr.io/umputun/reproxy`.

Latest stable version has `:vX.Y.Z` tag (with `:latest` alias) and the current master has `:master` tag.

## Providers

Proxy rules supplied by various providers. Currently included `file`, `docker` and `static`. Each provider may define multiple routing rules for both proxied request and static (assets). User can sets multiple providers at the same time.

_See examples of various providers in [examples](https://github.com/umputun/reproxy/tree/master/examples)_

### Static

This is the simplest provider defining all mapping rules directly in the command line (or environment). Multiple rules supported. Each rule is 3 or 4 comma-separated elements `server,sourceurl,destination,[ping-url]`. For example:

- `*,^/api/(.*),https://api.example.com/$1,` - proxy all request to any host/server with `/api` prefix to `https://api.example.com`
- `example.com,/foo/bar,https://api.example.com/zzz,https://api.example.com/ping` - proxy all requests to `example.com` and with `/foo/bar` url to `https://api.example.com/zzz`. Uses `https://api.example.com/ping` for the health check

The last (4th) element defines an optional ping url used for health reporting. I.e.`*,^/api/(.*),https://api.example.com/$1,https://api.example.com/ping`. See [Health check](#ping-and-health-checks) section for more details.

### File

`reproxy --file.enabled --file.name=config.yml`

Example of `config.yml`:

```yaml
default: # the same as * (catch-all) server
  - { route: "^/api/svc1/(.*)", dest: "http://127.0.0.1:8080/blah1/$1" }
  - {
      route: "/api/svc3/xyz",
      dest: "http://127.0.0.3:8080/blah3/xyz",
      "ping": "http://127.0.0.3:8080/ping",
    }
srv.example.com:
  - { route: "^/api/svc2/(.*)", dest: "http://127.0.0.2:8080/blah2/$1/abc" }
```

This is a dynamic provider and file change will be applied automatically.

### Docker

Docker provider supports a fully automatic discovery (with `--docker.auto`) with no extra configuration and by default redirects all requests like `http://<container_name>:<container_port>/(.*)` to the internal IP of the given container and the exposed port. Only active (running) containers will be detected.

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

If no `reproxy.route` defined, the default is `http://<container_name>:<container_port>/(.*)`. In case if all proxied source have the same pattern, for example `/api/(.*)` user can define the common prefix (in this case `/api`) for all container-based routes. This can be done with `--docker.prefix` parameter.

This is a dynamic provider and any change in container's status will be applied automatically.

### Consul Catalog

Use: `reproxy --consul-catalog.enabled`

Consul Catalog provider periodically (every second by default) calls Consul API for obtaining services, which has any tag with `reproxy.` prefix.

You can redefine check interval with `--consul-catalog.interval` command line flag.

Also, you can redefine consul address with `--consul-catalog.address` command line option. Default address is `http://127.0.0.1:8500`.

For example:
```
reproxy --consul-catalog.enabled --consul-catalog.address=http://192.168.1.100:8500 --consul-catalog.interval=10s  
```

By default, provider sets values for every service:
- enabled `false`
- server `*`
- route `^/(.*)`
- dest `http://<SERVICE_ADDRESS_FROM_CONSUL>/$1`
- ping `http://<SERVICE_ADDRESS_FROM_CONSUL>/ping`

This default can be changed with tags:

- `reproxy.server` - server (hostname) to match. Also, can be a list of comma-separated servers.
- `reproxy.route` - source route (location)
- `reproxy.dest` - destination path. Note: this is not full url, but just the path which will be appended to service's ip:port
- `reproxy.port` - destination port for the discovered service
- `reproxy.ping` - ping path for the destination service.
- `reproxy.enabled` - enable (`yes`, `true`, `1`) or disable (`any different value`) service from reproxy destinations.

## SSL support

SSL mode (by default none) can be set to `auto` (ACME/LE certificates), `static` (existing certificate) or `none`. If `auto` turned on SSL certificate will be issued automatically for all discovered server names. User can override it by setting `--ssl.fqdn` value(s)

## Logging

By default no request log generated. This can be turned on by setting `--logger.enabled`. The log (auto-rotated) has [Apache Combined Log Format](http://httpd.apache.org/docs/2.2/logs.html#combined)

User can also turn stdout log on with `--logger.stdout`. It won't affect the file logging but will output some minimal info about processed requests, something like this:

```
2021/04/16 01:17:25.601 [INFO]  GET - /echo/image.png - xxx.xxx.xxx.xxx - 200 (155400) - 371.661251ms
2021/04/16 01:18:18.959 [INFO]  GET - /api/v1/params - xxx.xxx.xxx.xxx - 200 (74) - 1.217669m
```

## Assets Server

Users may turn the assets server on (off by default) to serve static files. As long as `--assets.location` set it treats every non-proxied request under `assets.root` as a request for static files. The assets server can be used without any proxy providers; in this mode, reproxy acts as a simple web server for the static content.

In addition to the common assets server, multiple custom static servers are supported. Each provider has a different way to define such a static rule, and some providers may not support it at all. For example, multiple static servers make sense in static (command line provider), file provider, and even useful with docker providers.

1. static provider - if source element prefixed by `assets:` it will be treated as file-server. For example `*,assets:/web,/var/www,` will serve all `/web/*` request with a file server on top of `/var/www` directory.
2. file provider - setting optional field `assets: true`
3. docker provider - `reproxy.assets=web-root:location`, i.e. `reproxy.assets=/web:/var/www`.

Assets server supports caching control with the `--assets.cache=<duration>` parameter. `0s` duration (default) turns caching control off. A duration is a sequence of decimal numbers, each with optional fraction and a unit suffix, such as "300ms", "1.5h" or "2h45m". Valid time units are "ns", "us" (or "µs"), "ms", "s", "m", "h" and "d".

There are two ways to set cache duration:

1. A single value for all static assets. This is as simple as `--assets.cache=48h`.
2. Custom duration for different mime types. It should include two parts - the default value and the pairs of mime:duration. In command line this looks like multiple `--assets.cache` options, i.e. `--assets.cache=48h --assets.cache=text/html:24h --assets.cache=image/png:2h`. Environment values should be comma-separated, i.e.  `ASSETS_CACHE=48h,text/html:24h,image/png:2h`

## More options

- `--gzip` enables gzip compression for responses.
- `--max=N` allows to set the maximum size of request (default 64k)
- `--header` sets extra header(s) added to each proxied response. For example this is how it can be done with the docker compose:

```yaml
  environment:
      - HEADER=
          X-Frame-Options:SAMEORIGIN,
          X-XSS-Protection:1; mode=block;,
          Content-Security-Policy:default-src 'self'; style-src 'self' 'unsafe-inline';
```

- `--timeout.*` various timeouts for both server and proxy transport. See `timeout` section in [All Application Options](#all-application-options)

## Default ports

In order to eliminate the need to pass custom params/environment, the default `--listen` is dynamic and trying to be reasonable and helpful for the typical cases:

- If anything set by users to `--listen` all the logic below ignored and host:port passed in and used directly.
- If nothing set by users to `--listen` and reproxy runs outside of the docker container, the default is `127.0.0.1:80` for http mode (`ssl.type=none`) and `127.0.0.1:443` for ssl mode (`ssl.type=auto` or `ssl.type=static`).
-  If nothing set by users to `--listen` and reproxy runs inside the docker, the default is `0.0.0.0:8080` for http mode, and `0.0.0.0:8443` for ssl mode.

Another default set in the similar dynamic way is `-ssl.http-port`. For run inside of the docker container it set to `8080` and without to `80`. 

## Ping and health checks

reproxy provides 2 endpoints for this purpose:

- `/ping` responds with `pong` and indicates what reproxy up and running
- `/health` returns `200 OK` status if all destination servers responded to their ping request with `200` or `417 Expectation Failed` if any of servers responded with non-200 code. It also returns json body with details about passed/failed services.

## Management API

Optional, can be turned on with `--mgmt.enabled`. Exposes 2 endpoints on `mgmt.listen` (address:port):

- `GET /routes` - list of all discovered routes
- `GET /metrics` - returns prometheus metrics (`http_requests_total`, `response_status` and `http_response_time_seconds`)

_see also [examples/metrics](https://github.com/umputun/reproxy/examples/metrics)_

## Errors reporting

Reproxy returns 502 (Bad Gateway) error in case if request doesn't match to any provided routes and assets. In case if some unexpected, internal error happened it returns 500. By default reproxy renders the simplest text version of the error - "Server error". Setting `--error.enabled` turns on the default html error message and with `--error.template` user may set any custom html template file for the error rendering. The template has two vars: `{{.ErrCode}}` and `{{.ErrMessage}}`. For example this template `oh my! {{.ErrCode}} - {{.ErrMessage}}` will be rendered to `oh my! 502 - Bad Gateway`


## Options

Each option can be provided in two forms: command line or environment key:value. Some command line options have a short form, like `-l localhost:8080` and all of them have the long form, i.e `--listen=localhost:8080`. The environment key (name) listed for each option as a suffix, i.e. `[$LISTEN]`.

Some options are repeatable, in this case you may pass it multiple times with command line, or comma-separated in env. For example `--ssl.fqdn` is such an option and can be passed as `--ssl.fqdn=a1.example.com --ssl.fqdn=a2.example.com` or as env `SSL_ACME_FQDN=a1.example.com,a2.example.com`

This is the list of all options supporting multiple elements: 

- `ssl.fqdn` (`SSL_ACME_FQDN`)
- `assets.cache` (`ASSETS_CACHE`)
- `docker.exclude` (`DOCKER_EXCLUDE`)
- `static.rule` (`$STATIC_RULES`)

## All Application Options

```
Application Options:
  -l, --listen=                     listen on host:port (default: 0.0.0.0:8080/8443 under docker, 127.0.0.1:80/443 without) [$LISTEN]
  -m, --max=                        max request size (default: 64000) [$MAX_SIZE]
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
      --ssl.http-port=              http port for redirect to https and acme challenge test (default: 8080 under docker, 80 without) [$SSL_HTTP_PORT]
      --ssl.fqdn=                   FQDN(s) for ACME certificates [$SSL_ACME_FQDN]

assets:
  -a, --assets.location=            assets location [$ASSETS_LOCATION]
      --assets.root=                assets web root (default: /) [$ASSETS_ROOT]
      --assets.cache=               cache duration for assets [$ASSETS_CACHE]

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
      --docker.prefix=              prefix for docker source routes [$DOCKER_PREFIX]

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

mgmt:
      --mgmt.enabled                enable management API [$MGMT_ENABLED]
      --mgmt.listen=                listen on host:port (default: 0.0.0.0:8081) [$MGMT_LISTEN]

error:
      --error.enabled               enable html errors reporting [$ERROR_ENABLED]
      --error.template=             error message template file [$ERROR_TEMPLATE]

Help Options:
  -h, --help                        Show this help message
```

## Status

The project is under active development and may have breaking changes till `v1` released.
