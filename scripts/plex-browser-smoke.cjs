// Real Ordarr adapter against an explicitly disposable HTTP fixture; not a live Plex server.
const { chromium } = require('playwright');
const { spawn } = require('node:child_process');
const { mkdtemp, rm, readFile } = require('node:fs/promises');
const { tmpdir } = require('node:os');
const { join, resolve } = require('node:path');
const http = require('node:http');
const net = require('node:net');
const assert = require('node:assert/strict');
(async () => {
  const dir = await mkdtemp(join(tmpdir(), 'ordarr-plex-browser-'));
  let expectedToken = 'disposable-browser-plex-token';
  let unavailable = false;
  let calls = 0;
  const fixture = http.createServer((req, res) => {
    calls++;
    assert.equal(req.method, 'GET', 'Ordarr tried to write to Plex');
    assert(!req.url.includes(expectedToken), 'credential in request URL');
    if (unavailable || req.headers['x-plex-token'] !== expectedToken) {
      res.writeHead(401); res.end(expectedToken); return;
    }
    res.setHeader('Content-Type', 'application/json');
    if (req.url === '/') res.end(JSON.stringify({MediaContainer:{machineIdentifier:'browser-fixture-server',friendlyName:'Disposable Plex fixture',version:'1.43.4'}}));
    else if (/^\/library\/sections\/[12]\/all\?/.test(req.url)) {
      const query = new URL(req.url, 'http://fixture').searchParams;
      const offset = Number(query.get('X-Plex-Container-Start'));
      const kind = query.get('type') === '4' ? 'episode' : 'movie';
      const metadata = [offset+1,offset+2].filter(n=>n<=4).map(n=>({ratingKey:String(n),type:kind,title:`Fixture ${kind} ${n}`,year:2020,...(kind==='episode'?{grandparentTitle:'Fixture series',grandparentRatingKey:'100',parentIndex:0,index:n}:{}),Media:[{Part:[{file:'/private/fixture/file.mkv'}]}]}));
      res.end(JSON.stringify({MediaContainer:{size:metadata.length,offset,totalSize:4,Metadata:metadata}}));
    }
    else if (req.url.startsWith('/library/sections/all?')) res.end(JSON.stringify({MediaContainer:{size:2,Directory:[{key:'1',title:'Fixture Movies',type:'movie'},{key:'2',title:'Fixture Shows',type:'show'}]}}));
    else { res.writeHead(404); res.end(); }
  });
  await new Promise(r => fixture.listen(0, '127.0.0.1', r));
  const fixtureURL = `http://127.0.0.1:${fixture.address().port}`;
  const reserve = net.createServer();
  await new Promise(r => reserve.listen(0, '127.0.0.1', r));
  const port = reserve.address().port;
  await new Promise(r => reserve.close(r));
  const origin = `http://127.0.0.1:${port}`;
  let processHandle;
  let logs = '';
  let browser;
  async function start() {
    processHandle = spawn(resolve(__dirname, '../bin/ordarr'), [], {env:{...process.env,ORDARR_ADDR:`127.0.0.1:${port}`,ORDARR_DATA_DIR:dir,ORDARR_SECURE_COOKIES:'false'},stdio:['ignore','pipe','pipe']});
    processHandle.stdout.on('data', d => { logs += d; });
    processHandle.stderr.on('data', d => { logs += d; });
    for (let i=0; i<100; i++) {
      try { if ((await fetch(origin+'/healthz')).ok) return; } catch {}
      if (processHandle.exitCode !== null) throw Error('application exited');
      await new Promise(r => setTimeout(r,100));
    }
    throw Error('application not healthy');
  }
  async function stop() {
    if (processHandle?.exitCode === null) {
      const exited = new Promise(r => processHandle.once('exit', r));
      processHandle.kill('SIGTERM');
      assert.equal(await exited, 0);
    }
    processHandle = null;
  }
  try {
    await start();
    browser = await chromium.launch({headless:true,...(process.env.CHROMIUM_PATH?{executablePath:process.env.CHROMIUM_PATH}:{})});
    const page = await browser.newPage({viewport:{width:390,height:844}});
    await page.goto(origin);
    await page.getByLabel('Username',{exact:true}).fill('plex-test-admin');
    await page.getByLabel('Password',{exact:true}).fill('fixture-admin-passphrase');
    await page.getByRole('button',{name:'Create administrator',exact:true}).click();
    await page.getByRole('link',{name:'Settings',exact:true}).click();
    await page.getByRole('link',{name:'Manage Plex connections',exact:true}).click();
    await page.getByLabel('Connection name',{exact:true}).fill('Fixture connection');
    await page.getByLabel('Plex server URL',{exact:true}).fill(fixtureURL);
    await page.getByLabel('Plex token',{exact:true}).fill(expectedToken);
    await page.getByLabel('Allow plaintext HTTP to this trusted server',{exact:true}).check();
    await page.getByRole('button',{name:'Validate and save connection',exact:true}).click();
    const card = page.locator('.plex-connection');
    await card.getByText('Fixture Movies',{exact:false}).waitFor();
    assert(!(await page.content()).includes(expectedToken), 'raw token rendered');
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'Plex settings overflow mobile viewport');
    await card.getByRole('link',{name:'Browse Fixture Movies',exact:true}).click();
    await page.getByRole('button',{name:'Load first page',exact:true}).click();
    await page.getByRole('heading',{name:'Fixture movie 1',exact:true}).waitFor();
    await page.getByRole('button',{name:'Next page',exact:true}).click();
    await page.getByRole('heading',{name:'Fixture movie 3',exact:true}).waitFor();
    unavailable = true;
    await page.getByRole('button',{name:'Refresh this page',exact:true}).click();
    await page.getByText('The last fetch failed. Showing the previous cached page, which may be stale.',{exact:true}).waitFor();
    await page.getByRole('heading',{name:'Fixture movie 3',exact:true}).waitFor();
    unavailable = false;
    await page.getByRole('button',{name:'Previous page',exact:true}).click();
    await page.getByRole('heading',{name:'Fixture movie 1',exact:true}).waitFor();
    assert(!(await page.content()).includes('/private/fixture'));
    await page.getByRole('link',{name:'← Plex connections',exact:true}).click();
    await card.getByRole('link',{name:'Browse Fixture Shows',exact:true}).click();
    await page.getByRole('button',{name:'Load first page',exact:true}).click();
    await page.getByRole('heading',{name:'Fixture episode 1',exact:true}).waitFor();
    await page.getByText('Fixture series · Season 0 · Episode 1',{exact:true}).waitFor();
    assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'media browser overflows mobile viewport');
    assert(!(await page.content()).includes(expectedToken));
    await page.screenshot({path:join(tmpdir(),'ordarr-media-mobile.png'),fullPage:true});
    await page.getByRole('link',{name:'← Plex connections',exact:true}).click();
    const firstToken = expectedToken;
    const key = await readFile(join(dir,'credentials.key'));
    await stop();
    await start();
    assert.deepEqual(await readFile(join(dir,'credentials.key')),key,'key replaced on restart');
    await page.reload();
    const beforeCachedRead = calls;
    await card.getByRole('link',{name:'Browse Fixture Movies',exact:true}).click();
    await page.getByRole('heading',{name:'Fixture movie 1',exact:true}).waitFor();
    assert.equal(calls,beforeCachedRead,'cached page GET contacted Plex after restart');
    await page.getByRole('link',{name:'← Plex connections',exact:true}).click();
    unavailable = true;
    await card.getByRole('button',{name:'Recheck Fixture connection',exact:true}).click();
    await card.getByText('Showing the last successful library snapshot. It may be out of date.',{exact:true}).waitFor();
    await card.getByText('Fixture Movies',{exact:false}).waitFor();
    assert(!(await page.content()).includes(firstToken), 'error body reflected token');
    await page.screenshot({path:join(tmpdir(),'ordarr-plex-failure-mobile.png'),fullPage:true});
    unavailable = false;
    await card.getByRole('button',{name:'Recheck Fixture connection',exact:true}).click();
    await page.getByRole('status').waitFor();
    assert.equal(await card.getByRole('alert').count(),0);
    await card.getByText('Update connection or replace token',{exact:true}).click();
    expectedToken = 'rotated-disposable-plex-token';
    await card.getByLabel('Connection name',{exact:true}).fill('Rotated fixture');
    await card.getByLabel('Replacement token (blank keeps saved token at the same URL)',{exact:true}).fill(expectedToken);
    await card.getByRole('button',{name:'Validate and update Fixture connection',exact:true}).click();
    await card.getByRole('heading',{name:'Rotated fixture',exact:true}).waitFor();
    assert(!(await page.content()).includes(firstToken) && !(await page.content()).includes(expectedToken));
    assert(!logs.includes(firstToken) && !logs.includes(expectedToken), 'credential logged');
    await page.screenshot({path:join(tmpdir(),'ordarr-plex-settings-mobile.png'),fullPage:true});
    const beforeDelete = calls;
    await card.getByText('Remove connection',{exact:true}).click();
    await card.getByLabel('Type the connection name to remove it',{exact:true}).fill('Rotated fixture');
    await card.getByRole('button',{name:'Remove Rotated fixture',exact:true}).click();
    await page.getByRole('status').waitFor();
    assert.equal(await card.count(),0);
    assert.equal(calls,beforeDelete,'local deletion called Plex');
    console.log('PASS: Plex fixture browser connect, movie/episode pagination, cache retention, library discovery, encrypted restart, failed recheck retention, recovery, token rotation/redaction, mobile layout and local-only deletion. No live Plex server verified.');
  } finally {
    if (browser) await browser.close();
    await stop();
    await new Promise(r => fixture.close(r));
    await rm(dir,{recursive:true,force:true});
  }
})().catch(e => {console.error(e);process.exitCode=1;});
