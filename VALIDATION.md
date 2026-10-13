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
