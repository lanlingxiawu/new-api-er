import { spawn, execFileSync, type ChildProcess } from 'node:child_process'
import { randomUUID } from 'node:crypto'
import { once } from 'node:events'
import {
  createWriteStream,
  mkdirSync,
  mkdtempSync,
  writeFileSync,
} from 'node:fs'
import { rm } from 'node:fs/promises'
import { createServer } from 'node:net'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { setTimeout as delay } from 'node:timers/promises'

async function freePort() {
  const server = createServer()
  server.listen(0, '127.0.0.1')
  await once(server, 'listening')
  const address = server.address()
  if (!address || typeof address === 'string') throw new Error('No test port')
  const port = address.port
  await new Promise<void>((resolve, reject) =>
    server.close((error) => (error ? reject(error) : resolve()))
  )
  return port
}

async function waitForServer(url: string, child: ChildProcess) {
  for (let attempt = 0; attempt < 180; attempt++) {
    if (child.exitCode !== null) {
      throw new Error(
        `Test service exited: ${child.exitCode}; see results/user-exclusive-ratio`
      )
    }
    try {
      const response = await fetch(url, { signal: AbortSignal.timeout(1_000) })
      if (response.ok) return
    } catch {
      /* Service is still starting. */
    }
    await delay(500)
  }
  throw new Error(`Test service did not start: ${url}`)
}

export default async function setup() {
  const repository = path.resolve('..')
  const scratch = mkdtempSync(path.join(tmpdir(), 'exclusive-ratio-e2e-'))
  const artifacts = path.resolve('results/user-exclusive-ratio')
  mkdirSync(artifacts, { recursive: true })
  const children: ChildProcess[] = []
  const logs: ReturnType<typeof createWriteStream>[] = []
  const cleanup = async () => {
    for (const child of children.reverse()) {
      if (child.exitCode === null) {
        const exited = once(child, 'exit')
        child.kill()
        await exited
      }
    }
    for (const log of logs) log.end()
    // Only remove the temporary directory created by this invocation.
    if (
      path.dirname(path.resolve(scratch)) !== path.resolve(tmpdir()) ||
      !path.basename(scratch).startsWith('exclusive-ratio-e2e-')
    ) {
      throw new Error('Unexpected fixture directory')
    }
    await rm(scratch, { recursive: true, force: true })
  }

  try {
    const backendPort = await freePort()
    const frontendPort = await freePort()
    const backend = `http://127.0.0.1:${backendPort}`
    const frontend = `http://127.0.0.1:${frontendPort}`
    const executable = path.join(
      scratch,
      process.platform === 'win32' ? 'gateway.exe' : 'gateway'
    )
    const database = path.join(scratch, 'fixture.db')
    process.stdout.write(
      'Building current Go backend for isolated browser integration tests...\n'
    )
    execFileSync('go', ['build', '-o', executable, '.'], {
      cwd: repository,
      stdio: 'inherit',
      windowsHide: true,
    })
    const backendLog = createWriteStream(path.join(artifacts, 'backend.log'))
    logs.push(backendLog)
    const gateway = spawn(executable, ['--port', String(backendPort)], {
      cwd: scratch,
      windowsHide: true,
      env: {
        ...process.env,
        SQL_DSN: '',
        LOG_SQL_DSN: '',
        REDIS_CONN_STRING: '',
        SQLITE_PATH: database,
        SESSION_SECRET: randomUUID(),
        CRYPTO_SECRET: randomUUID(),
        DEFAULT_LANGUAGE: 'en',
        GIN_MODE: 'release',
        NODE_TYPE: 'master',
      },
      stdio: ['ignore', 'pipe', 'pipe'],
    })
    children.push(gateway)
    gateway.stdout?.pipe(backendLog)
    gateway.stderr?.pipe(backendLog)
    await waitForServer(`${backend}/api/setup`, gateway)
    const password = `${randomUUID()}aA9!`
    const setupResponse = await fetch(`${backend}/api/setup`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        username: 'ratio_root',
        password,
        confirmPassword: password,
      }),
    })
    if (!((await setupResponse.json()) as { success: boolean }).success) {
      throw new Error('Isolated setup failed')
    }

    // Seed only this invocation's database. User-list and search requests are
    // never mocked. The test root is authenticated through the actual login UI.
    execFileSync(
      'bun',
      [
        '--eval',
        `
      import { Database } from 'bun:sqlite';
      const db = new Database(process.argv[1]);
      db.exec('PRAGMA busy_timeout=5000');
      const insert = db.query('INSERT INTO users (username,password,role,status,"group",group_ratios,aff_code,setting,created_at) VALUES (?,?,1,1,?,?,?,?,?)');
      db.transaction(() => {
        for (let i=0; i<25; i++) insert.run('exr-ratio-'+String(i).padStart(2,'0'),'fixture-no-login',i<23?'default':'vip',JSON.stringify({default:i===0?0:0.5}),'ratio'+i,'{}',1700000000);
        const empty = ['',null,'{}',' { '+String.fromCharCode(10)+' } '];
        for (let i=0; i<24; i++) insert.run('exr-plain-'+String(i).padStart(2,'0'),'fixture-no-login',i<22?'default':'vip',empty[i%4],'plain'+i,'{}',1700000000);
      })();
      db.close();
    `,
        database,
      ],
      { cwd: repository, stdio: 'inherit', windowsHide: true }
    )

    const frontendLog = createWriteStream(path.join(artifacts, 'frontend.log'))
    logs.push(frontendLog)
    // Build into the isolated fixture directory, preserving the workspace dist.
    // Preview production assets to avoid dev overlays and lazy compilation.
    const rsbuild = path.join(
      repository,
      'web/node_modules/@rsbuild/core/bin/rsbuild.js'
    )
    const frontendDist = path.join(scratch, 'web')
    process.stdout.write('Building current frontend production assets...\n')
    const buildOutput = execFileSync(
      process.execPath,
      [rsbuild, 'build', '--dist-path', frontendDist],
      {
        cwd: path.join(repository, 'web'),
        windowsHide: true,
        env: { ...process.env, VITE_REACT_APP_SERVER_URL: backend },
        timeout: 240_000,
        maxBuffer: 16 * 1024 * 1024,
      }
    )
    writeFileSync(path.join(artifacts, 'frontend-build.log'), buildOutput)
    const ui = spawn(
      process.execPath,
      [
        rsbuild,
        'preview',
        '--dist-path',
        frontendDist,
        '--host',
        '127.0.0.1',
        '--port',
        String(frontendPort),
      ],
      {
        cwd: path.join(repository, 'web'),
        windowsHide: true,
        env: { ...process.env, VITE_REACT_APP_SERVER_URL: backend },
        stdio: ['ignore', 'pipe', 'pipe'],
      }
    )
    children.push(ui)
    ui.stdout?.pipe(frontendLog)
    ui.stderr?.pipe(frontendLog)
    await waitForServer(`${frontend}/sign-in`, ui)
    process.env.E2E_EXCLUSIVE_BASE_URL = frontend
    process.env.E2E_EXCLUSIVE_PASSWORD = password
    process.stdout.write(
      'Current frontend and backend are ready; testing via Chrome.\n'
    )
    return cleanup
  } catch (error) {
    await cleanup()
    throw error
  }
}
