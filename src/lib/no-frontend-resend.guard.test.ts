import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

/**
 * Browser must not depend on resend — email is server-side only
 * (internal/services/email_service.go). Prevents reintroducing VITE_ secret paths.
 */
describe("frontend dependency guard", () => {
  it("package.json must not list resend", () => {
    const pkg = JSON.parse(
      readFileSync(resolve(process.cwd(), "package.json"), "utf8"),
    ) as { dependencies?: Record<string, string>; devDependencies?: Record<string, string> };
    expect(pkg.dependencies?.resend).toBeUndefined();
    expect(pkg.devDependencies?.resend).toBeUndefined();
  });
});
