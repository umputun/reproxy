---
worth: maybe
added: 2026-11-26
---
# static route with `*` server and an anchored `^/(.*)` source never matches

`--static.rule='*,^/(.*),http://127.0.0.1:19001/$1'` loads (the log lists `static: * ^/(.*) -> ...`), yet
every request gets 502 with `[WARN] no match for  /x`. The same rule written `*,/(.*),...` matches and
proxies. Reproduced on master (5528c8d) and on fix-audit-findings during the security audit probes, so
the audit fixes did not cause it. The cause is not traced: the leading `^` may be rewritten or doubled
somewhere between the static provider and `Service.Match`. Tests use unanchored `/svc1/(.*)` forms,
which is why nothing caught it. Worth a failing test against `Service.Match` before deciding whether it is
a bug or an undocumented rule.
