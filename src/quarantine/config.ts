/**
 * Quarantine config resolution.
 *
 * Validates and resolves quarantine configuration from the OpenClaw config,
 * enforcing the constraint that the quarantine DB must live outside the
 * bot's state directory.
 */

import path from "node:path";
import type { OpenClawConfig } from "../config/config.js";
import { resolveStateDir } from "../config/paths.js";

const DEFAULT_QUARANTINE_PATH = "/quarantine";
const DEFAULT_MAX_BODY_BYTES = 512 * 1024;
const DEFAULT_MAX_PENDING_ITEMS = 1000;
const DEFAULT_MAX_CONTENT_BYTES = 256 * 1024;

export type QuarantineConfigResolved = {
  dbPath: string;
  token: string;
  basePath: string;
  maxBodyBytes: number;
  maxPendingItems: number;
  maxContentBytes: number;
  purgeAfterDays?: number;
};

export function resolveQuarantineConfig(cfg: OpenClawConfig): QuarantineConfigResolved | null {
  if (cfg.quarantine?.enabled !== true) {
    return null;
  }

  const token = cfg.quarantine.token?.trim();
  if (!token) {
    throw new Error("quarantine.enabled requires quarantine.token");
  }

  const dbPath = cfg.quarantine.dbPath?.trim();
  if (!dbPath) {
    throw new Error(
      "quarantine.enabled requires quarantine.dbPath — " +
        "the quarantine database must be stored outside the bot's state directory",
    );
  }

  // Enforce isolation: the quarantine DB must not live inside ~/.openclaw/.
  const stateDir = path.resolve(resolveStateDir());
  const resolvedDbPath = path.resolve(dbPath);
  if (resolvedDbPath === stateDir || resolvedDbPath.startsWith(stateDir + path.sep)) {
    throw new Error(
      `quarantine.dbPath must be outside the bot's state directory (${stateDir}). ` +
        `Pushers must never write to the bot's filesystem.`,
    );
  }

  const rawPath = cfg.quarantine.path?.trim() || DEFAULT_QUARANTINE_PATH;
  const withSlash = rawPath.startsWith("/") ? rawPath : `/${rawPath}`;
  const trimmed = withSlash.length > 1 ? withSlash.replace(/\/+$/, "") : withSlash;
  if (trimmed === "/") {
    throw new Error("quarantine.path may not be '/'");
  }

  return {
    dbPath: resolvedDbPath,
    token,
    basePath: trimmed,
    maxBodyBytes:
      cfg.quarantine.maxBodyBytes && cfg.quarantine.maxBodyBytes > 0
        ? cfg.quarantine.maxBodyBytes
        : DEFAULT_MAX_BODY_BYTES,
    maxPendingItems:
      cfg.quarantine.maxPendingItems && cfg.quarantine.maxPendingItems > 0
        ? cfg.quarantine.maxPendingItems
        : DEFAULT_MAX_PENDING_ITEMS,
    maxContentBytes:
      cfg.quarantine.maxContentBytes && cfg.quarantine.maxContentBytes > 0
        ? cfg.quarantine.maxContentBytes
        : DEFAULT_MAX_CONTENT_BYTES,
    purgeAfterDays: cfg.quarantine.purgeAfterDays,
  };
}
