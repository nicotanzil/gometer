'use strict';

// Uses only its own loopback fixture, never an application's database URL.
const {spawn, spawnSync} = require('node:child_process');
const crypto = require('node:crypto');
const path = require('node:path');
const repo = path.resolve(__dirname, '..');

function docker(args) {
  const result = spawnSync('docker', args, {encoding: 'utf8'});
  if (result.status !== 0) {
    throw Error(`Docker fixture operation failed: ${result.stderr?.trim() || 'Docker unavailable'}`);
  }
  return result.stdout.trim();
}

async function main() {
  let containerID;
  let testProcess;
  let interrupted = false;
  function interrupt() {
    interrupted = true;
    testProcess?.kill('SIGTERM');
  }
  function checkInterruption() {
    if (interrupted) throw Error('PostgreSQL fixture verification interrupted.');
  }
  process.once('SIGINT', interrupt);
  process.once('SIGTERM', interrupt);

  try {
    const password = crypto.randomBytes(24).toString('hex');
    const name = 'gometer-workspace-fixture-' + crypto.randomBytes(6).toString('hex');
    containerID = docker([
      'run', '--rm', '-d', '--name', name, '--label', 'gometer.fixture=workspace',
      '--tmpfs', '/var/lib/postgresql', '-p', '127.0.0.1::5432',
      '-e', 'POSTGRES_USER=gometer_fixture', '-e', 'POSTGRES_PASSWORD=' + password,
      '-e', 'POSTGRES_DB=gometer_test_workspace', 'postgres:18',
    ]);

    let ready = false;
    for (let attempt = 0; attempt < 80; attempt++) {
      checkInterruption();
      const result = spawnSync('docker', [
        'exec', containerID, 'pg_isready', '-U', 'gometer_fixture', '-d', 'gometer_test_workspace',
      ], {stdio: 'ignore'});
      if (result.status === 0) {
        ready = true;
        break;
      }
      await new Promise(resolve => setTimeout(resolve, 250));
    }
    checkInterruption();
    if (!ready) throw Error('PostgreSQL fixture startup timed out.');

    const address = docker(['port', containerID, '5432/tcp']);
    if (!/^127\.0\.0\.1:\d+$/.test(address)) {
      throw Error('Fixture is not exclusively loopback-bound.');
    }
    const dsn = `postgres://gometer_fixture:${password}@${address}/gometer_test_workspace?sslmode=disable`;
    let verificationOutput = '';
    const code = await new Promise((resolve, reject) => {
      // Packages share one disposable schema, so run them sequentially.
      testProcess = spawn('go', [
        'test', '-race', '-p=1', './internal/workspace', './internal/cli', './internal/team',
        '-run', 'TestPostgres|TestWorkspace|TestDatabase', '-count=1', '-v',
      ], {
        cwd: repo,
        stdio: ['ignore', 'pipe', 'inherit'],
        env: {...process.env, GOMETER_TEST_DATABASE_URL: dsn},
      });
      testProcess.stdout.on('data', chunk => {
        verificationOutput += chunk;
        process.stdout.write(chunk);
      });
      testProcess.once('error', reject);
      testProcess.once('close', code => resolve(code ?? 1));
    });
    testProcess = undefined;
    checkInterruption();
    if (code !== 0) throw Error('Workspace PostgreSQL integration tests failed.');
    const skipped = [...verificationOutput.matchAll(/--- SKIP: (TestPostgres\w+)/g)]
      .map(match => match[1])
      .filter(name => name !== 'TestPostgresTeamBrowser' || process.env.GOMETER_TEAM_BROWSER === '1');
    if (skipped.length || !verificationOutput.includes('--- PASS: TestPostgresWorkspaceAdministrationCommands') ||
        !verificationOutput.includes('--- PASS: TestPostgresWorkspaceServeStartsWithEnvironment')) {
      throw Error('Required PostgreSQL cases were skipped or did not run.');
    }
    console.log('PostgreSQL fixture tests passed; no personal workspace or external database was used.');
  } finally {
    try {
      if (containerID) docker(['rm', '-f', containerID]);
    } finally {
      process.removeListener('SIGINT', interrupt);
      process.removeListener('SIGTERM', interrupt);
    }
  }
}

main().catch(error => {
  console.error(error.message);
  process.exitCode = 1;
});
