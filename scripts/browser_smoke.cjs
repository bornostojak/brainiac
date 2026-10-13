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
    model: 'reported-model', tokensRemaining: 16000, contextTokens: 40000,
    contextLimit: 200000, observedAt: new Date(Date.now() - 10 * 60 * 1000).toISOString(),
  });
  await api('POST', `/projects/${project.id}/reports`, {
    agent: 'browser-builder', provider: 'OpenAI', workingOn: 'API implementation',
    tasks: [{ taskId: root.id, workingOn: 'Building request validation' }],
  });
  await api('POST', `/projects/${project.id}/reports`, {
    agent: 'browser-helper', provider: 'OpenAI', workingOn: 'Reviewing edge cases',
    tasks: [{ taskId: root.id, workingOn: 'Reviewing edge cases' }],
  });
  await api('POST', `/projects/${project.id}/reports`, {
    agent: 'browser-reviewer', provider: 'Anthropic', workingOn: 'Checking acceptance',
    tasks: [{ taskId: child.id, workingOn: 'Acceptance verification' }],
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
    const openai = page.locator('.vendor-table tbody tr').filter({ hasText: 'OpenAI' });
    assert.equal(await openai.locator('td').nth(1).innerText(), '2');
    assert.equal(await page.locator('.activity-ball').count(), 1);
    await page.locator('.activity-cell.working').first().hover();
    assert.ok((await page.locator('.agent-tooltip').first().innerText()).includes('Reviewing edge cases'));
    assert.ok((await page.locator('.agent-tooltip').first().innerText()).includes('Building request validation'));
    await page.locator('#overview').hover();
    await page.getByLabel('Show subtasks').check();
    assert.equal(await page.locator('.item').count(), 2);
    assert.equal(await page.locator('.activity-ball').count(), 2);
    assert.ok((await page.locator('#list').innerText()).includes('10%'));
    assert.ok((await page.locator('.usage-list').innerText()).includes('ctx 20%'));
    assert.ok((await page.locator('.usage-list').innerText()).includes('stale'));
    await page.locator('.item').first().locator('summary').click();
    await api('PATCH', `/projects/${project.id}/tasks/${root.id}`, { percent: 61, detail: 'Live update reached the browser.' });
    await page.waitForFunction(() => document.querySelector('#overview').textContent.includes('61%'));
    assert.equal(await page.locator('.item[open]').count(), 1);
    await page.getByLabel('Search', { exact: true }).fill('Subtask verification');
    assert.equal(await page.locator('.item').count(), 1);
    await page.getByLabel('Search', { exact: true }).fill('');
    await page.screenshot({ path: output, fullPage: true });
    for (const agent of ['browser-builder', 'browser-helper']) {
      await api('POST', `/projects/${project.id}/agents/${agent}/activity`, { active: false });
    }
    await page.waitForFunction(() => [...document.querySelectorAll('.vendor-table tbody tr')].some(row => row.children[0].textContent === 'OpenAI' && row.children[1].textContent === '0'));
    assert.equal(await page.locator('.item').first().locator('.activity-ball').count(), 0);
    const other = await api('POST', '/projects', { name: 'Another project needing input' });
    const otherPage = await browser.newPage();
    await otherPage.goto(base + '/projects/' + other.id);
    await otherPage.waitForFunction(() => document.querySelector('#connection').textContent.includes('Live'));
    const request = await api('POST', '/input-requests', {
      projectId: other.id, agent: 'browser-questioner', title: 'Choose output',
      message: 'Which format should we use? <img src=x onerror=alert(1)>', externalKey: 'output-choice',
    });
    const popup = page.locator(`.input-popup[data-request-id="${request.id}"]`);
    await popup.waitFor();
    await otherPage.locator('.input-popup').waitFor();
    assert.ok((await popup.innerText()).includes(other.name));
    assert.equal(await popup.locator('img').count(), 0);
    assert.ok(page.url().endsWith('/projects/' + project.id));
    await popup.getByRole('textbox').fill('Use Markdown');
    const second = await api('POST', '/input-requests', {
      projectId: project.id, agent: 'browser-questioner', title: 'Another choice', message: 'Confirm this option',
    });
    await page.waitForFunction(() => document.querySelectorAll('.input-popup').length === 2);
    assert.equal(await popup.getByRole('textbox').inputValue(), 'Use Markdown');
    await page.locator(`.input-popup[data-request-id="${second.id}"]`).getByRole('button', { name: 'Dismiss' }).click();
    assert.equal(await page.locator('.input-popup').count(), 1);
    await page.getByRole('button', { name: '2 input requests' }).click();
    assert.equal(await page.locator('.input-popup').count(), 2);
    await api('PATCH', '/input-requests/' + second.id, { status: 'cancelled', expectedRevision: 1 });
    await page.waitForFunction(() => document.querySelectorAll('.input-popup').length === 1);
    await page.reload();
    await popup.waitFor();
    await popup.getByRole('textbox').fill('Use Markdown');
    await popup.getByRole('button', { name: 'Send response' }).click();
    await page.waitForFunction(() => document.querySelectorAll('.input-popup').length === 0);
    await otherPage.waitForFunction(() => document.querySelectorAll('.input-popup').length === 0);
    const answered = await api('GET', '/input-requests/' + request.id);
    assert.equal(answered.response, 'Use Markdown');
    await otherPage.close();
    await page.setViewportSize({ width: 390, height: 844 });
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth));
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
      projectId: other.id, agent: 'browser-questioner', title: 'Polling input', message: 'Input with SSE unavailable',
    });
    await pollingPage.locator(`.input-popup[data-request-id="${pollingRequest.id}"]`).waitFor({ timeout: 22000 });
    await pollingPage.close();
    await api('PATCH', '/input-requests/' + pollingRequest.id, { status: 'cancelled', expectedRevision: 1 });
    await page.waitForFunction(() => document.querySelectorAll('.input-popup').length === 0);
    const articleRequest = await api('POST', '/input-requests', {
      projectId: other.id, agent: 'browser-questioner', title: 'README input', message: 'Input from another project',
    });
    await page.goto(base + '/README.md');
    await page.locator(`.input-popup[data-request-id="${articleRequest.id}"]`).waitFor();
    await api('PATCH', '/input-requests/' + articleRequest.id, { status: 'cancelled', expectedRevision: 1 });
    await page.waitForFunction(() => document.querySelectorAll('.input-popup').length === 0);
    assert.deepEqual(errors, []);
    console.log('Browser acceptance passed: cross-project popups, polling fallback, safe request text, drafts, dismissal/reload, answers across tabs and README views; vendor counts, task hover, subtasks, usage, SSE, filters, mobile and knowledge.');
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
