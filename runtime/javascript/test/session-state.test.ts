import fs from "node:fs/promises";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { readStoredSession, sessionStatePath, writeStoredSession } from "../src/session-state.js";
import { withTempSession } from "./helpers.js";

describe("provider session state", () => {
  it("uses the compatible provider state path", async () => {
    await withTempSession(async (root) => {
      expect(sessionStatePath(path.join(root, "state"), "codex")).toBe(
        path.join(root, "state", "agents", "providers", "codex.json"),
      );
    });
  });

  it("returns null for absent or malformed state", async () => {
    await withTempSession(async (root) => {
      const stateRoot = path.join(root, "state");
      expect(await readStoredSession(stateRoot, "codex")).toBeNull();

      const target = sessionStatePath(stateRoot, "codex");
      await fs.mkdir(path.dirname(target), { recursive: true });
      await fs.writeFile(target, "{\"sessionId\": 3}", "utf8");

      expect(await readStoredSession(stateRoot, "codex")).toBeNull();
    });
  });

  it("writes and reads session id state", async () => {
    await withTempSession(async (root) => {
      const stateRoot = path.join(root, "state");
      const now = new Date("2026-01-01T00:00:00.000Z");

      await writeStoredSession(stateRoot, "claude", "session-1", now);

      await expect(readStoredSession(stateRoot, "claude")).resolves.toEqual({
        provider: "claude",
        sessionId: "session-1",
        updatedAt: now.toISOString(),
      });
    });
  });

  it("does not create state for an empty session id", async () => {
    await withTempSession(async (root) => {
      const stateRoot = path.join(root, "state");

      await writeStoredSession(stateRoot, "gemini", "");

      await expect(fs.stat(sessionStatePath(stateRoot, "gemini"))).rejects.toMatchObject({ code: "ENOENT" });
    });
  });
});
