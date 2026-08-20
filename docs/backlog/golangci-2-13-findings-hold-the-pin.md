---
worth: later
where: .github/workflows/test.yml:52
added: 2026-08-19
---
# four golangci-lint v2.13.0 findings hold the linter pin at v2.12.2

`golangci-lint` was pinned to `v2.12.2` in `6044439` because `latest` moved to `v2.13.0` mid-session and
turned CI red on four findings in existing code. The pin is a version baseline, not a fix: until the four
are resolved the repo cannot move forward to a current linter.

The four:

- `app/proxy/ssl.go:78` - `strings.Split(r.Host, ":")[0]` should be `strings.Cut` (modernize, stringscut)
- `app/proxy/ssl.go:247` - `tls.Config.PreferServerCipherSuites` deprecated and ignored since Go 1.18 (SA1019)
- `app/main.go:270` - SA4023, the `e != nil` guard on `svc.Run(ctx)` is always true
- `app/proxy/proxy.go:243` - `httputil.ReverseProxy.Director` deprecated since Go 1.26, use `Rewrite` (SA1019)

The fix and the pin advance to `v2.13.0` must go in the same change. Fixing the code while leaving
`v2.12.2` in place turns the pin from a baseline into concealment, which is the whole reason it is
recorded here rather than left implicit.

Agreed commit split, from the chat that produced this item: the first three are mechanical and share a
commit; `Director` to `Rewrite` gets its own commit with proxy tests written first, so any change to
forwarding-header behaviour is deliberate rather than incidental; the pin advance is the final commit, so
it stands as proof that all four hold under the selected linter.

Deferred rather than done inline because it surfaced while shipping the v1.7.1 release pipeline fix
(PR #259), and a migration through the core proxy path was not something to rush into a release.
