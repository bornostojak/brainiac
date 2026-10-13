#!/usr/bin/env python3
"""Dependency-free progress board. Run --help or --self-test; no project imports."""
import argparse
import copy
import datetime as dt
import hmac
import ipaddress
import json
import math
import os
from pathlib import Path
import queue
import re
import subprocess
import tempfile
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlsplit, parse_qs

STATUSES = {'pending', 'partial', 'active', 'done', 'blocked'}
FEATURE_FIELDS = {'id', 'name', 'percent', 'status', 'detail', 'next', 'verification', 'agent'}


def now():
    return dt.datetime.now(dt.timezone.utc).isoformat()


def number(value):
    return not isinstance(value, bool) and isinstance(value, (int, float)) and math.isfinite(value)


def decode(data):
    def reject(value):
        raise ValueError('Non-finite JSON numbers are not supported')
    return json.loads(data, parse_constant=reject)


def feature(value, complete=False):
    if not isinstance(value, dict) or set(value) - FEATURE_FIELDS:
        raise ValueError('Unknown feature field or invalid object')
    item = dict(value)
    if not isinstance(item.get('id'), str) or not re.fullmatch(r'[a-z0-9][a-z0-9-]{0,63}', item['id']):
        raise ValueError('id must be a lowercase slug, up to 64 characters')
    if complete:
        item = {'name': item['id'], 'percent': 0, 'status': 'pending', 'detail': '', 'next': '', 'verification': '', 'agent': '', **item}
    if 'percent' in item and (not number(item['percent']) or not 0 <= item['percent'] <= 100):
        raise ValueError('percent must be a finite number from 0 through 100')
    if 'status' in item and item['status'] not in STATUSES:
        raise ValueError('Invalid status')
    for key, maximum in [('name', 200), ('detail', 2000), ('next', 2000), ('verification', 200), ('agent', 128)]:
        if key in item and (not isinstance(item[key], str) or len(item[key]) > maximum):
            raise ValueError(f'{key} must be text, up to {maximum} characters')
    return item


def configuration(value):
    if not isinstance(value, dict) or set(value) - {'title', 'features'}:
        raise ValueError('Board configuration accepts only title and features')
    title = value.get('title', 'Progress monitor')
    if not isinstance(title, str) or not title.strip() or len(title) > 200:
        raise ValueError('title must be nonempty text, up to 200 characters')
    values = value.get('features')
    if not isinstance(values, list) or not 1 <= len(values) <= 200:
        raise ValueError('features must contain between 1 and 200 items')
    items = [feature(item, True) for item in values]
    if len({item['id'] for item in items}) != len(items):
        raise ValueError('Duplicate feature id')
    return {'title': title, 'features': items}


class Board:
    def __init__(self, path, seed):
        self.path = Path(path)
        self.condition = threading.Condition(threading.RLock())
        self.stopped = threading.Event()
        self.version = 0
        self.usage = {'status': 'unavailable', 'windows': [], 'updatedAt': None, 'error': 'Optional Codex usage collection is disabled.'}
        if self.path.exists():
            saved = decode(self.path.read_text())
            checked = configuration({'title': saved['title'], 'features': [{key: value for key, value in item.items() if key != 'updatedAt'} for item in saved['features']]})
            for original, item in zip(saved['features'], checked['features']):
                if isinstance(original.get('updatedAt'), str):
                    item['updatedAt'] = original['updatedAt']
            self.state = {**checked, 'updatedAt': saved.get('updatedAt', now()), 'events': saved.get('events', [])[:80]}
        else:
            self.state = {**configuration(seed), 'updatedAt': now(), 'events': []}
            self.save(self.state)

    def save(self, value):
        self.path.parent.mkdir(parents=True, exist_ok=True)
        fd, temporary = tempfile.mkstemp(prefix=self.path.name + '.', dir=self.path.parent)
        try:
            with os.fdopen(fd, 'w') as handle:
                json.dump(value, handle, allow_nan=False)
                handle.flush()
                os.fsync(handle.fileno())
            os.replace(temporary, self.path)
        finally:
            if os.path.exists(temporary):
                os.unlink(temporary)

    def snapshot(self):
        with self.condition:
            result = copy.deepcopy(self.state)
            result['percent'] = round(sum(item['percent'] for item in result['features']) / len(result['features']), 1)
            result['sessionUsage'] = copy.deepcopy(self.usage)
            return result

    def change(self, method, path, payload):
        with self.condition:
            updated = copy.deepcopy(self.state)
            if method == 'PUT' and path == '/api/progress':
                checked = configuration(payload)
                updated.update(checked)
                label, detail = 'Board', 'Board configuration replaced'
            elif method == 'POST' and path == '/api/features':
                item = feature(payload, True)
                if any(node['id'] == item['id'] for node in updated['features']):
                    raise ValueError('Feature already exists')
                if len(updated['features']) >= 200:
                    raise ValueError('At most 200 features are supported')
                updated['features'].append(item)
                label, detail = item['name'], 'Feature added'
            else:
                if method == 'PATCH':
                    if not isinstance(payload, dict):
                        raise ValueError('Expected JSON object')
                    identifier = path.rsplit('/', 1)[-1]
                    if payload.get('id', identifier) != identifier:
                        raise ValueError('Feature id does not match URL')
                    payload = {**payload, 'id': identifier}
                patch = feature(payload)
                if len(patch) < 2:
                    raise ValueError('Provide at least one field to update')
                item = next((node for node in updated['features'] if node['id'] == patch['id']), None)
                if item is None:
                    raise ValueError('Unknown feature id')
                item.update(patch)
                label, detail = item['name'], patch.get('detail', f"{item['status']} · {item['percent']}%")
            updated['updatedAt'] = now()
            for item in updated['features']:
                if method == 'PUT' or (payload.get('id') == item['id']):
                    item['updatedAt'] = updated['updatedAt']
            updated['events'].insert(0, {'at': updated['updatedAt'], 'name': label, 'detail': detail})
            updated['events'] = updated['events'][:80]
            self.save(updated)
            self.state = updated
            self.version += 1
            self.condition.notify_all()
            return self.snapshot()

    def publish_usage(self, observation=None):
        with self.condition:
            self.usage = observation or {**self.usage, 'status': 'stale' if self.usage['windows'] else 'unavailable', 'error': 'Codex usage could not be read.'}
            self.version += 1
            self.condition.notify_all()


def codex_usage(binary):
    """Read the documented quota API, exposing no raw account/auth metadata."""
    child = subprocess.Popen([binary, 'app-server', '--stdio'], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True)
    messages = queue.Queue()
    def receive():
        for line in child.stdout:
            try:
                messages.put(json.loads(line))
            except ValueError:
                pass
        messages.put(None)
    threading.Thread(target=receive, daemon=True).start()
    def send(value):
        child.stdin.write(json.dumps(value) + '\n')
        child.stdin.flush()
    deadline = time.monotonic() + 15
    try:
        send({'method': 'initialize', 'id': 1, 'params': {'clientInfo': {'name': 'live_progress_monitor', 'version': '1.0.0'}}})
        while True:
            message = messages.get(timeout=max(0.01, deadline - time.monotonic()))
            if message is None or message.get('error'):
                raise ValueError('Usage unavailable')
            if message.get('id') == 1:
                send({'method': 'initialized', 'params': {}})
                send({'method': 'account/rateLimits/read', 'id': 2})
            elif message.get('id') == 2:
                result = message.get('result', {})
                bucket = (result.get('rateLimitsByLimitId') or {}).get('codex') or result.get('rateLimits') or {}
                if bucket.get('limitId', 'codex') != 'codex':
                    raise ValueError('No Codex quota bucket')
                windows = []
                for key in ['primary', 'secondary']:
                    window = bucket.get(key)
                    if not window:
                        continue
                    used, minutes = window.get('usedPercent'), window.get('windowDurationMins')
                    if not number(used) or not 0 <= used <= 100 or not number(minutes) or minutes <= 0:
                        raise ValueError('Invalid quota observation')
                    reset = window.get('resetsAt')
                    windows.append({'label': {10080: 'Weekly', 300: '5-hour', 1440: 'Daily'}.get(minutes, f'{minutes}-minute'), 'remainingPercent': round(100 - used, 1), 'windowMinutes': minutes, 'resetsAt': dt.datetime.fromtimestamp(reset, dt.timezone.utc).isoformat() if number(reset) else None})
                if not windows:
                    raise ValueError('No quota windows')
                return {'status': 'live', 'updatedAt': now(), 'windows': windows}
    finally:
        child.terminate()
        try:
            child.wait(timeout=2)
        except subprocess.TimeoutExpired:
            child.kill()
            child.wait(timeout=2)
        child.stdin.close()
        child.stdout.close()


def collect_usage(board, binary):
    while not board.stopped.is_set():
        try:
            board.publish_usage(codex_usage(binary))
        except Exception:
            board.publish_usage()
        board.stopped.wait(60)


class Server(ThreadingHTTPServer):
    daemon_threads = True
    def __init__(self, address, board, token=''):
        self.board, self.token = board, token
        self.stream_slots = threading.BoundedSemaphore(32)
        super().__init__(address, Handler)
        self.public_base = f'http://127.0.0.1:{self.server_port}'


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def setup(self):
        super().setup()
        self.connection.settimeout(10)

    def reply(self, code, value, content_type='application/json'):
        content = json.dumps(value, allow_nan=False).encode() if content_type == 'application/json' else value.encode()
        self.send_response(code)
        self.send_header('Content-Type', content_type)
        self.send_header('Content-Length', str(len(content)))
        self.send_header('Cache-Control', 'no-store')
        self.send_header('X-Content-Type-Options', 'nosniff')
        self.end_headers()
        self.wfile.write(content)

    def do_GET(self):
        parsed = urlsplit(self.path)
        path = parsed.path
        if path in ['/', '/progress']:
            return self.reply(200, HTML, 'text/html; charset=utf-8')
        if path in ['/README.md', '/api/README.md', '/api/progress/README.md']:
            identifier = self.server.board.snapshot()['features'][0]['id']
            readme = API_README.replace('{{BASE}}', self.server.public_base).replace('{{LOCAL}}', f'http://127.0.0.1:{self.server.server_port}').replace('{{ID}}', identifier)
            return self.reply(200, readme, 'text/plain; charset=utf-8')
        if path == '/api/progress':
            return self.reply(200, self.server.board.snapshot())
        if path == '/healthz':
            return self.reply(200, {'ok': True, 'service': 'live-progress-monitor'})
        if path in ['/api/features', '/api/summary', '/api/agents']:
            try:
                query = parse_qs(parsed.query, keep_blank_values=True)
                if set(query) - {'agent', 'status', 'format'} or any(len(value) != 1 for value in query.values()):
                    raise ValueError('Unknown or repeated query filter')
                status = query.get('status', [''])[0]
                if status and status not in STATUSES:
                    raise ValueError('Invalid status filter')
                if query.get('format', ['json'])[0] not in ['json', 'text']:
                    raise ValueError('format must be json or text')
                snapshot = self.server.board.snapshot()
                items = [item for item in snapshot['features'] if ('agent' not in query or item.get('agent', '') == query['agent'][0]) and (not status or item['status'] == status)]
                counts = {name: sum(item['status'] == name for item in items) for name in sorted(STATUSES)}
                percent = round(sum(item['percent'] for item in items) / len(items), 1) if items else None
                agents = []
                for name in sorted({item.get('agent', '') for item in items}):
                    assigned = [item for item in items if item.get('agent', '') == name]
                    agents.append({'agent': name, 'features': len(assigned), 'percent': round(sum(item['percent'] for item in assigned) / len(assigned), 1), 'blocked': sum(item['status'] == 'blocked' for item in assigned)})
                report = {'title': snapshot['title'], 'updatedAt': snapshot['updatedAt'], 'percent': percent, 'counts': counts, 'agents': agents, 'features': items}
                if query.get('format') == ['text']:
                    text = snapshot['title'] + '\n' + '\n'.join(f"{item['id']}: {item['percent']}% {item['status']} | agent={item.get('agent') or 'unassigned'} | {item['detail']} | next={item['next']}" for item in items)
                    if not items:
                        text += 'No matching features or agent reports.\n'
                    return self.reply(200, text + '\n', 'text/plain; charset=utf-8')
                if path == '/api/agents':
                    return self.reply(200, {'agents': agents, 'updatedAt': snapshot['updatedAt']})
                return self.reply(200, report)
            except ValueError as error:
                return self.reply(400, {'error': str(error)})
        if re.fullmatch(r'/api/features/[a-z0-9][a-z0-9-]{0,63}', path):
            identifier = path.rsplit('/', 1)[-1]
            item = next((item for item in self.server.board.snapshot()['features'] if item['id'] == identifier), None)
            return self.reply(200, item) if item else self.reply(404, {'error': 'Unknown feature id'})
        if path == '/events':
            return self.events()
        self.reply(404, {'error': 'Not found'})

    def events(self):
        if not self.server.stream_slots.acquire(False):
            return self.reply(503, {'error': 'Too many viewers'})
        board = self.server.board
        try:
            self.send_response(200)
            self.send_header('Content-Type', 'text/event-stream')
            self.send_header('Cache-Control', 'no-store')
            self.end_headers()
            previous = -1
            while not board.stopped.is_set():
                with board.condition:
                    if previous == board.version:
                        board.condition.wait(15)
                    previous = board.version
                    message = json.dumps(board.snapshot(), allow_nan=False)
                self.wfile.write(f'data: {message}\n\n'.encode())
                self.wfile.flush()
        except (OSError, ValueError):
            pass
        finally:
            self.server.stream_slots.release()

    def mutate(self):
        path = urlsplit(self.path).path
        if not ((self.command in ['POST', 'PUT'] and path == '/api/progress') or (self.command == 'POST' and path == '/api/features') or (self.command == 'PATCH' and re.fullmatch(r'/api/features/[a-z0-9][a-z0-9-]{0,63}', path))):
            return self.reply(404, {'error': 'Not found'})
        address = ipaddress.ip_address(self.client_address[0])
        supplied = self.headers.get('Authorization', '')
        remote_allowed = self.server.token and hmac.compare_digest(supplied.encode(), f'Bearer {self.server.token}'.encode())
        if self.headers.get('Origin') or not (address.is_loopback or remote_allowed):
            return self.reply(403, {'error': 'Updates require loopback curl or a configured bearer token; browser writes are disabled'})
        if self.headers.get('Content-Type', '').split(';')[0] != 'application/json':
            return self.reply(415, {'error': 'Expected application/json'})
        try:
            if self.headers.get('Transfer-Encoding'):
                return self.reply(400, {'error': 'Chunked update bodies are unsupported'})
            length = int(self.headers.get('Content-Length', '-1'))
            if length < 0:
                return self.reply(411, {'error': 'Content-Length required'})
            if length > 65536:
                return self.reply(413, {'error': 'Update exceeds 64 KiB'})
            payload = decode(self.rfile.read(length))
            result = self.server.board.change(self.command, path, payload)
        except (ValueError, TypeError, UnicodeError) as error:
            return self.reply(400, {'error': str(error)})
        except OSError:
            return self.reply(503, {'error': 'Update could not be persisted; previous progress retained'})
        self.reply(200, result)

    do_POST = do_PATCH = do_PUT = mutate


API_README = r'''# Live Progress Monitor API

Base URL: {{BASE}}
Interactive board: {{BASE}}/progress
This guide: {{BASE}}/README.md
Aliases: /api/README.md and /api/progress/README.md

Reads are public. Default writes require curl on the monitor host using
loopback. Browser writes are disabled. For remote writes, start the server with
--token-file PRIVATE_FILE and pass Authorization: Bearer TOKEN. Do not put the
token or private information into progress details.

## Check readiness and read reports

```bash
curl --fail --max-time 3 '{{BASE}}/healthz'
curl --fail --max-time 5 '{{BASE}}/api/progress'
curl --fail --max-time 5 '{{BASE}}/api/summary'
curl --fail --max-time 5 '{{BASE}}/api/features/{{ID}}'
curl --fail --max-time 5 '{{BASE}}/api/agents'
curl --fail --max-time 5 '{{BASE}}/api/summary?agent=builder&format=text'
curl --fail --max-time 5 '{{BASE}}/api/features?status=blocked'
```

Reports accept exact agent/status filters and format=json|text. For an agent
name containing punctuation, use curl --get --data-urlencode 'agent=NAME'.
No matching reports produce an empty list and null percent, or an explicit
no-match message in text. Each changed feature includes its update timestamp.
Assigning an agent is report metadata; it does not start or delegate work.

## Update one feature

The following is an example. Use percentages and descriptions reflecting
observed progress, not the sample values merely to demonstrate the API.

```bash
curl --fail-with-body -X PATCH '{{LOCAL}}/api/features/{{ID}}' \
  -H 'Content-Type: application/json' \
  -d '{"agent":"builder","percent":40,"status":"active","detail":"Implementation underway","next":"Run acceptance checks"}'
```

POST /api/progress is equivalent when the JSON object also includes id.
IDs are immutable lowercase slugs of at most 64 characters. Editable fields:

| Field | Meaning |
| --- | --- |
| name | Display name, up to 200 characters |
| percent | Finite JSON number from 0 through 100 |
| status | pending, partial, active, done, blocked |
| detail | Current progress/evidence, up to 2000 characters |
| next | Next milestone, up to 2000 characters |
| verification | Evidence description, up to 200 characters |
| agent | Reporting/owning agent name, up to 128 characters |

updatedAt is server-assigned. Percent does not automatically change status.
Overall is the equal-weight feature average, not coverage or a time forecast.

## Add a milestone or replace the board

```bash
curl --fail-with-body -X POST '{{LOCAL}}/api/features' \
  -H 'Content-Type: application/json' \
  -d '{"id":"new-milestone","name":"New milestone","agent":"reviewer"}'
```

PUT /api/progress accepts {"title":"Task","features":[{"id":"build"}]} and
replaces the complete title/feature configuration. Omitted features disappear.
Use PATCH for ordinary updates; use PUT only for intentional restructuring.
At most 200 features and 64 KiB per update are accepted.

## Live updates, persistence and errors

GET /events streams server-sent JSON snapshots; reconnect retrieves the latest
state. Use curl -N '{{BASE}}/events' for an ongoing stream.
Writes are serialized and persisted before being broadcast. Last 80 changes
are retained. Restart with the same --state path to resume. Temporary state can
be lost to system cleanup. Only one process should own a state file.

400: invalid input/filter; 403: writes not authorized; 404: unknown route or
feature; 411: Content-Length required; 413: body over 64 KiB; 415: JSON content
type required; 503: persistence failure or too many stream viewers.
Failed persistence retains the previous progress state.

Optional sessionUsage shows sanitized Codex account allowance windows. It is
not a per-task token budget. Unavailable observations stay unknown; failed
refreshes retain a labelled stale reading. No credentials or raw RPC data are
exposed. This API is a task-progress board, not an application monitoring API.
'''

HTML = r'''<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Live progress</title>
<style>
:root{font:13px/1.45 system-ui,sans-serif;color:#293b33;background:#f3f5f0;--line:#dce3d9;--muted:#69766d}*{box-sizing:border-box}body{max-width:1250px;margin:auto;padding:22px}header,.line,.filters{display:flex;align-items:center;justify-content:space-between;gap:14px}header{margin-bottom:16px}.brand{font-size:18px;font-weight:650}.muted,small{font-size:11px;color:var(--muted)}h1{font-size:16px;margin:0}h2{font-size:12px;margin:0}button,input,select{font:inherit}input,select{padding:5px 8px;border:1px solid var(--line);border-radius:6px;background:white}input{width:190px}.overview{display:grid;grid-template-columns:1.5fr 1fr;gap:12px}.panel,.features{background:white;border:1px solid var(--line);border-radius:10px}.panel{padding:15px}.overall{display:flex;gap:16px;align-items:center}.overall>div{flex:1}#percent{font-size:30px;font-weight:650}progress{appearance:none;border:0;background:#e9eee6;border-radius:8px;overflow:hidden;height:6px;width:100%}progress::-webkit-progress-bar{background:#e9eee6}progress::-webkit-progress-value{background:#53775c}progress::-moz-progress-bar{background:#53775c}.stats{display:flex;gap:18px;margin-top:8px;font-size:11px}.toolbar{display:flex;align-items:center;justify-content:space-between;margin:17px 0 9px;gap:12px}.features{overflow:hidden}.head,.row{display:grid;grid-template-columns:minmax(160px,1fr) 95px minmax(130px,200px) 20px;align-items:center;gap:16px;padding:10px 16px}.head{font-size:10px;color:var(--muted);background:#eef2e9;padding-block:7px}.feature{border-top:1px solid #e8ece4}.row{cursor:pointer;list-style:none;min-height:40px}.row::-webkit-details-marker{display:none}.row:hover{background:#f7f9f4}.name{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.badge{font-size:10px;background:#f1f3ef;padding:2px 7px;border-radius:20px;justify-self:start}.badge.active,.badge.done{background:#e7f1e5;color:#346043}.badge.partial,.badge.blocked{background:#f6efdc;color:#75623b}.bar{display:flex;align-items:center;gap:10px}.bar strong{min-width:35px;text-align:right;font-size:12px}.chevron{color:#83917f}.feature[open] .chevron{transform:rotate(90deg)}.detail{background:#f9faf7;padding:12px 16px;border-top:1px solid #edf0e9;display:grid;grid-template-columns:1fr 1fr;gap:10px 20px}.detail p{margin:0;font-size:12px;overflow-wrap:anywhere}.meta{grid-column:1/-1;color:var(--muted);font-size:11px}.bottom{display:flex;gap:12px;margin-top:12px}.bottom>details{flex:1}.bottom summary{font-size:11px;cursor:pointer}.bottom p{font-size:11px;color:var(--muted)}pre{white-space:pre-wrap;overflow-wrap:anywhere;font-size:11px;background:#f2f5ef;padding:10px;border-radius:7px}summary:focus-visible,input:focus-visible,select:focus-visible{outline:2px solid #53775c;outline-offset:2px}.usage-window{margin-top:8px}.usage-window .line{font-size:11px}.empty{padding:20px;color:var(--muted)}.info{position:relative}.info div{max-width:310px;font-size:11px;color:var(--muted)}@media(max-width:650px){body{padding:14px}.overview{grid-template-columns:1fr}.row,.head{grid-template-columns:minmax(80px,1fr) 70px 95px 12px;gap:7px;padding-inline:10px}.toolbar{align-items:start;flex-direction:column}.detail{grid-template-columns:1fr}.bottom{flex-direction:column}.bottom>details{width:100%}.name{white-space:normal;font-size:11px}.filters{width:100%;gap:6px}input{flex:1;min-width:0;width:100px}}
</style><header><span class="brand">live / progress</span><span id="connection" class="muted">Connecting…</span></header>
<div class="overview"><section class="panel"><div class="overall"><div><h1 id="title">Progress</h1><small id="updated"></small><progress id="overall" max="100"></progress></div><strong id="percent">—</strong></div><div id="stats" class="stats"></div></section><section class="panel"><div class="line"><h2>Codex account allowance</h2><small id="usage-state">Optional</small></div><div id="usage"></div></section></div>
<div class="toolbar"><div><h2>Features <small id="count"></small></h2></div><div class="filters"><input id="search" type="search" aria-label="Search features" placeholder="Search features…"><select id="filter" aria-label="Status filter"><option value="all">All statuses</option><option>pending</option><option>partial</option><option>active</option><option>done</option><option>blocked</option></select></div></div>
<main class="features"><div class="head"><span>Feature · click for details</span><span>Status</span><span>Completion</span><span></span></div><div id="features"></div></main>
<div class="bottom"><details class="panel"><summary>Recent updates</summary><div id="history"></div></details><details class="panel"><summary>API & information</summary><p>GET <a href="/api/progress">/api/progress</a>, <a href="/api/summary">/api/summary</a> or <a href="/api/agents">/api/agents</a>. Filter reports with ?agent=NAME or ?status=blocked; add format=text for a short readable report. Use loopback curl to update progress, or a configured bearer token for remote writes. Percentages are human estimates, not test coverage or a time forecast. Overall is the equal-weight feature average. Account quota is separate from task completion; only reported windows appear.</p><pre id="example"></pre></details></div>
<script>
const $=id=>document.getElementById(id),el=(tag,text,cls)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n};let state,live=false;const opened=new Set();const date=s=>s?new Date(s).toLocaleString():'Not reported';
function rows(){if(!state)return;const query=$('search').value.toLowerCase(),filter=$('filter').value;const found=state.features.filter(n=>(filter==='all'||filter===n.status)&&[n.name,n.id,n.next,n.detail].join(' ').toLowerCase().includes(query));$('count').textContent=found.length+' / '+state.features.length;
$('features').replaceChildren(...found.map(n=>{const card=el('details',undefined,'feature');card.open=opened.has(n.id);card.addEventListener('toggle',()=>{if(card.open)opened.add(n.id);else if(card.isConnected)opened.delete(n.id)});const summary=el('summary',undefined,'row'),name=el('span',n.name,'name');name.title=n.name;const bar=el('div',undefined,'bar'),p=el('progress');p.max=100;p.value=n.percent;p.setAttribute('aria-label',n.name+' completion');bar.append(p,el('strong',n.percent+'%'));summary.append(name,el('span',n.status,'badge '+n.status),bar,el('span','›','chevron'));const detail=el('div',undefined,'detail');for(const [label,value]of [['Next milestone',n.next],['Evidence / current state',n.detail]]){const box=el('div');box.append(el('small',label),el('p',value||'Not recorded'));detail.append(box)}detail.append(el('div','ID: '+n.id+' · Verification: '+(n.verification||'Not recorded'),'meta'));card.append(summary,detail);return card}));if(!found.length)$('features').append(el('div','No matching features','empty'))}
function render(s){state=s;document.title=s.title;$('title').textContent=s.title;$('percent').textContent=s.percent+'%';$('overall').value=s.percent;$('updated').textContent='Updated '+date(s.updatedAt);$('stats').replaceChildren(...[s.features.length+' features',s.features.filter(n=>n.status==='done').length+' complete',s.features.filter(n=>n.status==='active').length+' active',s.features.filter(n=>n.status==='blocked').length+' blocked'].map(t=>el('span',t)));rows();$('history').replaceChildren(...s.events.slice(0,25).map(e=>el('p',date(e.at)+' · '+e.name+' — '+e.detail)));
const usage=s.sessionUsage||{status:'unavailable',windows:[]};$('usage-state').textContent=usage.status;const windows=usage.status==='unavailable'?[]:usage.windows;$('usage').replaceChildren(...windows.map(w=>{const box=el('div',undefined,'usage-window'),line=el('div',undefined,'line'),bar=el('progress');bar.max=100;bar.value=w.remainingPercent;bar.setAttribute('aria-label',w.label+' allowance remaining');line.append(el('span',w.label),el('strong',w.remainingPercent+'% left'));box.append(line,bar,el('small','Resets '+date(w.resetsAt)));return box}));if(!windows.length)$('usage').append(el('p',usage.error||'Unavailable','muted'));$('usage').title='Last reported '+date(usage.updatedAt);
const n=s.features[0];if(n){const body=JSON.stringify({percent:n.percent,status:n.status,detail:n.detail,next:n.next}).replace(/'/g,"'\\''");$('example').textContent='curl --fail-with-body -X PATCH http://127.0.0.1:'+location.port+'/api/features/'+n.id+' \\\n  -H \'Content-Type: application/json\' \\\n  -d \''+body+'\''}}
async function refresh(){try{const r=await fetch('/api/progress',{cache:'no-store'});if(!r.ok)throw Error();render(await r.json());if(!live)$('connection').textContent='Polling · reconnecting'}catch{$('connection').textContent='Offline · last received state'}}$('search').oninput=rows;$('filter').onchange=rows;refresh();const events=new EventSource('/events');events.onopen=()=>{live=true;$('connection').textContent='● Live'};events.onmessage=e=>{try{render(JSON.parse(e.data))}catch{refresh()}};events.onerror=()=>{live=false;$('connection').textContent='Reconnecting';refresh()};setInterval(()=>{if(!live&&!document.hidden)refresh()},15000);
</script></html>'''


def self_test():
    import unittest
    import urllib.error
    import urllib.request
    class Checks(unittest.TestCase):
        def test_api_and_persistence(self):
            with tempfile.TemporaryDirectory(prefix='progress-self-test-') as directory:
                seed = {'title': 'Test', 'features': [{'id': 'build'}, {'id': 'verify'}]}
                board = Board(Path(directory) / 'state.json', seed)
                server = Server(('127.0.0.1', 0), board)
                thread = threading.Thread(target=server.serve_forever, daemon=True)
                thread.start()
                base = f'http://127.0.0.1:{server.server_port}'
                def request(path, method='GET', data=None, headers=None):
                    return urllib.request.urlopen(urllib.request.Request(base + path, data=json.dumps(data).encode() if data is not None else None, method=method, headers={'Content-Type': 'application/json', **(headers or {})}), timeout=3)
                try:
                    self.assertEqual(request('/progress').status, 200)
                    for path in ['/README.md', '/api/README.md', '/api/progress/README.md']:
                        document = request(path)
                        body = document.read().decode()
                        self.assertEqual(document.headers['Content-Type'], 'text/plain; charset=utf-8')
                        self.assertIn(base + '/api/features/build', body)
                        self.assertNotIn('{{BASE}}', body)
                    self.assertEqual(len(decode(request('/api/progress').read())['features']), 2)
                    decode(request('/api/features/build', 'PATCH', {'percent': 70, 'status': 'active', 'agent': 'builder'}).read())
                    self.assertEqual(Board(board.path, seed).snapshot()['features'][0]['percent'], 70)
                    self.assertEqual(decode(request('/healthz').read())['ok'], True)
                    self.assertEqual(decode(request('/api/features/build').read())['agent'], 'builder')
                    report = decode(request('/api/summary?agent=builder').read())
                    self.assertEqual(report['percent'], 70)
                    self.assertEqual(len(report['features']), 1)
                    self.assertIsNone(decode(request('/api/summary?agent=missing').read())['percent'])
                    self.assertIn(b'build: 70%', request('/api/summary?agent=builder&format=text').read())
                    self.assertEqual(decode(request('/api/agents').read())['agents'][1]['agent'], 'builder')
                    for bad in [{'percent': 101}, {'percent': True}, {'unknown': 1}]:
                        with self.assertRaises(urllib.error.HTTPError) as caught:
                            request('/api/features/build', 'PATCH', bad)
                        self.assertEqual(caught.exception.code, 400)
                    with self.assertRaises(urllib.error.HTTPError) as caught:
                        request('/api/features/build', 'PATCH', {'percent': 90}, {'Origin': 'http://foreign.test'})
                    self.assertEqual(caught.exception.code, 403)
                    stream = request('/events')
                    self.assertTrue(stream.readline().startswith(b'data: '))
                    stream.close()
                    request('/api/features', 'POST', {'id': 'deliver'}).close()
                    self.assertEqual(len(board.snapshot()['features']), 3)
                    board.publish_usage({'status': 'live', 'updatedAt': now(), 'windows': [{'label': 'Weekly', 'remainingPercent': 0}]})
                    board.publish_usage()
                    self.assertEqual(board.snapshot()['sessionUsage']['status'], 'stale')
                    self.assertEqual(board.snapshot()['sessionUsage']['windows'][0]['remainingPercent'], 0)
                    request('/api/progress', 'PUT', {'title': 'Replacement', 'features': [{'id': 'only'}]}).close()
                    self.assertEqual(len(board.snapshot()['features']), 1)
                finally:
                    board.stopped.set()
                    with board.condition:
                        board.condition.notify_all()
                    server.shutdown()
                    server.server_close()
    result = unittest.TextTestRunner(verbosity=2).run(unittest.defaultTestLoader.loadTestsFromTestCase(Checks))
    raise SystemExit(0 if result.wasSuccessful() else 1)


def main():
    parser = argparse.ArgumentParser(description='Single-file live progress monitor; no Python packages required.')
    parser.add_argument('--host', default='127.0.0.1', help='Bind address; use 0.0.0.0 for LAN readers')
    parser.add_argument('--advertise-host', help='Reachable IP/hostname printed in URLs; never a bind wildcard')
    parser.add_argument('--port', type=int, default=0, help='0 selects a random available port (default)')
    parser.add_argument('--title', default='Progress monitor', help='Title for a new board')
    parser.add_argument('--config', type=Path, help='Initial JSON {title, features}; existing state wins')
    parser.add_argument('--state', type=Path, help='Persistent JSON path; default is a new private temporary directory')
    parser.add_argument('--info-file', type=Path, help='Write startup URLs, selected port, PID and state path as JSON')
    parser.add_argument('--token-file', type=Path, help='Optional private bearer-token file for remote writes')
    parser.add_argument('--codex-usage', action='store_true', help='Read optional account allowance every 60s; never starts a model turn')
    parser.add_argument('--codex-bin', default='codex', help='Codex executable for optional usage collection')
    parser.add_argument('--self-test', action='store_true', help='Run disposable API/persistence checks and exit')
    args = parser.parse_args()
    if args.self_test:
        self_test()
    if not 0 <= args.port <= 65535:
        parser.error('port must be 0 through 65535')
    if args.advertise_host in ['0.0.0.0', '::']:
        parser.error('advertise-host must be reachable, not a wildcard')
    seed = decode(args.config.read_text()) if args.config else {'title': args.title, 'features': [{'id': name.lower(), 'name': name} for name in ['Planning', 'Implementation', 'Verification', 'Delivery']]}
    state_path = args.state or Path(tempfile.mkdtemp(prefix='live-progress-')) / 'state.json'
    token = ''
    if args.token_file:
        if args.token_file.stat().st_mode & 0o077:
            parser.error('token-file must have private permissions (0600)')
        token = args.token_file.read_text().strip()
        if not token:
            parser.error('token-file is empty')
    board = Board(state_path, seed)
    server = Server((args.host, args.port), board, token)
    address = args.advertise_host or ('127.0.0.1' if args.host == '0.0.0.0' else args.host)
    url = f'http://{address}:{server.server_port}'
    server.public_base = url
    info = {'url': url + '/progress', 'api': url + '/api/progress', 'readme': url + '/README.md', 'localApi': f'http://127.0.0.1:{server.server_port}/api/progress', 'port': server.server_port, 'pid': os.getpid(), 'state': str(board.path.absolute())}
    if args.info_file:
        args.info_file.parent.mkdir(parents=True, exist_ok=True)
        fd = os.open(args.info_file, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, 'w') as handle:
            json.dump(info, handle)
    print(json.dumps(info), flush=True)
    if args.codex_usage:
        threading.Thread(target=collect_usage, args=(board, args.codex_bin), daemon=True).start()
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        board.stopped.set()
        with board.condition:
            board.condition.notify_all()
        server.server_close()


if __name__ == '__main__':
    main()
