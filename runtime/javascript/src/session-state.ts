import fs from "node:fs/promises";
import path from "node:path";
import { ensureDir, readText } from "./fs.js";
import type { Provider, StoredSession } from "./types.js";

export function sessionStatePath(stateRoot: string, provider: Provider): string {
  return path.join(stateRoot, "agents", "providers", `${provider}.json`);
}

export async function readStoredSession(stateRoot: string, provider: Provider): Promise<StoredSession | null> {
  try {
    const raw = await readText(sessionStatePath(stateRoot, provider));
    const payload = JSON.parse(raw);
    return typeof payload?.sessionId === "string" ? payload : null;
  } catch {
    return null;
  }
}

export async function writeStoredSession(
  stateRoot: string,
  provider: Provider,
  sessionId: string,
  now: Date = new Date(),
): Promise<void> {
  if (!sessionId) {
    return;
  }
  const target = sessionStatePath(stateRoot, provider);
  await ensureDir(path.dirname(target));
  const payload = {
    provider,
    sessionId,
    updatedAt: now.toISOString(),
  };
  await fs.writeFile(target, `${JSON.stringify(payload, null, 2)}\n`, "utf8");
}
