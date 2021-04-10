# Example of a file provider

To run it do `make run` and try to hit it, for example
- `curl localhost:8080/api/svc1/aaaaa`
- `curl localhost:8080/api/svc1`
- `curl localhost:8080/api/svc2/something`
- `curl localhost:8080/api/svc3/something`
- `curl 127.0.0.1:8080/api/svc3/something`

for health check try - `curl localhost:8080/health`

In order to kill all services run `make kill`
