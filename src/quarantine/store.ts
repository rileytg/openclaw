/**
 * Quarantine store — a fully standalone SQLite database that lives *outside*
 * the bot's state directory.  Pushers write here; the bot never reads from it.
 *
 * The only bridge is the explicit `approve` path which copies content into the
 * bot's workspace via the promotion module.
 */

import { randomUUID } from "node:crypto";
import fs from "node:fs/promises";
import path from "node:path";
import type { DatabaseSync, StatementSync } from "node:sqlite";
import { requireNodeSqlite } from "../memory/sqlite.js";
import type {
  QuarantineItem,
  QuarantineItemStatus,
  QuarantineListFilter,
  QuarantineListResult,
  QuarantinePushPayload,
  QuarantinePushResult,
  QuarantineReviewPayload,
} from "./types.js";

const SCHEMA_VERSION = 1;

function ensureQuarantineSchema(db: DatabaseSync): void {
  db.exec(`
    CREATE TABLE IF NOT EXISTS meta (
      key   TEXT PRIMARY KEY,
      value TEXT NOT NULL
    );
  `);

  db.exec(`
    CREATE TABLE IF NOT EXISTS items (
      id          TEXT PRIMARY KEY,
      created_at  TEXT NOT NULL,
      updated_at  TEXT NOT NULL,
      status      TEXT NOT NULL DEFAULT 'pending',
      kind        TEXT NOT NULL,
      source      TEXT NOT NULL,
      label       TEXT,
      content     TEXT NOT NULL,
      target_path TEXT,
      metadata    TEXT,
      reviewed_by TEXT,
      review_note TEXT
    );
  `);

  db.exec(`CREATE INDEX IF NOT EXISTS idx_items_status ON items(status);`);
  db.exec(`CREATE INDEX IF NOT EXISTS idx_items_kind   ON items(kind);`);
  db.exec(`CREATE INDEX IF NOT EXISTS idx_items_source ON items(source);`);
  db.exec(`CREATE INDEX IF NOT EXISTS idx_items_created ON items(created_at);`);

  // Record schema version for future migrations.
  const existing = db.prepare(`SELECT value FROM meta WHERE key = 'schema_version'`).get() as
    | { value: string }
    | undefined;
  if (!existing) {
    db.prepare(`INSERT INTO meta (key, value) VALUES ('schema_version', ?)`).run(
      String(SCHEMA_VERSION),
    );
  }
}

function rowToItem(row: Record<string, unknown>): QuarantineItem {
  return {
    id: row.id as string,
    createdAt: row.created_at as string,
    updatedAt: row.updated_at as string,
    status: row.status as QuarantineItemStatus,
    kind: row.kind as QuarantineItem["kind"],
    source: row.source as string,
    label: (row.label as string) || undefined,
    content: row.content as string,
    targetPath: (row.target_path as string) || undefined,
    metadata: (row.metadata as string) || undefined,
    reviewedBy: (row.reviewed_by as string) || undefined,
    reviewNote: (row.review_note as string) || undefined,
  };
}

export class QuarantineStore {
  private readonly db: DatabaseSync;
  private readonly dbPath: string;

  // Prepared statements (lazily initialised).
  private stmtInsert: StatementSync | null = null;
  private stmtGetById: StatementSync | null = null;
  private stmtUpdateStatus: StatementSync | null = null;
  private stmtDelete: StatementSync | null = null;
  private stmtCountAll: StatementSync | null = null;

  private constructor(db: DatabaseSync, dbPath: string) {
    this.db = db;
    this.dbPath = dbPath;
  }

  /** Open (or create) a quarantine store at `dbPath`. */
  static async open(dbPath: string): Promise<QuarantineStore> {
    await fs.mkdir(path.dirname(dbPath), { recursive: true });
    const { DatabaseSync } = requireNodeSqlite();
    const db = new DatabaseSync(dbPath);
    // WAL mode for better concurrent-reader behaviour.
    db.exec(`PRAGMA journal_mode = WAL;`);
    db.exec(`PRAGMA foreign_keys = ON;`);
    ensureQuarantineSchema(db);
    return new QuarantineStore(db, dbPath);
  }

  /** Push a new item into quarantine. Returns the generated id. */
  push(payload: QuarantinePushPayload, source: string): QuarantinePushResult {
    const id = randomUUID();
    const now = new Date().toISOString();
    const metadata = payload.metadata ? JSON.stringify(payload.metadata) : null;

    if (!this.stmtInsert) {
      this.stmtInsert = this.db.prepare(`
        INSERT INTO items (id, created_at, updated_at, status, kind, source, label, content, target_path, metadata)
        VALUES (?, ?, ?, 'pending', ?, ?, ?, ?, ?, ?)
      `);
    }

    this.stmtInsert.run(
      id,
      now,
      now,
      payload.kind,
      source,
      payload.label ?? null,
      payload.content,
      payload.targetPath ?? null,
      metadata,
    );

    return { id, status: "pending" };
  }

  /** Get a single item by id, or null if not found. */
  get(id: string): QuarantineItem | null {
    if (!this.stmtGetById) {
      this.stmtGetById = this.db.prepare(`SELECT * FROM items WHERE id = ?`);
    }
    const row = this.stmtGetById.get(id) as Record<string, unknown> | undefined;
    return row ? rowToItem(row) : null;
  }

  /** List items with optional filters. */
  list(filter?: QuarantineListFilter): QuarantineListResult {
    const conditions: string[] = [];
    const params: unknown[] = [];

    if (filter?.status) {
      conditions.push(`status = ?`);
      params.push(filter.status);
    }
    if (filter?.kind) {
      conditions.push(`kind = ?`);
      params.push(filter.kind);
    }
    if (filter?.source) {
      conditions.push(`source = ?`);
      params.push(filter.source);
    }

    const where = conditions.length > 0 ? `WHERE ${conditions.join(" AND ")}` : "";
    const limit = filter?.limit ?? 50;
    const offset = filter?.offset ?? 0;

    const countRow = this.db
      .prepare(`SELECT COUNT(*) as total FROM items ${where}`)
      .get(...params) as { total: number };
    const total = countRow.total;

    const rows = this.db
      .prepare(`SELECT * FROM items ${where} ORDER BY created_at DESC LIMIT ? OFFSET ?`)
      .all(...params, limit, offset) as Array<Record<string, unknown>>;

    return {
      items: rows.map(rowToItem),
      total,
    };
  }

  /** Review (approve or reject) an item. Returns the updated item or null if not found. */
  review(id: string, payload: QuarantineReviewPayload): QuarantineItem | null {
    const existing = this.get(id);
    if (!existing) {
      return null;
    }
    if (existing.status !== "pending") {
      throw new Error(`Item ${id} has already been reviewed (status: ${existing.status})`);
    }

    const now = new Date().toISOString();

    if (!this.stmtUpdateStatus) {
      this.stmtUpdateStatus = this.db.prepare(`
        UPDATE items
        SET status = ?, updated_at = ?, reviewed_by = ?, review_note = ?
        WHERE id = ?
      `);
    }

    this.stmtUpdateStatus.run(
      payload.decision,
      now,
      payload.reviewedBy ?? null,
      payload.reviewNote ?? null,
      id,
    );

    return this.get(id);
  }

  /** Permanently delete an item. Returns true if it existed. */
  delete(id: string): boolean {
    if (!this.stmtDelete) {
      this.stmtDelete = this.db.prepare(`DELETE FROM items WHERE id = ?`);
    }
    const result = this.stmtDelete.run(id);
    return result.changes > 0;
  }

  /** Purge all items matching a status (or all items if no status given). */
  purge(status?: QuarantineItemStatus): number {
    if (status) {
      const result = this.db.prepare(`DELETE FROM items WHERE status = ?`).run(status);
      return result.changes;
    }
    const result = this.db.prepare(`DELETE FROM items`).run();
    return result.changes;
  }

  /** Total number of items in the store (optionally filtered by status). */
  count(status?: QuarantineItemStatus): number {
    if (status) {
      const row = this.db
        .prepare(`SELECT COUNT(*) as total FROM items WHERE status = ?`)
        .get(status) as { total: number };
      return row.total;
    }
    if (!this.stmtCountAll) {
      this.stmtCountAll = this.db.prepare(`SELECT COUNT(*) as total FROM items`);
    }
    const row = this.stmtCountAll.get() as { total: number };
    return row.total;
  }

  /** Close the database. */
  close(): void {
    try {
      this.db.close();
    } catch {
      // ignore
    }
  }

  /** The resolved database path. */
  get path(): string {
    return this.dbPath;
  }
}
