---
worth: later
where: app/proxy/proxy.go:137
added: 2026-09-06
---
# recoverer turns ReverseProxy's abort sentinel into a 500 body on a committed response

`httputil.ReverseProxy` panics with `http.ErrAbortHandler` when the upstream body copy fails after the
status was sent (`net/http/httputil/reverseproxy.go`, `shouldPanicOnCopyError`). The sentinel exists so
`net/http` aborts the connection and the client sees a truncated response. `rest.Recoverer` from
go-pkgz/rest (`vendor/github.com/go-pkgz/rest/middleware.go:104`) only skips the stack log for it and still
calls `http.Error`, so the 500 text is appended to the already-committed 200 body and the connection stays
open. Both recoverers in the chain do this: the outer one in `Run` and the one inside `gzipHandler` (added
in PR #265), so under `--gzip` the text now ends up inside the gzip stream instead of after it.

Surfaced by the final revmux round on PR #265, rated minor. The fix belongs in go-pkgz/rest: re-panic
`http.ErrAbortHandler` in `Recoverer` and bump the dependency here. A local recover wrapper in reproxy would
work too but duplicates the library's middleware.
