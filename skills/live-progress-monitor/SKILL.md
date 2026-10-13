---
name: live-progress-monitor
description: Prefer a running Brainiac progress tracker, or deploy the original temporary Python progress board when unavailable. Provides percentages, agent ownership and curl reporting for task progress; not production application monitoring.
---

# Live Progress Monitor

## Prefer the shared tracker

When this task already has an associated board/backend, reuse it. Otherwise,
first run the discovery helper in the sibling `progress-tracker` skill:
`python3 ABSOLUTE_PROGRESS_TRACKER_PATH/scripts/discover.py --ensure --workspace /absolute/repository --name 'Project name'`.
It checks the configured service origin or the default local Brainiac service,
validates identity/API compatibility, and reuses an exact project key/ID. If it
returns `backend: brainiac`, follow [Progress Tracker](../progress-tracker/SKILL.md)
and use the returned project URL/API. Do not deploy this Python board.

If it returns `backend: legacy`, continue below without repeating discovery.
An invalid explicit project ID is an error, not a reason to create a fallback.
Do not start Docker automatically, migrate an old board, or silently change an
existing task's backend. The original script and its API remain the fallback.

Use the bundled `scripts/progress_monitor.py`: one Python 3 file with embedded
HTML/CSS/JavaScript and standard-library networking. It is independent of the
current project, requires no pip packages, and selects an available random port
by default. Copying that file alone is enough to deploy the monitor elsewhere.

## Start or resume a board

Resolve the script path relative to this SKILL.md and run it using an absolute
path. Prefer a distinct private temporary directory for each task's JSON state
and launch information. Do not replace another task's board or stop its server.
When a board is already associated with this task, reuse its URL and saved state.

1. Define the task's actual milestones in a JSON config with `title` and
   `features`. Each feature has a unique lowercase slug `id`, `name`, `percent`,
   `status`, optional `detail`, `next`, `verification`, and `agent`.
   Assign stable agent names when multiple agents report into the same board.
2. Start the script with `--config PATH --state PATH --port 0 --info-file PATH`.
   For LAN viewing use `--host 0.0.0.0 --advertise-host ACTUAL_HOST_IP`; otherwise
   keep the default loopback bind. Discover a reachable interface address rather
   than advertising `0.0.0.0` or another machine's loopback.
3. Keep the process in a retained terminal or a named transient user service.
   Leave it running when the user asked for a hosted monitor. This alone does
   not install a boot service or change firewall/network configuration.
4. Read the startup JSON (stdout or `--info-file`) for the kernel-selected port,
   `/progress` URL, API URL, PID and state path. Verify `/healthz`, JSON reporting
   and the browser page before returning the full reachable URL.

Example configuration:

```json
{"title":"Example task","features":[
  {"id":"build","name":"Implementation","percent":0,"status":"pending","agent":"builder","next":"Implement the accepted scope"},
  {"id":"verify","name":"Acceptance checks","percent":0,"status":"pending","agent":"reviewer","next":"Verify observable behavior"}
]}
```

The default four-item board is only a starter; adapt it to the user's task.
Percentages are human estimates, not elapsed time, coverage or release guarantees.
The overall percentage is the equal-weight feature average. Record real evidence
and the next milestone; do not simulate progress merely to demonstrate the API.
UI details, history and API instructions are collapsed for a compact overview.

## Update via curl

Batch reports at work start, meaningful milestones/phase changes, blockers,
handoffs and completion. During uninterrupted work, check in around every
10–15 minutes. Do not report each command, file edit or reasoning step, or read
the board repeatedly without a coordination need. Reuse saved IDs/URLs. With
Brainiac, prefer one `report_work` call combining task updates and agent activity;
the original Python fallback uses its existing PATCH API at the same cadence.

Use the selected port from startup JSON. Local writes require loopback curl:

```bash
curl --fail-with-body -X PATCH http://127.0.0.1:PORT/api/features/build \
  -H 'Content-Type: application/json' \
  -d '{"agent":"builder","percent":40,"status":"active","detail":"Core implementation complete; acceptance remains.","next":"Run the acceptance checks"}'
```

Use values representing observed progress. Status is `pending`, `partial`,
`active`, `done`, or `blocked`; percent is a finite number from 0 to 100.
`verification` is a short free-text description of the evidence.
Feature IDs are immutable; `updatedAt` is assigned by the server.

- `POST /api/progress` also updates one feature, with `id` in the JSON body.
- `POST /api/features` adds a feature with a new ID.
- `PUT /api/progress` replaces the complete title/feature configuration. Use
  only when intentionally resetting/restructuring the board; omitted features
  are removed. Normal progress updates should use PATCH instead.

Writes are serialized, persisted atomically and broadcast to browsers via SSE.
Restart using the same `--state` path to resume; saved state wins over `--config`.
Default temporary state can be lost to system cleanup. Choose durable storage
when the user needs the board to survive reboot. Only one process owns a state.

## Reports for agents

Other agents can query without opening a browser or reading conversation logs:

```bash
curl --fail --max-time 3 http://HOST:PORT/healthz
curl --fail --max-time 5 http://HOST:PORT/README.md
curl --fail --max-time 5 http://HOST:PORT/api/summary
curl --fail --max-time 5 'http://HOST:PORT/api/summary?agent=builder&format=text'
curl --fail --max-time 5 'http://HOST:PORT/api/features?status=blocked'
curl --fail --max-time 5 http://HOST:PORT/api/features/build
curl --fail --max-time 5 http://HOST:PORT/api/agents
```

The server exposes a plain Markdown API guide at `/README.md`, `/api/README.md`
and `/api/progress/README.md`. Thus appending `/README.md` to the returned API
URL also works. Read it to discover the live server's supported routes and curl
examples; it includes the actual advertised host, selected port and feature ID.

Use `curl --get --data-urlencode 'agent=NAME'` for names containing punctuation.
`/api/features` and `/api/summary` accept exact `agent` and `status` filters,
plus `format=json|text`. JSON reports contain current feature details, next
milestones, timestamps, status counts and per-agent aggregates. No matches
produce an empty list and a null percentage, not a misleading zero result.
`/api/agents` lists agents and their aggregate progress; it does not start work,
send messages or supervise them. Assigning an agent is metadata only.

Check health first with a bounded timeout. If the monitor is unavailable, report
that fact rather than inventing a progress report or restarting somebody else's
process. Use timestamps to distinguish a recent report from an old observation.
`GET /api/progress` returns the complete board; `/events` streams snapshots and
reconnects to the latest state. The last 80 update records are retained.

## Access and optional usage

LAN reads are public. Default writes are loopback-only and browser writes are
disabled. If remote agents need to update, supply `--token-file PATH` pointing
to a private 0600 file and send `Authorization: Bearer TOKEN`. The simple server
uses HTTP; use an HTTPS proxy if update credentials cross an untrusted network.
Never put credentials, private keys or private transcripts in public reports.

Optional `--codex-usage` reads account allowance every minute through the local
signed-in Codex CLI; `--codex-bin PATH` selects it. It uses the documented
[app-server](https://learn.chatgpt.com/docs/app-server) `account/rateLimits/read`
method, not a model turn, login change or reset redemption. Only actual windows,
remaining percentages and reset timestamps appear. Account quotas are not
per-task token budgets. Missing readings stay unknown; failed refreshes label
the previous reading stale. Generic use does not require Codex or an API key.

Run `python3 ABSOLUTE_SCRIPT_PATH --self-test` after changes. It tests actual
HTTP update/reporting, validation, persistence, SSE and stale usage behavior
against disposable files and a random loopback port. Stop only a monitor you
own when the user requests it; retain the state unless cleanup was requested.
