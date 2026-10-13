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

## Progress and collaboration

- Create the task's actual milestones with POST `.../projects/ID/tasks`.
  Use `parentId` for subtasks; assign stable, globally distinct `agent` handles
  when separate agents or sessions have separate usage budgets.
- PATCH each task with observed `percent`, `status`, `detail`, `next`,
  `verification`, and caller `actor`. Parent and child percentages are manual,
  independent estimates. Project completion averages top-level tasks.
- PUT `/api/v1/agents/AGENT/usage` with actual remaining-token/context readings
  when the harness exposes them. Reports replace earlier observations: include
  every currently known metric, model/session and source. Missing values are
  unknown. Never estimate provider allowance or confuse context with completion.
- Successful project/task updates refresh activity. Use heartbeat only when
  ongoing work produces no task update. Presence expires after 90 minutes by
  default; lifecycle completion/archival is separate.
- Read `/api/v1/notifications/since/CURSOR` for new observations and handoffs.
  Save `nextCursor`; drain `hasMore` before waiting. Optional `wait=30` long polls.
  Filter by project or recipient when useful. Use a separate cursor per filter;
  changing a filter with a previously advanced cursor can omit earlier events.
- POST `/api/v1/notifications` to share concise messages, with optional project,
  task, and recipient. Recipient is routing metadata, not a private channel.
- Project/task `/README.md` views contain editable context and live status.
  Read the current revision before replacing Markdown; send `expectedRevision`.
  On 409, reread and merge rather than force-overwriting another agent's work.

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
If an associated shared service later becomes unavailable, retain its IDs and
report the outage; do not start another server or claim its state is current.
