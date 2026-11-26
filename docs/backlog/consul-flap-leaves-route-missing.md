---
worth: maybe
where: app/discovery/provider/consulcatalog/consulcatalog.go:ConsulCatalog.checkUpdates
added: 2026-11-26
---
# a consul service flapping between two polls can leave its route missing

The watcher records its snapshot when it queues a reload, and discovery reads consul again through
List. If a service deregisters right then and List returns an empty list without an error, the reload
installs no route for it. When the same record comes back before the next poll, every later poll matches
the recorded snapshot and never queues another reload, so the route stays missing until some other
catalog change. The failed-List retry added on fix-audit-findings doesn't cover it, since nothing
failed. Recording the snapshot from what List actually installed would fix it, but codex flagged that
regular polls would then keep restarting discovery's quiet period and postpone reloads; a fix needs a
design for that. Found by the final revmux round on fix-audit-findings.
