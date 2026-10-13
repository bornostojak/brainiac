'use strict';
(() => {
  const node = (tag, text, cls) => { const n = document.createElement(tag); if (text !== undefined) n.textContent = text; if (cls) n.className = cls; return n; };
  const host = node('aside', undefined, 'input-popups');
  host.setAttribute('aria-label', 'User input requests across projects');
  host.setAttribute('aria-live', 'polite');
  document.body.append(host);
  const nav = document.querySelector('header nav');
  const pendingButton = node('button', 'Input requests', 'alert-button');
  pendingButton.hidden = true;
  const desktopButton = node('button', 'Desktop alerts', 'alert-button');
  desktopButton.hidden = !('Notification' in window) || !window.isSecureContext;
  if (nav) nav.append(pendingButton, desktopButton);
  const load = key => { try { return JSON.parse(sessionStorage.getItem(key) || '{}'); } catch { return {}; } };
  const save = (key, value) => { try { sessionStorage.setItem(key, JSON.stringify(value)); } catch {} };
  let requests = [], dismissed = load('brainiac.dismissedInputs'), notified = load('brainiac.notifiedInputs');
  let timer, refreshing, rerun = false, stream, live = false;
  const cards = new Map();
  const target = request => '/projects/' + encodeURIComponent(request.projectId) + (request.taskId ? '/tasks/' + encodeURIComponent(request.taskId) : '');

  function notify(request) {
    const key = request.id + ':' + request.revision;
    if (desktopButton.hidden || Notification.permission !== 'granted' || notified[key]) return;
    try {
      const notice = new Notification('Input required: ' + request.title, {
        body: request.projectName + ' · ' + request.message, tag: 'brainiac-input-' + request.id,
      });
      notice.onclick = () => { window.focus(); location.href = target(request); notice.close(); };
      notified[key] = true;
      const keys = Object.keys(notified);
      if (keys.length > 200) delete notified[keys[0]];
      save('brainiac.notifiedInputs', notified);
    } catch {} // In-page alerts always work, even without browser permissions.
  }

  function card(request) {
    const box = node('section', undefined, 'input-popup');
    box.setAttribute('role', 'alert');
    box.dataset.requestId = request.id;
    const head = node('div', undefined, 'line');
    head.append(node('strong', 'Input required · ' + request.title));
    const dismiss = node('button', 'Dismiss', 'alert-button');
    dismiss.type = 'button';
    dismiss.onclick = () => { dismissed[request.id] = request.revision; save('brainiac.dismissedInputs', dismissed); render(); };
    head.append(dismiss);
    const context = node('a', request.projectName + ' · ' + request.agent);
    context.href = target(request);
    const message = node('p', request.message);
    const form = node('form');
    const answer = node('textarea');
    answer.maxLength = 8000;
    answer.required = true;
    answer.placeholder = 'Your response…';
    answer.setAttribute('aria-label', 'Response to ' + request.title);
    const send = node('button', 'Send response');
    send.type = 'submit';
    const error = node('p', '', 'input-error');
    error.setAttribute('role', 'status');
    form.append(answer, send, error);
    form.onsubmit = async event => {
      event.preventDefault();
      if (!answer.value.trim()) return;
      send.disabled = true;
      error.textContent = '';
      try {
        const response = await fetch('/api/v1/input-requests/' + encodeURIComponent(request.id), {
          method: 'PATCH', headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ status: 'resolved', response: answer.value, expectedRevision: request.revision, actor: 'browser-user' }),
        });
        if (!response.ok) {
          const body = await response.json();
          if (response.status === 409) await refresh();
          throw Error(body.error?.message || 'Unable to send response');
        }
        await refresh();
      } catch (e) { error.textContent = e.message; }
      finally { send.disabled = false; }
    };
    box.append(head, context, message, form);
    return box;
  }

  function render() {
    const visible = new Set();
    for (const request of requests) {
      if (dismissed[request.id] === request.revision) continue;
      visible.add(request.id);
      if (!cards.has(request.id)) { const box = card(request); cards.set(request.id, box); host.append(box); }
    }
    for (const [id, box] of cards) if (!visible.has(id)) { box.remove(); cards.delete(id); }
    pendingButton.hidden = requests.length === 0;
    pendingButton.textContent = requests.length + ' input request' + (requests.length === 1 ? '' : 's');
    if (!desktopButton.hidden) {
      desktopButton.textContent = Notification.permission === 'granted' ? 'Desktop alerts on' : Notification.permission === 'denied' ? 'Desktop alerts blocked' : 'Desktop alerts';
      desktopButton.disabled = Notification.permission !== 'default';
    }
  }

  async function refresh() {
    if (refreshing) { rerun = true; return refreshing; }
    refreshing = (async () => {
      try {
        const response = await fetch('/api/v1/input-requests', { cache: 'no-store' });
        if (!response.ok) throw Error('Unable to load input requests');
        const body = await response.json();
        requests = body.requests.sort((a, b) => a.createdAt.localeCompare(b.createdAt));
        const open = new Set(requests.map(r => r.id));
        for (const id of Object.keys(dismissed)) if (!open.has(id)) delete dismissed[id];
        save('brainiac.dismissedInputs', dismissed);
        requests.forEach(notify);
        render();
      } catch { if (!timer) timer = setTimeout(() => { timer = null; refresh(); }, 15000); }
      finally { refreshing = null; if (rerun) { rerun = false; schedule(); } }
    })();
    return refreshing;
  }
  function schedule() { if (!timer) timer = setTimeout(() => { timer = null; refresh(); }, 100); }
  function onEvent(event) { if (event.kind.startsWith('input.')) schedule(); }
  pendingButton.onclick = () => { dismissed = {}; save('brainiac.dismissedInputs', dismissed); render(); host.firstElementChild?.scrollIntoView({ block: 'nearest' }); };
  desktopButton.onclick = async () => { try { await Notification.requestPermission(); requests.forEach(notify); render(); } catch {} };
  window.BrainiacAlerts = { refresh, onEvent };

  // Markdown/browser pages have no dashboard stream, but still receive alerts.
  if (!document.body.hasAttribute('data-progress-page')) {
    (async () => {
      let cursor = 0;
      try { cursor = (await (await fetch('/api/v1/notifications?limit=1')).json()).latestCursor; } catch {}
      await refresh();
      stream = new EventSource('/events?since=' + cursor);
      stream.onopen = () => { live = true; refresh(); };
      stream.onerror = () => { live = false; };
      stream.addEventListener('update', event => { try { onEvent(JSON.parse(event.data)); } catch {} });
    })();
    setInterval(() => { if (!live) refresh(); }, 15000);
    window.addEventListener('pagehide', () => stream?.close());
  }
})();
