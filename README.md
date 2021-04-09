# reproxy [![build](https://github.com/umputun/reproxy/actions/workflows/ci.yml/badge.svg)](https://github.com/umputun/reproxy/actions/workflows/ci.yml)

Reproxy is simple edge HTTP(s) sever / reverse proxy supporting various providers (docker, static, file).
One or more providers supply information about requested server, requested url, destination url and health check url.
Distributed as a single binary or as a docker container.

Server can be set as FQDN, i.e. `s.example.com` or `*` (catch all). Requested url can be regex, for example `^/api/(.*)` and destination url
may have regex matched groups in, i.e. `http://d.example.com:8080/$1`. For the example above `http://s.example.com/api/something?foo=bar` will be proxied to `http://d.example.com:8080/something?foo=bar`.

For convenience, requests with the trailing `/` and without regex groups expanded to `/(.*)`, and destinations in those cases 
expanded to `/$1`. I.e. `/api/` -> `http://127.0.0.1/service` will be translated to `^/api/(.*)` ->  `http://127.0.0.1/service/$1`

Both HTTP and HTTPS supported. For HTTPS, static certificate can be used as well as automated ACME (Let's Encrypt) certificates. 
Optional assets server can be used to serve static files.

Starting reproxy requires at least one provider defined. The rest of parameters are strictly optional and have sane default.

example with a static provider: `reproxy --static.enabled --rule="example.com/api/(.*),https://api.example.com/$1"`

## Install

- for a binary distribution pick the proper file in the release section
- docker container available via docker hub (umputun/reproxy) as well as via github container registry (ghcr.io/umputun/reproxy). Latest stable version has `:vX.Y.Z` tag (with `:latest` alias) and the current master has `:master` tag.

## Providers

User can sets multiple providers at the same time.

### Static

This is the simplest provider defining all mapping rules directly in the command line (or environment). Multiple rules supported.
Each rule is 3 or 4 comma-separated elements `server,sourceurl,destination,[ping-url]`. For example:

- `*,^/api/(.*),https://api.example.com/$1` - proxy all request to any host/server with `/api` prefix to `https://api.example.com`
- `example.com,/foo/bar,https://api.example.com/zzz` - proxy all requests to `example.com` and with `/foo/bar` url to `https://api.example.com/zzz` 

The last (4th) element defines an optional ping url used for health reporting. I.e.`*,^/api/(.*),https://api.example.com/$1,https://api.example.com/ping`. See [Health check]() section for more details.

### File

`reproxy --file.enabled --file.name=config.yml`

example of `config.yml`:

```yaml
default: # the same as * (catch-all) server
  - {route: "^/api/svc1/(.*)", dest: "http://127.0.0.1:8080/blah1/$1"}
  - {route: "/api/svc3/xyz", dest: "http://127.0.0.3:8080/blah3/xyz", "ping": "http://127.0.0.3:8080/ping"}
srv.example.com:
  - {route: "^/api/svc2/(.*)", dest: "http://127.0.0.2:8080/blah2/$1/abc"}

```

This is a dynamic provider and file change will be applied automatically.

### Docker

Docker provider works with no extra configuration and by default redirects all requests like  `https://server/api/<container_name>/(.*)` to the internal IP of given container and the exposed port. Only active (running) containers will be detected.

This default can be changed with labels:

- reproxy.server - server (hostname) to match
- reproxy.route - source route (location)
- reproxy.dest - destination URL
- reproxy.ping - ping url for the destination container

This is a dynamic provider and any change in container's status will be applied automatically.

## SSL support

SSL mode (by default none) can be set to `auto` (ACME/LE certificates), `static` (existing certificate) or `none`. If `auto` turned on SSL certificate will be issued automatically for all discovered server names. User can override it by setting  `--ssl.fqdn` value(s)

## Logging 

By default no request log generated. This can be turned on by setting `--logger.enabled`. The log (auto-rotated) has [Apache Combined Log Format](http://httpd.apache.org/docs/2.2/logs.html#combined)

## Assets Server

User may turn assets server on (off by default) to serve static files. As long as `--assets.location` set it will treat every non-proxied request under `assets.root` as a request for static files. 

## More options

- `--gzip` enables gizp compression for responses.
- `--max=N` allows to set the maximum size of request (default 64k)
- `--header` sets extra header(s) added to each proxied request

## Ping and health checks

reproxy provides 2 endpoints for this purpose:

- `/ping` responds with `pong` and indicates what reproxy up and running
- `/health` returns `200 OK` status if all destination servers responded to their ping request with `200` or `417 Expectation Failed` if any of servers responded with non-200 code. It also returns json body with details about passed/failed services. 

## All Application Options

```
  -l, --listen=                     listen on host:port (default: 127.0.0.1:8080) [$LISTEN]
  -t, --timeout=                    proxy timeout (default: 5s) [$TIMEOUT]
  -m, --max=                        max response size (default: 64000) [$MAX_SIZE]
  -g, --gzip                        enable gz compression [$GZIP]
  -x, --header=                     proxy headers [$HEADER]
      --no-signature                disable reproxy signature headers [$NO_SIGNATURE]
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
      --logger.enabled              enable access and error rotated logs [$LOGGER_ENABLED]
      --logger.file=                location of access log (default: access.log) [$LOGGER_FILE]
      --logger.max-size=            maximum size in megabytes before it gets rotated (default: 100) [$LOGGER_MAX_SIZE]
      --logger.max-backups=         maximum number of old log files to retain (default: 10) [$LOGGER_MAX_BACKUPS]

docker:
      --docker.enabled              enable docker provider [$DOCKER_ENABLED]
      --docker.host=                docker host (default: unix:///var/run/docker.sock) [$DOCKER_HOST]
      --docker.network=             docker network (default: bridge) [$DOCKER_NETWORK]
      --docker.exclude=             excluded containers [$DOCKER_EXCLUDE]

file:
      --file.enabled                enable file provider [$FILE_ENABLED]
      --file.name=                  file name (default: reproxy.yml) [$FILE_NAME]
      --file.interval=              file check interval (default: 3s) [$FILE_INTERVAL]
      --file.delay=                 file event delay (default: 500ms) [$FILE_DELAY]

static:
      --static.enabled              enable static provider [$STATIC_ENABLED]
      --static.rule=                routing rules [$STATIC_RULES]

Help Options:
  -h, --help                        Show this help message
  
```

## Status

The project is under active development and may have breaking changes till `v1` released.
