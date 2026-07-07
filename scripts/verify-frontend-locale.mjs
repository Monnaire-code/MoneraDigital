// scripts/verify-frontend-locale.mjs
// Smoke check: switch to Chinese locale via localStorage, reload,
// confirm Chinese month/format rendering and that no errors fire.

import { chromium } from "playwright";

const BASE = process.env.BASE_URL ?? "http://localhost:5001";
const browser = await chromium.launch();
const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 } });
const page = await ctx.newPage();

const consoleErrors = [];
const pageErrors = [];
page.on("console", (m) => { if (m.type() === "error") consoleErrors.push(m.text()); });
page.on("pageerror", (e) => pageErrors.push(e.message));

// Set i18next language before any script runs.
await page.addInitScript(() => {
  window.localStorage.setItem("i18n-language", "zh");
});

console.log(`\n=== Loading ${BASE} with locale=zh ===\n`);
await page.goto(BASE, { waitUntil: "domcontentloaded", timeout: 15000 });
await page.waitForTimeout(2500);

const bodyText = await page.evaluate(() => document.body.innerText);
const sampleLines = bodyText
  .split("\n")
  .map((s) => s.trim())
  .filter(Boolean)
  .filter((s) => /[\u4e00-\u9fff]/.test(s))
  .slice(0, 8);

await page.screenshot({ path: "/tmp/monera-homepage-zh.png", fullPage: false });

console.log(`Chinese sample lines (first 8 with CJK chars):`);
sampleLines.forEach((l) => console.log(`  - ${l.slice(0, 80)}`));

console.log(`\npage errors:  ${pageErrors.length}`);
console.log(`console errs: ${consoleErrors.length}`);
if (consoleErrors.length) {
  console.log(`  first: ${consoleErrors[0].slice(0, 160)}`);
}

await browser.close();
console.log(`\nScreenshot: /tmp/monera-homepage-zh.png`);
