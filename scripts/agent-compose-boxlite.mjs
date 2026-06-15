#!/usr/bin/env node
import { JsBoxlite, SimpleBox } from '@boxlite-ai/boxlite';
import { setTimeout as delay } from 'node:timers/promises';
import http from 'node:http';
import fs from 'node:fs/promises';
import path from 'node:path';

function readStdin() {
  return new Promise((resolve, reject) => {
    const chunks = [];
    process.stdin.on('data', (chunk) => chunks.push(chunk));
    process.stdin.on('end', () => resolve(Buffer.concat(chunks).toString('utf8')));
    process.stdin.on('error', reject);
  });
}

function shellQuote(value) {
  return `'${String(value).replace(/'/g, `'"'"'`)}'`;
}

function buildRuntime(request) {
  const { config } = request;
  return new JsBoxlite({
    homeDir: config.boxliteHome,
    imageRegistries: [config.imageRegistry],
  });
}

function buildBox(request, runtime) {
  const { config, session } = request;
  const options = {
    runtime,
    name: session.runtimeRef,
    reuseExisting: true,
    autoRemove: false,
    detach: true,
    workingDir: config.guestWorkspacePath,
    volumes: [
      { hostPath: session.workspacePath, guestPath: config.guestWorkspacePath },
      { hostPath: session.contextPath, guestPath: config.guestContextPath, readOnly: true },
      { hostPath: session.skillsPath, guestPath: config.guestSkillsPath, readOnly: true },
      { hostPath: config.runtimeAssetRoot, guestPath: config.guestRuntimeRoot, readOnly: true },
    ],
    ports: [{ hostPort: session.hostPort, guestPort: config.jupyterGuestPort }],
  };
  if (config.boxRootfsPath) {
    options.rootfsPath = config.boxRootfsPath;
  } else {
    options.image = config.defaultImage;
  }
  return new SimpleBox(options);
}

async function httpRequest(url) {
  return new Promise((resolve, reject) => {
    const req = http.get(url, (res) => {
      const chunks = [];
      res.on('data', (chunk) => chunks.push(chunk));
      res.on('end', () => resolve({
        statusCode: res.statusCode ?? 0,
        body: Buffer.concat(chunks).toString('utf8'),
      }));
    });
    req.on('error', reject);
  });
}

async function waitForJupyter(session, timeoutMs) {
  const url = `http://127.0.0.1:${session.hostPort}/api/kernelspecs?token=${encodeURIComponent(session.token)}`;
  const deadline = Date.now() + timeoutMs;
  let lastError = '';
  while (Date.now() < deadline) {
    try {
      const resp = await httpRequest(url);
      if (resp.statusCode >= 200 && resp.statusCode < 500 && (resp.body.includes('javascript') || resp.body.includes('python3') || resp.body.includes('bash'))) {
        return;
      }
      lastError = `unexpected status ${resp.statusCode}`;
    } catch (err) {
      lastError = err instanceof Error ? err.message : String(err);
    }
    await delay(1000);
  }
  throw new Error(`jupyter did not become ready on ${url}: ${lastError}`);
}

async function readLog(box, guestWorkspacePath) {
  try {
    const result = await box.exec('sh', ['-lc', `cat ${shellQuote(path.posix.join(path.posix.dirname(guestWorkspacePath), 'logs', 'jupyter.log'))} 2>/dev/null || true`]);
    return result.stdout.trim();
  } catch {
    return '';
  }
}

function jupyterLaunchCommand(config, session) {
  const logDir = path.posix.join(path.posix.dirname(config.guestWorkspacePath), 'logs');
  const logPath = path.posix.join(logDir, 'jupyter.log');
  const pythonPath = `${config.guestRuntimeRoot}/python/site-packages`;
  const jupyterPath = `${pythonPath}/share/jupyter`;
  const appDir = `${jupyterPath}/lab`;
  return [
    'set -eu',
    `mkdir -p ${shellQuote(logDir)}`,
    `export PYTHONPATH=${pythonPath}:${'$'}{PYTHONPATH:-}`,
    `export JUPYTER_PATH=${shellQuote(jupyterPath)}`,
    `nohup python3 -m jupyterlab --ServerApp.ip=0.0.0.0 --ServerApp.port=${config.jupyterGuestPort} --ServerApp.root_dir=${shellQuote(config.guestWorkspacePath)} --IdentityProvider.token=${shellQuote(session.token)} --ServerApp.password= --ServerApp.allow_origin='*' --ServerApp.disable_check_xsrf=True --LabApp.app_dir=${shellQuote(appDir)} --allow-root --no-browser > ${shellQuote(logPath)} 2>&1 < /dev/null &`,
  ].join(' && ');
}

async function startSession(request) {
  const { config, session } = request;
  await fs.access(config.runtimeAssetRoot);
  await fs.access(`${config.runtimeAssetRoot}/python/site-packages`);
  if (config.boxRootfsPath) {
    await fs.access(config.boxRootfsPath);
  }

  const runtime = buildRuntime(request);
  try {
    const box = buildBox(request, runtime);
    await box.exec('sh', ['-lc', `mkdir -p ${shellQuote(config.guestWorkspacePath)} ${shellQuote(path.posix.join(path.posix.dirname(config.guestWorkspacePath), 'logs'))}`]);
    try {
      await waitForJupyter(session, request.startTimeoutMs ?? 120000);
    } catch {
      await box.exec('sh', ['-lc', jupyterLaunchCommand(config, session)]);
      try {
        await waitForJupyter(session, request.startTimeoutMs ?? 120000);
      } catch (err) {
        const logText = await readLog(box, config.guestWorkspacePath);
        throw new Error(`${err instanceof Error ? err.message : String(err)}${logText ? `\nGuest log:\n${logText}` : ''}`);
      }
    }

    const boxID = await box.getId();
    const info = await box.getInfo();
    return {
      boxID,
      boxName: box.name,
      jupyterURL: `http://127.0.0.1:${session.hostPort}/lab?token=${encodeURIComponent(session.token)}`,
      info,
    };
  } finally {
    await runtime.close();
  }
}

async function stopSession(request) {
  const runtime = buildRuntime(request);
  try {
    const existing = await runtime.get(request.session.runtimeRef);
    if (!existing) {
      return { stopped: false, missing: true };
    }
    await existing.stop();
    return { stopped: true, missing: false };
  } finally {
    await runtime.close();
  }
}

async function execInSession(request) {
  const runtime = buildRuntime(request);
  try {
    const box = buildBox(request, runtime);
    const spec = request.exec ?? {};
    const result = await box.exec(
      spec.command,
      spec.args ?? [],
      spec.env ?? {},
      {
        cwd: spec.cwd || request.config.guestWorkspacePath,
        timeoutSecs: request.timeoutSecs,
      },
    );
    return {
      exitCode: result.exitCode,
      stdout: result.stdout,
      stderr: result.stderr,
      success: result.exitCode === 0,
    };
  } finally {
    await runtime.close();
  }
}

async function main() {
  const raw = await readStdin();
  const request = JSON.parse(raw || '{}');
  let result;
  if (request.action === 'start-session') {
    result = await startSession(request);
  } else if (request.action === 'stop-session') {
    result = await stopSession(request);
  } else if (request.action === 'exec') {
    result = await execInSession(request);
  } else {
    throw new Error(`unsupported action: ${request.action}`);
  }
  process.stdout.write(`${JSON.stringify(result)}\n`);
}

main().catch((err) => {
  const message = err instanceof Error ? err.stack || err.message : String(err);
  process.stderr.write(`${message}\n`);
  process.exit(1);
});
