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
  const browser = await chromium.launch({ headless: true });
  try {
    const page = await browser.newPage({ viewport: { width: 1200, height: 850 } });
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    await page.goto(base + '/projects/' + project.id);
    await page.waitForSelector('.item');
    assert.equal(await page.locator('.item').count(), 1);
    assert.ok((await page.locator('#overview').innerText()).includes('55%'));
    await page.getByLabel('Show subtasks').check();
    assert.equal(await page.locator('.item').count(), 2);
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
    assert.deepEqual(errors, []);
    console.log('Browser acceptance passed: subtasks, manual progress, usage, SSE, expansion, filters, mobile layout, task view and knowledge.');
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
