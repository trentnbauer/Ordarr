// Optional browser check: install Playwright externally; no frontend build is needed.
const { chromium } = require('playwright');
const { spawn } = require('node:child_process');
const { mkdtemp, rm } = require('node:fs/promises');
const { tmpdir } = require('node:os');
const { join, resolve } = require('node:path');
const net = require('node:net');
const assert = require('node:assert/strict');
(async () => {
  const temp = await mkdtemp(join(tmpdir(), 'ordarr-browser-'));
  const listener = net.createServer();
  await new Promise(r => listener.listen(0, '127.0.0.1', r));
  const port = listener.address().port;
  await new Promise(r => listener.close(r));
  const origin = `http://127.0.0.1:${port}`;
  const server = spawn(resolve(__dirname, '../bin/ordarr'), [], {
    env: { ...process.env, ORDARR_ADDR: `127.0.0.1:${port}`, ORDARR_DATA_DIR: temp, ORDARR_SECURE_COOKIES: 'false' },
    stdio: 'ignore',
  });
  let browser;
  try {
    let ready = false;
    for (let i = 0; i < 100; i++) {
      try { if ((await fetch(`${origin}/healthz`)).ok) { ready = true; break; } } catch {}
      await new Promise(r => setTimeout(r, 100));
    }
    assert(ready, 'server did not start');
    browser = await chromium.launch({ headless: true, ...(process.env.CHROMIUM_PATH ? { executablePath: process.env.CHROMIUM_PATH } : {}) });
    const page = await browser.newPage({ viewport: { width: 390, height: 844 } });
    const errors = [];
    page.on('pageerror', e => errors.push(e.message));
    await page.goto(origin);
    assert.equal(new URL(page.url()).pathname, '/setup');
    await page.getByLabel('Username', { exact: true }).fill('browser-admin');
    await page.getByLabel('Password', { exact: true }).fill('browser-smoke-passphrase');
    await page.getByRole('button', { name: 'Create administrator' }).click();
    await page.getByRole('heading', { name: 'Your watch orders' }).waitFor({ timeout: 5000 }).catch(async e => { console.log(await page.locator('body').innerText()); throw e; });
    await page.getByRole('link', { name: 'Settings', exact: true }).click();
    await page.getByText('browser-admin', { exact: true }).waitFor();
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'mobile layout overflows');
    await page.screenshot({ path: join(tmpdir(), 'ordarr-settings-mobile.png'), fullPage: true });
    await page.getByRole('button', { name: 'Sign out' }).click();
    await page.getByLabel('Username', { exact: true }).fill('browser-admin');
    await page.getByLabel('Password', { exact: true }).fill('wrong');
    await page.getByRole('button', { name: 'Sign in', exact: true }).click();
    await page.getByRole('alert').waitFor();
    await page.getByLabel('Username', { exact: true }).fill('browser-admin');
    await page.getByLabel('Password', { exact: true }).fill('browser-smoke-passphrase');
    await page.getByRole('button', { name: 'Sign in', exact: true }).click();
    await page.getByRole('heading', { name: 'Your watch orders' }).waitFor({ timeout: 5000 }).catch(async e => { console.log(await page.locator('body').innerText()); throw e; });
    await page.getByRole('link', { name: 'Create watch order', exact: true }).click();
    await page.getByLabel('Collection title', { exact: true }).fill('Browser mixed order');
    await page.getByLabel('Description', { exact: true }).fill('Movies, specials and deliberate repeats.');
    await page.getByRole('button', { name: 'Create collection', exact: true }).click();
    await page.getByRole('heading', { name: 'Canonical order', exact: true }).waitFor();
    const sourcePath = new URL(page.url()).pathname;
    const rowIDs = () => page.locator('#entry-list [data-entry-id]').evaluateAll(rows => rows.map(row => row.dataset.entryId));
    const titles = () => page.locator('#entry-list h3').allTextContents();
    const addMovie = async () => {
      await page.getByLabel('Media type', { exact: true }).selectOption('movie');
      await page.getByLabel('Media title / series title', { exact: true }).fill('Opening movie');
      await page.getByLabel('Movie ID / series ID', { exact: true }).fill('1771');
      await page.getByRole('button', { name: 'Add to watch order', exact: true }).click();
      await page.waitForURL(url => url.pathname === sourcePath);
    };
    await addMovie();
    await page.getByLabel('Media type', { exact: true }).selectOption('episode_range');
    await page.getByLabel('Media title / series title', { exact: true }).fill('Specials');
    await page.getByLabel('Movie ID / series ID', { exact: true }).fill('83867');
    await page.getByLabel('Season (0 = specials)', { exact: true }).fill('0');
    await page.getByLabel('Episode / first episode', { exact: true }).fill('1');
    await page.getByLabel('Last episode / whole-season episode count', { exact: true }).fill('3');
    await page.getByRole('button', { name: 'Add to watch order', exact: true }).click();
    await page.locator('#entry-list [data-entry-id]').nth(3).waitFor();
    await addMovie();
    await page.locator('#entry-list [data-entry-id]').nth(4).waitFor();
    assert.deepEqual(await titles(), ['Opening movie', 'Specials', 'Specials', 'Specials', 'Opening movie']);
    const initial = await rowIDs();
    assert.notEqual(initial[0], initial[4], 'repeat was collapsed');
    await page.getByRole('link', { name: 'Edit entry 1', exact: true }).click();
    await page.getByLabel('Media title / series title', { exact: true }).fill('Opening movie revised');
    await page.getByRole('button', { name: 'Save entry', exact: true }).click();
    await page.waitForURL(url => url.hash === '#entry-' + initial[0]);
    assert.equal((await rowIDs())[0], initial[0]);
    const keyboardUp = page.getByRole('button', { name: 'Move entry 2 up', exact: true });
    await keyboardUp.focus();
    await keyboardUp.press('Enter');
    await page.waitForURL(url => url.hash === '#entry-' + initial[1]);
    const afterKeyboard = [initial[1], initial[0], initial[2], initial[3], initial[4]];
    assert.deepEqual(await rowIDs(), afterKeyboard);
    assert.equal(await page.evaluate(() => document.activeElement.id), 'entry-' + initial[1]);
    await page.setViewportSize({width:1280,height:1600});
    const handle = page.locator('.drag-handle').last();
    const destination = page.locator('#entry-list .entry').first();
    await page.locator('#entry-list').evaluate(list => list.scrollIntoView({block:'center'}));
    await handle.scrollIntoViewIfNeeded();
    const sourceBox = await handle.boundingBox();
    const targetBox = await destination.boundingBox();
    await page.mouse.move(sourceBox.x + sourceBox.width/2, sourceBox.y + sourceBox.height/2);
    await page.mouse.down();
    await page.mouse.move(sourceBox.x + sourceBox.width/2 + 10, sourceBox.y + sourceBox.height/2 - 10, {steps:5});
    await page.mouse.move(targetBox.x + 20, targetBox.y + 15, {steps:20});
    await page.mouse.move(targetBox.x + 21, targetBox.y + 16);
    await page.mouse.up();
    await page.waitForURL(url => url.pathname === sourcePath && url.hash === '', {timeout:5000});
    const afterDrag = [initial[4], ...afterKeyboard.slice(0, 4)];
    assert.deepEqual(await rowIDs(), afterDrag);
    await page.reload();
    assert.deepEqual(await rowIDs(), afterDrag, 'drag order was not saved');
    // A cancelled drag must restore the authoritative visible order.
    await page.evaluate(() => {
      const rows = document.querySelectorAll('#entry-list .entry');
      const handle = rows[0].querySelector('.drag-handle');
      const dataTransfer = new DataTransfer();
      handle.dispatchEvent(new DragEvent('dragstart', { bubbles: true, dataTransfer }));
      const rect = rows[rows.length - 1].getBoundingClientRect();
      rows[rows.length - 1].dispatchEvent(new DragEvent('dragover', { bubbles: true, cancelable: true, clientY: rect.bottom - 1, dataTransfer }));
      handle.dispatchEvent(new DragEvent('dragend', { bubbles: true, dataTransfer }));
    });
    assert.deepEqual(await rowIDs(), afterDrag, 'cancelled drag left an unsaved order');
    await page.locator('#entry-list input[type=checkbox]').nth(0).check();
    await page.locator('#entry-list input[type=checkbox]').nth(2).check();
    await page.getByLabel('Final position of the first moved entry', { exact: true }).fill('2');
    await page.getByRole('button', { name: 'Apply to selected entries', exact: true }).click();
    await page.waitForURL(url => url.pathname === sourcePath);
    assert.deepEqual(await rowIDs(), [afterDrag[1], afterDrag[0], afterDrag[2], afterDrag[3], afterDrag[4]]);
    await page.setViewportSize({width:390,height:844});
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'mobile editor overflows');
    await page.screenshot({ path: join(tmpdir(), 'ordarr-collection-mobile.png'), fullPage: true });
    const sourceOrder = await titles();
    const sourceIDs = await rowIDs();
    await page.getByRole('button', { name: 'Duplicate / fork', exact: true }).click();
    await page.waitForURL(url => url.pathname !== sourcePath);
    assert.deepEqual(await titles(), sourceOrder);
    assert((await rowIDs()).every(id => !sourceIDs.includes(id)), 'fork reused source entry IDs');
    await page.getByText('Delete this collection', { exact: true }).click();
    await page.getByLabel('Type the collection title to confirm', { exact: true }).fill('Browser mixed order (copy)');
    await page.getByRole('button', { name: 'Delete collection permanently', exact: true }).click();
    await page.getByRole('heading', { name: 'Your watch orders', exact: true }).waitFor();
    await page.getByLabel('Search collections', { exact: true }).fill('Browser mixed');
    await page.getByLabel('Show', { exact: true }).selectOption('movie');
    await page.getByRole('button', { name: 'Search', exact: true }).click();
    await page.getByRole('link', { name: 'Browser mixed order', exact: true }).click();
    assert.deepEqual(await titles(), sourceOrder);
    // Bulk removal is explicit, and preserves the remaining entries.
    await page.locator('#entry-list input[type=checkbox]').last().check();
    await page.getByLabel('Bulk action', { exact: true }).selectOption('remove');
    await page.getByLabel('I confirm removal if I chose Remove.', { exact: true }).check();
    await page.getByRole('button', { name: 'Apply to selected entries', exact: true }).click();
    await page.locator('#entry-list .entry').nth(3).waitFor();
    assert.equal((await rowIDs()).length, 4);
    const noJS = await browser.newContext({ javaScriptEnabled: false });
    await noJS.addCookies(await page.context().cookies());
    const plain = await noJS.newPage();
    await plain.goto(origin + sourcePath);
    assert.equal(await plain.locator('.drag-handle').first().isVisible(), false);
    await plain.getByLabel('Media type', { exact: true }).selectOption('season');
    await plain.getByLabel('Media title / series title', { exact: true }).fill('Full season');
    await plain.getByLabel('Movie ID / series ID', { exact: true }).fill('42');
    await plain.getByLabel('Season (0 = specials)', { exact: true }).fill('1');
    await plain.getByLabel('Last episode / whole-season episode count', { exact: true }).fill('2');
    await plain.getByRole('button', { name: 'Add to watch order', exact: true }).click();
    await plain.locator('#entry-list .entry').nth(5).waitFor();
    assert.equal(await plain.locator('#entry-list h3').filter({hasText:'Full season'}).count(), 2);
    // Movie editing must remain valid when irrelevant episode fields are not hidden by JS.
    await plain.getByRole('link', {name:'Edit entry 2', exact:true}).click();
    await plain.getByLabel('Media title / series title', {exact:true}).fill('Edited without JavaScript');
    await plain.getByRole('button', {name:'Save entry', exact:true}).click();
    await plain.locator('#entry-list h3').filter({hasText:'Edited without JavaScript'}).waitFor();
    await noJS.close();
    await page.reload();
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.screenshot({ path: join(tmpdir(), 'ordarr-dashboard-desktop.png'), fullPage: true });
    assert.deepEqual(errors, []);
    console.log('PASS: browser authentication, mixed collection editing, repeats, keyboard and drag ordering, cancelled drag recovery, bulk move/removal, independent fork/delete, search/filter, mobile layout and season/movie editing without JavaScript');
  } finally {
    if (browser) await browser.close();
    if (server.exitCode === null) {
      const stopped = new Promise(r => server.once('exit', r));
      server.kill('SIGTERM');
      await stopped;
    }
    await rm(temp, { recursive: true, force: true });
  }
})().catch(e => { console.error(e); process.exitCode = 1; });
