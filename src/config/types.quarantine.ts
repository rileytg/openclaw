/**
 * Configuration for the quarantine system.
 *
 * The quarantine is a standalone system that lives *outside* the bot's
 * state directory.  Pushers write to it; the bot never reads from it.
 */

export type QuarantineConfig = {
  /** Enable the quarantine push endpoint. Default: false. */
  enabled?: boolean;
  /**
   * Path to the quarantine SQLite database.
   * MUST be outside the bot's state directory (~/.openclaw/).
   * Example: "/var/lib/openclaw-quarantine/quarantine.db"
   */
  dbPath?: string;
  /**
   * Bearer token required for pushers to submit items.
   * Must differ from the gateway auth token and the hooks token.
   */
  token?: string;
  /** HTTP path prefix for the quarantine endpoint. Default: "/quarantine". */
  path?: string;
  /** Maximum push payload size in bytes. Default: 512KB. */
  maxBodyBytes?: number;
  /** Maximum number of pending items before new pushes are rejected. Default: 1000. */
  maxPendingItems?: number;
  /** Maximum content size per item in bytes. Default: 256KB. */
  maxContentBytes?: number;
  /**
   * Auto-purge reviewed items older than this many days.
   * Only applies to approved/rejected items, never pending.
   * Default: no auto-purge.
   */
  purgeAfterDays?: number;
};
