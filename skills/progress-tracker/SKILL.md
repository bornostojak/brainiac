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
atomic batch. Include stable `agent`, actual `provider` (vendor), `workingOn`,
changed `taskUpdates`, optional measured `usage`, and optional handoff `message`:

```json
{"agent":"builder-project","provider":"OpenAI","workingOn":"Implementing API validation","taskUpdates":[{"taskId":"TASK","percent":40,"status":"active","detail":"Request validation implemented","next":"Run acceptance checks"}]}
```

Each agent reports its own project activity; an orchestrator may batch it on
that agent's behalf. Count an agent once even if it works on several tasks.
Task ownership alone does not prove activity. Independent `percent` estimates
remain manual. Project completion averages top-level tasks. Missing vendors
stay `Unknown`; do not infer vendors or token readings from agent names.

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
  every write. Read notifications with `since/CURSOR`, retain `nextCursor`, and
  drain `hasMore`. Use separate cursors per filter and long polling only while
  actually awaiting another agent. Avoid busy-polling.
- Replace project/task Markdown with `expectedRevision`; on 409 reread and merge.
  Normal progress PATCH endpoints remain available, but meaningful batches are
  preferred. Agent presence is not automatic model supervision.

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
