import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { QuarantineStore } from "./store.js";

let tmpDir: string;
let dbPath: string;

beforeEach(async () => {
  tmpDir = await fs.mkdtemp(path.join(os.tmpdir(), "quarantine-test-"));
  dbPath = path.join(tmpDir, "quarantine.db");
});

afterEach(async () => {
  await fs.rm(tmpDir, { recursive: true, force: true });
});

describe("QuarantineStore", () => {
  it("opens and creates the database", async () => {
    const store = await QuarantineStore.open(dbPath);
    expect(store.path).toBe(dbPath);
    expect(store.count()).toBe(0);
    store.close();
  });

  it("pushes and retrieves an item", async () => {
    const store = await QuarantineStore.open(dbPath);
    const result = store.push(
      { kind: "memory", content: "The sky is blue.", label: "fact" },
      "test-source",
    );
    expect(result.id).toBeDefined();
    expect(result.status).toBe("pending");

    const item = store.get(result.id);
    expect(item).not.toBeNull();
    expect(item!.kind).toBe("memory");
    expect(item!.content).toBe("The sky is blue.");
    expect(item!.label).toBe("fact");
    expect(item!.source).toBe("test-source");
    expect(item!.status).toBe("pending");
    store.close();
  });

  it("lists items with filters", async () => {
    const store = await QuarantineStore.open(dbPath);
    store.push({ kind: "memory", content: "a" }, "src-a");
    store.push({ kind: "file", content: "b" }, "src-b");
    store.push({ kind: "message", content: "c" }, "src-a");

    const all = store.list();
    expect(all.total).toBe(3);
    expect(all.items).toHaveLength(3);

    const memoryOnly = store.list({ kind: "memory" });
    expect(memoryOnly.total).toBe(1);
    expect(memoryOnly.items[0].content).toBe("a");

    const srcA = store.list({ source: "src-a" });
    expect(srcA.total).toBe(2);

    store.close();
  });

  it("approves an item", async () => {
    const store = await QuarantineStore.open(dbPath);
    const { id } = store.push({ kind: "memory", content: "fact" }, "src");
    const updated = store.review(id, {
      decision: "approved",
      reviewedBy: "admin",
      reviewNote: "looks good",
    });
    expect(updated).not.toBeNull();
    expect(updated!.status).toBe("approved");
    expect(updated!.reviewedBy).toBe("admin");
    expect(updated!.reviewNote).toBe("looks good");
    store.close();
  });

  it("rejects an item", async () => {
    const store = await QuarantineStore.open(dbPath);
    const { id } = store.push({ kind: "memory", content: "spam" }, "src");
    const updated = store.review(id, { decision: "rejected" });
    expect(updated!.status).toBe("rejected");
    store.close();
  });

  it("throws when reviewing an already-reviewed item", async () => {
    const store = await QuarantineStore.open(dbPath);
    const { id } = store.push({ kind: "memory", content: "fact" }, "src");
    store.review(id, { decision: "approved" });
    expect(() => store.review(id, { decision: "rejected" })).toThrow(/already been reviewed/);
    store.close();
  });

  it("returns null for non-existent item", async () => {
    const store = await QuarantineStore.open(dbPath);
    expect(store.get("nonexistent")).toBeNull();
    expect(store.review("nonexistent", { decision: "approved" })).toBeNull();
    store.close();
  });

  it("deletes an item", async () => {
    const store = await QuarantineStore.open(dbPath);
    const { id } = store.push({ kind: "memory", content: "x" }, "src");
    expect(store.delete(id)).toBe(true);
    expect(store.get(id)).toBeNull();
    expect(store.delete(id)).toBe(false);
    store.close();
  });

  it("purges by status", async () => {
    const store = await QuarantineStore.open(dbPath);
    store.push({ kind: "memory", content: "a" }, "src");
    const { id } = store.push({ kind: "memory", content: "b" }, "src");
    store.review(id, { decision: "approved" });
    store.push({ kind: "memory", content: "c" }, "src");

    const purged = store.purge("approved");
    expect(purged).toBe(1);
    expect(store.count()).toBe(2);
    expect(store.count("pending")).toBe(2);
    store.close();
  });

  it("counts by status", async () => {
    const store = await QuarantineStore.open(dbPath);
    store.push({ kind: "memory", content: "a" }, "src");
    store.push({ kind: "memory", content: "b" }, "src");
    const { id } = store.push({ kind: "memory", content: "c" }, "src");
    store.review(id, { decision: "rejected" });

    expect(store.count()).toBe(3);
    expect(store.count("pending")).toBe(2);
    expect(store.count("rejected")).toBe(1);
    expect(store.count("approved")).toBe(0);
    store.close();
  });

  it("stores metadata as JSON", async () => {
    const store = await QuarantineStore.open(dbPath);
    const { id } = store.push(
      {
        kind: "file",
        content: "data",
        metadata: { origin: "github", pr: 42 },
        targetPath: "notes/pr42.md",
      },
      "src",
    );
    const item = store.get(id)!;
    expect(JSON.parse(item.metadata!)).toEqual({ origin: "github", pr: 42 });
    expect(item.targetPath).toBe("notes/pr42.md");
    store.close();
  });

  it("pagination works", async () => {
    const store = await QuarantineStore.open(dbPath);
    for (let i = 0; i < 10; i++) {
      store.push({ kind: "memory", content: `item-${i}` }, "src");
    }
    const page1 = store.list({ limit: 3, offset: 0 });
    expect(page1.items).toHaveLength(3);
    expect(page1.total).toBe(10);

    const page2 = store.list({ limit: 3, offset: 3 });
    expect(page2.items).toHaveLength(3);

    // No overlap.
    const ids1 = new Set(page1.items.map((i) => i.id));
    for (const item of page2.items) {
      expect(ids1.has(item.id)).toBe(false);
    }

    store.close();
  });
});
