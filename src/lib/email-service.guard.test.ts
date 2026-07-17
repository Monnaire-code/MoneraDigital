import { existsSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

/**
 * Regression: browser-side Resend client must not ship.
 * VITE_RESEND_API_KEY would be bundled into the frontend if this file returned.
 * Email is sent by the Go backend (internal/services/email_service.go) only.
 */
describe("frontend email-service guard", () => {
  it("must not include src/lib/email-service.ts (dead code + VITE_ secret risk)", () => {
    const dir = dirname(fileURLToPath(import.meta.url));
    const target = resolve(dir, "email-service.ts");
    expect(existsSync(target)).toBe(false);
  });
});
