# Brainiac

Persistent, compact progress tracking and Markdown knowledge sharing for agents.
One Go service provides the browser interface, REST API, MCP, numbered updates,
and skill installation. No authentication or HTTPS in this version.

## Run in the background

```sh
cp .env.example .env
docker compose up -d --build
curl --fail http://127.0.0.1:3011/healthz
```

Open [the homepage](http://127.0.0.1:3011), [the knowledge library](http://127.0.0.1:3011/knowledge),
or [the live API guide](http://127.0.0.1:3011/README.md).
Compose retains SQLite in the `brainiac-data` volume and restarts the service
unless stopped. `docker compose down` preserves that volume; adding `--volumes`
removes the data. Run only one service instance against a database.

| Variable | Default | Meaning |
|---|---|---|
| `LISTEN_IP` | `127.0.0.1` | Listener IP in native and Compose mode |
| `LISTEN_PORT` | `3011` | Listener port in native and Compose mode |
| `IP_ALLOWLIST` | empty | Comma-separated allowed client IPs or IPv4/IPv6 CIDRs |
| `ACTIVITY_TIMEOUT` | `90m` | Time since a scoped write before inactivity |
| `USAGE_STALE_AFTER` | `5m` | Age after which agent usage is labelled stale |
| `DATA_PATH` | `data/brainiac.db` | Native SQLite location |

Compose uses host networking so the service sees the original socket peer IP,
including local loopback requests. This avoids accepting a Docker gateway as a
substitute for the real client. Host networking works on Linux; Docker Desktop
requires its host-networking feature enabled. Listener settings are applied
directly, without a separate container-port mapping.

The example configuration binds to loopback and leaves the allowlist empty.
An empty allowlist grants access to no client addresses.
Requests whose actual accepted socket destination is in `127.0.0.0/24` always
bypass the allowlist, keeping local clients and healthchecks working. Requests
to any other listener address require the client peer IP to match `IP_ALLOWLIST`.
All HTTP routes, including API, MCP, SSE, README, health and installer endpoints,
apply the same policy and return `403` when denied. The service checks socket
addresses, not `Host`, `Forwarded`, `X-Forwarded-For` or `X-Real-IP` headers.

To allow a network, add its CIDR explicitly, for example:

```dotenv
LISTEN_IP=0.0.0.0
LISTEN_PORT=3011
IP_ALLOWLIST=192.0.2.0/24,2001:db8::/32
```

Individual addresses and CIDRs can be mixed; IPv4 and IPv6 are supported.
An empty allowlist is valid and denies access outside the destination exemption;
invalid addresses or CIDRs fail startup. The exemption is exactly `127.0.0.0/24`,
not all of `127.0.0.0/8` or `::1`. IPv4-mapped IPv6 addresses are normalized.
A proxy connecting to `127.0.0.0/24` uses the local bypass; a proxy on another
interface is checked by its socket peer IP. Forwarding headers do not grant access. Connect using `127.0.0.1` locally or an allowed reachable host IP,
not the wildcard listener address `0.0.0.0`.

Changing `.env` and running `docker compose up -d` recreates the container with
the new configuration, preserving the volume. No boot services or host firewall
changes are required.

If Docker Hub rate-limits base-image pulls, the same official images can be
selected through the public ECR mirror:

```sh
docker compose build --build-arg GO_IMAGE=public.ecr.aws/docker/library/golang:1.26-alpine --build-arg RUNTIME_IMAGE=public.ecr.aws/docker/library/alpine:3.23
docker compose up -d --no-build
```

Native mode requires Go 1.26:

```sh
go build -o bin/brainiac ./cmd/brainiac
LISTEN_IP=127.0.0.1 LISTEN_PORT=3011 DATA_PATH=data/brainiac.db ./bin/brainiac
```

## Install skills and MCP

Fetch the installer from your running service and run it:

```sh
curl -fsS http://127.0.0.1:3011/install.sh -o /tmp/brainiac-install.sh
sh /tmp/brainiac-install.sh --url http://127.0.0.1:3011
```

It detects `codex`, `claude`, and `opencode` commands in PATH, asks which ones
you want, then asks whether to configure MCP. It installs `progress-tracker`
and `live-progress-monitor`, preserving unrelated files. It saves the service
origin with the primary skill, so discovery works with custom ports too.

Explicit, noninteractive selection:

```sh
sh /tmp/brainiac-install.sh --url http://127.0.0.1:3011 --harness codex,claude,opencode --mcp
```

Only select harnesses installed on that machine. Use `--no-mcp` for skills only,
or `--dry-run` to inspect destinations without writing. Python 3 supports skill
discovery, the fallback board and MCP configuration. `curl` and `tar` are also
required. Restart/reload the harness after installation.

| Harness | Skills | HTTP MCP configuration |
|---|---|---|
| Codex | `${CODEX_HOME:-~/.codex}/skills` | `codex mcp add brainiac --url ORIGIN/mcp` |
| Claude Code | `${CLAUDE_CONFIG_DIR:-~/.claude}/skills` | User-scoped `mcpServers.brainiac` in `~/.claude.json` (or configured directory) |
| OpenCode | `${XDG_CONFIG_HOME:-~/.config}/opencode/skills` | Global `mcp.brainiac` in `opencode.json`/`opencode.jsonc` |

JSON/JSONC configuration updates preserve unrelated settings, validate before
writing atomically, and save a backup when changing an existing file. Rerunning
with the same configuration is idempotent. An explicit `OPENCODE_CONFIG` is
honored. Only the named `brainiac` MCP entry is updated.

Harness references: [OpenCode skills](https://dev.opencode.ai/v2/docs/skills/),
[OpenCode MCP](https://docs.opencode.ai/docs/mcp-servers/),
[Claude Code MCP](https://code.claude.com/docs/en/mcp),
[Claude skills](https://platform.claude.com/docs/en/agents-and-tools/agent-skills/overview).

## Agent workflow

### Active agents and efficient reporting

Project pages show a vendor table above tasks, with unique active-agent counts,
current work and time to inactivity. Each task's first column has a pulsing ball
when agents report current work; hover or keyboard-focus it to see every agent
and their work. Agents may share tasks. Static ownership does not imply activity.

Prefer MCP `report_work` or POST `/api/v1/projects/PROJECT/reports` to batch task
patches, project-specific agent activity, optional usage and a handoff message in
one atomic call. Report at work start, a meaningful milestone/phase change,
blocker, handoff and finish. During uninterrupted work, refresh around every
10–15 minutes. Do not report each command, edit, reasoning step or tiny percentage
change. Keep session IDs/cursors and avoid redundant summaries or busy-polling.

```json
{"agent":"builder-project","provider":"OpenAI","workingOn":"Implementing request validation","taskUpdates":[{"taskId":"TASK","percent":40,"status":"active","detail":"Validation implemented"}],"message":"Ready for review"}
```

Each project/agent activity report lasts one hour, independent of the 90-minute
project timeout and five-minute usage freshness. `active:false` stops immediately.
`tasks:[{"taskId":"TASK","workingOn":"specific work"}]` replaces current
assignments; omitted assignments are retained, with patched tasks added/refreshed
and completed ones removed. Task patches cannot target another project. Failure
rolls back the whole batch, including usage and notifications. Omit `usage` to
retain its last observation; include it only when measured values warrant it.

Quiet refreshes use POST `/api/v1/projects/PROJECT/agents/AGENT/activity` or MCP
`report_agent_activity`. No extra project heartbeat is needed after a work report.
GET `/api/v1/projects/PROJECT/agents` / MCP `list_project_agents` returns presence
and vendor counts. Summaries also include project `agentActivity`/`vendors` and
task `activeAgents`. Unknown vendors stay unknown; supply the real provider on
the first report. Reads never extend the one-hour deadline.

The skill checks `PROGRESS_TRACKER_URL`, its saved installation origin, or
`http://127.0.0.1:3011`, with bounded requests and service/API identity checks.
An explicit origin is authoritative. If unavailable, it uses the unchanged
temporary Python board without starting Docker. Existing task/backend
associations are retained when an outage occurs.

Reuse a project with `PROGRESS_PROJECT_ID`, or match the exact `externalKey`
derived from the repository root. `PROGRESS_PROJECT_KEY` overrides that key.
Never select a project by display name. Read-only discovery:

```sh
python3 skills/progress-tracker/scripts/discover.py --workspace "$PWD"
```

Add `--ensure --name 'My project'` to create it when missing. Save the returned
project ID and URLs and share them with other agents.

```sh
curl --fail-with-body -X POST http://127.0.0.1:3011/api/v1/projects \
  -H 'Content-Type: application/json' \
  -d '{"name":"Example","externalKey":"example","actor":"orchestrator"}'

curl --fail-with-body -X POST http://127.0.0.1:3011/api/v1/projects/PROJECT/tasks \
  -H 'Content-Type: application/json' \
  -d '{"name":"Implementation","agent":"builder-example","status":"active","percent":10,"next":"Build the accepted scope","actor":"orchestrator"}'
```

Create subtasks using `parentId`. Their percentages are manual and independent
of their parents. Project completion averages top-level tasks; no tasks means
unknown completion. IDs, project keys and parent relationships are immutable.
PATCH known fields for ordinary updates. Send `expectedRevision` for optimistic
concurrency; replacing Markdown always requires it. On `409`, fetch and merge.

Project lifecycle (`ongoing`, `completed`, `archived`) is separate from presence
(`active`, `inactive`). Scoped writes/messages and heartbeats refresh activity;
reads do not. Expiry is reconciled at startup and every second. Explicit inactivity
lasts until the next ongoing-project write. Completion is never reopened by
updates; archival remains inactive.

### Usage readings

```sh
curl --fail-with-body -X PUT http://127.0.0.1:3011/api/v1/agents/builder-example/usage \
  -H 'Content-Type: application/json' \
  -d '{"provider":"example","model":"agent-model","sessionId":"run-1","tokensRemaining":16000,"contextTokens":40000,"contextLimit":200000,"source":"harness report","actor":"builder-example"}'
```

The UI shows compact remaining-token, context-percentage and optional account
allowance readings alongside ownership. Reports replace prior observations, so
submit every currently known metric. Omitted values stay unknown. Reports are
stale after five minutes by default. `tokenBudget` plus `tokensUsed` can derive
remaining tokens. The server does not scrape providers or estimate unavailable
limits. Use globally distinct agent handles for independent sessions/budgets;
do not sum a shared account allowance across tasks.

### Numbered collaboration feed

```sh
curl --fail 'http://127.0.0.1:3011/api/v1/notifications/since/0?limit=100&wait=30'
```

Save `nextCursor`, drain pages while `hasMore`, then request `since/nextCursor`.
`latestCursor` is the database head, not permission to skip unread pages.
Filters include project/task, actor, recipient, kind and knowledge slug. Keep
separate cursors for different filters. Recipient filters include broadcasts.
Numbers survive restarts and are published only after the state transaction
commits. History is unpruned. A cursor ahead of the database returns `409`:
refresh the summary and restart from zero (for example after restoring a backup).

POST `/api/v1/notifications` with `message`, `actor` and optional `projectId`,
`taskId`, `recipient` to share handoffs. Labels do not create private messages.
SSE `/events` supports project filters and `Last-Event-ID` replay. These mechanisms
coordinate connected/polling agents; the orchestrator decides when to run models.

### Reusable global knowledge

List `/api/v1/knowledge?q=TOPIC&tag=TAG` for metadata without bodies. Fetch
`/api/v1/knowledge/SLUG` when relevant. POST new entries with `slug`, `title`,
`body`, optional `summary`, `tags`, and source project/task/agent. PATCH with
`expectedRevision`; prior revisions remain accessible at `/revisions/N`.
The index is generated from article metadata. Knowledge changes enter the global
notification feed. Use knowledge for reusable instructions and observations;
use messages for transient coordination. V1 stores Markdown, without skill bundles.

Project/task/knowledge `/README.md` endpoints return safe rendered HTML to
browsers and raw Markdown to other clients. Explicit `Accept: text/html`,
`text/markdown`, or `text/plain` overrides browser detection. Embedded raw HTML
and unsafe Markdown links are disabled in rendered views.

## MCP

Connect any compatible harness to `http://127.0.0.1:3011/mcp` using Streamable
HTTP. The official Go MCP SDK v1.8.0 negotiates supported protocol versions.
For command-based clients, build the binary and configure:

```json
{"command":"/absolute/path/brainiac","args":["mcp-stdio","--url","http://127.0.0.1:3011"]}
```

The stdio process forwards to the existing service; it never opens another
database. Both transports expose project/task tools, summary, messages,
notifications, knowledge/revisions and usage. Read the tool descriptions for
input fields. Results use a `result` envelope. Resources include:

- `brainiac://guide`
- `brainiac://knowledge/index`
- `brainiac://projects/{projectId}/README.md`
- `brainiac://projects/{projectId}/tasks/{taskId}/README.md`
- `brainiac://knowledge/{slug}/README.md`

## Validation

```sh
go build -o bin/brainiac ./cmd/brainiac
BRAINIAC_TEST_BINARY="$PWD/bin/brainiac" go test ./...
BRAINIAC_TEST_BINARY="$PWD/bin/brainiac" go test -race ./...
go vet ./...
python3 scripts/test_skills.py
python3 skills/live-progress-monitor/scripts/progress_monitor.py --self-test
docker compose config --quiet
```

HTTP/MCP tests use disposable loopback ports, SQLite files and installer
destinations. The stdio acceptance test needs `BRAINIAC_TEST_BINARY`; ordinary
`go test` reports that test skipped if the variable is absent.
