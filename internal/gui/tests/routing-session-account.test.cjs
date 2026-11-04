// Uses only local page/API fixtures. Run with the existing Playwright setup.
const assert = require("node:assert/strict");
const { test } = require("node:test");
const fs = require("node:fs/promises");
const path = require("node:path");
const { chromium, webkit } = require("playwright");
const assets = path.resolve(__dirname, "../assets");

for (const engine of ["chromium", "webkit"]) for (const lang of ["en", "zh"]) {
  test(`${engine} ${lang}: choose one session's account and return to automatic`, async (t) => {
    const browser = await (engine === "webkit" ? webkit.launch() : chromium.launch({ channel: "chromium" }));
    t.after(() => browser.close());
    const context = await browser.newContext({ viewport: { width: 1100, height: 760 }, reducedMotion: "reduce" });
    const page = await context.newPage();
    page.setDefaultTimeout(5000);
    const now = new Date().toISOString(), saves = [], errors = [];
    let account = "";
    const seat = { id: "codex", provider: "codex", name: "Codex", who: "me@example.com", kind: "account", model: "gpt-5.5" };
    const request = { id: 100, seq: 100, time: now, agent: "codex", session: "fixture-session", model: "codex/gpt-5.5", provider: "codex", order: [seat], tries: [{ id: "codex", model: "gpt-5.5", done: true, status: 200 }], done: true, status: 200 };
    page.on("pageerror", (e) => errors.push(e.message));
    await page.route("**/*", async (route) => {
      const url = new URL(route.request().url()), json = (data) => route.fulfill({ json: data });
      if (url.pathname === "/boot.js") return route.fulfill({ contentType: "text/javascript", body: `window.bootPrefs = {lang:${JSON.stringify(lang)},theme:"light",web:true};` });
      if (url.pathname === "/wails/runtime.js") return route.fulfill({ contentType: "text/javascript", body: "export const Window = {};" });
      if (url.pathname === "/api/state") return json({ agents: [{ id: "codex", name: "Codex", fields: [] }], profiles: [], settings: { lang, theme: "light" } });
      if (url.pathname === "/api/gateway/trace") {
        if (url.searchParams.get("wait")) await new Promise((resolve) => setTimeout(resolve, 100));
        return json({ mine: true, now, seq: 100, routes: url.searchParams.get("wait") ? [] : [request], totals: {} });
      }
      if (url.pathname === "/api/gateway/session-account") {
        if (route.request().method() === "POST") {
          const body = route.request().postDataJSON();
          saves.push(body); account = body.account;
        }
        return json({ account, accounts: ["me@example.com", "spare@example.com"] });
      }
      if (url.pathname === "/api/gateway/history") return json({ cut: false, days: [], routes: [] });
      if (url.pathname === "/api/groups") return json({ models: [], pools: [], groups: [] });
      if (url.pathname === "/api/providers") return json({ providers: [], gateway: { running: true, window: true } });
      if (url.pathname.startsWith("/api/")) return json({});
      const file = path.join(assets, url.pathname === "/" ? "index.html" : url.pathname);
      const contentType = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".png": "image/png" }[path.extname(file)];
      return route.fulfill({ body: await fs.readFile(file).catch(() => ""), contentType });
    });
    await page.goto("http://magpie.test/?view=routing");
    const pick = page.getByRole("button", { name: lang === "zh" ? "会话账号" : "Choose session account", exact: true });
    await pick.click();
    await page.getByText("spare@example.com", { exact: true }).click();
    await page.waitForFunction(() => !document.querySelector(".proto-menu"));
    assert.deepEqual(saves[0], { id: 100, account: "spare@example.com" });
    await pick.click();
    await page.getByText(lang === "zh" ? "自动路由" : "Automatic routing", { exact: true }).click();
    await page.waitForFunction(() => !document.querySelector(".proto-menu"));
    assert.deepEqual(saves[1], { id: 100, account: "" });
    assert.deepEqual(errors, []);
  });
}
