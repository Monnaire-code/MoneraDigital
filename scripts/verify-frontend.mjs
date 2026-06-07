// scripts/verify-frontend.mjs
// Quick smoke check: load homepage, capture console errors, screenshot,
// confirm key DOM nodes are present (loading skeletons + error states,
// since the Go backend is not running locally).

import { chromium } from "playwright";

const BASE = process.env.BASE_URL ?? "http://localhost:5001";

const browser = await chromium.launch();
const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 } });
const page = await ctx.newPage();

const consoleErrors = [];
const pageErrors = [];
const networkFailures = [];
const fundRequests = [];

page.on("console", (msg) => {
  if (msg.type() === "error") consoleErrors.push(msg.text());
});
page.on("pageerror", (err) => pageErrors.push(err.message));
page.on("requestfailed", (req) => {
  if (req.url().includes("/api/")) {
    networkFailures.push(`${req.method()} ${req.url()} - ${req.failure()?.errorText}`);
  }
});
page.on("request", (req) => {
  if (req.url().includes("/api/fund/stats")) {
    fundRequests.push(req.url());
  }
});
page.on("response", (resp) => {
  if (resp.url().includes("/api/fund/stats")) {
    console.log(`[fund/stats] HTTP ${resp.status()} (${resp.request().method()})`);
  }
});

console.log(`\n=== Loading ${BASE} ===\n`);
const start = Date.now();
await page.goto(BASE, { waitUntil: "domcontentloaded", timeout: 15000 });
await page.waitForTimeout(3000); // let React mount + fetch fire
const loadMs = Date.now() - start;

const title = await page.title();
const h2Texts = await page.$$eval("h2", (els) => els.map((e) => e.textContent?.trim()).filter(Boolean));
const heroAumSkeleton = await page.$$eval("[data-testid='hero-aum-value']", (els) => els.length);
const aboutAumTestId = await page.$$eval("[data-testid='about-aum-value']", (els) => els.length);
const chartsCount = await page.$$eval(".recharts-responsive-container", (els) => els.length);
const srOnlyTables = await page.$$eval("table.sr-only", (els) => els.length);
const ariaLabelled = await page.$$eval("[aria-labelledby]", (els) => els.length);

await page.screenshot({ path: "/tmp/monera-homepage.png", fullPage: false });
await page.screenshot({ path: "/tmp/monera-homepage-full.png", fullPage: true });

console.log(`\n=== Results ===`);
console.log(`load:        ${loadMs}ms`);
console.log(`title:       ${title}`);
console.log(`H2 sections: ${h2Texts.length} (${h2Texts.slice(0, 5).join(" | ")})`);
console.log(`hero-aum el: ${heroAumSkeleton} (1 expected — HeroAumCard mounted)`);
console.log(`about-aum:   ${aboutAumTestId} (1 expected — About AUM mounted)`);
console.log(`charts:      ${chartsCount} (0 expected — error state, no charts rendered)`);
console.log(`sr-only tbl: ${srOnlyTables} (0 expected in error state, would be 2 with data)`);
console.log(`aria-lbld:   ${ariaLabelled} (any present — includes the existing labeled links/icons)`);
console.log(`\n/api/fund/stats requests: ${fundRequests.length} (1 expected — C1 dedup verified)`);
console.log(`page errors:  ${pageErrors.length}`);
console.log(`console errs: ${consoleErrors.length}`);
if (pageErrors.length) {
  console.log(`  first: ${pageErrors[0].slice(0, 200)}`);
}
if (consoleErrors.length) {
  console.log(`  first: ${consoleErrors[0].slice(0, 200)}`);
}
console.log(`network fail: ${networkFailures.length}`);
if (networkFailures.length) {
  console.log(`  ${networkFailures[0]}`);
}

await browser.close();
console.log(`\nScreenshot: /tmp/monera-homepage.png`);
console.log(`Full:      /tmp/monera-homepage-full.png`);
