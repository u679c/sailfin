import { test } from "node:test";
import assert from "node:assert/strict";
import {
  safePath,
  safeProps,
  shouldCollect,
} from "../browser-agent/src/policy.js";
test("URLs drop credentials, query and fragment", () => {
  assert.equal(
    safePath("https://name:password@example.com/a?token=secret#private"),
    "/a",
  );
});
test("properties whitelist excludes secrets and nested values", () => {
  assert.deepEqual(
    safeProps({
      password: "secret",
      text: "private",
      element: "button",
      elementId: "checkout",
      value: NaN,
      nested: { x: 1 },
      durationMs: 100,
    }),
    { element: "button", elementId: "checkout", durationMs: 100 },
  );
});
test("collection is opt-in and respects policy", () => {
  assert.equal(shouldCollect("click", "/", {}), undefined);
  assert.equal(
    shouldCollect("click", "/private", {
      enabled: true,
      excludePaths: ["/private"],
    }),
    false,
  );
  assert.equal(
    shouldCollect("click", "/", { enabled: true, eventTypes: ["page_view"] }),
    false,
  );
  assert.equal(
    shouldCollect("click", "/", { enabled: true, sampleRate: 0 }, () => 0.5),
    false,
  );
  assert.equal(
    shouldCollect("page_view", "/", { enabled: true }, () => 0),
    true,
  );
});
