// Runs the built dashboard in real Chrome against a real prox. Unit tests never
// load the bundle, so a blank page, a missing i18n import or a failing request
// on load only shows up here. Everything lives in a temp home.
//
//   bun run check:bundle <path to a prox binary>
import { spawn } from 'node:child_process';
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { chromium, type Page } from 'playwright-core';

const PORT = 47391;
const PASSWORD = 'bundle-check-password';
const ORIGIN = `http://127.0.0.1:${PORT}`;
const CHROME = '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome';
const READY_ATTEMPTS = 100;
const SETTLE_MS = 1500;
// A translation key on screen looks like auth_files.card_ok.
const RAW_I18N_KEY = /\b[a-z]+(?:_[a-z]+)*\.[a-z]+_[a-z_]+\b/;
const UPSTREAM_NAME = /cliproxyapi/i;

const binary = process.argv[2];
if (!binary) throw new Error('usage: check-bundle <path to a prox binary>');

const failures: string[] = [];

function writeConfig(home: string) {
  mkdirSync(join(home, 'auth'), { recursive: true });
  writeFileSync(
    join(home, 'config.yaml'),
    [
      'config-version: 8',
      `server: {host: "127.0.0.1", port: ${PORT}}`,
      'management: {secret-key: "", disable-auto-update-panel: true}',
      'access: {api-keys: ["client-key"]}',
      `oauth: {auth-dir: "${join(home, 'auth')}"}`,
      '',
    ].join('\n')
  );
}

async function waitForServer() {
  for (let attempt = 0; attempt < READY_ATTEMPTS; attempt += 1) {
    const healthy = await fetch(`${ORIGIN}/healthz`).then(
      (response) => response.ok,
      () => false
    );
    if (healthy) return;
    await Bun.sleep(100);
  }
  throw new Error('prox did not start');
}

function watchForErrors(page: Page) {
  page.on('pageerror', (error) => failures.push(`page error: ${error.message}`));
  page.on('response', (response) => {
    if (response.status() >= 400 && response.url().startsWith(ORIGIN)) {
      failures.push(`${response.status()} ${new URL(response.url()).pathname}`);
    }
  });
}

async function checkPage(page: Page, route: string) {
  const text = await page.locator('body').innerText();
  if (text.trim().length === 0) failures.push(`${route}: blank page`);
  const rawKey = text.match(RAW_I18N_KEY);
  if (rawKey) failures.push(`${route}: raw translation key ${rawKey[0]}`);
  if (UPSTREAM_NAME.test(text) && !route.includes('plugin-store')) {
    failures.push(`${route}: shows the upstream name`);
  }
}

async function logIn(page: Page) {
  await page.goto(`${ORIGIN}/`);
  await page.waitForTimeout(SETTLE_MS);
  if (!page.url().endsWith('/dashboard/login')) failures.push(`/ landed on ${page.url()}`);
  await checkPage(page, 'login');
  await page.locator('input[type=password]').first().fill(PASSWORD);
  await page.keyboard.press('Enter');
  await page.waitForTimeout(SETTLE_MS);
}

async function visitEveryPage(page: Page) {
  const links = await page.locator('aside a[href^="/dashboard"]').evaluateAll((anchors) =>
    anchors.map((anchor) => anchor.getAttribute('href') ?? '')
  );
  if (links.length < 8) failures.push(`only ${links.length} sidebar links`);
  for (const route of links) {
    await page.locator(`aside a[href="${route}"]`).first().click();
    await page.waitForTimeout(SETTLE_MS);
    if (new URL(page.url()).pathname !== route) failures.push(`${route}: landed on ${page.url()}`);
    await checkPage(page, route);
    await page.reload();
    await page.waitForTimeout(SETTLE_MS);
    if (new URL(page.url()).pathname !== route) failures.push(`${route}: reload left the page`);
  }
  return links.length;
}

const home = mkdtempSync(join(tmpdir(), 'prox-bundle-'));
writeConfig(home);
const server = spawn(binary, ['start'], {
  env: { ...process.env, HOME: home, PROX_HOME: home, PROX_MASTER_PASSWORD: PASSWORD },
  stdio: 'ignore',
});

try {
  await waitForServer();
  const browser = await chromium.launch({ executablePath: CHROME, headless: true });
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
  watchForErrors(page);
  await logIn(page);
  const visited = await visitEveryPage(page);
  await browser.close();
  console.log(`visited ${visited} pages`);
} finally {
  server.kill();
  rmSync(home, { recursive: true, force: true });
}

if (failures.length > 0) {
  console.error([...new Set(failures)].join('\n'));
  process.exit(1);
}
console.log('bundle ok');
