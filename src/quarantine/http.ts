/**
 * Quarantine HTTP endpoint.
 *
 * This is a standalone HTTP handler that accepts push requests from external
 * sources and writes them into the quarantine SQLite database.  It never
 * touches the bot's workspace, memory, or session data.
 *
 * Endpoints:
 *   POST /quarantine/push      — submit a new item
 *   GET  /quarantine/pending    — count pending items (for pusher health checks)
 */

import { createHash } from "node:crypto";
import type { IncomingMessage, ServerResponse } from "node:http";
import { extractHookToken, readJsonBody } from "../gateway/hooks.js";
import { createSubsystemLogger } from "../logging/subsystem.js";
import { safeEqualSecret } from "../security/secret-equal.js";
import type { QuarantineConfigResolved } from "./config.js";
import type { QuarantineStore } from "./store.js";
import type { QuarantineContentKind, QuarantinePushPayload } from "./types.js";

const log = createSubsystemLogger("quarantine");

const VALID_KINDS = new Set<QuarantineContentKind>(["memory", "file", "message"]);

function sendJson(res: ServerResponse, status: number, body: unknown) {
  res.statusCode = status;
  res.setHeader("Content-Type", "application/json; charset=utf-8");
  res.end(JSON.stringify(body));
}

/** Derive a short, non-reversible source identifier from the bearer token. */
function tokenToSourceId(token: string): string {
  return createHash("sha256").update(token).digest("hex").slice(0, 12);
}

function validatePushPayload(
  raw: Record<string, unknown>,
  maxContentBytes: number,
): { ok: true; value: QuarantinePushPayload } | { ok: false; error: string } {
  const kind = raw.kind;
  if (typeof kind !== "string" || !VALID_KINDS.has(kind as QuarantineContentKind)) {
    return { ok: false, error: `kind must be one of: ${[...VALID_KINDS].join(", ")}` };
  }

  const content = raw.content;
  if (typeof content !== "string" || !content.trim()) {
    return { ok: false, error: "content is required and must be a non-empty string" };
  }
  if (Buffer.byteLength(content, "utf-8") > maxContentBytes) {
    return {
      ok: false,
      error: `content exceeds maximum size of ${maxContentBytes} bytes`,
    };
  }

  const label = typeof raw.label === "string" ? raw.label.trim() || undefined : undefined;
  const targetPath =
    typeof raw.targetPath === "string" ? raw.targetPath.trim() || undefined : undefined;

  // Prevent path traversal in targetPath.
  if (targetPath) {
    if (targetPath.includes("..") || targetPath.startsWith("/") || targetPath.startsWith("\\")) {
      return { ok: false, error: "targetPath must be a relative path without .." };
    }
  }

  const metadata =
    typeof raw.metadata === "object" && raw.metadata !== null
      ? (raw.metadata as Record<string, unknown>)
      : undefined;

  return {
    ok: true,
    value: {
      kind: kind as QuarantineContentKind,
      content: content.trim(),
      label,
      targetPath,
      metadata,
    },
  };
}

export type QuarantineRequestHandler = (
  req: IncomingMessage,
  res: ServerResponse,
) => Promise<boolean>;

export function createQuarantineRequestHandler(opts: {
  getConfig: () => QuarantineConfigResolved | null;
  getStore: () => QuarantineStore | null;
  bindHost: string;
  port: number;
}): QuarantineRequestHandler {
  const { getConfig, getStore, bindHost, port } = opts;

  return async (req, res) => {
    const config = getConfig();
    if (!config) {
      return false;
    }

    const url = new URL(req.url ?? "/", `http://${bindHost}:${port}`);
    const basePath = config.basePath;

    if (url.pathname !== basePath && !url.pathname.startsWith(`${basePath}/`)) {
      return false;
    }

    // --- Auth (separate token from hooks/gateway) ---
    const token = extractHookToken(req);
    if (!safeEqualSecret(token, config.token)) {
      res.statusCode = 401;
      res.setHeader("Content-Type", "text/plain; charset=utf-8");
      res.end("Unauthorized");
      return true;
    }

    const subPath = url.pathname.slice(basePath.length).replace(/^\/+/, "");

    // GET /quarantine/pending — lightweight health check for pushers.
    if (subPath === "pending" && req.method === "GET") {
      const store = getStore();
      if (!store) {
        sendJson(res, 503, { ok: false, error: "quarantine store unavailable" });
        return true;
      }
      const count = store.count("pending");
      sendJson(res, 200, { ok: true, pending: count });
      return true;
    }

    // Everything else is POST-only.
    if (req.method !== "POST") {
      res.statusCode = 405;
      res.setHeader("Allow", "POST");
      res.setHeader("Content-Type", "text/plain; charset=utf-8");
      res.end("Method Not Allowed");
      return true;
    }

    if (subPath !== "push") {
      sendJson(res, 404, { ok: false, error: "not found" });
      return true;
    }

    // --- POST /quarantine/push ---
    const store = getStore();
    if (!store) {
      sendJson(res, 503, { ok: false, error: "quarantine store unavailable" });
      return true;
    }

    // Enforce pending-item cap.
    const pendingCount = store.count("pending");
    if (pendingCount >= config.maxPendingItems) {
      sendJson(res, 429, {
        ok: false,
        error: `quarantine is full (${pendingCount} pending items, max ${config.maxPendingItems})`,
      });
      return true;
    }

    const body = await readJsonBody(req, config.maxBodyBytes);
    if (!body.ok) {
      const status =
        body.error === "payload too large"
          ? 413
          : body.error === "request body timeout"
            ? 408
            : 400;
      sendJson(res, status, { ok: false, error: body.error });
      return true;
    }

    const raw =
      typeof body.value === "object" && body.value !== null
        ? (body.value as Record<string, unknown>)
        : {};

    const validated = validatePushPayload(raw, config.maxContentBytes);
    if (!validated.ok) {
      sendJson(res, 400, { ok: false, error: validated.error });
      return true;
    }

    const source = tokenToSourceId(token!);

    try {
      const result = store.push(validated.value, source);
      log.info(`quarantine push: id=${result.id} kind=${validated.value.kind} source=${source}`);
      sendJson(res, 201, { ok: true, ...result });
    } catch (err) {
      log.error(`quarantine push failed: ${String(err)}`);
      sendJson(res, 500, { ok: false, error: "internal error" });
    }

    return true;
  };
}
