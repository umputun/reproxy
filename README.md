# docker-proxy

Simple edge HTTP(s) proxy for docker containers

```
Application Options:
  -l, --listen=          listen on host:port (default: 127.0.0.1:8080) [$LISTEN]
  -t, --timeout=         proxy timeout (default: 5s) [$TIMEOUT]
      --max=             max response size (default: 64000) [$MAX_SIZE]
  -g, --gzip             enable gz compression [$GZIP]
  -x, --header=          proxy headers [$HEADER]
      --dbg              debug mode [$DEBUG]

assets:
  -a, --assets.location= assets location [$ASSETS_LOCATION]
      --assets.root=     assets web root (default: /) [$ASSETS_ROOT]

docker:
      --docker.enabled   enable docker provider [$DOCKER_ENABLED]
      --docker.host=     docker host (default: unix:///var/run/docker.sock) [$DOCKER_HOST]
      --docker.exclude=  excluded containers [$DOCKER_EXCLUDE]

file:
      --file.enabled     enable file provider [$FILE_ENABLED]
      --file.name=       file name (default: dpx.conf) [$FILE_NAME]
      --file.interval=   file check interval (default: 3s) [$FILE_INTERVAL]
      --file.delay=      file event delay (default: 500ms) [$FILE_DELAY]

static:
      --static.enabled   enable static provider [$STATIC_ENABLED]
      --static.rule=     routing rules [$STATIC_RULES]

Help Options:
  -h, --help             Show this help message

```