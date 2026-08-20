---
worth: later
where: app/proxy/proxy.go:64
added: 2026-08-20
---
# DNS-01 resolvers are a test seam with no user-facing control

`dnsResolvers` is unexported and commented "used to mock DNS resolvers for testing", so it reaches
`certmagic.DNSManager.Resolvers` (`app/proxy/ssl.go:183`) only from tests. An operator whose propagation
check fails because of *how* it resolves, rather than how long it waits, has nothing to set.

Deliberately left out of the issue #258 fix, which exposed the propagation timeout only. Resolvers is not
another duration knob: `vendor/github.com/caddyserver/certmagic/solvers.go:442` computes
`checkAuthoritativeServers := len(m.Resolvers) == 0`, so supplying resolvers turns off authoritative-server
checking, and `solvers.go:382` shows it also steers zone discovery through `FindZoneByFQDN`. Exposing it
changes what counts as propagated and promotes a test seam to public API.

No reported failure behind it yet. Worth doing only if someone reports a propagation check failing against
a resolver that disagrees with the authoritative servers.
