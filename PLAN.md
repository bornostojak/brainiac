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

Networking update: this device listens on 0.0.0.0:3011. Enforce a global
IP_ALLOWLIST (empty by default) supporting IPv4/IPv6 addresses and CIDRs.
Connections whose actual socket destination is in 127.0.0.0/24 bypass the list;
other destinations require an allowed peer IP. Host/forwarded headers are ignored. Compose host networking preserves socket
peer IPs; update installed skill/configuration/installer copies used here.

Agent activity feature: project vendor counts and per-task pulsing indicators
show explicit, per-project agent reports with a one-hour expiry. Support shared
tasks and work descriptions. Batch task changes, activity, usage and handoffs
atomically; guide MCP/skills to report meaningful events or quiet 10–15 minute
check-ins rather than individual commands/edits. Refresh relevant installations.

Cross-project attention: persistent user input requests, globally delivered
browser popups with optional desktop permission and revision-guarded notice
closure. Questions and answers stay in the originating chat. Independent service instructions use stable keys, per-agent revision
receipts and embedded-skill fingerprints; return pending guidance with normal
reports to avoid extra polling. Expose equivalent REST/MCP tools and skill resources.

Project overview refinement: 2:1 header split with a live concise workSummary;
project lead updates it through existing report batches, workers update workingOn.
Replace grouped vendor rows with one compact row per agent, explicit vendor/model
columns and used/limit bars with remaining values. Keep unknown/stale readings
truthful, retain all columns with mobile horizontal scrolling, update installed
skills/deployments and publish the versioned Git release after validation.

Attention-only clarification: Brainiac announces that an agent needs the user
in the originating conversation. Keep the actual question and answer in native
host input/chat; remove browser reply controls. Notices contain project/agent/
task and optional harness/conversation metadata, with revision-guarded closure
after native input is handled. Exclude legacy question/answer content from
public notices and historical input events without deleting stored history.
Update REST/MCP contracts, both skills and the live deployment together.

Project learning and review: record durable, individually addressable lessons
for failed approaches, miscalculations, misunderstandings and explicit user
corrections. Keep evidence, scope and confidence; current user instructions take
precedence. Provide lightweight filtered indexes, revision guards/history, safe
Markdown and REST/MCP resources. Batch new lessons into meaningful progress
reports. Add compact project lesson views and a user-facing numbered notification
review page with filters, pagination and live refresh. Validate isolation, atomic
rollback, persistence, browser/MCP behavior, then refresh deployment/skills and
publish the versioned commit.

Project-only notifications: remove the global REST notification routes; require
project IDs in REST/MCP reads and messages. Browser /notifications remains a
project chooser and never combines feeds. Discovery and skills retain one cursor
per project/filter. Preserve explicit browser overview/attention streams and
project SSE for agents. Clear all existing numbered notification rows locally
as requested, preserving entity state and monotonic sequence high-water marks.
Validate isolation, pagination/long-polling, cursor behavior after clear and
browser project selection; refresh skills/stack and publish the release.
