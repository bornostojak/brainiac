# Brainiac implementation plan

Implement the accepted persistent Go progress and knowledge tracker:

1. Scaffold: Go binary, Dockerfile, loopback-only Compose publishing, durable volume,
   configurable listener IP/port and 90-minute activity timeout.
2. Persistence: SQLite migrations, immutable project/task identifiers, nested tasks,
   manual percentages, separate lifecycle/presence, optimistic revisions,
   transactional numbered notifications, Markdown knowledge and revision history.
3. HTTP: versioned REST API, filtered summaries, notification cursors and long
   polling, SSE replay, negotiated project/task/knowledge READMEs, OpenAPI.
4. Interface: compact project overview, expandable task rows, subtasks toggle,
   readable context and knowledge, live updates and reconnect fallback.
5. MCP: official Go SDK v1.8.0, stateless HTTP, matching tools/resources,
   stdio bridge to the existing server (no separate database).
6. Skills: primary progress-tracker; service-first live-progress-monitor with
   unchanged Python fallback; bounded authoritative discovery and exact project
   reuse; install tracked skill packages without removing unrelated files.
7. Acceptance: Go tests/race/vet, persistence/concurrency/cursor/expiry checks,
   MCP clients, Python self-tests and skill validation, Docker build/recreation,
   browser smoke checks; leave the validated Compose service running.

Defaults: 127.0.0.1:3011, 90-minute expiry, one instance/database, HTTP without
authentication, Markdown-only knowledge. Browser UI is read-oriented. Agents
estimate each task/subtask independently. Project progress averages top-level
tasks. No authentication, TLS, skill bundles or automatic model execution in v1.

Commit each completed stage. Preserve notification history and knowledge
revisions. Writes and their events are atomic; reads never extend activity.

Accepted additions: compact reported token/context/model/freshness indicators,
and a served /install.sh that detects installed OpenCode, Claude Code and Codex,
asks which targets the user wants, and distributes the primary/fallback skills.
