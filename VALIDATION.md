# Acceptance results

Validated on 2026-10-13 with Go 1.26.0 and Docker Compose 2.40.3.

- `go test ./...`, with a compiled `BRAINIAC_TEST_BINARY`: passed, including
  HTTP and stdio MCP clients and the served installer on disposable destinations.
- `go test -race ./...`: passed. `go vet ./...`: passed.
- Persistence checks cover project isolation, immutable task hierarchy, manual
  percentages, optimistic revision conflicts, durable knowledge history,
  transactional rejection, concurrent notifications, filtered pagination,
  long-poll wakeup/cancellation, usage freshness, and exact activity expiry.
- HTTP checks cover validation, duplicate project keys, cross-origin writes,
  lightweight knowledge indexes, README content negotiation, safe Markdown,
  and SSE replay with `Last-Event-ID`.
- MCP checks cover tool/resource discovery, project/task writes, knowledge,
  usage reports and README resources. The stdio bridge creates no local database.
- Python skill/discovery/MCP configuration checks: passed. JSONC comments and
  unrelated configuration survive merges; repeated installation is idempotent.
- Both skill packages passed the skill validator. The original Python monitor
  is byte-identical to its source and its HTTP/persistence self-test passed.
- Headless Chromium checks passed for the subtasks toggle, independent progress,
  stale/unknown usage, SSE refresh, preserved expansion, search/status filtering,
  mobile width, task views, knowledge lookup and safe article rendering.
- Docker image built and Compose became healthy with host publication exclusively
  at `127.0.0.1:8765`. A disposable image instance also served successfully at
  `127.0.0.2:18766` and was removed afterward.
- Container recreation retained the repository project, all seven milestone
  IDs, knowledge content and revision, and the numbered notification cursor.
- The two skills were synchronized into the existing Codex skills directory.
  The served installer offers explicit skills/MCP target selection for Codex,
  Claude Code and OpenCode; unrelated files are retained.

Docker Hub returned a pull rate limit during the initial build. The same Go and
Alpine images were fetched from the public ECR mirror through the documented
build arguments. The final Compose service remains running in the background.

Usage values are agent-reported. This session exposes no measured remaining-token
or context values, so the live project displays those readings as unknown.

## Default-port update

The default port is now `3011` across the native service, Docker image, Compose,
healthcheck, MCP bridge, skill discovery and documentation. The rebuilt local
deployment is healthy at `127.0.0.1:3011`; the installed Codex skill's saved
origin also uses that address. Go and skill checks passed, and the migration
preserved project/task IDs, knowledge revisions and notification history.

## Listener and IP/CIDR access policy

The default listener is `127.0.0.1:3011` with an empty `IP_ALLOWLIST`.
Deployment-specific listener and allowlist values belong in ignored `.env` files.
Requests whose actual socket destination is in `127.0.0.0/24` bypass the list;
other destinations require an allowed client address or IPv4/IPv6 CIDR.
Host/forwarding headers cannot enable the exemption. Compose uses host networking
to preserve the real socket source and destination addresses.

Go tests with race detection, vet and skill checks passed. Live checks returned
`200` on `127.0.0.1` and `127.0.0.254`, and `403` on `127.0.1.1` and the LAN
address, including requests with spoofed Host/forwarding headers. A disposable
instance with an explicit CIDR allowed its matching client and was then removed.
Existing project/task IDs, knowledge revisions and notification history survived
the deployment. The used Codex skill packages and installer copy were refreshed;
Existing harness settings were preserved.

## Project agent activity and efficient reporting

- One-hour agent expiry passed with an injected clock, including exact boundary,
  reads, stop/renewal, restart, and one inactivity event per transition.
- Project/vendor counts remain isolated and count shared-task agents only once.
  Explicit assignments supply per-task work descriptions; ownership alone never
  creates presence. Completed tasks have no working indicator.
- Atomic reports combine task patches, presence, measured usage and messages.
  Cross-project references and revision conflicts roll back the complete batch,
  including notifications; partial patches preserve other assignments.
- `go test ./...`, `go test -race ./...`, and `go vet ./...` passed. HTTP and
  stdio SDK clients exercised all three added activity/report tools.
- Headless Chromium passed vendor counts, multiple-agent hover descriptions,
  live stop/removal, nested-task indicators, preserved expansion, filters,
  usage, mobile width and safe knowledge rendering. The original Python
  monitor self-test and both skill validators passed.
- The rebuilt Compose deployment is healthy at `127.0.0.1:3011`, retaining
  privately configured listener/access settings, project/task IDs, knowledge
  revisions and notification history. Live MCP discovery exposes 24 tools and
  the efficient reporting instructions.
- Updated both installed Codex skills and the used installer copy; tracked files
  match installed packages. Server guidance favors one batched report
  per meaningful milestone and quiet 10–15 minute check-ins, avoiding calls for
  each command, edit or reasoning step.

## Publication audit

Reviewed every reachable commit and tracked configuration before publication.
Deployment-specific network ranges were removed from their original commits;
CIDR examples and tests use documentation-only networks. No credentials,
personal filesystem paths, deployed entity identifiers or local databases were
found in tracked history. Local `.env` files were never tracked. Author and
committer attribution is retained as explicitly requested.

The generic `compose.example.yaml` validates using `.env.example`: loopback
listener, port 3011, empty allowlist, persistent volume and healthcheck. Git and
Docker ignores protect local environment files, overrides, saved skill origins
and database artifacts. The active private deployment settings remain local.

## Cross-project input, instructions and remaining limits (1.1.0)

- Persistent input questions, retry deduplication, same-project task references,
  atomic blocker/question reports, guarded answers and terminal-state protection
  passed. Request and response events include the durable question ID and agent
  routing label.
- Independent instructions passed recipient/project filtering, acknowledgement
  persistence, revision conflicts, retirement and redelivery after changes.
  Publishing/acknowledging guidance leaves numbered cursors and project activity
  unchanged. Work reports carry pending guidance without an extra polling call.
- Embedded skill fingerprints match served package metadata. Unchanged restarts
  retain receipts without repeated notices; changed content produces a new
  independent revision. Reserved service notices reject external edits.
- HTTP and stdio MCP clients exercised input requests, responses, instruction
  tools/receipts and current-skill retrieval; resources include skill Markdown.
  The deployed service exposes 34 tools and four resource templates.
- Chromium passed cross-project popups, draft preservation during new arrivals,
  dismissal/reopening, reload recovery, responses across tabs, Markdown alerts,
  safe text rendering and polling with SSE deliberately disabled. Mobile popups
  fit the viewport. Headless Chromium denied desktop notification permission;
  in-page fallback passed, while operating-system delivery was not verified.
- Project/vendor limits include task owners and presence-only agents, unknown
  readings, stale observations, context headroom and reported allowance.
  Remaining limits persist after an agent stops; unrelated agents are excluded.
  Browser checks verified measured token/context/allowance values and compact
  mobile layout. Vendor account allowances are never summed across agents.
- Go tests, race detection, vet, Python skill checks and both skill validators
  passed. The rebuilt Compose service remains healthy on port 3011 with existing
  IDs, knowledge revisions, numbered history and private access settings intact.
  Both installed Codex skill packages and the used installer copy were refreshed.
