import { test } from "node:test";
import assert from "node:assert/strict";
import { init } from "../browser-agent/src/index.js";
test("SDK does not pretend to collect outside a browser", () => {
  assert.throws(
    () => init({ projectId: "test", endpoint: "http://localhost" }),
    /requires a browser/,
  );
});

// A small in-memory DOM fixture exercises the real SDK lifecycle without driving a browser.
test("SDK waits for opt-in config, captures SPA events and fully stops", async () => {
  const keys = [
    "window",
    "document",
    "location",
    "history",
    "navigator",
    "localStorage",
    "sessionStorage",
    "fetch",
    "crypto",
  ];
  const previous = new Map(
    keys.map((k) => [k, Object.getOwnPropertyDescriptor(globalThis, k)]),
  );
  const win = new EventTarget(),
    doc = new EventTarget();
  doc.visibilityState = "visible";
  const location = new URL("http://website.example/start?token=private#secret");
  const history = {
    pushState(_s, _t, path) {
      location.href = new URL(path, location).href;
    },
    replaceState(_s, _t, path) {
      location.href = new URL(path, location).href;
    },
  };
  const original = history.pushState;
  const storage = () => {
    const m = new Map();
    return { getItem: (k) => m.get(k) || null, setItem: (k, v) => m.set(k, v) };
  };
  const batches = [];
  const set = (key, value) =>
    Object.defineProperty(globalThis, key, {
      configurable: true,
      writable: true,
      value,
    });
  set("window", win);
  set("document", doc);
  set("location", location);
  set("history", history);
  set("navigator", { doNotTrack: "0" });
  set("localStorage", storage());
  set("sessionStorage", storage());
  const { webcrypto } = await import("node:crypto");
  set("crypto", webcrypto);
  set("fetch", async (_url, options = {}) => {
    if (options.method === "POST") {
      batches.push(JSON.parse(options.body));
      return { ok: true, status: 200 };
    }
    return {
      ok: true,
      status: 200,
      json: async () => ({
        enabled: true,
        batchSize: 100,
        intervalSeconds: 100,
      }),
    };
  });
  let sdk;
  try {
    sdk = init({
      projectId: "real-project",
      endpoint: "http://core.example/api/v1/collect/browser",
    });
    assert.equal(sdk.enabled, false);
    await new Promise((resolve) => setImmediate(resolve));
    assert.equal(sdk.enabled, true);
    history.pushState({}, "", "/next?password=private");
    sdk.track("click", {
      element: "button",
      elementId: "safe-id",
      password: "secret",
      text: "private",
    });
    await sdk.flush();
    const events = batches.flatMap((b) => b.events);
    assert.ok(events.some((e) => e.eventType === "page_view"));
    assert.ok(events.some((e) => e.eventType === "session_start"));
    assert.ok(
      events.some((e) => e.eventType === "route_change" && e.path === "/next"),
    );
    assert.ok(
      events.every((e) => !e.path.includes("?") && !e.path.includes("#")),
    );
    const click = events.find((e) => e.eventType === "click");
    assert.deepEqual(click.properties, {
      element: "button",
      elementId: "safe-id",
      pagePath: "/next",
    });
    assert.equal(new Set(events.map((e) => e.sessionId)).size, 1);
    sdk.stop();
    assert.equal(history.pushState, original);
    const count = batches.length;
    sdk.track("click", { element: "a" });
    await sdk.flush();
    assert.equal(batches.length, count);
  } finally {
    sdk?.stop();
    for (const [k, descriptor] of previous) {
      if (descriptor) Object.defineProperty(globalThis, k, descriptor);
      else delete globalThis[k];
    }
  }
});
