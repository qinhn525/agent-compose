#!/usr/bin/env node

import { spawn } from 'node:child_process';
import fs from 'node:fs/promises';
import path from 'node:path';
import process from 'node:process';
import { fileURLToPath } from 'node:url';
import { inspect } from 'node:util';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const repoRoot = path.resolve(__dirname, '..');
const playgroundDir = path.join(repoRoot, 'playground');
const stateDir = path.join(__dirname, '.state');
const defaultMaxRound = 32;
let activeLogger = null;

function usage() {
  console.error('usage: node agent-script/implement_and_verify.js <prompt> [--session-id <id>] [--max-round <n>]');
}

function parseArgs(argv) {
  const positional = [];
  const flags = new Map();
  for (let i = 0; i < argv.length; i += 1) {
    const item = argv[i];
    if (!item.startsWith('--')) {
      positional.push(item);
      continue;
    }
    const key = item.slice(2);
    const next = argv[i + 1];
    if (next === undefined || next.startsWith('--')) {
      flags.set(key, 'true');
      continue;
    }
    flags.set(key, next);
    i += 1;
  }
  return { positional, flags };
}

function parseMaxRound(raw) {
  if (raw == null || raw === '') {
    return defaultMaxRound;
  }
  const value = Number.parseInt(String(raw), 10);
  if (!Number.isFinite(value) || value <= 0) {
    throw new Error(`invalid --max-round value: ${raw}`);
  }
  return value;
}

function now() {
  return new Date().toISOString();
}

function formatError(error) {
  if (error instanceof Error) {
    return error.stack || error.message;
  }
  try {
    return JSON.stringify(error, null, 2);
  } catch {
    return inspect(error, { depth: 8, breakLength: 120 });
  }
}

function normalizeText(value) {
  return String(value || '').replace(/\r\n/g, '\n');
}

async function ensureStateDir() {
  await fs.mkdir(stateDir, { recursive: true });
}

function sessionLogPath(sessionId) {
  return path.join(stateDir, sessionId, 'log.txt');
}

class SessionLogger {
  constructor(initialSessionId = '') {
    this.sessionId = initialSessionId;
    this.pendingChunks = [];
  }

  currentLogPath() {
    return this.sessionId ? sessionLogPath(this.sessionId) : '';
  }

  async setSessionId(sessionId) {
    if (!sessionId || sessionId === this.sessionId) {
      return;
    }
    this.sessionId = sessionId;
    const target = this.currentLogPath();
    await fs.mkdir(path.dirname(target), { recursive: true });
    if (this.pendingChunks.length > 0) {
      const buffered = this.pendingChunks.join('');
      this.pendingChunks = [];
      await fs.appendFile(target, buffered, 'utf8');
    }
  }

  async append(text) {
    const chunk = normalizeText(text);
    if (!chunk) {
      return;
    }
    if (!this.sessionId) {
      this.pendingChunks.push(chunk);
      return;
    }
    const target = this.currentLogPath();
    await fs.mkdir(path.dirname(target), { recursive: true });
    await fs.appendFile(target, chunk, 'utf8');
  }

  async line(text = '') {
    const line = text.endsWith('\n') ? text : `${text}\n`;
    process.stdout.write(line);
    await this.append(line);
  }
}

function threadOptions() {
  return {
    workingDirectory: repoRoot,
    additionalDirectories: [repoRoot, stateDir, process.env.HOME || repoRoot],
    skipGitRepoCheck: true,
    sandboxMode: 'danger-full-access',
    approvalPolicy: 'never',
    networkAccessEnabled: true,
  };
}

function extractItemText(item) {
  if (!item || typeof item !== 'object') {
    return '';
  }
  if (typeof item.text === 'string') {
    return item.text;
  }
  if (Array.isArray(item.content)) {
    return item.content
      .map((entry) => {
        if (typeof entry === 'string') {
          return entry;
        }
        if (entry && typeof entry.text === 'string') {
          return entry.text;
        }
        return '';
      })
      .join('');
  }
  return '';
}

async function runAgentTurn(prompt, previousSessionId, logger) {
  const { Codex } = await import('@openai/codex-sdk');
  const codex = new Codex({
    env: { ...process.env },
  });

  const thread = previousSessionId
    ? codex.resumeThread(previousSessionId, threadOptions())
    : codex.startThread(threadOptions());

  let sessionId = previousSessionId || '';
  let finalText = '';
  let transcript = '';

  await logger.line(`\n[agent] ${previousSessionId ? 'resume' : 'start'} session`);
  const { events } = await thread.runStreamed(prompt);
  for await (const event of events) {
    if (event.type === 'thread.started' && event.thread_id) {
      sessionId = event.thread_id;
      await logger.setSessionId(sessionId);
      continue;
    }
    if (event.type === 'turn.failed') {
      throw new Error(event.error?.message || 'codex turn failed');
    }
    const item = event.item;
    if (!item) {
      continue;
    }
    if (item.type === 'agent_message') {
      const text = extractItemText(item);
      if (text) {
        finalText = text;
        transcript += text;
        await logger.line(text);
      }
      continue;
    }
    if (item.type === 'command_execution' && event.type === 'item.started') {
      await logger.line(`\n[agent-command] ${item.command}`);
      continue;
    }
    if (item.type === 'command_execution' && item.aggregated_output && event.type === 'item.completed') {
      await logger.line(item.aggregated_output);
      continue;
    }
    if (item.type === 'file_change' && event.type === 'item.completed') {
      const changes = Array.isArray(item.changes) ? item.changes : [];
      if (changes.length > 0) {
        await logger.line('[agent-file-change]');
        for (const change of changes) {
          await logger.line(`${change.kind}: ${change.path}`);
        }
      }
    }
  }

  sessionId = thread.id || sessionId;
  await logger.setSessionId(sessionId);
  await logger.line(`[agent] session_id=${sessionId}`);
  return { sessionId, finalText, transcript };
}

async function runShellCommand(command, cwd, logger) {
  await logger.line(`\n[shell] cwd=${cwd}`);
  await logger.line(`[shell] cmd=${command}`);

  return await new Promise((resolve, reject) => {
    const child = spawn('bash', ['-lc', command], {
      cwd,
      env: { ...process.env },
      stdio: ['ignore', 'pipe', 'pipe'],
    });

    let stdout = '';
    let stderr = '';

    child.stdout.on('data', (chunk) => {
      const text = String(chunk || '');
      stdout += text;
      process.stdout.write(text);
    });

    child.stderr.on('data', (chunk) => {
      const text = String(chunk || '');
      stderr += text;
      process.stderr.write(text);
    });

    child.once('error', reject);
    child.once('close', async (code) => {
      const combined = `${stdout}${stderr}`;
      if (combined) {
        await logger.append(combined.endsWith('\n') ? combined : `${combined}\n`);
      }
      resolve({ code: code ?? 1, stdout, stderr });
    });
  });
}

async function verifyCommands(logger) {
  const commands = [
    { command: 'task image:agent-compose', cwd: repoRoot },
    { command: 'docker compose up -d', cwd: playgroundDir },
  ];

  for (const entry of commands) {
    const result = await runShellCommand(entry.command, entry.cwd, logger);
    if (result.code !== 0) {
      return {
        ok: false,
        failedCommand: entry.command,
        cwd: entry.cwd,
        code: result.code,
        stdout: result.stdout,
        stderr: result.stderr,
      };
    }
  }

  return { ok: true };
}

function buildRetryPrompt(originalPrompt, failure, attempt) {
  const output = normalizeText(`${failure.stdout}${failure.stderr}`).trim() || '(no output)';
  return [
    '继续处理同一个任务，并直接修改当前仓库直到验证通过。',
    '',
    `原始任务: ${originalPrompt}`,
    `失败轮次: ${attempt}`,
    `失败命令: ${failure.failedCommand}`,
    `执行目录: ${failure.cwd}`,
    `退出码: ${failure.code}`,
    `错误日志文件: ${failure.logPath || '(session id not available yet)'}`,
    '',
    '这是刚刚失败命令的完整输出：',
    '```text',
    output,
    '```',
    '',
    '请修复导致失败的问题，然后结束本轮。修复后我会重新执行相同的验证命令。',
  ].join('\n');
}

function buildPostDeployVerificationPrompt(originalPrompt, attempt) {
  return [
    '继续使用当前 session 验证刚刚启动的 playground 部署是否真的可用。',
    '',
    `原始任务: ${originalPrompt}`,
    `验证轮次: ${attempt}`,
    `仓库目录: ${repoRoot}`,
    `playground 目录: ${playgroundDir}`,
    '',
    '要求：',
    '1. 必须亲自运行必要的命令、HTTP 请求或容器检查来验证刚刚运转的功能。',
    '2. 如果验证过程中发现问题，可以直接继续修改并处理。',
    '3. 回复的最后一行必须严格是 `VERIFICATION_RESULT: PASS` 或 `VERIFICATION_RESULT: FAIL`。',
    '4. 除了这两个结果标记，不要输出其他类似标记。',
  ].join('\n');
}

function extractVerificationResult(agentResult) {
  const text = normalizeText(`${agentResult.finalText || ''}\n${agentResult.transcript || ''}`);
  const matches = [...text.matchAll(/VERIFICATION_RESULT:\s*(PASS|FAIL)/g)];
  if (matches.length === 0) {
    return '';
  }
  return matches[matches.length - 1][1];
}

async function runBoundedAgentTurn(prompt, sessionId, state, reason, logger) {
  if (state.usedRounds >= state.maxRounds) {
    throw new Error(`codex round limit exceeded: used=${state.usedRounds}, max=${state.maxRounds}, reason=${reason}`);
  }
  state.usedRounds += 1;
  await logger.line(`[agent] round=${state.usedRounds}/${state.maxRounds} reason=${reason}`);
  return await runAgentTurn(prompt, sessionId, logger);
}

async function main() {
  const { positional, flags } = parseArgs(process.argv.slice(2));
  const [prompt] = positional;
  if (!prompt) {
    usage();
    process.exit(1);
  }

  const maxRounds = parseMaxRound(flags.get('max-round'));
  const sessionArg = flags.get('session-id') || '';

  if (!(await fs.stat(playgroundDir).catch(() => null))) {
    throw new Error(`playground directory not found: ${playgroundDir}`);
  }

  await ensureStateDir();
  let sessionId = sessionArg;
  const roundState = { usedRounds: 0, maxRounds };
  const logger = new SessionLogger(sessionId);
  activeLogger = logger;

  await logger.line(`[start] ${now()}`);
  await logger.line(`[state] state_dir=${stateDir}`);
  await logger.line(`[state] initial_session_id=${sessionId || '(new session)'}`);
  await logger.line(`[state] initial_log_file=${logger.currentLogPath() || '(pending until session starts)'}`);
  await logger.line(`[state] max_round=${maxRounds}`);

  const initial = await runBoundedAgentTurn(prompt, sessionId, roundState, 'initial-task', logger);
  sessionId = initial.sessionId;

  let attempt = 1;
  while (true) {
    await logger.line(`\n[verify] attempt=${attempt}`);
    const result = await verifyCommands(logger);
    if (result.ok) {
      const verificationPrompt = buildPostDeployVerificationPrompt(prompt, attempt);
      const verification = await runBoundedAgentTurn(verificationPrompt, sessionId, roundState, 'post-deploy-verification', logger);
      sessionId = verification.sessionId;
      const verificationResult = extractVerificationResult(verification);
      await logger.line(`[verify] post_deploy_result=${verificationResult || 'UNKNOWN'}`);
      if (verificationResult === 'PASS') {
        await logger.line(`[success] verification passed on attempt ${attempt}`);
        await logger.line(`[success] session_id=${sessionId}`);
        await logger.line(`[success] log_file=${logger.currentLogPath()}`);
        break;
      }

      await logger.line('[verify] post-deploy verification did not pass, continuing loop');
      attempt += 1;
      continue;
    }

    result.logPath = logger.currentLogPath();
    await logger.line(`[verify] failed command=${result.failedCommand} code=${result.code}`);
    const retryPrompt = buildRetryPrompt(prompt, result, attempt);
    const retry = await runBoundedAgentTurn(retryPrompt, sessionId, roundState, 'repair-after-command-failure', logger);
    sessionId = retry.sessionId;
    attempt += 1;
  }
}

main().catch(async (error) => {
  const text = `[fatal] ${formatError(error)}\n`;
  process.stderr.write(text);
  try {
    if (activeLogger) {
      await activeLogger.append(text);
    }
  } catch {
    // Ignore logging failure during fatal shutdown.
  }
  process.exit(1);
});
