# docker-proxy (dpx)

Simple edge HTTP(s) proxy for various providers (docker, static, file). One or more providers supply information 
about requested server, requested url and destination url. 

Server can be FQDN, i.e. `s.example.com` or `*` (catch all). Requested url can be regex, for example `^/api/(.*)` and destination url
may have regex matched groups, i.e. `http://d.example.com:8080/$1`. For the example above `http://s.example.com/api/something?foo=bar` will be proxied to `http://d.example.com:8080/something?foo=bar`.

Both HTTP and HTTPS supported for the server. For HTTPS static certificate can be used as well as automated 
ACME (Let's Encrypt) certificates. Optional assets server can be used to serve static files.

Starting dpx requires at least one provider defined. The rest of parameters are strictly optional and have sane default.

example with a static provider: `dbx --static.enabled --rule="example.com/api/(.*),https://api.example.com/$1"`

## Providers

### Static

This is the simplest provider defining all mapping rules directly in the command line (or environment). Multiple rules can be defined.
Each rule is 2 or 3 comma-separated elements `[server,]sourceurl,destination`. For example:

- `^/api/(.*),https://api.example.com/$1` - proxy all request to any host/server with `/api` prefix to `https://api.example.com`
- `example.com,/foo/bar,https://api.example.com/zzz` - proxy all requests to `example.com` and with `/foo/bar` url to `https://api.example.com/zzz` 

## File

`dbx --file.enabled --file.name=config.yml`

example of `config.yml`:

```yaml
- {server: "*", route: "^/api/svc1/(.*)", dest: "http://127.0.0.1:8080/blah1/$1"}
- {server: "srv.example.com", route: "^/api/svc2/(.*)", dest: "http://127.0.0.2:8080/blah2/$1/abc"}
- {server: "*", route: "/api/svc3/xyz", dest: "http://127.0.0.3:8080/blah3/xyz"}
```
## Docker

## Application Options

```
  -l, --listen=                     listen on host:port (default: 127.0.0.1:8080) [$LISTEN]
  -t, --timeout=                    proxy timeout (default: 5s) [$TIMEOUT]
      --max=                        max response size (default: 64000) [$MAX_SIZE]
  -g, --gzip                        enable gz compression [$GZIP]
  -x, --header=                     proxy headers [$HEADER]
      --dbg                         debug mode [$DEBUG]

ssl:
      --ssl.type=[none|static|auto] ssl (auto) support (default: none) [$SSL_TYPE]
      --ssl.cert=                   path to cert.pem file [$SSL_CERT]
      --ssl.key=                    path to key.pem file [$SSL_KEY]
      --ssl.acme-location=          dir where certificates will be stored by autocert manager (default: ./var/acme) [$SSL_ACME_LOCATION]
      --ssl.acme-email=             admin email for certificate notifications [$SSL_ACME_EMAIL]
      --ssl.http-port=              http port for redirect to https and acme challenge [$SSL_HTTP_PORT]

assets:
  -a, --assets.location=            assets location [$ASSETS_LOCATION]
      --assets.root=                assets web root (default: /) [$ASSETS_ROOT]

docker:
      --docker.enabled              enable docker provider [$DOCKER_ENABLED]
      --docker.host=                docker host (default: unix:///var/run/docker.sock) [$DOCKER_HOST]
      --docker.network=             docker network (default: default) [$DOCKER_NETWORK]
      --docker.exclude=             excluded containers [$DOCKER_EXCLUDE]

file:
      --file.enabled                enable file provider [$FILE_ENABLED]
      --file.name=                  file name (default: dpx.yml) [$FILE_NAME]
      --file.interval=              file check interval (default: 3s) [$FILE_INTERVAL]
      --file.delay=                 file event delay (default: 500ms) [$FILE_DELAY]

static:
      --static.enabled              enable static provider [$STATIC_ENABLED]
      --static.rule=                routing rules [$STATIC_RULES]

Help Options:
  -h, --help                        Show this help message
  
```