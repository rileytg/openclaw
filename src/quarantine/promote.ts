/**
 * Promotion: the trusted bridge between quarantine and the bot's workspace.
 *
 * This is the ONLY code path that reads from quarantine and writes into the
 * bot's filesystem.  It is invoked exclusively by explicit human action
 * (CLI approve command or gateway RPC).
 */

import fs from "node:fs/promises";
import path from "node:path";
import { resolveAgentWorkspaceDir } from "../agents/agent-scope.js";
import { resolveDefaultAgentId } from "../agents/agent-scope.js";
import type { OpenClawConfig } from "../config/config.js";
import type { QuarantineItem } from "./types.js";

export type PromotionResult = {
  id: string;
  targetPath: string;
  written: boolean;
};

/**
 * Promote an approved quarantine item into the bot's workspace.
 *
 * For `memory` and `file` items a `targetPath` is required.  The path is
 * resolved relative to the agent's workspace directory and must stay within
 * it (no escapes via `..`).
 *
 * For `message` items, no file write occurs — the caller is responsible for
 * injecting the message into a session if desired.
 */
export async function promoteItem(params: {
  item: QuarantineItem;
  cfg: OpenClawConfig;
  agentId?: string;
}): Promise<PromotionResult> {
  const { item, cfg } = params;

  if (item.status !== "approved") {
    throw new Error(
      `Cannot promote item ${item.id}: status is "${item.status}", expected "approved"`,
    );
  }

  if (item.kind === "message") {
    // Messages don't have a filesystem target.
    return { id: item.id, targetPath: "(message — no file)", written: false };
  }

  const targetPath = item.targetPath;
  if (!targetPath) {
    throw new Error(
      `Cannot promote ${item.kind} item ${item.id}: targetPath is required for "${item.kind}" items`,
    );
  }

  const agentId = params.agentId ?? resolveDefaultAgentId(cfg);
  const workspaceDir = resolveAgentWorkspaceDir(cfg, agentId);
  const resolved = path.resolve(workspaceDir, targetPath);

  // Safety: ensure the resolved path stays inside the workspace.
  if (!resolved.startsWith(workspaceDir + path.sep) && resolved !== workspaceDir) {
    throw new Error(`targetPath "${targetPath}" escapes workspace directory "${workspaceDir}"`);
  }

  await fs.mkdir(path.dirname(resolved), { recursive: true });

  // If the target already exists, append with a separator.
  try {
    const existing = await fs.readFile(resolved, "utf-8");
    const separator = "\n\n---\n\n";
    await fs.writeFile(resolved, existing + separator + item.content, "utf-8");
  } catch {
    // File doesn't exist — create it.
    await fs.writeFile(resolved, item.content, "utf-8");
  }

  return { id: item.id, targetPath: resolved, written: true };
}
