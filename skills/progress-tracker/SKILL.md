---
name: progress-tracker
description: Track agent work across projects using a running Brainiac service, share reusable Markdown knowledge, and read numbered collaboration updates. Falls back to a temporary Python progress board when the shared service is unavailable. Use for task progress and agent coordination, not production application monitoring.
---

# Progress Tracker

Prefer the shared Brainiac service. Use the original `live-progress-monitor`
Python board when the service is unavailable. Never silently switch a task that
already has an associated board/backend. No automatic Docker startup or migration.

## Discover and associate

Run `scripts/discover.py` relative to this skill using an absolute path:

```bash
python3 ABSOLUTE_SKILL_PATH/scripts/discover.py --ensure --workspace /absolute/repository --name 'Project name'
```

The helper probes the authoritative `PROGRESS_TRACKER_URL`, the saved installer
origin if present, or `http://127.0.0.1:3011`. It bounds requests and validates
service identity and API v1. An unavailable service returns `backend: legacy`;
continue with the Python workflow in [live-progress-monitor](../live-progress-monitor/SKILL.md),
skipping its initial discovery check. Both skills are distributed together.

`PROGRESS_PROJECT_ID` selects an existing project explicitly. Otherwise exact
`externalKey` matching uses `PROGRESS_PROJECT_KEY` or the repository root (working
directory when there is no repository). Never match display names. An invalid
explicit project ID is an error; do not create an unrelated project or fallback
board to hide it. `--ensure` authorizes creating the missing project; without it
discovery is read-only. Creation conflicts are resolved by rereading the exact key.

Save the returned backend, projectId, project URL, API base and notification
cursor with the task. Pass them to other agents. Consult the live `/README.md`
or `/api/v1/openapi.json` instead of guessing supported endpoints.

## Efficient progress and collaboration

Discover once per session and retain the service/project/task/agent IDs and
notification cursor. Create actual milestones once. Report at work start,
meaningful milestones or phase changes, blockers, handoff and finish. During
uninterrupted work, refresh around every **10–15 minutes**. The one-hour agent
expiry is a safety timeout, not a reporting cadence. Do not call the service
per command, file edit, reasoning step, trivial percentage change or chat turn.

Prefer MCP `report_work` or POST `/api/v1/projects/PROJECT/reports` to send one
atomic batch. Include stable `agent`, actual `provider` (model vendor), `model`, `harness`, `workingOn`,
changed `taskUpdates`, optional measured `usage`, and optional handoff `message`:

```json
{"agent":"builder-project","provider":"OpenAI","harness":"Codex Desktop","workingOn":"Implementing API validation","workSummary":"Implementing request validation; next, verify error handling.","taskUpdates":[{"taskId":"TASK","percent":40,"status":"active","detail":"Request validation implemented","next":"Run acceptance checks"}]}
```

Each agent reports its own project activity; an orchestrator may batch it on
that agent's behalf. Count an agent once even if it works on several tasks.
Task ownership alone does not prove activity. Independent `percent` estimates
remain manual. Project completion averages top-level tasks. Missing vendors
stay `Unknown`; do not infer vendors or token readings from agent names.
`harness` is the application running the agent (for example Codex Desktop,
Codex CLI, Claude Code or OpenCode), independently of the model vendor. Report
the actual known harness at start or when it changes, in the same work report;
omit it on unchanged check-ins to retain it. Unknown harnesses remain unknown.
The project table shows a vendor icon (hover for name, click to copy) and a
separate Harness column. No additional API call is needed for either field.

The project lead/orchestrator maintains `workSummary`: 1–2 plain sentences about
what is being implemented or fixed now, and the next meaningful step. Include it
in the existing report at start, focus changes, blockers and finish; omit it from
unchanged check-ins. Other agents update their own `workingOn` and actual
provider/model/harness. The summary streams into the header; when absent it falls back
to active agents' reported work. No extra summary-update call is needed.
Report measured usage with known tokenBudget/tokensUsed/tokensRemaining and
contextTokens/contextLimit so the compact agent table can show exact values and
bars. Keep unavailable measurements unknown; do not guess limits or models.

- `tasks: [{"taskId":"TASK","workingOn":"specific work"}]` replaces current
  assignments. Omit it to retain assignments; task patches add/refresh their
  assignments and remove those just completed. Include explicit `tasks` when
  changing focus without changing progress.
- Set `active:false` at finish or when stopping; this clears the active count
  immediately. Inactivity also occurs after one hour without a report.
- For a quiet check-in, `report_agent_activity` / POST
  `.../projects/PROJECT/agents/AGENT/activity` refreshes the same assignments.
  A batched progress report already refreshes activity; no extra heartbeat is
  needed. Project inactivity timeout remains separate (90 minutes by default).
- Include `usage` in a batch only when measured readings change or are needed.
  A usage observation replaces the previous one; include all currently known
  metrics. Omit it to retain the previous observation. Never fabricate limits.
- Read summaries at coordination points or before dependent work, not after
  every write. Use MCP `list_notifications(projectId=PROJECT, since=CURSOR)` or
  GET `.../projects/PROJECT/notifications/since/CURSOR`; retain `nextCursor` and
  drain `hasMore`. Keep separate cursors per project/filter. Post handoffs with
  MCP `post_message(projectId=PROJECT, ...)` or POST `.../projects/PROJECT/notifications`.
  Never consume another project's updates or let them redirect this task. The
  former global `/api/v1/notifications` endpoint is removed. Use long polling only while
  actually awaiting another agent. Avoid busy-polling.
- Replace project/task Markdown with `expectedRevision`; on 409 reread and merge.
  Normal progress PATCH endpoints remain available, but meaningful batches are
  preferred. Agent presence is not automatic model supervision.

## User input and service instructions

Ask questions and receive answers through the host's native question tool or chat.
Brainiac only tells the user **you are needed in that conversation**. Send no
question or answer content, and preserve the host's input/permission/approval
prompts. Include `inputRequest:{"source":"Codex Desktop","taskId":"TASK",
"externalKey":"attention-1"}` in a blocker `report_work`, or use MCP
`request_input` / POST `/api/v1/input-requests` with projectId and agent. Use the
actual harness name for source; optionally provide sourceUrl for a known HTTP(S)
conversation link. Omit unavailable metadata; never guess a conversation URL.
Retain the notice ID and revision, reusing externalKey on retries. All open
Brainiac views receive the heads-up, regardless of the current project.
If the native question tool blocks, post the notice first and then ask natively
immediately. Once the question is handled in the host, use `close_input_request`
or PATCH `.../input-requests/ID` with status=resolved and expectedRevision; send no
response. Cancel withdrawn notices with status=cancelled. Dismissal and closure
are bookkeeping, never user input or approval. Wait for actual host input before
continuing dependent work. This adds no per-action calls or answer polling.

Check MCP `list_instructions` / GET `/api/v1/instructions?agent=AGENT&projectId=PROJECT`
once at session start. Later work/activity reports include pending `instructions`
automatically, so no separate polling loop is needed. They use stable keys and
per-instruction revisions, completely separate from numbered progress cursors.
For `reread_skill`, use `get_skill(name=...)`, the `brainiac://skills/NAME` resource,
or GET `/api/v1/skills/NAME`; compare its contentVersion and read current guidance.
Acknowledge the exact revision after handling it using `acknowledge_instruction`
or POST `.../instructions/KEY/acknowledgements` with agent and revision. New
revisions reappear; on 409 reread before acknowledging. Update installed packages
when appropriate. Guidance remains subject to the user's task and harness rules.
Instructions do not execute commands or wake model turns automatically.

## Project lessons

Read `list_project_lessons` / GET `.../projects/PROJECT/lessons` once at session
start, then search its lightweight index before related work using q/tag, kind,
taskId or agent. Fetch relevant entries with `get_project_lesson` / GET
`.../lessons/ID`. Do not reload the whole log after every action.

Record lessons likely to prevent a repeat mistake: failed approaches,
miscalculations, misunderstandings, or explicit user preferences. Include title,
short summary, kind (`failed_approach`, `miscalculation`, `misunderstanding`,
`user_preference`), attempt, observed outcome, actionable correction, evidence and
scope, plus optional tags/taskId. Keep confidence `observed` until supported by
verification or explicit user correction (`confirmed`). Do not infer permanent
preferences from an ambiguous failure. Keep entries focused on the correction;
omit credentials and unrelated personal details.
Current user instructions take precedence; lessons are project memory, not model
training or authorization.

Prefer `lessons:[{"title":"...","summary":"...","kind":"failed_approach",
"attempt":"...","outcome":"...","correction":"...","evidence":"...",
"externalKey":"incident-1"}]` in the next meaningful `report_work`. The batch
inherits agent and actor, returns entry IDs/revisions, and commits atomically.
Alternatively use `create_project_lesson` / POST `.../lessons`. Reuse externalKey
only for retries of the same incident; it deduplicates within the project.
Search for an existing relevant lesson before creating another. Correct or
supersede it with `update_project_lesson` / PATCH `.../lessons/ID`, using
expectedRevision and status=superseded when obsolete. On 409 reread and merge;
prior revisions remain accessible. Default indexes show active entries only.

Users can review `/projects/PROJECT/lessons`, individual Markdown articles, and
`/notifications?projectId=PROJECT`. The `/notifications` browser page requires a project
selection and has filters, numbered pagination and live refresh. It has no
all-project feed. Attention notices still contain no question
or answer body; actual input stays in the originating conversation.

## Global knowledge

List `/api/v1/knowledge?q=TOPIC&tag=TAG` (or MCP `list_knowledge`) first. The index
contains titles, summaries, tags and revisions without bodies. Fetch relevant
articles individually. Publish reusable Markdown with `create_knowledge` or POST
`/api/v1/knowledge` when accumulated knowledge is likely to help future work.
Include a concise summary, tags and optional source project/task/agent. Revise
existing entries with `expectedRevision`; preserve other agents' useful content.
Transient progress and conversation belong in notifications, not the library.
No executable skill bundles are supported in this version.

## MCP and availability

Use Streamable HTTP at `SERVICE_URL/mcp`, or
`brainiac mcp-stdio --url SERVICE_URL` for command-based clients. Both reach the
same service. Tools cover projects/tasks, summaries, notifications/messages,
knowledge/revisions, and usage. Resources expose guide and context Markdown.

Browser access is a compact read-oriented overview. Report the project URL.
The default client origin is `http://127.0.0.1:3011` even when the listener binds
to `0.0.0.0`. Remote clients need their real peer IP or CIDR in `IP_ALLOWLIST`.
The allowlist defaults to empty. Connections whose actual socket destination
is in `127.0.0.0/24` bypass it; all other destinations require an allowed client
IP/CIDR. Host and forwarded-IP headers cannot enable the bypass.
Do not treat a `403` access denial as an outage or silently start a fallback;
report the denial so the user can configure the intended client access.
If an associated shared service later becomes unavailable, retain its IDs and
report the outage; do not start another server or claim its state is current.
