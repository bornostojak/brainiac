/* Run only against a disposable service: this creates acceptance-test records. */
const { chromium } = require('playwright');
const assert = require('node:assert/strict');
const { randomUUID } = require('node:crypto');

const base = process.env.BRAINIAC_BROWSER_TEST_URL;
if (!base) throw Error('Set BRAINIAC_BROWSER_TEST_URL to a disposable Brainiac instance');
const output = process.env.BRAINIAC_SCREENSHOT || '/tmp/brainiac-browser.png';

async function api(method, path, payload) {
  const response = await fetch(base + '/api/v1' + path, {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: payload === undefined ? undefined : JSON.stringify(payload),
  });
  const result = await response.json();
  assert.ok(response.ok, JSON.stringify(result));
  return result;
}

(async () => {
  const project = await api('POST', '/projects', {
    name: 'Brainiac browser acceptance', externalKey: 'browser-test:' + randomUUID(),
    description: 'Disposable acceptance data for compact progress, nested tasks and usage.',
  });
  const root = await api('POST', `/projects/${project.id}/tasks`, {
    name: 'Implementation', percent: 55, status: 'active', agent: 'browser-builder', next: 'Run acceptance checks',
  });
  const child = await api('POST', `/projects/${project.id}/tasks`, {
    name: 'Subtask verification', parentId: root.id, percent: 10, status: 'pending', agent: 'browser-reviewer',
  });
  await api('PUT', '/agents/browser-builder/usage', {
    model: 'reported-model', tokenBudget: 20000, tokensUsed: 4000, tokensRemaining: 16000, contextTokens: 40000,
    contextLimit: 200000, accountRemainingPercent: 45, observedAt: new Date(Date.now() - 10 * 60 * 1000).toISOString(),
  });
  await api('POST', `/projects/${project.id}/reports`, {
    agent: 'browser-builder', provider: 'OpenAI', harness: 'Codex Desktop', workingOn: 'API implementation',
    workSummary: 'Building API validation; checking error responses next.',
    tasks: [{ taskId: root.id, workingOn: 'Building request validation' }],
  });
  await api('POST', `/projects/${project.id}/reports`, {
    agent: 'browser-helper', provider: 'OpenAI', harness: 'OpenCode', model: 'helper-model', workingOn: 'Reviewing edge cases',
    tasks: [{ taskId: root.id, workingOn: 'Reviewing edge cases' }],
    usage: { tokenBudget: 12000, tokensUsed: 4000, tokensRemaining: 8000, contextTokens: 10000, contextLimit: 50000, accountRemainingPercent: 70 },
  });
  await api('POST', `/projects/${project.id}/reports`, {
    agent: 'browser-reviewer', provider: 'Anthropic', harness: 'Claude Code', model: 'reviewer-model', workingOn: 'Checking acceptance',
    tasks: [{ taskId: child.id, workingOn: 'Acceptance verification' }],
    usage: { tokensRemaining: 9000 },
  });
  const browser = await chromium.launch({ headless: true });
  try {
    const page = await browser.newPage({ viewport: { width: 1200, height: 850 } });
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    await page.goto(base + '/projects/' + project.id);
    await page.waitForSelector('.item');
    assert.equal(await page.locator('.item').count(), 1);
    assert.ok((await page.locator('#overview').innerText()).includes('55%'));
    assert.equal(await page.getByRole('button', { name: 'Hide idle agents', exact: true }).getAttribute('aria-pressed'), 'true');
    assert.equal(await page.locator('.agent-table tbody tr').count(), 3);
    const builder = page.locator('.agent-table tr[data-agent="browser-builder"]');
    const helper = page.locator('.agent-table tr[data-agent="browser-helper"]');
    const reviewer = page.locator('.agent-table tr[data-agent="browser-reviewer"]');
    assert.equal(await builder.locator('.vendor-button').getAttribute('title'), 'OpenAI');
    assert.equal(await builder.locator('.vendor-button img').getAttribute('src'), '/assets/vendor-icons/openai.svg');
    assert.equal(await builder.locator('.agent-harness').innerText(), 'Codex Desktop');
    assert.equal(await helper.locator('.agent-harness').innerText(), 'OpenCode');
    assert.equal(await reviewer.locator('.agent-harness').innerText(), 'Claude Code');
    await page.context().grantPermissions(['clipboard-read', 'clipboard-write'], { origin: base });
    await builder.getByRole('button', { name: 'Copy OpenAI vendor name' }).click();
    assert.equal(await page.evaluate(() => navigator.clipboard.readText()), 'OpenAI');
    await page.evaluate(() => { window.testClipboard = navigator.clipboard; Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined }); });
    await reviewer.getByRole('button', { name: 'Copy Anthropic vendor name' }).click();
    assert.ok((await page.locator('.copy-status').innerText()).includes('Copied Anthropic'));
    await page.evaluate(() => Object.defineProperty(navigator, 'clipboard', { configurable: true, value: window.testClipboard }));
    assert.equal(await page.evaluate(() => navigator.clipboard.readText()), 'Anthropic');
    await page.waitForFunction(() => [...document.querySelectorAll('.vendor-button img')].every(img => img.complete && img.naturalWidth > 0));
    assert.equal(await builder.locator('td').nth(2).innerText(), 'reported-model');
    assert.equal(await helper.locator('td').nth(2).innerText(), 'helper-model');
    assert.ok((await builder.locator('.limit-cell').nth(0).innerText()).includes('4K / 20K'));
    assert.ok((await builder.locator('.limit-cell').nth(0).innerText()).includes('16K left'));
    assert.ok((await builder.locator('.limit-cell').nth(1).innerText()).includes('40K / 200K'));
    assert.ok((await builder.locator('.limit-cell').nth(1).innerText()).includes('160K left'));
    assert.ok((await builder.locator('.limit-cell').nth(2).innerText()).includes('55% / 100%'));
    assert.ok((await builder.locator('.limit-cell').nth(2).innerText()).includes('45% left'));
    assert.equal(await builder.locator('progress').nth(0).getAttribute('value'), '4000');
    assert.equal(await builder.locator('progress').nth(0).getAttribute('max'), '20000');
    assert.equal(await reviewer.locator('.limit-cell').nth(0).locator('progress').count(), 0);
    assert.ok((await reviewer.locator('.limit-cell').nth(0).innerText()).includes('9K left'));
    assert.ok((await reviewer.locator('.limit-cell').nth(1).innerText()).includes('remaining unknown'));
    assert.ok((await page.locator('#work-text').innerText()).includes('Building API validation'));
    const overview = await page.locator('#overview').boundingBox(), focus = await page.locator('#current-work').boundingBox();
    assert.ok(Math.abs(overview.width / focus.width - 2) < 0.03);
    assert.equal(await page.locator('.activity-ball').count(), 1);
    await page.locator('.activity-cell.working').first().hover();
    assert.ok((await page.locator('.agent-tooltip').first().innerText()).includes('Reviewing edge cases'));
    assert.ok((await page.locator('.agent-tooltip').first().innerText()).includes('Building request validation'));
    await page.locator('#overview').hover();
    await page.getByLabel('Show subtasks').check();
    assert.equal(await page.locator('.item').count(), 2);
    assert.equal(await page.locator('.activity-ball').count(), 2);
    assert.ok((await page.locator('#list').innerText()).includes('10%'));
    assert.ok((await builder.innerText()).includes('stale'));
    await page.locator('.item').first().locator('summary').click();
    await api('PATCH', `/projects/${project.id}/tasks/${root.id}`, { percent: 61, detail: 'Live update reached the browser.' });
    await page.waitForFunction(() => document.querySelector('#overview').textContent.includes('61%'));
    assert.equal(await page.locator('.item[open]').count(), 1);
    await api('POST', `/projects/${project.id}/reports`, { agent: 'browser-builder', workSummary: 'Fixing error handling; preparing final checks.' });
    await page.waitForFunction(() => document.querySelector('#work-text').textContent.includes('Fixing error handling'));
    assert.equal(await page.locator('.item[open]').count(), 1);
    await api('PATCH', `/projects/${project.id}`, { workSummary: '' });
    await page.waitForFunction(() => document.querySelector('#work-text').textContent.includes('Reviewing edge cases'));
    await api('POST', `/projects/${project.id}/reports`, { agent: 'browser-builder', workSummary: 'Fixing error handling; preparing final checks.' });
    await page.waitForFunction(() => document.querySelector('#work-text').textContent.includes('Fixing error handling'));
    await page.getByLabel('Search', { exact: true }).fill('Subtask verification');
    assert.equal(await page.locator('.item').count(), 1);
    await page.getByLabel('Search', { exact: true }).fill('');
    for (const [agent, provider] of [['browser-zai', 'Z.ai'], ['browser-google', 'Google'], ['browser-custom', 'Other vendor']]) {
      await api('POST', `/projects/${project.id}/reports`, { agent, provider, harness: 'OpenCode' });
    }
    await page.waitForFunction(() => document.querySelectorAll('.agent-table tbody tr').length === 6);
    await page.waitForFunction(() => [...document.querySelectorAll('.vendor-button img')].every(img => img.complete && img.naturalWidth > 0));
    assert.equal(await page.locator('[data-agent="browser-custom"] .vendor-fallback').innerText(), 'O');
    assert.equal(await page.locator('[data-agent="browser-custom"] .vendor-button').getAttribute('title'), 'Other vendor');
    assert.equal(await builder.locator('.agent-harness').innerText(), 'Codex Desktop');
    await page.screenshot({ path: output, fullPage: true });
    for (const agent of ['browser-builder', 'browser-helper']) {
      await api('POST', `/projects/${project.id}/agents/${agent}/activity`, { active: false });
    }
    await page.waitForFunction(() => !document.querySelector('.agent-table tr[data-agent="browser-builder"]') && !document.querySelector('.agent-table tr[data-agent="browser-helper"]'));
    assert.equal(await page.locator('.agent-table tbody tr').count(), 4);
    await page.getByRole('button', { name: 'Hide idle agents', exact: true }).click();
    assert.equal(await page.getByRole('button', { name: 'Hide idle agents', exact: true }).getAttribute('aria-pressed'), 'false');
    await page.waitForFunction(() => document.querySelector('.agent-table tr[data-agent="browser-builder"]').dataset.active === 'false' && document.querySelector('.agent-table tr[data-agent="browser-helper"]').dataset.active === 'false');
    assert.equal(await page.locator('.item').first().locator('.activity-ball').count(), 0);
    assert.ok((await builder.locator('.limit-cell').nth(0).innerText()).includes('16K left'));
    const other = await api('POST', '/projects', { name: 'Another project needing input' });
    const otherPage = await browser.newPage();
    await otherPage.goto(base + '/projects/' + other.id);
    await otherPage.waitForFunction(() => document.querySelector('#connection').textContent.includes('Live'));
    const request = await api('POST', '/input-requests', {
      projectId: other.id, agent: 'browser-questioner', source: 'Codex Desktop', sourceUrl: 'https://example.com/chat/1', title: 'Choose output',
      message: 'Which format should we use? <img src=x onerror=alert(1)>', externalKey: 'output-choice',
    });
    const popup = page.locator(`.input-popup[data-request-id="${request.id}"]`);
    await popup.waitFor();
    await otherPage.locator('.input-popup').waitFor();
    assert.ok((await popup.innerText()).includes(other.name));
    assert.equal(await popup.locator('img').count(), 0);
    assert.ok(page.url().endsWith('/projects/' + project.id));
    assert.equal(await popup.locator('form, textarea, input').count(), 0);
    assert.ok((await popup.innerText()).includes('Return to Codex Desktop to reply.'));
    assert.ok(!(await popup.innerText()).includes('Which format'));
    assert.ok(!(await popup.innerText()).includes('Choose output'));
    assert.equal(await popup.getByRole('link', { name: 'Open conversation' }).getAttribute('href'), 'https://example.com/chat/1');
    const second = await api('POST', '/input-requests', {
      projectId: project.id, agent: 'browser-questioner', source: 'Claude Code',
    });
    await page.waitForFunction(() => document.querySelectorAll('.input-popup').length === 2);
    assert.equal(await popup.locator('form, textarea, input').count(), 0);
    await page.locator(`.input-popup[data-request-id="${second.id}"]`).getByRole('button', { name: 'Dismiss' }).click();
    assert.equal(await page.locator('.input-popup').count(), 1);
    await page.getByRole('button', { name: '2 attention notices' }).click();
    assert.equal(await page.locator('.input-popup').count(), 2);
    await api('PATCH', '/input-requests/' + second.id, { status: 'cancelled', expectedRevision: 1 });
    await page.waitForFunction(() => document.querySelectorAll('.input-popup').length === 1);
    await page.reload();
    await popup.waitFor();
    assert.equal(await popup.locator('form, textarea, input').count(), 0);
    await page.screenshot({ path: output.replace(/\.png$/, '-attention.png'), fullPage: true });
    await api('PATCH', '/input-requests/' + request.id, { status: 'resolved', expectedRevision: 1, actor: 'browser-questioner' });
    await page.waitForFunction(() => document.querySelectorAll('.input-popup').length === 0);
    await otherPage.waitForFunction(() => document.querySelectorAll('.input-popup').length === 0);
    const answered = await api('GET', '/input-requests/' + request.id);
    assert.equal(answered.status, 'resolved');
    assert.equal(answered.response, undefined);
    assert.equal(answered.message, undefined);
    assert.equal(answered.title, undefined);
    await otherPage.close();
    await page.setViewportSize({ width: 390, height: 844 });
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth));
    assert.ok(await page.evaluate(() => document.querySelector('.agent-scroll').scrollWidth > document.querySelector('.agent-scroll').clientWidth));
    const narrowOverview = await page.locator('#overview').boundingBox(), narrowFocus = await page.locator('#current-work').boundingBox();
    assert.ok(narrowFocus.y >= narrowOverview.y + narrowOverview.height);
    await page.goto(base + '/projects/' + project.id + '/tasks/' + child.id);
    await page.waitForSelector('.item');
    assert.equal(await page.locator('.item').count(), 1);
    const article = await api('POST', '/knowledge', {
      slug: 'browser-' + randomUUID(), title: 'Reusable acceptance guide',
      summary: 'Metadata search and safe Markdown.', tags: ['acceptance'], body: '**Readable knowledge**\n\n<script>alert(1)</script>',
    });
    await page.goto(base + '/knowledge');
    await page.waitForSelector('.knowledge-row');
    await page.getByLabel('Search', { exact: true }).fill(article.title);
    assert.equal(await page.locator('.knowledge-row').count(), 1);
    await page.getByRole('link', { name: article.title, exact: true }).click();
    await page.waitForSelector('.markdown strong');
    assert.equal(await page.locator('.markdown script').count(), 0);
    await page.goto(base + '/');
    await page.waitForSelector('.item');
    await page.getByLabel('Activity filter').selectOption('inactive');
    assert.equal(await page.locator('.item').count(), 0);
    const pollingPage = await browser.newPage();
    await pollingPage.route('**/events**', route => route.abort());
    await pollingPage.goto(base + '/projects/' + project.id);
    await pollingPage.waitForSelector('#overview h1');
    const pollingRequest = await api('POST', '/input-requests', {
      projectId: other.id, agent: 'browser-questioner', source: 'OpenCode',
    });
    await pollingPage.locator(`.input-popup[data-request-id="${pollingRequest.id}"]`).waitFor({ timeout: 22000 });
    await pollingPage.close();
    await api('PATCH', '/input-requests/' + pollingRequest.id, { status: 'cancelled', expectedRevision: 1 });
    await page.waitForFunction(() => document.querySelectorAll('.input-popup').length === 0);
    const articleRequest = await api('POST', '/input-requests', {
      projectId: other.id, agent: 'browser-questioner', source: 'Codex Desktop',
    });
    await page.goto(base + '/README.md');
    await page.locator(`.input-popup[data-request-id="${articleRequest.id}"]`).waitFor();
    await api('PATCH', '/input-requests/' + articleRequest.id, { status: 'cancelled', expectedRevision: 1 });
    await page.waitForFunction(() => document.querySelectorAll('.input-popup').length === 0);
    // Project memory stays scoped and loads long lesson content only on demand.
    await page.goto(base + '/projects/' + project.id);
    await page.waitForFunction(() => document.querySelector('#connection').textContent.includes('Live'));
    const lesson = await api('POST', `/projects/${project.id}/lessons`, {
      title: 'Retry scope correction', summary: 'Reuse the same incident key on retries',
      kind: 'misunderstanding', attempt: 'Created duplicate entries for the same incident',
      outcome: 'The index contained duplicate corrections', correction: 'Reuse externalKey for the same incident.',
      evidence: 'Acceptance test: <script>alert(1)</script>', tags: ['retries'], agent: 'browser-reviewer', taskId: root.id,
    });
    await api('POST', `/projects/${other.id}/lessons`, { title: 'Foreign lesson', correction: 'Must stay in its project' });
    await page.getByRole('link', { name: lesson.title, exact: true }).waitFor();
    await page.getByRole('link', { name: 'All lessons', exact: true }).click();
    await page.waitForSelector('.lesson-row');
    assert.equal(await page.locator('.lesson-row').count(), 1);
    await page.locator('.lesson-row summary').click();
    await page.waitForFunction(() => document.querySelector('.lesson-content').textContent.includes('Reuse externalKey'));
    assert.equal(await page.locator('.lesson-content script').count(), 0);
    await page.getByLabel('Search', { exact: true }).fill('missing-lesson');
    await page.waitForFunction(() => document.querySelectorAll('.lesson-row').length === 0);
    await page.getByLabel('Search', { exact: true }).fill('Retry scope');
    await page.waitForSelector('.lesson-row');
    await api('PATCH', `/projects/${project.id}/lessons/${lesson.id}`, { correction: 'Use a new key only for a different incident.', confidence: 'confirmed', expectedRevision: 1 });
    await page.waitForFunction(() => document.querySelector('.lesson-content').textContent.includes('different incident'));
    await api('PATCH', `/projects/${project.id}/lessons/${lesson.id}`, { status: 'superseded', expectedRevision: 2 });
    await page.waitForFunction(() => document.querySelectorAll('.lesson-row').length === 0);
    await page.getByLabel('Status', { exact: true }).selectOption('all');
    await page.getByRole('button', { name: 'Apply filters' }).click();
    await page.waitForSelector('.lesson-row');
    await page.getByRole('link', { name: 'Read Markdown', exact: true }).click();
    await page.waitForSelector('.markdown');
    assert.equal(await page.locator('.markdown script').count(), 0);
    assert.ok((await page.locator('.markdown').innerText()).includes('different incident'));

    // Full-history review uses the same numbered feed and keeps filters on pages.
    for (let start = 0; start < 101; start += 20) {
      await Promise.all(Array.from({ length: Math.min(20, 101 - start) }, (_, i) => api('POST', `/projects/${project.id}/notifications`, {
        projectId: project.id, actor: 'browser-review-feed', message: 'Review entry ' + (start + i),
      })));
    }
    await api('POST', `/projects/${other.id}/notifications`, { projectId: other.id, actor: 'browser-review-feed', message: 'Foreign review entry' });
    const globalResponse = await fetch(base + '/api/v1/notifications');
    assert.equal(globalResponse.status, 404);
    await page.goto(base + '/notifications');
    await page.waitForFunction(() => document.querySelector('#review-count').textContent.includes('Choose a project'));
    assert.equal(await page.locator('.notification-row').count(), 0);
    await page.getByLabel('Project', { exact: true }).selectOption(project.id);
    await page.waitForSelector('.notification-row');
    await page.goto(base + '/notifications?projectId=' + project.id);
    await page.waitForFunction(() => document.querySelector('#connection').textContent.includes('Live'));
    await page.getByLabel('Agent', { exact: true }).fill('browser-review-feed');
    await page.getByRole('button', { name: 'Apply filters' }).click();
    await page.waitForFunction(() => document.querySelectorAll('.notification-row').length === 100 && !document.querySelector('#review-next').disabled);
    const numbers = await page.locator('.notification-row').evaluateAll(rows => rows.map(r => Number(r.dataset.key)));
    assert.ok(numbers.every((n, i) => i === 0 || n > numbers[i - 1]));
    assert.ok(!(await page.locator('#review-list').innerText()).includes('Foreign review entry'));
    await page.getByRole('button', { name: 'Next page', exact: true }).click();
    await page.waitForFunction(() => document.querySelectorAll('.notification-row').length === 1 && document.querySelector('#review-next').disabled);
    const last = Number(await page.locator('.notification-row').getAttribute('data-key'));
    assert.ok(last > numbers.at(-1));
    await api('POST', `/projects/${project.id}/notifications`, { projectId: project.id, actor: 'browser-review-feed', message: 'Live review update' });
    await page.waitForFunction(() => document.querySelectorAll('.notification-row').length === 2);
    assert.ok((await page.locator('#review-list').innerText()).includes('Live review update'));
    await page.getByRole('button', { name: 'Previous page', exact: true }).click();
    await page.waitForFunction(() => document.querySelectorAll('.notification-row').length === 100);
    await page.getByRole('button', { name: 'Recent updates', exact: true }).click();
    await page.waitForFunction(() => document.querySelectorAll('.notification-row').length < 100);
    await page.getByLabel('Search', { exact: true }).fill('Live review update');
    assert.equal(await page.locator('.notification-row').count(), 1);
    await page.screenshot({ path: output.replace(/\.png$/, '-notifications.png'), fullPage: true });
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth));
    assert.deepEqual(errors, []);
    console.log('Browser acceptance passed: 2:1 header, live work summary, compact agent/model/harness rows, default-on hide-idle toggle with live removal/reveal, vendor icons/hover/clipboard and HTTP copy fallback, exact usage bars, unknown limits, mobile table; attention-only cross-project notices, native-chat links, polling, subtasks, SSE, filters, knowledge, scoped lesson memory/revisions and paginated live notification review.');
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
