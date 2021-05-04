# Example of a docker provider with an automatic SSL (Let's Encrypt)

This example should run on the machine with resolvable FQDN. All files use example.com, make sure you **replace it with your domain**.

run this example with `docker-compose up` and try to hit containers:

- `curl https://example.com/api/svc1/123`
- `curl http://example.com/api/svc2/345`
- `curl http://example.com/whoami/test`
