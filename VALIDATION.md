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

## Live project focus and compact agent table (1.2.0)

- Project work summaries passed bounded text validation, author/timestamp
  provenance, revision guards and atomic rollback with invalid report batches.
  Reads and worker reports without a summary leave its provenance unchanged.
  HTTP and stdio MCP clients reported and retrieved the shared focus field.
- Chromium verified the desktop 2:1 header ratio, live summary updates without
  reload, fallback to active agents' work, and preserved task expansion.
  Mobile stacks the header panels and scrolls all agent columns inside the panel
  without overflowing the page.
- The table has one row per agent with explicit vendor/model, activity, current
  work and compact token/context/allowance bars. Checks verified used/limit and
  remaining values, actual progress-bar ranges, stale labels, and a known
  remaining count with unknown budget showing no fabricated ratio.
- Existing subtasks, global input popups, responses across tabs, polling fallback,
  filtering and safe knowledge rendering remained functional.
- Go tests, race detection, vet, Python checks and both skill validators passed.
  The deployment preserves project/task IDs, knowledge revisions and private
  settings. Updated skill packages teach the project lead to maintain summary
  text in existing meaningful report batches; workers update their own focus and
  actual vendor/model. No additional per-action request is required.

## Native input routing guidance (1.2.1)

Both skill packages, MCP initialization/tool descriptions and the API guide now
preserve native host question/chat input and permission/approval paths. Brainiac
requests complement those paths as mirror alerts; posting a request never opens
a native host dialog. A real native answer resolves its mirror without requiring
another answer. Brainiac-only input requires an explicit user choice or a host
without an input channel.

Go acceptance tests, including HTTP/stdio MCP and the served installer, Python
checks and both skill validators passed. Native dialog presentation remains a
host responsibility; this correction changes agent guidance and does not
retroactively open dialogs for agents already waiting on an existing request.

## Attention-only notices (1.3.0)

- Brainiac now gives a generic heads-up with project, agent and optional harness/
  conversation link. Questions and answers stay in the native host conversation.
  The earlier browser-answer workflow described for 1.1–1.2.1 is superseded.
- REST and MCP notice creation accept metadata without question text. Legacy
  title/message writes are discarded; replacing an answer is rejected. Closure
  requires the current revision, preserves terminal-state protection and sends
  a generic event. The new close_input_request tool has a deprecated metadata-only
  answer_input_request alias; neither accepts a response.
- Historical notice reads, deduplicated reports and input-event replay exclude
  old question/answer bodies. Tests confirm the stored history and event numbers
  remain intact. Conversation links reject unsafe schemes and URL credentials.
- Chromium verified cross-project delivery, no question text or reply controls,
  known conversation links, dismissal/reopening, reloads, cross-tab closure,
  Markdown notices and polling with SSE disabled. The compact agent table, limits,
  header split, nested tasks, live summary and knowledge views still pass.
- Go acceptance tests exercised HTTP/stdio MCP and both closure tools. Full tests,
  race detection, vet, Python checks and both skill validators passed. Desktop OS
  delivery and native host dialog presentation were not automated; actual input
  remains a host responsibility.
- The existing Compose deployment is healthy on port 3011. Both installed Codex
  skills and the used installer copy were refreshed; the local stdio binary was
  rebuilt. No additional harness installs or existing Brainiac MCP configurations
  were found on this device. Current private access settings remain local.
- All reachable file history and tracked working files passed the privacy scan.
  Runtime data/configuration remain ignored, and requested commit attribution
  is preserved.

## Project lessons and notification review (1.4.0)

- Project lessons retain approach/assumption, observed outcome, actionable
  correction, evidence/scope, classification, confidence, tags and provenance.
  Immutable project/entry identifiers and same-project task references passed
  isolation checks. Current user instructions take precedence over stored lessons.
- Lightweight indexes and revision listings exclude long text; exact tags and
  project/task/agent/kind/confidence/status filtering passed. Active-only default
  lists exclude superseded entries. Guarded PATCH conflicts preserve prior content,
  and historical revisions remain immutable and survive database reopen.
- Concurrent incident-key retries create one entry/event. Progress reports can
  include up to 20 lessons; invalid later entries roll back progress, presence,
  lessons and numbered events together. Events contain durable lesson references
  and support exact lessonId filtering without changing cursor semantics.
- HTTP and stdio SDK clients exercised all six lesson tools and both new Markdown
  resource templates. Numeric revision path schemas now match SDK validation for
  lesson and knowledge history. The deployed service exposes 41 tools and six
  resource templates; read-only smoke checks passed on both live transports.
- Chromium passed live project lesson previews, individual content retrieval,
  search, revision refresh, supersession, safe Markdown and project isolation.
  Notification review passed more than 100 filtered updates, strict increasing
  order, next/previous pages, recent-window selection, retained project/agent
  filters, live additions and mobile layout. Earlier compact progress/agent/usage
  views and attention-only notices continue to pass.
- Go tests, race detection, vet, Python checks and both skill validators passed.
  The native stdio binary was rebuilt and both installed Codex skill packages
  match the served packages. Skill guidance favors relevant index reads and
  repeat-preventing corrections in existing milestone batches, not per-action
  calls or unverified permanent preferences.
- The Compose deployment is healthy on port 3011; original project/task IDs,
  knowledge revisions and numbered history survived recreation. Private listener
  and allowlist settings match the local configuration and remain ignored.
  The first real lesson preserves the user's explicit attention-only input
  correction; acceptance fixtures were confined to disposable services.
- All reachable tracked history and new working files passed the privacy scan;
  requested author/committer attribution is retained.

## Project-only notification APIs and authorized history clear (1.5.0)

- The global /api/v1/notifications and since routes now return 404 for reads and
  writes. REST and MCP notification reads/messages require an immutable project
  path/ID. Payload/query project mismatches are rejected. Tests with identical
  agent and recipient labels in separate projects confirm no cross-project
  delivery; unrelated writes do not complete a project's long-poll read.
- Browser /notifications requires a project selection and never combines feeds.
  Chromium passed empty initial selection, choosing a project, filtering, paging
  and live refresh. Homepage combined history is hidden; project recent updates
  stay scoped. Explicit all-project overview/attention SSE streams preserve the
  user's cross-project heads-up alerts without reopening the global REST feed.
- Discovery returns the selected project's notification endpoint/cursor, and
  both installed skill packages teach per-project/filter cursors and project-only
  messages. The HTTP/stdio MCP smoke checks passed against the deployed service;
  notification tools require projectId. Attention-list MCP reads also require it.
- Local clear-notifications administration deletes only notification rows, does
  not run migrations or reset sqlite_sequence, and is not exposed through API/MCP.
  Tests confirm entity state, independent guidance, revision histories and held
  cursors survive clearing, and future updates never reuse notification numbers.
- At the user's explicit request, 535 existing notification rows were removed
  from the deployment. All project feeds were verified empty; project/task IDs,
  lesson indexes, knowledge and attention notices remained intact. Existing cursor
  high-water marks were preserved. Future project changes can create new updates.
- Full Go tests, race detection, vet, Python checks, skill validation and browser
  acceptance passed. Compose remains healthy on port 3011; both served/installed
  packages and the native stdio bridge were refreshed. Private listener/allowlist
  settings remain unchanged and ignored; tracked/new content passed the privacy
  scan. Requested Git attribution is retained.

## Direct project notification navigation (1.5.1)

The header Notifications link on project, task, README and lessons views now
includes the current project ID. Browser checks verified that project, README
and lessons links open the notification feed with that project already selected.
Existing Go acceptance tests passed. No skill or API behavior changed.

## Vendor icons, clipboard and harness identity (1.6.0)

- The compact per-agent table replaces vendor text with locally bundled vendor
  icons; hover names and keyboard-accessible buttons copy the vendor name.
  OpenAI, Anthropic, Z.ai and Google assets were checked, with source/license
  notices retained. Other/unknown providers use a local initial badge. Browsers
  make no external icon requests.
- Chromium verified vendor names, icon loading, Clipboard API copy, selection-copy
  fallback with the Clipboard API unavailable, copy feedback, the separate Harness
  column, retained identity through live updates and mobile horizontal scrolling.
  Existing progress, limits, subtasks, lessons and project notifications passed.
- Harness metadata is independently reported and bounded in activity/usage REST
  and MCP schemas. Persistence, omitted-field retention, whitespace handling,
  invalid-value rollback and project-specific precedence over shared usage passed.
  Two projects using the same agent ID retain their different harnesses; vendors
  are not inferred from host applications. Old records remain valid and unknown.
- Go tests, race detection, vet, Python checks, both skill validators and the
  unchanged Python fallback self-test passed. HTTP/stdio MCP checks also passed
  against the deployed service and rebuilt local bridge.
- Compose is healthy on port 3011. Existing project/task IDs, knowledge revisions
  and private listener/allowlist settings were preserved. Both Brainiac packages
  were refreshed in Codex and OpenCode (four installed packages); served, tracked
  and installed content matches, including the existing saved local origins.
  Guidance reports actual harness at start/change in existing work batches.
- Tracked/new content passed the private-data scan. Requested Git attribution
  is retained; synthetic browser records remained in the disposable instance.
