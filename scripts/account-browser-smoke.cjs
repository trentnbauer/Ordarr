// Run by the optional Go account browser test, against the real app with a test-only
// account transport. No Plex auth page is opened and no real credentials are used.
const { chromium } = require('playwright');
const { join } = require('node:path');
const { tmpdir } = require('node:os');
const assert = require('node:assert/strict');
(async()=>{
 const origin=process.argv[2];
 const browser=await chromium.launch({headless:true,...(process.env.CHROMIUM_PATH?{executablePath:process.env.CHROMIUM_PATH}:{})});
 try{
  const page=await browser.newPage({viewport:{width:390,height:844}});
  const errors=[];page.on('pageerror',e=>errors.push(e.message));
  await page.goto(origin);
  await page.getByLabel('Username',{exact:true}).fill('account-browser-admin');
  await page.getByLabel('Password',{exact:true}).fill('account-browser-passphrase');
  await page.getByRole('button',{name:'Create administrator',exact:true}).click();
  await page.getByRole('link',{name:'Settings',exact:true}).click();
  await page.getByRole('link',{name:'Manage Plex connections',exact:true}).click();
  await page.getByRole('button',{name:'Sign in with Plex',exact:true}).click();
  const authorise=page.getByRole('link',{name:'Open Plex authorisation',exact:true});
  const external=new URL(await authorise.getAttribute('href'));
  assert.equal(external.origin,'https://app.plex.tv');assert.equal(external.search,'');assert(external.hash.includes('clientID='));assert(!external.hash.includes('forwardUrl'));
  await page.getByRole('button',{name:'Cancel sign-in attempt',exact:true}).click();
  await page.getByRole('button',{name:'Sign in with Plex',exact:true}).click();
  await page.getByRole('button',{name:'Check Plex authorisation',exact:true}).click();
  await page.getByRole('heading',{name:'Servers available to plex-user',exact:true}).waitFor();
  const resource=page.locator('.account-server');
  await resource.getByLabel('Allow plaintext HTTP to this trusted server',{exact:true}).check();
  await resource.getByRole('button',{name:'Connect Account server',exact:true}).click();
  const connection=page.locator('.plex-connection');
  await connection.getByText('Account movies',{exact:false}).waitFor();
  assert.equal(await connection.getByText('Update connection or replace token',{exact:true}).count(),0);
  await page.getByRole('button',{name:'Refresh Plex account',exact:true}).click();
  await page.getByRole('status').waitFor();
  await page.getByRole('button',{name:'Reauthorise Plex account',exact:true}).click();
  await page.getByRole('button',{name:'Check Plex authorisation',exact:true}).click();
  await connection.getByText('Last recheck failed:',{exact:false}).waitFor();
  await connection.getByRole('button',{name:'Recheck Account server',exact:true}).click();
  await page.getByRole('status').waitFor();
  assert.equal(await connection.getByRole('alert').count(),0);
  assert(!(await page.content()).includes('provider-fixture-signature'));
  assert(!(await page.content()).includes('resource-scoped-token'));
  assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),'account settings overflow mobile viewport');
  await page.screenshot({path:join(tmpdir(),'ordarr-account-mobile.png'),fullPage:true});
  await page.getByText('Disconnect Plex account',{exact:true}).click();
  await page.getByLabel('Type the connected Plex username to disconnect it',{exact:true}).fill('plex-user');
  await page.getByRole('button',{name:'Disconnect account and linked servers',exact:true}).click();
  await page.getByRole('button',{name:'Sign in with Plex',exact:true}).waitFor();
  assert.equal(await connection.count(),0);
  assert.deepEqual(errors,[]);
  console.log('PASS: fixture browser account start/cancel, external auth link, verified account/server selection, scoped server connection, refresh, reauthorisation, recheck, redaction and local disconnect; no live Plex account authorised.');
 }finally{await browser.close()}
})().catch(e=>{console.error(e);process.exitCode=1});
