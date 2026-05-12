# Per-Route Timeout and Throttle (issue #140)

## Overview

Add optional per-route `Timeout` (request deadline) and `Throttle` (req/sec per user) to `URLMapper`, with zero-value semantics meaning "inherit the existing global setting". Solves the two reporter use cases from issue #140:

- long-running upload/report endpoints that today trip the global `--timeout.write` (30s default) and return 502
- per-route brute-force rate limiting (e.g. login route) without raising the global ceiling for unrelated routes

Pattern follows existing per-route options (`AuthUsers`, `OnlyFromIPs`, `ForwardHealthChecks`, `KeepHost`): optional `URLMapper` field, fallback to global when zero. No new flags or env vars; existing `--timeout.*` and `--throttle.*` remain as the global fallback.

## Context (from discovery)

- **Files involved:**
  - `app/discovery/discovery.go` — `URLMapper` struct (line 32) gets two new fields
  - `app/discovery/provider/file.go` — YAML config, add `timeout` and `throttle` fields
  - `app/discovery/provider/static.go` — extend the positional CSV format (currently 5 fields including `forward-health-checks`) to 7 fields
  - `app/discovery/provider/docker.go` — add `reproxy.<n>.timeout` and `reproxy.<n>.throttle` labels, parsed via existing `labelN` helper
  - `app/discovery/provider/consulcatalog/consulcatalog.go` — same labels via `c.Labels[...]`
  - `app/proxy/proxy.go` — middleware chain (line 141) gains `routeTimeoutHandler` right after `matchHandler`
  - `app/proxy/handlers.go` — `limiterUserHandler` (line 142) gets a `sync.Map` of route-scoped limiters
  - `README.md` — provider sections gain `timeout`/`throttle` examples
- **Related patterns observed:**
  - per-route options follow zero-value-inherits-global semantics across all providers
  - middleware reads matched mapper via `r.Context().Value(ctxMatch).(discovery.MatchedRoute)` (see `perRouteAuthHandler` in `handlers.go:173`)
  - middleware functions are standalone (not methods) because they only depend on config injected at construction and request-scoped context (this is the established cross-cutting-helper exception)
- **Dependencies identified:**
  - `github.com/didip/tollbooth/v7 v7.0.2` for rate limiting (existing, verified in `go.mod` and `app/proxy/handlers.go`)
  - `http.ResponseController` for connection-deadline override (Go 1.20+; project is on Go 1.26 per commit `014b3cb`)
- **Architectural verification:** confirmed by go-architect — placement of timeout middleware ahead of limiters is correct (auth/throttle work runs *inside* the per-route deadline); `sync.Map` leak from rate-keyed entries is bounded by configuration-edit frequency and acceptable; **the per-route limiter MUST preserve the existing `[ip, dst]` key shape** so per-user throttling is not regressed to a single bucket per route.

## Development Approach

- **testing approach**: Regular (code first, then tests) — incremental per task, each task ends with passing tests
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - tests are not optional — they are a required part of the checklist
  - write unit tests for new functions/methods
  - write unit tests for modified functions/methods
  - add new test cases for new code paths
  - update existing test cases if behavior changes
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change with `cd app && go test -race -timeout=60s -count 1 ./...`
- maintain backward compatibility — zero-value fields preserve current behavior

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

**Project-specific exception:** existing middleware in `app/proxy/handlers.go` (`limiterSystemHandler`, `limiterUserHandler`, `signatureHandler`, `perRouteAuthHandler`, `passThroughHandler`) are standalone functions because they are tiny cross-cutting helpers that compose into the chain independent of any struct's state. The new `routeTimeoutHandler` follows this same pattern and is exempted from the "convert to method" rule for the same reason.

## Testing Strategy

- **unit tests**: required for every task (see Development Approach above)
- per project's Testing Patterns section in CLAUDE.md:
  - real `httptest.Server` per request path, always `defer ds.Close()` for cleanup
  - port allocation via `net.Listen("tcp", "127.0.0.1:0")` not random range
  - context timeouts longer than `waitForServer` timeouts to avoid races
- table-driven with testify assertions
- one test file per source file: `foo.go` → `foo_test.go` only
- **no e2e tests**: reproxy has no UI/browser-based e2e tests

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview

Two orthogonal per-route knobs share the same shape:

1. **Per-route timeout** — new `URLMapper.Timeout time.Duration`. A new middleware `routeTimeoutHandler` runs immediately after `matchHandler`, before any auth/throttle middleware. When the matched mapper has `Timeout > 0` it:
   - wraps the request context with `context.WithTimeout`
   - overrides the connection's read/write deadlines via `http.ResponseController.SetReadDeadline` and `.SetWriteDeadline`. This is the only way to extend (or shorten) past the global `http.Server.WriteTimeout` cap, which is set at connection level and otherwise unbeatable from inside a handler.
   - tolerates `http.ErrNotSupported` from `ResponseController` (HTTP/2, some response writer wrappers); the ctx timeout still applies in that case.

2. **Per-route throttle** — new `URLMapper.Throttle int`. `limiterUserHandler` keeps a lazy `sync.Map[string]*limiter.Limiter` cache. On each request:
   - if the matched mapper has `Throttle > 0`: load-or-store a limiter keyed by `server|srcMatch.String()|rate` at the route's rate, then call `tollbooth.LimitByKeys(routeLimiter, []string{ip, dst})` — same key shape as the global path, preserving per-user-per-destination behavior
   - else: use the single global limiter at the global rate (existing behavior)
   - **factory shape change**: the existing `if reqSec <= 0 { return passThroughHandler }` short-circuit is removed. The factory must always return a real middleware so per-route Throttle still applies when the global `--throttle.user` is 0. The global limiter is built lazily only when actually needed; when global rate is 0, the global `LimitByKeys` call is simply skipped per request (routes with their own Throttle still hit their cached limiter).

The cache key includes `rate` so editing config picks up new rates on next request. Removed/changed routes leak their old limiter entries until process restart; total cardinality is bounded by the count of distinct `(server, srcMatch, rate)` tuples seen over the process lifetime — small in practice for reproxy's stable configuration model.

**Backward compatibility:** zero-value fields (`Timeout = 0`, `Throttle = 0`) preserve current behavior exactly. No flag changes, no env var changes, no breaking API.

**Known limitation (documented, not a bug):** per-route `Timeout` overrides the `http.Server` connection deadlines (`Read`/`Write`) and the request context — solving the slow-upload / slow-client-write case. It does **NOT** override the transport-level `--timeout.resp-header` (default 5s), because `http.Transport.ResponseHeaderTimeout` is set on the shared `Transport` and would require per-destination transports to override per-route. So a route with `Timeout = 5m` waiting on an upstream that itself takes 60s to begin sending response headers will still see the global `--timeout.resp-header` fire first. Workaround for affected users: raise `--timeout.resp-header` globally to the max needed by any slow-response route. README documents this trade-off in the per-route timeout section.

**Error status from per-route timeout:** when the per-route context deadline fires, `httputil.ReverseProxy`'s `RoundTrip` returns `context.DeadlineExceeded`. Because `proxy.go` does NOT set a custom `ErrorHandler` on `ReverseProxy`, the stdlib default writes **HTTP 502 Bad Gateway** with the proxy-error message logged. This matches the existing behavior reported in issue #140 ("returns 502 code when wait too long") and stays consistent across global and per-route timeout failures. Tests must assert 502, not 504. We deliberately do NOT change the error status as part of this PR.

**Discovered during Task 7 (integration testing):** delivering 502 to the client when the per-route timeout fires requires the connection's write deadline to be set *slightly later* than the ctx deadline. If both fire at the same instant, the server-side conn is severed before the proxy's ErrorHandler can flush 502 to the client and the client sees EOF instead. The implementation uses a 500ms grace (`writeDeadlineGrace`) on `SetWriteDeadline` relative to `SetReadDeadline` / ctx timeout for exactly this reason; the read deadline still matches the ctx timeout so the request body read fails at the timeout boundary.

**Discovered during Task 7 (keep-alive conn reuse):** when the per-route timeout fires on a connection, Go's `http.Server` marks the conn as broken at the deadline boundary even after the response is flushed. The next request reused on the same keep-alive connection sees its context canceled before the handler reaches the upstream, producing 502. The middleware defers a reset of both read/write deadlines to zero so subsequent server-side request handling can rearm them via standard `WriteTimeout`/`ReadTimeout`. Despite the reset, a single follow-up request on the same kept-alive conn may still observe the broken state — clients are expected to open a fresh TCP connection after a per-route timeout fires (which is what every standard HTTP client does once a conn is in a half-broken state). Tests use `Transport{DisableKeepAlives: true}` to isolate sub-tests from this carry-over.

## Technical Details

**`URLMapper` extension** (`app/discovery/discovery.go:32`):
```go
type URLMapper struct {
    // ... existing fields ...
    Timeout  time.Duration // per-route request timeout, 0 = use global
    Throttle int           // per-route req/sec per user, 0 = use global throttle.user
    // ... rest ...
}
```

**Provider parsing:**

- **file (YAML)**: extend the anonymous struct in `File.List()`. **YAML lib is `gopkg.in/yaml.v3` which does NOT auto-parse Go duration strings into `time.Duration`**. So the YAML-mapped field is `string`:
  ```go
  Timeout  string `yaml:"timeout"`
  Throttle int    `yaml:"throttle"`
  ```
  Then inside `List()` parse `f.Timeout` via `time.ParseDuration` (empty string → zero duration, no error). Example yaml:
  ```yaml
  - { route: "^/upload/(.*)", dest: "http://...", timeout: 5m, throttle: 2 }
  ```
  Validate parsed timeout `< 0` returns parse error; validate throttle `< 0` returns parse error.

- **static (CSV positional)**: extend rule format from `server,source,dest[,ping[,forward-health-checks]]` to `server,source,dest[,ping[,forward-health-checks[,timeout[,throttle]]]]`. Empty positional field means "inherit global". `time.ParseDuration` and `strconv.Atoi` errors propagate as parse errors (config-time validation, errors surface to operator). Update header doc comments to reflect new 7-field format.

- **docker (labels)**: parse via existing `labelN(labels, n, "timeout")` and `labelN(labels, n, "throttle")` helpers. Same `<n>.timeout` / `<n>.throttle` shape as other per-route labels. Invalid values log a warning (`[WARN] timeout label value %s is not valid, ignoring`) and fall back to zero — matches the existing `forward-health-checks` behavior in `getForwardHealthChecksValue` at `docker.go:452-463`.

- **consul-catalog**: parse `c.Labels["reproxy.timeout"]` and `c.Labels["reproxy.throttle"]`. Same warn-and-zero on bad values.

**Parsing strictness asymmetry (intentional)**: static returns parse errors (config strings loaded once at startup — errors must surface to the operator); docker / consul-catalog warn-and-zero (labels come from external runtime sources where a single bad value should not crash discovery for the entire fleet). Do not homogenize these in either direction.

**`routeTimeoutHandler`** (new in `app/proxy/handlers.go`):
```go
const writeDeadlineGrace = 500 * time.Millisecond

// routeTimeoutHandler applies a per-route request deadline when the matched mapper has Timeout > 0.
func routeTimeoutHandler(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        reqCtx := r.Context()
        if reqCtx.Value(ctxMatch) == nil {
            next.ServeHTTP(w, r)
            return
        }
        match := reqCtx.Value(ctxMatch).(discovery.MatchedRoute)
        if match.Mapper.Timeout <= 0 {
            next.ServeHTTP(w, r)
            return
        }
        readDeadline := time.Now().Add(match.Mapper.Timeout)
        ctx, cancel := context.WithDeadline(reqCtx, readDeadline)
        defer cancel()
        rc := http.NewResponseController(w) // always non-nil per stdlib contract
        if err := rc.SetReadDeadline(readDeadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
            log.Printf("[DEBUG] route timeout: SetReadDeadline failed: %v", err)
        }
        if err := rc.SetWriteDeadline(readDeadline.Add(writeDeadlineGrace)); err != nil && !errors.Is(err, http.ErrNotSupported) {
            log.Printf("[DEBUG] route timeout: SetWriteDeadline failed: %v", err)
        }
        // clear deadlines so kept-alive conns don't carry an expired write deadline into subsequent requests
        defer func() {
            _ = rc.SetReadDeadline(time.Time{})
            _ = rc.SetWriteDeadline(time.Time{})
        }()
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

Notes:
- `http.NewResponseController` is documented to always return a non-nil `*http.ResponseController` — no nil check needed.
- `http.ErrNotSupported` is returned when the underlying writer can't surface a deadline (e.g. some HTTP/2 paths or buffering wrappers). Treat that as expected — the ctx deadline still applies and propagates to upstream calls via the reverse proxy's transport. Other errors are logged at DEBUG once per request.
- Write deadline gets a 500ms grace beyond the ctx timeout so the proxy's default ErrorHandler can flush 502 to the client when the per-route ctx cancels the upstream call (see Solution Overview note on error status).
- Both deadlines are reset to zero in a defer so kept-alive conns don't carry an expired write deadline into subsequent requests; the server's normal `WriteTimeout`/`ReadTimeout` reapply on the next request.

**Modified `limiterUserHandler`** (`app/proxy/handlers.go:142`): retains the existing global limiter for routes without per-route Throttle. Adds an outer `sync.Map` for route-scoped limiters. Key construction:
```go
routeKey := match.Mapper.Server + "|" + match.Mapper.SrcMatch.String() + "|" + strconv.Itoa(match.Mapper.Throttle)
```
Use the raw `match.Mapper.Server` value without normalization (`*` literal stays `*`; whatever the provider stored is what the key carries). Route-scoped limiter still uses `tollbooth.LimitByKeys(lmt, []string{ip, dst})` to preserve per-user throttling.

The factory no longer short-circuits with `passThroughHandler` when `reqSec <= 0` — it must always return a real middleware so per-route Throttle still applies when global throttle is disabled. The global limiter is constructed lazily (`sync.Once`) and its `LimitByKeys` call is skipped per request when the global rate is zero.

**Chain order in `proxy.go:136-154`** (NEW row marked; method-call form preserved to match real code):
```
R.Recoverer(log.Default())
signatureHandler(h.Signature, h.Version)
h.pingHandler
h.healthMiddleware
h.matchHandler
routeTimeoutHandler                          <-- NEW: needs matched mapper in ctx
h.OnlyFrom.Handler
perRouteAuthHandler
h.basicAuthHandler()
limiterSystemHandler(h.ThrottleSystem)
limiterUserHandler(h.ThrottleUser)           <-- MODIFIED: per-route throttle override
... rest unchanged ...
```

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): all code, tests, README updates within this repo
- **Post-Completion** (no checkboxes): manual smoke testing the binary, optional issue update on GitHub (handled after PR merges)

## Implementation Steps

### Task 1: Add Timeout and Throttle fields to URLMapper + extendMapper + file provider parsing

**Files:**
- Modify: `app/discovery/discovery.go` (struct + extendMapper)
- Modify: `app/discovery/discovery_test.go` (extendMapper coverage for new fields)
- Modify: `app/discovery/provider/file.go`
- Modify: `app/discovery/provider/file_test.go` (extend existing test functions; do NOT create a new test file)
- Modify: `app/discovery/provider/testdata/config.yml` (existing fixture loaded by file_test.go)

- [x] add `Timeout time.Duration` and `Throttle int` fields to `URLMapper` in `app/discovery/discovery.go`
- [x] **critical — preserve fields through `extendMapper`** (`discovery.go:463-477`): this function reconstructs a fresh `URLMapper` field-by-field for simple no-capture routes that need implicit regex/destination expansion. Add `Timeout: m.Timeout` and `Throttle: m.Throttle` to the `res := URLMapper{...}` literal. Without this, simple routes (e.g. `/api/` → `/dst/`) silently lose their per-route timeout/throttle before reaching `Match()`. Provider-level tests do NOT catch this — the loss happens later in `mergeLists` → `extendMapper`.
- [x] add a discovery-level test in `discovery_test.go`: construct a `URLMapper` with a simple-extension src (e.g. `^/api/` with dst `http://upstream/`) and non-zero `Timeout`/`Throttle`, run it through `extendMapper`, assert both fields survive. Also assert the non-extension path (src with capture group `(.*)`) preserves them.
- [x] in `app/discovery/provider/file.go`, add `Timeout string \`yaml:"timeout"\`` and `Throttle int \`yaml:"throttle"\`` to the anonymous struct inside `File.List()` (yaml.v3 does NOT auto-parse Go duration strings into `time.Duration`, so the yaml-mapped field is `string` and parsed manually)
- [x] in the mapper construction loop, call `time.ParseDuration(f.Timeout)` only when `f.Timeout != ""`; on parse error return `fmt.Errorf("can't parse timeout %s: %w", f.Timeout, err)`
- [x] validate negative values: parsed timeout `< 0` and `f.Throttle < 0` each return a descriptive parse error
- [x] populate `mapper.Timeout` (parsed duration or zero) and `mapper.Throttle` (`f.Throttle`)
- [x] extend `testdata/config.yml` with: one route that sets `timeout: 5m` only, one that sets `throttle: 10` only, one that sets both, and verify existing routes (without the new fields) still parse correctly
- [x] add assertions in the existing `TestFile_List` (or equivalent existing test function) — locate mappers by their `SrcMatch.String()` and assert `Timeout` / `Throttle` values match expected; do NOT create a new test file
- [x] add test cases for negative timeout (e.g. yaml `timeout: -5s`) and negative throttle (`throttle: -1`) — both return parse errors
- [x] run `cd app && go test -race -timeout=60s -count 1 ./discovery/...` — must pass before next task
- [x] verify per-task gate (formatter, golangci-lint, rule scan) before marking complete

### Task 2: Static provider — positional CSV extension

**Files:**
- Modify: `app/discovery/provider/static.go`
- Modify: `app/discovery/provider/static_test.go`

- [x] extend the static rule format from `server,source_url,destination[,ping[,forward-health-checks]]` to `server,source_url,destination[,ping[,forward-health-checks[,timeout[,throttle]]]]`
- [x] update the package-level doc comment and `Rules` field comment to reflect the 7-field format
- [x] in `parse()`, read `elems[5]` as `time.Duration` via `time.ParseDuration` when present and non-empty; read `elems[6]` as `int` via `strconv.Atoi` when present and non-empty
- [x] return descriptive parse errors for invalid duration / non-integer / negative values
- [x] populate `URLMapper.Timeout` and `URLMapper.Throttle` in the result
- [x] add test cases covering: 5-field input (back-compat), 6-field input with timeout only, 7-field with both, invalid duration, invalid throttle, negative throttle
- [x] run `cd app && go test -race -timeout=60s -count 1 ./discovery/...` — must pass before next task
- [x] verify per-task gate

### Task 3: Docker provider — labels

**Files:**
- Modify: `app/discovery/provider/docker.go`
- Modify: `app/discovery/provider/docker_test.go`

- [x] in `parseContainerInfo`, read `reproxy.<n>.timeout` and `reproxy.<n>.throttle` labels via the existing `labelN` helper
- [x] parse timeout as `time.Duration`; on `time.ParseDuration` error log a warning (`[WARN] timeout label value %s is not valid, ignoring`) and fall back to zero — matches the `forward-health-checks` pattern at `docker.go:452-463`
- [x] parse throttle as `int` via `strconv.Atoi`; on error or negative, warn and fall back to zero
- [x] populate `Timeout` and `Throttle` in the resulting `URLMapper`
- [x] add docker_test.go cases: container with valid `reproxy.0.timeout=5m`, container with valid `reproxy.0.throttle=10`, container with invalid duration (must warn + zero), container with no labels (back-compat)
- [x] run `cd app && go test -race -timeout=60s -count 1 ./discovery/...` — must pass before next task
- [x] verify per-task gate

### Task 4: Consul catalog provider — labels

**Files:**
- Modify: `app/discovery/provider/consulcatalog/consulcatalog.go`
- Modify: `app/discovery/provider/consulcatalog/consulcatalog_test.go`

- [x] in the provider's list/parse loop, read `c.Labels["reproxy.timeout"]` and `c.Labels["reproxy.throttle"]` and parse with the same warn-and-zero semantics as docker
- [x] populate `Timeout` and `Throttle` in the `URLMapper` construction
- [x] add test cases mirroring the docker tests: valid values, invalid duration, invalid throttle, missing labels
- [x] run `cd app && go test -race -timeout=60s -count 1 ./discovery/...` — must pass before next task
- [x] verify per-task gate

### Task 5: routeTimeoutHandler middleware + wire into chain

**Files:**
- Modify: `app/proxy/handlers.go`
- Modify: `app/proxy/proxy.go`
- Modify: `app/proxy/handlers_test.go`

- [x] add `routeTimeoutHandler` in `app/proxy/handlers.go` per the Technical Details section above (standalone — established middleware pattern, exempted from method-conversion rule per the project-specific exception in the Code-Quality block; one-sentence godoc as shown in the Technical Details snippet)
- [x] insert `routeTimeoutHandler` into the middleware chain in `app/proxy/proxy.go` immediately after `h.matchHandler` and before `h.OnlyFrom.Handler` (around line 142)
- [x] add `handlers_test.go` cases. Each test injects a `MatchedRoute` into the request context via a tiny test wrapper that calls `context.WithValue(r.Context(), ctxMatch, discovery.MatchedRoute{Mapper: ...})` — same pattern existing tests use; do NOT depend on the real `matchHandler`.
  Define a small **recording response writer** in the test file that wraps `httptest.ResponseRecorder` and records every `SetReadDeadline` / `SetWriteDeadline` call. `httptest.ResponseRecorder` itself does NOT implement deadline setters; `http.NewResponseController` would return `http.ErrNotSupported` against it. The recording writer is the only way to assert deadline behavior in unit tests. It must satisfy the `interface { SetReadDeadline(time.Time) error; SetWriteDeadline(time.Time) error }` shape so `ResponseController` unwraps it.
  - **passthrough zero timeout**: matched route with `Timeout = 0` → next handler runs to completion; assert the recording writer received ZERO `SetReadDeadline` / `SetWriteDeadline` calls (proves the middleware did not touch deadlines).
  - **timeout fires**: matched route with `Timeout = 100ms`. The next handler is a **context-aware stub** that does `select { case <-r.Context().Done(): http.Error(w, "ctx done", http.StatusGatewayTimeout); case <-time.After(500ms): w.Write([]byte("late")) }`. Assert: response body matches "ctx done" (or check the recorded status the stub wrote) — never "late". This proves the per-route timeout actually cancels the downstream context. **Note**: this status code is for the test stub's choice; in real proxy flow, `httputil.ReverseProxy`'s default ErrorHandler returns 502 for context cancellation — that case is covered in Task 7's integration test.
  - **deadlines are actually set when Timeout > 0**: matched route with `Timeout = 5s`. Next handler responds immediately with 200 OK. Assert success AND assert the recording writer received exactly one `SetReadDeadline` and one `SetWriteDeadline` call at approximately `time.Now() + 5s` (allow a small slack for clock skew). Proves the middleware writes deadlines when Timeout > 0.
  - **no match in context**: request without `ctxMatch` value → passthrough (next handler runs, no panic, no deadline calls recorded).
  - **ErrNotSupported tolerance**: recording writer's `SetWriteDeadline` returns `http.ErrNotSupported`. Assert no panic, no log spam (capture stderr if needed), and ctx-cancel path still fires for the timeout case. **Note**: in Go 1.26 the stdlib HTTP/2 response writer does support deadlines, so `ErrNotSupported` is a defensive fallback for unusual response writer wrappers — not a routine case.
- [x] run `cd app && go test -race -timeout=60s -count 1 ./proxy/...` — must pass before next task
- [x] verify per-task gate

### Task 6: Per-route throttle in limiterUserHandler

**Files:**
- Modify: `app/proxy/handlers.go`
- Modify: `app/proxy/handlers_test.go`

- [x] modify `limiterUserHandler` in `app/proxy/handlers.go` to maintain a `sync.Map` of route-scoped `*limiter.Limiter` instances (one outer cache per factory call)
- [x] **remove the existing `if reqSec <= 0 { return passThroughHandler }` short-circuit** at handlers.go:143-145 — the factory must always return a real middleware so per-route Throttle still applies when global throttle is disabled
- [x] construct the global limiter lazily via `sync.Once` (build only when first needed). When global rate is zero, skip the global `LimitByKeys` call per request — but still consult per-route Throttle.
- [x] on each request:
  - if `ctxMatch` is present and `match.Mapper.Throttle > 0`:
    - build cache key: `match.Mapper.Server + "|" + match.Mapper.SrcMatch.String() + "|" + strconv.Itoa(match.Mapper.Throttle)`
    - `LoadOrStore` a limiter at rate `float64(match.Mapper.Throttle)`
    - call `tollbooth.LimitByKeys(routeLimiter, []string{ip, dst})` — preserving `[ip, dst]` key shape
    - return 429 on limit hit; otherwise continue
  - else (no per-route override): use the existing single global limiter path (key shape unchanged — `[ip]` or `[ip, dst]` for MTProxy matches, exactly as today)
- [x] add `handlers_test.go` cases. Tests inject `MatchedRoute` via the same wrapper used in Task 5; each test uses `httptest.NewRecorder` to send requests with explicit `RemoteAddr` so tollbooth's IP detection works deterministically.
  - **route throttle fires**: `Throttle = 2`. 3 sequential requests from `RemoteAddr=1.2.3.4:1234` to the same route within 1s. Assert: first two return 200, third returns 429.
  - **key isolation across routes**: two routes with `Throttle = 1` each (different `SrcMatch`). One request to each. Both succeed (no cross-contamination through the cache).
  - **per-user preserved**: route with `Throttle = 2`. Two distinct `RemoteAddr` values (`1.1.1.1:1` and `2.2.2.2:1`) each send 2 requests. All 4 succeed (each IP gets its own 2/s budget).
  - **fallback to global**: matched route with `Throttle = 0` and factory built with `reqSec = 5`. Behavior identical to current master — verify via 6th rapid request returning 429.
  - **per-route works when global is zero**: factory built with `reqSec = 0` (which under current code returns `passThroughHandler` — but after the fix must return a real middleware). Matched route with `Throttle = 3`. Send 4 rapid requests; assert 4th returns 429.
  - **rate-change cache key**: two separate test invocations of the middleware with two distinct `MatchedRoute` contexts that have the SAME `Server` and `SrcMatch` but `Throttle = 2` first and `Throttle = 5` second. Send 3 requests at rate-2 (3rd returns 429), then 6 requests at rate-5 (6th returns 429 from a freshly-cached limiter at rate 5). This proves the cache key includes rate.
- [x] run `cd app && go test -race -timeout=60s -count 1 ./proxy/...` — must pass before next task
- [x] verify per-task gate

### Task 7: End-to-end integration tests in proxy_test.go

**Files:**
- Modify: `app/proxy/proxy_test.go`
- Create (or reuse existing): `app/proxy/testdata/per_route.yml` — small fixture used only by this test

- [x] add an integration test that wires up a real `Http` server with a `provider.File` source pointing at the fixture. Fixture contains one route with `timeout: 200ms`, one with `throttle: 2`, one with both, and one with neither (control). Each route maps to a separate `httptest.Server` upstream.
- [x] **verify timeout end-to-end**: upstream for the timeout route does a context-aware sleep (`select` on `r.Context().Done()` vs `time.After(500ms)`). Client request times out; **client receives HTTP 502** (the stdlib `httputil.ReverseProxy` default ErrorHandler writes 502 for `context.DeadlineExceeded` from `RoundTrip` — this matches existing reproxy behavior on global timeout, see issue #140 body). Sibling route without per-route timeout responds normally even if it takes 250ms. Assert the response arrives close to the route's Timeout duration (not the upstream's 500ms), proving cancellation actually fired.
- [x] **verify throttle end-to-end**: 3 rapid client requests to the throttled route — first two pass, third returns 429. 3 rapid requests to the sibling route — all succeed.
- [x] **verify chain ordering interaction**: route with BOTH `timeout: 50ms` and `throttle: 1` — first request that exceeds the throttle should still respect the deadline (routeTimeoutHandler runs before limiterUserHandler). Verify the 429 response is returned within the deadline window (not blocked by a slow upstream that the deadline would have cancelled anyway).
- [x] use `net.Listen("tcp", "127.0.0.1:0")` for the reproxy listener (per CLAUDE.md Testing Patterns)
- [x] `defer ds.Close()` on every `httptest.Server` and on the reproxy listener
- [x] context timeouts in the test wrap must be longer than the per-route timeouts to avoid spurious failures (per the same Testing Patterns note about waitForServer timeouts)
- [x] run `cd app && go test -race -timeout=60s -count 1 ./...` — must pass before next task
- [x] verify per-task gate

**Implementation notes (Task 7):**
- Fixture uses `__TIMEOUT_URL__` / `__THROTTLE_URL__` / `__BOTH_URL__` / `__CONTROL_URL__` placeholders that the test substitutes at runtime with actual `httptest.Server` URLs (each upstream listens on an OS-allocated port), then writes the substituted YAML into `t.TempDir()` and points the `File` provider at it.
- Tests disable keep-alives on the client (`Transport{DisableKeepAlives: true}`) so a per-route timeout firing in one sub-test does not poison a kept-alive conn carried into the next sub-test. See the conn-state caveat documented in Solution Overview.
- `Test_routeTimeoutHandler` unit test (Task 5) was updated to verify both setters are called twice — once to arm the deadline, once (via defer) to clear it back to zero. Reset paths verified.

### Task 8: Update README

**Files:**
- Modify: `README.md`

- [x] in the file-provider section, document `timeout` and `throttle` yaml fields with one example each (mirror the `auth` / `forward-health-checks` style)
- [x] in the static-provider section, document the new positional fields and update the format string in the docs
- [x] in the docker-provider section, document the new `reproxy.<n>.timeout` and `reproxy.<n>.throttle` labels
- [x] in the consul-catalog section, document the same labels
- [x] add one short subsection (or note alongside the existing `--timeout.*` docs) explaining the global-vs-per-route precedence: "zero inherits global, positive overrides"
- [x] one-line note that per-route timeout overrides the connection write deadline for matched requests; routes without per-route timeout still respect global `--timeout.write`
- [x] **document the response-header-timeout limitation**: per-route `timeout` does NOT override the transport-level `--timeout.resp-header` (default 5s). If an upstream takes longer than `--timeout.resp-header` to begin sending response headers (e.g. a slow report-generation endpoint), the request will still fail at that boundary regardless of per-route `timeout`. To support such routes, raise `--timeout.resp-header` globally to the max needed by any slow-response route. Per-route override of transport-level timeouts is intentionally out of scope for this feature.
- [x] no test changes (documentation only)
- [x] run `cd app && go test -race -timeout=60s -count 1 ./...` to confirm no regression

### Task 9: Verify acceptance criteria

- [ ] verify the two issue #140 use cases are exercised by the integration tests from Task 7 (long upload route bypasses 30s global; brute-force route enforces lower rate than global)
- [ ] verify backward compatibility: any existing config (no per-route fields) produces identical behavior to current master — covered by existing tests staying green, plus the explicit "control" route in the Task 7 fixture
- [ ] final verification re-run of formatter, linter, and full test suite as a pre-merge check (per-task gate already enforced these per task; this is a belt-and-braces sweep):
  - `~/.claude/format.sh`
  - `golangci-lint run --max-issues-per-linter=0 --max-same-issues=0` (from repo root)
  - `cd app && go test -race -timeout=60s -count 1 ./...`

### Task 10: Final — Update documentation and move plan

- [ ] update `CLAUDE.md` if any new patterns were discovered (per-route option pattern is already documented via existing fields; likely no change needed)
- [ ] move this plan to `docs/plans/completed/` (create directory if it does not exist)

## Post-Completion

*Items requiring manual intervention or external systems — informational only, handled after the PR merges.*

**Manual verification:**
- smoke-test a built binary against a real upstream with a long-running endpoint (e.g. simulate a slow upload via `curl --data-binary @largefile`) to confirm the route-level deadline actually extends past the global write timeout
- smoke-test rate-limiting against a real route with `wrk` or `hey` to confirm the per-route limiter rejects with 429 as expected

**External system updates:**
- after the PR merges, the GitHub issue #140 will be auto-closed via PR linkage (or commented on by the maintainer); this is not part of the plan's scope
