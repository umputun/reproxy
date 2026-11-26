---
worth: maybe
where: app/discovery/discovery.go:Service.Run
added: 2026-11-26
---
# literal FQDN server keys match as unanchored regexes

Server keys other than `*` and `*.domain` are compiled as regexes without anchors, so a literal key
`billing.example.com` also matches `preview.billing.example.com`, and `billingXexample.com` since `.` is
a wildcard. With `*.example.com` configured too, the longest-key precedence from fix-audit-findings sends
`preview.billing.example.com` to the billing route every time, skipping any auth or allowlist on the
wildcard route; before it the winner was random. Fixing it means telling literal hosts from regexes and
matching literals exactly, which changes routing for configs relying on substring matches and needs a
migration decision. Found by the revmux round on fix-audit-findings.
