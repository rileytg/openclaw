import { describe, expect, it } from "vitest";
import type { OpenClawConfig } from "../config/config.js";
import { resolveQuarantineConfig } from "./config.js";

describe("resolveQuarantineConfig", () => {
  it("returns null when not enabled", () => {
    expect(resolveQuarantineConfig({})).toBeNull();
    expect(resolveQuarantineConfig({ quarantine: { enabled: false } })).toBeNull();
  });

  it("throws when enabled without token", () => {
    expect(() =>
      resolveQuarantineConfig({
        quarantine: { enabled: true, dbPath: "/tmp/q.db" },
      } as OpenClawConfig),
    ).toThrow(/requires quarantine.token/);
  });

  it("throws when enabled without dbPath", () => {
    expect(() =>
      resolveQuarantineConfig({
        quarantine: { enabled: true, token: "secret" },
      } as OpenClawConfig),
    ).toThrow(/requires quarantine.dbPath/);
  });

  it("resolves valid config with defaults", () => {
    const result = resolveQuarantineConfig({
      quarantine: {
        enabled: true,
        token: "secret-token",
        dbPath: "/tmp/quarantine/q.db",
      },
    } as OpenClawConfig);

    expect(result).not.toBeNull();
    expect(result!.token).toBe("secret-token");
    expect(result!.basePath).toBe("/quarantine");
    expect(result!.maxBodyBytes).toBe(512 * 1024);
    expect(result!.maxPendingItems).toBe(1000);
    expect(result!.maxContentBytes).toBe(256 * 1024);
  });

  it("respects custom path and limits", () => {
    const result = resolveQuarantineConfig({
      quarantine: {
        enabled: true,
        token: "t",
        dbPath: "/tmp/q.db",
        path: "/inbox",
        maxBodyBytes: 1024,
        maxPendingItems: 50,
        maxContentBytes: 4096,
        purgeAfterDays: 30,
      },
    } as OpenClawConfig);

    expect(result!.basePath).toBe("/inbox");
    expect(result!.maxBodyBytes).toBe(1024);
    expect(result!.maxPendingItems).toBe(50);
    expect(result!.maxContentBytes).toBe(4096);
    expect(result!.purgeAfterDays).toBe(30);
  });

  it("rejects path '/'", () => {
    expect(() =>
      resolveQuarantineConfig({
        quarantine: { enabled: true, token: "t", dbPath: "/tmp/q.db", path: "/" },
      } as OpenClawConfig),
    ).toThrow(/may not be/);
  });
});
