/**
 * Quarantine system types.
 *
 * The quarantine is a **standalone** system, fully isolated from the bot's
 * workspace and memory.  Pushers write to the quarantine; the bot never reads
 * from it.  Only an explicit "approve" action promotes content into the bot's
 * workspace.
 */

/** Every item pushed into quarantine gets one of these states. */
export type QuarantineItemStatus = "pending" | "approved" | "rejected";

/** Content types that can be quarantined. */
export type QuarantineContentKind = "memory" | "file" | "message";

/** A single quarantined item as stored in the database. */
export type QuarantineItem = {
  /** Unique item id (UUIDv4). */
  id: string;
  /** ISO-8601 timestamp of when the item was pushed. */
  createdAt: string;
  /** ISO-8601 timestamp of last status change (or creation). */
  updatedAt: string;
  /** Current review status. */
  status: QuarantineItemStatus;
  /** What kind of content this is. */
  kind: QuarantineContentKind;
  /** Identifier of the pusher (token hash prefix, service name, etc.). */
  source: string;
  /** Human-readable label supplied by the pusher (optional). */
  label?: string;
  /** The actual content (text, markdown, etc.). */
  content: string;
  /** Suggested target path within the bot workspace (e.g. "memory/facts.md"). */
  targetPath?: string;
  /** Freeform metadata blob (JSON-serialised). */
  metadata?: string;
  /** Who approved/rejected (human id, CLI user, etc.). */
  reviewedBy?: string;
  /** Optional review note. */
  reviewNote?: string;
};

/** Shape returned by the push endpoint on success. */
export type QuarantinePushResult = {
  id: string;
  status: "pending";
};

/** Shape returned by list/inspect operations. */
export type QuarantineListResult = {
  items: QuarantineItem[];
  total: number;
};

/** Filter options for listing quarantined items. */
export type QuarantineListFilter = {
  status?: QuarantineItemStatus;
  kind?: QuarantineContentKind;
  source?: string;
  limit?: number;
  offset?: number;
};

/** Payload accepted by the push endpoint. */
export type QuarantinePushPayload = {
  kind: QuarantineContentKind;
  content: string;
  label?: string;
  targetPath?: string;
  metadata?: Record<string, unknown>;
};

/** Payload accepted by the review (approve/reject) endpoint. */
export type QuarantineReviewPayload = {
  decision: "approved" | "rejected";
  reviewedBy?: string;
  reviewNote?: string;
};
