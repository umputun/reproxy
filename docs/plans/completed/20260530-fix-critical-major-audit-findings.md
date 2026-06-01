# Fix Critical and Major Audit Findings

## Overview
- fixes the 5 highest-severity issues confirmed by the verified code audit: one critical remote-triggerable crash and four major correctness/security defects
- restores proxy stability under concurrent traffic, stops a file-descriptor leak, corrects consul route duplication, removes a plugin lock leak, and documents an IP-allowlist trust assumption
- all changes are surgical and confined to existing types; no new public API or flags

## Context (from discovery)
- files/components involved:
  - `app/discovery/discovery.go` — `Service.Match`, `findMatchingMappers`, `URLMapper.ping`, `Service` struct
  - `app/discovery/provider/consulcatalog/client.go` — `consulClient.filterServices`
  - `app/plugin/conductor.go` — `Conductor.Middleware`
  - `app/proxy/only_from.go` + `README.md` — only-from header trust (docs only)
- related patterns found: `Service` already uses `sync.RWMutex` (`s.lock`); cache is reset wholesale under the exclusive `s.lock.Lock()` in the update loop (discovery.go:157-163); plugins held in `[]Handler` guarded by `c.lock sync.RWMutex`
- dependencies identified: Go 1.26 (per recent commit); existing tests present for every target file

## Development Approach
- **testing approach**: TDD (tests first) — write a failing test reproducing each defect, confirm it fails, then fix
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - write unit tests for new/modified functions
  - cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run `go test -race ./...` after each change
- maintain backward compatibility (no flag/behavior changes except documentation)

## Code-Quality Rules (HARD — verify against every task before marking complete)

These rules supplement project CLAUDE.md and are NOT optional. They are the gate for marking any task complete. If a rule is violated, the task is not done — refactor, re-test, then mark complete.

**Signatures (hard limits):**
- No function or method has 4+ parameters. `ctx context.Context` does not count toward the budget. If you need 4+, use an option struct (e.g., `type fooOpts struct { ... }`).
- No function or method has 4+ return values. Split the function into two single-purpose ones, or return a struct.
- Multiple adjacent same-type parameters (`oldLine, newLine int`) are a swap hazard — review whether they belong on a struct.

**Methods vs standalone helpers (project rule, hard):**
- If a function is called only from methods of a single struct, it MUST be a method on that struct. Calling pattern decides, not field access.
- Standalone helpers are reserved for: (a) constructors and entry points (`Parse...`, `New...`, `Decorate...`), (b) utilities shared by multiple unrelated types or by both standalone functions AND methods, (c) tiny cross-cutting helpers.
- Before adding any standalone helper, mentally walk its callers. If every caller is a method of one type, make the helper a method on that type.

**Visibility (private by default, hard):**
- Lowercase identifiers by default. Only export when an out-of-package caller exists.
- Exception (per CLAUDE.md): methods called by other structs in the same package CAN be exported for inter-component API clarity. This is the only exception. It does not extend to types, functions, constants, or variables.
- Before exporting any new identifier, grep for cross-package callers. If none, lowercase it.

**Comments (default: none, hard):**
- Default to writing no comments. Add one only when the WHY is non-obvious (a hidden invariant, a workaround, behavior that would surprise a reader).
- Exported items get godoc comments starting with the name. Unexported items get lowercase non-godoc comments — or no comment at all.
- Never describe WHAT the code does when the code itself is self-evident. Never write multi-paragraph comments on routine helpers.

**Per-task gate (before marking ANY checkbox complete):**
1. Formatter runs clean (`~/.claude/format.sh` or `gofmt -s -w` + `goimports -w`).
2. `golangci-lint run --max-issues-per-linter=0 --max-same-issues=0` reports zero issues.
3. `go test ./... -race` passes.
4. Scan the new code for the four rule classes above. Specifically:
   - Grep new function signatures: `grep -nE '^func.*\(.*,.*,.*,.*\)' app/<path>/*.go` — any hit with 4+ comma-separated params (excluding `ctx`) is a violation. Same for the return-value side.
   - For every new standalone helper, `grep -rn 'helperName(' --include='*.go'` and confirm at least one caller is NOT a method of a single type. If all callers are methods of one type, convert.
   - For every new exported identifier, grep cross-package. If no out-of-package hit, lowercase it.
5. Only after 1–4 pass: mark the task complete.

If a previous task shipped a violation (spotted later by user, reviewer, or yourself): fix it in the next commit BEFORE starting the next task. Do not let violations accumulate.

## Testing Strategy
- **unit tests**: required for every task (see Development Approach above)
- **race tests**: Task 1 ships a `-race` test that fires concurrent `Match()` calls against wildcard/regex servers to reproduce the fatal concurrent-map-write; it must crash on unpatched code and pass under `-race` after the fix
- **e2e tests**: project has no UI e2e suite; not applicable
- run the full suite with `cd app && go test -race -timeout=60s -count 1 ./...`

## Progress Tracking
- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope

## Solution Overview
- **#1 crash**: add a dedicated `cacheLock sync.RWMutex` to `Service` that guards only `mappersCache`; convert the standalone `findMatchingMappers(s, srvName)` into a method `(s *Service) findMatchingMappers(srvName string)` (also resolves the audit's structural finding) and serialize the lazy cache read/write through `cacheLock`. The wholesale cache reset stays under the existing exclusive `s.lock.Lock()`, which already excludes all readers, so it needs no extra guarding. The hot `Match` read path keeps its `RLock` — concurrency is preserved.
- **#2 ping leak**: `defer resp.Body.Close()` immediately after the error check in `URLMapper.ping`.
- **#3 consul dup**: `break` out of the per-tag loop in `filterServices` after the first `reproxy.`-prefixed tag, so a multi-tagged service is listed once.
- **#4 plugin lock**: in `Conductor.Middleware`, snapshot the alive plugins into a local slice under `RLock`, release the lock, then perform the blocking RPC calls outside it — removing both the cross-RPC lock hold and the missing-`RUnlock` leak on the Call-error path.
- **#5 only-from**: documentation only — README and flag help state that `--remote-lookup-headers` trusts client-supplied `X-Real-IP`/`X-Forwarded-For` and must only be enabled behind a trusted fronting proxy. No code change.

## Technical Details
- **Service struct** (`discovery.go:24-27`): add field `cacheLock sync.RWMutex` alongside the existing `lock sync.RWMutex`. `mappersCache map[string][]URLMapper` stays typed.
- **method signature**: `func (s *Service) findMatchingMappers(srvName string) []URLMapper` (1 param, 1 return — within limits). Call site in `Match` becomes `s.findMatchingMappers(srvName)`.
- **cache access inside the method**:
  - read: `s.cacheLock.RLock(); v, ok := s.mappersCache[srvName]; s.cacheLock.RUnlock()` then return on hit
  - write: `s.cacheLock.Lock(); s.mappersCache[srvName] = mapper; s.cacheLock.Unlock()` before returning the wildcard/regex match
- **lock ordering**: `Match` holds `s.lock.RLock` then briefly takes `s.cacheLock`; the update loop holds `s.lock.Lock` (exclusive, no reader present) and touches the cache via plain assignment. No path takes `cacheLock` before `s.lock`, so no deadlock.
- **ping**: insert `defer resp.Body.Close()` after the `if err != nil` block, before the status-code check.
- **filterServices**: add `break` after `result = append(result, serviceName)`.
- **Conductor.Middleware**: replace the in-loop locked iteration with `c.lock.RLock(); alive := append([]Handler(nil), c.plugins...); c.lock.RUnlock()`, filter `p.Alive` while iterating the local copy, then call RPC. Remove the manual `c.lock.RUnlock()` at the 4xx branch (no longer holding the lock there).

## What Goes Where
- **Implementation Steps** (`[ ]`): all five fixes, their tests, and the doc update — fully achievable in this repo
- **Post-Completion** (no checkboxes): manual smoke test of a live proxy with wildcard servers and a plugin, if desired

## Implementation Steps

### Task 1: Fix concurrent map write on the Match read path (#1, critical)

**Files:**
- Modify: `app/discovery/discovery.go`
- Modify: `app/discovery/discovery_test.go`

- [x] add a `-race` test in `discovery_test.go` that configures a `Service` with wildcard (`*.example.com`) and/or regex server mappers, then launches many concurrent `Match()` goroutines for **distinct uncached** hostnames (each must reach the lazy cache write at the wildcard/regex branch); rely on `-race` to flag the unsynchronized map access deterministically — do NOT depend on the timing-dependent `fatal error: concurrent map writes` panic, which may not fire every run
- [x] add `cacheLock sync.RWMutex` field to the `Service` struct
- [x] convert `findMatchingMappers(s *Service, srvName string)` to method `(s *Service) findMatchingMappers(srvName string)` and update the call site in `Match`
- [x] guard the `mappersCache` read and the two cache writes inside the method with `s.cacheLock`
- [x] run the new race test under `-race` — must pass; run `go test -race ./app/discovery/...` — must pass before next task

### Task 2: Close the health-ping response body (#2, major)

**Files:**
- Modify: `app/discovery/discovery.go`
- Modify: `app/discovery/discovery_test.go`

- [x] add/extend a test in `discovery_test.go` for `URLMapper.ping` covering success, non-200, and transport-error cases against an `httptest.Server` (regression coverage for `ping` behavior)
- [x] add `defer resp.Body.Close()` immediately after the error check in `ping`
- [x] note: the body-close itself is not directly assertable in a unit test — it is verified by the `bodyclose` linter / code review, not a behavioral assertion
- [x] run `golangci-lint run` and `go test -race ./app/discovery/...` — both must pass before next task

### Task 3: Deduplicate consul services with multiple reproxy tags (#3, major)

**Files:**
- Modify: `app/discovery/provider/consulcatalog/client.go`
- Modify: `app/discovery/provider/consulcatalog/client_test.go`

- [x] add a table case to `client_test.go` for `filterServices` where a service carries several `reproxy.*` tags and assert it appears exactly once (and that ordering stays sorted)
- [x] confirm the case fails on current code
- [x] add `break` after the append in `(cl *consulClient) filterServices` so only the first matching tag counts
- [x] run `go test -race ./app/discovery/provider/consulcatalog/...` — must pass before next task

### Task 4: Release plugin lock before blocking RPC and fix the leak (#4, major)

**Files:**
- Modify: `app/plugin/conductor.go`
- Modify: `app/plugin/conductor_test.go`

- [x] add a test in `conductor_test.go` where a registered plugin's `Call` returns an error, the middleware is invoked, and a subsequent `Register`/`Unregister` (or second request) completes within a timeout — this deadlocks/leaks on current code via the missing `RUnlock`
- [x] confirm the test fails (hangs/times out) before the fix
- [x] in `Middleware`, snapshot alive plugins under `RLock` into a local slice, `RUnlock`, then perform the RPC calls outside the lock
- [x] remove the now-stale manual `c.lock.RUnlock()` in the 4xx branch
- [x] run `go test -race ./app/plugin/...` — must pass before next task

### Task 5: Document only-from header trust assumption (#5, major, docs-only)

**Files:**
- Modify: `README.md`
- Modify: `app/main.go`

- [x] extend the `--remote-lookup-headers` paragraph in `README.md` (around line 460) to state that `X-Real-IP`/`X-Forwarded-For` are client-supplied and spoofable, so the flag must only be enabled when reproxy sits behind a trusted proxy that overwrites these headers
- [x] add a short security note near the only-from / allowed-source documentation that the IP allowlist relies on this trust assumption when header lookups are enabled (consolidated into the same IP-based access control section)
- [x] tighten the `--remote-lookup-headers` flag `description` struct tag in `main.go` if it can carry the trust caveat concisely without becoming verbose (updated to "enable remote lookup headers, trust only behind a trusted proxy"; README flags list line synced)
- [x] no unit test (documentation only); run `go build ./...` to confirm the struct-tag edit compiles

### Task 6: Verify acceptance criteria
- [x] verify all five findings are addressed per the Overview — all confirmed present and correct: #1 cacheLock + `findMatchingMappers` method + guarded cache read/writes (discovery.go:28,235-281); #2 `defer resp.Body.Close()` in ping (discovery.go:569); #3 `break` after append in filterServices (client.go:91); #4 plugin snapshot under RLock then RPC outside lock, no manual RUnlock (conductor.go:109-132); #5 only-from header trust docs (README.md:460-462, main.go:49)
- [x] run the full suite: `cd app && go test -race -timeout=120s -count 1 ./...` — green (all 7 test packages ok)
- [x] run `golangci-lint run --max-issues-per-linter=0 --max-same-issues=0` from repo root — 0 issues
- [x] run `~/.claude/format.sh` (or gofmt + goimports) and confirm no diff — clean, only intentional main.go change
- [x] verify test coverage for changed files did not regress — discovery 96.0%, consulcatalog 91.3%, plugin 95.8%, app 84.4%
- ⚠️ logger race (Test_MainWithPlugin): root-caused and FIXED. Not a test-isolation issue fixable in the test file — `run()` launched `svc.Run(context.Background())` (discovery + provider watchers) and never awaited it, so the discovery `File.Events` goroutine outlived `main()` and raced the next test's `setupLog()`→`lgr.Setup()` on the global logger. Minimal production fix in `app/main.go`: pass the cancellable `ctx` to `svc.Run`/`ScheduleHealthCheck`, add a `discoveryDone` channel, and `defer <-discoveryDone` in `run()` so discovery stops before shutdown returns; suppress the expected `context.Canceled` to avoid a spurious shutdown WARN. This is graceful-shutdown context propagation, not a logging-behavior change. Verified: app package race-clean 5/5 consecutive runs (was ~1-in-2 failing), full repo suite green.

### Task 7: [Final] Update documentation and close out
- [x] no new pattern — no CLAUDE.md change needed
- [x] deferred — plan kept in place until review/finalize complete (per exec orchestrator convention)

## Post-Completion
*Items requiring manual intervention or external systems — no checkboxes, informational only*

**Manual verification** (optional):
- run a local proxy with wildcard servers (`*.example.com`) under concurrent load (e.g. `hey`/`wrk`) to confirm the crash no longer reproduces
- exercise a plugin that returns an error and confirm subsequent requests and plugin re-registration keep working

**Note on remaining audit findings:**
- this plan covers only the critical + major findings (#1–#5); the 11 minor and 21 info findings from the audit are out of scope here and can be planned separately

---

Smells pre-check: passed (no rule violations; two cosmetic receiver-name nits folded into Task 3)
