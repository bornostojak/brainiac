'use strict';
(() => {
  const $ = id => document.getElementById(id);
  const el = (tag, text, cls) => { const n = document.createElement(tag); if (text !== undefined) n.textContent = text; if (cls) n.className = cls; return n; };
  const link = (text, href) => { const n = el('a', text); n.href = href; return n; };
  const parts = location.pathname.split('/').filter(Boolean);
  const lessons = parts[0] === 'projects' && parts[2] === 'lessons', pid = lessons ? parts[1] : null;
  const lessonKinds = ['failed_approach', 'miscalculation', 'misunderstanding', 'user_preference'];
  const eventKinds = ['project.created', 'project.updated', 'project.work_summary', 'project.inactive', 'project.heartbeat', 'task.created', 'task.updated', 'agent.activity', 'agent.inactive', 'agent.usage', 'message', 'knowledge.created', 'knowledge.updated', 'input.required', 'input.resolved', 'input.cancelled', 'lesson.created', 'lesson.updated'];
  const human = s => s.replaceAll('_', ' '), date = s => new Date(s).toLocaleString();
  let entries = [], page = null, start = 0, previous = [], cursor = 0, stream, live = false, timer, generation = 0;
  const opened = new Set(), articles = new Map();
  const projectNames = new Map();
  let applied = new URLSearchParams();
  function applyFilters() {
    applied = new URLSearchParams();
    for (const [id,key] of [['review-kind','kind'],['review-task','taskId'],['review-agent',lessons?'agent':'actor'],['review-recipient','recipient'],['review-project','projectId'],['review-status','status'],['review-confidence','confidence'],['review-tag','tag']]) {
      if ($(id).value && (lessons || !['status','confidence','tag'].includes(key))) applied.set(key,$(id).value);
    }
  }
  async function json(url) { const r = await fetch(url, { cache: 'no-store' }); if (!r.ok) { const b = await r.json(); throw Error(b.error?.message || 'HTTP ' + r.status); } return r.json(); }
  function fields(box, d) {
    for (const [key, title] of [['attempt', 'Attempt / assumption'], ['outcome', 'Observed outcome'], ['correction', 'Correction / future approach'], ['evidence', 'Evidence / scope']]) {
      const field = el('div'); field.append(el('small', title), el('p', d[key] || 'Not recorded')); box.append(field);
    }
  }
  function render() {
    const list = $('review-list'); list.replaceChildren();
    const query = $('review-search').value.toLowerCase();
    const visible = lessons ? entries : entries.filter(d => [d.message, d.actor, d.kind, d.recipient, d.number, projectNames.get(d.projectId)].join(' ').toLowerCase().includes(query));
    for (const d of visible) {
      const key = lessons ? d.id : String(d.number), row = el('details', undefined, 'review-item' + (lessons ? ' lesson-row' : ' notification-row'));
      row.dataset.key = key; row.open = opened.has(key);
      const head = el('summary', undefined, 'review-row'), title = el('span', lessons ? d.title : '#' + d.number + ' · ' + d.message, 'review-message');
      head.append(title, el('span', lessons ? human(d.kind) : d.kind, 'badge'), el('small', lessons ? d.confidence + ' · ' + d.status : d.actor || 'Unattributed'), el('small', date(lessons ? d.updatedAt : d.at)));
      const body = el('div', undefined, 'review-detail');
      if (lessons) {
        body.append(el('p', d.summary || 'No summary', 'muted'));
        const content = el('div', undefined, 'lesson-content'); body.append(content);
        body.append(link('Read Markdown', '/projects/' + pid + '/lessons/' + d.id + '/README.md'), el('small', ' · revision ' + d.revision + ' · ' + (d.agent || 'Unattributed') + ' · ' + d.tags.join(' · ')));
        const loadArticle = async () => {
          if (!row.open) return;
          const cacheKey = d.id + ':' + d.revision;
          try {
            let article = articles.get(cacheKey);
            if (!article) { content.textContent = 'Loading lesson…'; article = await json('/api/v1/projects/' + pid + '/lessons/' + d.id); articles.set(d.id + ':' + article.revision, article); }
            content.replaceChildren(); fields(content, article);
          } catch (e) { content.textContent = e.message; }
        };
        row.addEventListener('toggle', loadArticle);
      } else {
        body.append(el('p', d.message));
        if (d.projectId) body.append(link(projectNames.get(d.projectId) || 'Project', '/projects/' + d.projectId));
        if (d.taskId && d.projectId) body.append(el('span', ' · '), link('Task', '/projects/' + d.projectId + '/tasks/' + d.taskId));
        if (d.lessonId && d.projectId) body.append(el('span', ' · '), link('Lesson', '/projects/' + d.projectId + '/lessons/' + d.lessonId + '/README.md'));
        if (d.knowledgeSlug) body.append(el('span', ' · '), link('Knowledge', '/knowledge/' + encodeURIComponent(d.knowledgeSlug) + '/README.md'));
        if (d.recipient) body.append(el('p', 'Recipient: ' + d.recipient, 'muted'));
      }
      row.addEventListener('toggle', () => { if (row.open) opened.add(key); else if (row.isConnected) opened.delete(key); });
      row.append(head, body); list.append(row);
    }
    if (!visible.length) list.append(el('p', lessons ? 'No matching lessons recorded.' : 'No matching notifications on this page.', 'empty'));
    $('review-count').textContent = lessons ? visible.length + ' indexed lessons · fetch details only when needed' : visible.length + ' of ' + entries.length + ' updates on this page · oldest first · latest #' + (page?.latestCursor || 0);
    $('review-next').disabled = !page?.hasMore; $('review-previous').disabled = previous.length === 0;
  }
  async function refresh() {
    const request = ++generation;
    const q = new URLSearchParams(applied);
    if (lessons) { if ($('review-search').value) q.set('q', $('review-search').value); }
    else { q.set('since', String(start)); q.set('limit', '100'); }
    $('review-next').disabled = true; $('review-previous').disabled = true;
    try {
      const data = await json(lessons ? '/api/v1/projects/' + pid + '/lessons?' + q : '/api/v1/notifications?' + q);
      if (request !== generation) return;
      page = lessons ? null : data; entries = lessons ? data.lessons : data.notifications; render();
      if (!live) $('connection').textContent = 'Polling · reconnecting';
    } catch (e) { if (request === generation) { $('review-count').textContent = e.message; $('connection').textContent = 'Offline · last received state'; } }
  }
  function schedule() { if (!timer) timer = setTimeout(() => { timer = null; refresh(); }, 150); }
  function connect() {
    stream = new EventSource('/events?since=' + cursor);
    stream.onopen = () => { live = true; $('connection').textContent = '● Live'; refresh(); window.BrainiacAlerts.refresh(); };
    stream.onerror = () => { live = false; $('connection').textContent = 'Reconnecting'; };
    stream.addEventListener('cursor', e => { cursor = Number(e.data); });
    stream.addEventListener('update', e => { try { const n = JSON.parse(e.data); cursor = n.number; window.BrainiacAlerts.onEvent(n); if (!lessons || n.projectId === pid && n.kind.startsWith('lesson.')) schedule(); } catch {} });
  }
  $('review-filters').onsubmit = e => { e.preventDefault(); previous = []; start = Number($('review-since').value) || 0; applyFilters(); refresh(); };
  $('review-search').oninput = () => { if (lessons) schedule(); else render(); };
  $('review-next').onclick = () => { if (!page?.hasMore) return; previous.push(start); start = page.nextCursor; $('review-since').value = start; refresh(); };
  $('review-previous').onclick = () => { if (!previous.length) return; start = previous.pop(); $('review-since').value = start; refresh(); };
  $('review-recent').onclick = () => { previous = []; start = Math.max(0, (page?.latestCursor || 0) - 100); $('review-since').value = start; refresh(); };
  (async () => {
    $('review-title').textContent = lessons ? 'Project lessons' : 'Notifications'; document.title = $('review-title').textContent + ' · Brainiac';
    $('review-description').textContent = lessons ? 'Failures, mistaken assumptions and user corrections. Read relevant entries before related work; current user instructions take precedence.' : 'Review the numbered project and agent update history. Filters apply to the feed; search covers the current page.';
    for (const kind of lessons ? lessonKinds : eventKinds) $('review-kind').append(new Option(human(kind), kind));
    for (const id of ['status-label', 'confidence-label', 'tag-label']) $(id).hidden = !lessons;
    for (const id of ['project-label', 'recipient-label', 'since-label', 'review-pagination']) $(id).hidden = lessons;
    try {
      const projects = (await json('/api/v1/projects')).projects;
      for (const p of projects) { projectNames.set(p.id, p.name); $('review-project').append(new Option(p.name, p.id)); }
      if (lessons) { $('review-title').textContent = 'Lessons · ' + (projectNames.get(pid) || 'Project'); $('review-links').append(link('Project', '/projects/' + pid), link('README index', '/projects/' + pid + '/lessons/README.md'), link('Project notifications', '/notifications?projectId=' + pid)); }
      else { const filter = new URLSearchParams(location.search).get('projectId'); if (filter) $('review-project').value = filter; }
      cursor = (await json('/api/v1/notifications?limit=1')).latestCursor;
    } catch (e) { $('review-count').textContent = e.message; }
    applyFilters(); await Promise.all([refresh(), window.BrainiacAlerts.refresh()]); connect();
  })();
  setInterval(() => { if (!live) { refresh(); window.BrainiacAlerts.refresh(); } }, 15000);
  window.addEventListener('pagehide', () => stream?.close());
})();
