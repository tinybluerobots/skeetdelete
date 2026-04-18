const { chromium } = require('playwright');
const http = require('http');
const { execSync, spawn } = require('child_process');

async function fetchJSON(url) {
  return new Promise((resolve, reject) => {
    http.get(url, (res) => {
      let data = '';
      res.on('data', chunk => data += chunk);
      res.on('end', () => {
        try { resolve(JSON.parse(data)); } catch(e) { resolve(data); }
      });
    }).on('error', reject);
  });
}

async function main() {
  console.log('=== SkeetDelete E2E Test ===\n');

  // Verify mock server is running
  try {
    const session = await fetchJSON('http://localhost:8081/xrpc/com.atproto.server.createSession');
    if (!session.did) throw new Error('No did in response');
    console.log('✓ Mock API server responding (got did:', session.did + ')');
  } catch (e) {
    console.error('✗ Mock API server not responding on :8081');
    process.exit(1);
  }

  // Verify dev server is running
  try {
    await new Promise((resolve, reject) => {
      http.get('http://localhost:8080/', (res) => {
        if (res.statusCode === 200) resolve();
        else reject(new Error('Status ' + res.statusCode));
      }).on('error', reject);
    });
    console.log('✓ Dev server responding on :8080');
  } catch (e) {
    console.error('✗ Dev server not responding on :8080');
    process.exit(1);
  }

  // Launch headless browser
  const browser = await chromium.launch({
    headless: true,
    args: ['--no-sandbox', '--disable-setuid-sandbox'],
  });
  const context = await browser.newContext({
    ignoreHTTPSErrors: true,
  });
  const page = await context.newPage();

  // Collect console messages
  const consoleMessages = [];
  page.on('console', msg => {
    consoleMessages.push(`[console.${msg.type()}] ${msg.text()}`);
  });
  page.on('pageerror', err => {
    consoleMessages.push(`[pageerror] ${err.message}`);
  });

  // Set up route interception BEFORE navigating
  // The WASM app calls https://bsky.social/xrpc/... directly
  await page.route('https://bsky.social/xrpc/**', async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const mockUrl = 'http://localhost:8081' + url.pathname + url.search;

    const options = {
      hostname: 'localhost',
      port: 8081,
      path: url.pathname + url.search,
      method: req.method(),
      headers: { 'Content-Type': 'application/json' },
    };

    if (req.method() === 'POST') {
      const postData = req.postData() || '';
      options.headers['Content-Length'] = Buffer.byteLength(postData);
      
      const resp = await new Promise((resolve, reject) => {
        const r = http.request(options, (res) => {
          const chunks = [];
          res.on('data', c => chunks.push(c));
          res.on('end', () => {
            resolve({
              status: res.statusCode,
              headers: res.headers,
              body: Buffer.concat(chunks),
            });
          });
        });
        r.on('error', reject);
        r.write(postData);
        r.end();
      });

      await route.fulfill({
        status: resp.status,
        headers: { 'content-type': resp.headers['content-type'] || 'application/json', 'access-control-allow-origin': '*' },
        body: resp.body,
      });
    } else {
      // GET request - handle CAR binary responses properly
      const resp = await new Promise((resolve, reject) => {
        http.get(mockUrl, (res) => {
          const chunks = [];
          res.on('data', c => chunks.push(c));
          res.on('end', () => {
            resolve({
              status: res.statusCode,
              headers: res.headers,
              body: Buffer.concat(chunks),
            });
          });
        }).on('error', reject);
      });

      await route.fulfill({
        status: resp.status,
        headers: { 'content-type': resp.headers['content-type'] || 'application/octet-stream', 'access-control-allow-origin': '*' },
        body: resp.body,
      });
    }
  });

  // Intercept PLC directory
  await page.route('https://plc.directory/**', async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    const didPart = url.pathname;  // /did:plc:test123456
    const mockUrl = 'http://localhost:8081/plc.directory/' + didPart;

    const resp = await new Promise((resolve, reject) => {
      http.get(mockUrl, (res) => {
        const chunks = [];
        res.on('data', c => chunks.push(c));
        res.on('end', () => {
          resolve({
            status: res.statusCode,
            headers: res.headers,
            body: Buffer.concat(chunks),
          });
        });
      }).on('error', reject);
    });

    await route.fulfill({
      status: resp.status,
      headers: { 'content-type': 'application/json', 'access-control-allow-origin': '*' },
      body: resp.body,
    });
  });

  // Step 1: Navigate to the app
  console.log('\n--- Step 1: Navigate to app ---');
  await page.goto('http://localhost:8080/', { waitUntil: 'networkidle' });

  // Step 2: Wait for WASM to load
  console.log('--- Step 2: Wait for WASM load ---');
  await page.waitForFunction(() => typeof window.skeetDelete === 'function', { timeout: 30000 });
  console.log('✓ WASM loaded (skeetDelete function available)');

  // Check for "SkeetDelete WASM loaded" in console
  const wasmLoadedMsg = consoleMessages.find(m => m.includes('SkeetDelete WASM loaded'));
  if (wasmLoadedMsg) {
    console.log('✓ Console shows: "SkeetDelete WASM loaded"');
  } else {
    console.log('  (WASM loaded but console message may have been before route setup)');
  }

  // Step 3: Fill login form
  console.log('\n--- Step 3: Login ---');
  await page.fill('#identifier', 'test.bsky.social');
  await page.fill('#password', 'fake-app-password');
  console.log('✓ Filled handle and password');

  // Step 4: Click Sign In
  await page.click('#btn-login');
  console.log('✓ Clicked Sign In');

  // Step 5: Wait for login success (config card visible)
  await page.waitForFunction(() => {
    const el = document.getElementById('config-card');
    return el && el.style.display !== 'none';
  }, { timeout: 15000 });
  console.log('✓ Login succeeded (config card visible)');

  // Verify DID from progress
  const loginProgress = await page.evaluate(() => {
    return JSON.parse(window.skeetGetProgress());
  });
  console.log('  Login result did:', loginProgress.did || '(from earlier poll)');

  // Step 6: Check cleanup options (Likes should be checked by default)
  console.log('\n--- Step 6: Configure cleanup ---');
  const likeCheckbox = await page.$('input[type="checkbox"][value="like"]');
  const isLikeChecked = await likeCheckbox.isChecked();
  console.log('✓ Likes checkbox checked:', isLikeChecked);

  // Ensure dry run is enabled
  const dryRunCheckbox = await page.$('#dry-run');
  const isDryRun = await dryRunCheckbox.isChecked();
  console.log('✓ Dry run enabled:', isDryRun);

  // Step 7: Click Start Cleanup
  console.log('\n--- Step 7: Start Cleanup ---');
  await page.click('#btn-start');
  console.log('✓ Clicked Start Cleanup');

  // Step 8: Wait for progress - records found > 0
  await page.waitForFunction(() => {
    try {
      const el = document.getElementById('stat-found');
      return el && parseInt(el.textContent) > 0;
    } catch(e) { return false; }
  }, { timeout: 30000 });
  
  const foundCount = await page.$eval('#stat-found', el => el.textContent);
  console.log('✓ Records found:', foundCount);

  // Step 9: Wait for completion
  console.log('\n--- Step 9: Wait for completion ---');
  await page.waitForFunction(() => {
    try {
      const p = JSON.parse(window.skeetGetProgress());
      return p.state === 'completed' || p.state === 'error';
    } catch(e) { return false; }
  }, { timeout: 60000 });

  const finalProgress = await page.evaluate(() => JSON.parse(window.skeetGetProgress()));
  console.log('✓ Final state:', finalProgress.state);
  console.log('  Records found:', finalProgress.records_found);
  console.log('  Records deleted:', finalProgress.records_deleted);
  console.log('  Records skipped:', finalProgress.records_skipped);
  console.log('  Is dry run:', finalProgress.is_dry_run);
  console.log('  Current action:', finalProgress.current_action);

  // Wait for result card to appear
  await page.waitForFunction(() => {
    const el = document.getElementById('result-card');
    return el && el.classList.contains('visible');
  }, { timeout: 10000 }).catch(() => {});

  // Step 10: Take screenshot
  console.log('\n--- Step 10: Screenshot ---');
  await page.screenshot({ path: '/home/jon/dev/skeetdelete/e2e-result.png', fullPage: true });
  console.log('✓ Screenshot saved to /home/jon/dev/skeetdelete/e2e-result.png');

  // Verify results
  console.log('\n=== E2E Test Results ===');
  let passed = true;

  if (finalProgress.state !== 'completed') {
    console.log('✗ State should be "completed", got:', finalProgress.state);
    passed = false;
  } else {
    console.log('✓ State is "completed"');
  }

  if (finalProgress.records_found < 2) {
    console.log('✗ Should find at least 2 records, found:', finalProgress.records_found);
    passed = false;
  } else {
    console.log('✓ Records found >= 2 (' + finalProgress.records_found + ')');
  }

  if (!finalProgress.is_dry_run) {
    console.log('✗ Should be dry run');
    passed = false;
  } else {
    console.log('✓ Dry run mode confirmed');
  }

  // Check for errors
  if (finalProgress.error_message) {
    console.log('✗ Error message:', finalProgress.error_message);
    passed = false;
  } else {
    console.log('✓ No error messages');
  }

  // Print any relevant console errors
  const errors = consoleMessages.filter(m => m.includes('error') || m.includes('Error') || m.includes('PANIC'));
  if (errors.length > 0) {
    console.log('\nConsole errors/warnings:');
    errors.forEach(e => console.log('  ', e));
  }

  await browser.close();

  if (passed) {
    console.log('\n✅ ALL E2E TESTS PASSED');
    process.exit(0);
  } else {
    console.log('\n❌ SOME E2E TESTS FAILED');
    process.exit(1);
  }
}

main().catch(err => {
  console.error('E2E test error:', err);
  process.exit(1);
});