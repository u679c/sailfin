import { safePath, safeProps, shouldCollect } from "./policy.js";
export function init(options) {
  if (typeof window === "undefined")
    throw new Error("TraceLite requires a browser");
  if (!options?.projectId || !options?.endpoint)
    throw new Error("projectId and endpoint are required");
  const endpoint = new URL(options.endpoint, location.href);
  endpoint.searchParams.set("projectId", options.projectId);
  const configURL = new URL(
    options.configEndpoint || "/api/v1/collection/config",
    endpoint,
  );
  configURL.searchParams.set("projectId", options.projectId);
  let stopped = false,
    config = { enabled: false },
    queue = [],
    inflight = false,
    controller = null,
    activeStart = 0,
    visitor = "",
    session = "",
    lastActivity = 0,
    lastFlush = 0;
  const listeners = [];
  const project = options.projectId;
  const id = () => crypto.randomUUID();
  const key = `tracelite:${project}`;
  function identity() {
    const now = Date.now();
    try {
      visitor = localStorage.getItem(`${key}:visitor`) || id();
      localStorage.setItem(`${key}:visitor`, visitor);
      const old = JSON.parse(
        sessionStorage.getItem(`${key}:session`) || "null",
      );
      if (!session) {
        session = old?.id || id();
        lastActivity = old?.last || 0;
      }
      if (now - lastActivity > 1800000) {
        session = id();
        lastActivity = now;
        sessionStorage.setItem(
          `${key}:session`,
          JSON.stringify({ id: session, last: now }),
        );
        return true;
      }
      lastActivity = now;
      sessionStorage.setItem(
        `${key}:session`,
        JSON.stringify({ id: session, last: now }),
      );
    } catch {
      visitor ||= id();
      if (!session || now - lastActivity > 1800000) {
        session = id();
        lastActivity = now;
        return true;
      }
      lastActivity = now;
    }
    return false;
  }
  function on(target, type, fn) {
    target.addEventListener(type, fn);
    listeners.push(() => target.removeEventListener(type, fn));
  }
  function track(type, properties = {}) {
    if (
      stopped ||
      !config.enabled ||
      navigator.doNotTrack === "1" ||
      window.TRACELITE_DISABLED
    )
      return;
    const path = safePath(location.href);
    if (!shouldCollect(type, path, config)) return;
    const fresh = identity();
    if (
      fresh &&
      type !== "session_start" &&
      shouldCollect("session_start", path, config)
    )
      push("session_start", {});
    push(type, safeProps(properties));
  }
  function push(type, properties) {
    if (queue.length >= 200) queue.shift();
    queue.push({
      eventId: id(),
      projectId: project,
      sessionId: session,
      visitorId: visitor,
      eventType: type,
      timestamp: Date.now(),
      path: safePath(location.href),
      properties,
    });
    if (queue.length >= Math.min(config.batchSize || 20, 100)) void flush();
  }
  async function flush() {
    if (
      stopped ||
      !config.enabled ||
      window.TRACELITE_DISABLED ||
      navigator.doNotTrack === "1"
    ) {
      queue = [];
      return;
    }
    if (inflight || !queue.length) return;
    const batch = queue.splice(0, Math.min(config.batchSize || 20, 100));
    inflight = true;
    controller = new AbortController();
    const timeout = setTimeout(() => controller?.abort(), 10000);
    try {
      const res = await fetch(endpoint, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ events: batch }),
        keepalive: true,
        credentials: "omit",
        signal: controller.signal,
      });
      if (res.status === 403) {
        config.enabled = false;
        queue = [];
      } else if (res.status === 429 || res.status >= 500)
        queue = [...batch, ...queue].slice(0, 200);
    } catch {
      if (!stopped) queue = [...batch, ...queue].slice(0, 200);
    } finally {
      clearTimeout(timeout);
      controller = null;
      inflight = false;
    }
  }
  async function sync() {
    if (stopped || window.TRACELITE_DISABLED || navigator.doNotTrack === "1") {
      config.enabled = false;
      queue = [];
      return;
    }
    try {
      const res = await fetch(configURL, {
        credentials: "omit",
        signal: AbortSignal.timeout(10000),
      });
      if (!res.ok) {
        config.enabled = false;
        queue = [];
        return;
      }
      const next = await res.json();
      if (stopped) return;
      const first = !config.enabled;
      config = next;
      if (first && config.enabled) {
        activeStart = Date.now();
        track("page_view");
      }
    } catch {
      config.enabled = false;
      queue = [];
    }
  }
  const originalPush = history.pushState,
    originalReplace = history.replaceState;
  function route() {
    if (activeStart && document.visibilityState === "visible")
      track("page_hidden", {
        durationMs: Math.min(Date.now() - activeStart, 1800000),
      });
    activeStart = Date.now();
    track("route_change");
  }
  function wrappedPush(...args) {
    originalPush.apply(history, args);
    route();
  }
  function wrappedReplace(...args) {
    originalReplace.apply(history, args);
    route();
  }
  history.pushState = wrappedPush;
  history.replaceState = wrappedReplace;
  on(window, "popstate", route);
  on(window, "hashchange", route);
  on(document, "click", (e) => {
    if (!config.enabled || !(e.target instanceof Element)) return;
    const el = e.target.closest("button,a,[data-tracelite-id]");
    if (
      !el ||
      el.closest(
        "input,textarea,select,[contenteditable],[data-tracelite-ignore]",
      )
    )
      return;
    try {
      if (
        (config.excludeElements || []).some(
          (s) => el.matches(s) || el.closest(s),
        )
      )
        return;
    } catch {
      return;
    }
    track("click", {
      element: el.tagName.toLowerCase(),
      elementId: el.getAttribute("data-tracelite-id") || "",
    });
  });
  on(document, "visibilitychange", () => {
    if (document.visibilityState === "hidden") {
      if (activeStart)
        track("page_hidden", {
          durationMs: Math.min(Date.now() - activeStart, 1800000),
        });
      activeStart = 0;
      void flush();
    } else activeStart = Date.now();
  });
  on(window, "pagehide", () => {
    if (activeStart)
      track("page_hidden", {
        durationMs: Math.min(Date.now() - activeStart, 1800000),
      });
    activeStart = 0;
    void flush();
  });
  // Error messages and rejected values may contain secrets. Record only the error category.
  on(window, "error", () => track("js_error", {}));
  on(window, "unhandledrejection", () => track("unhandled_rejection", {}));
  const timer = setInterval(() => {
    if (
      Date.now() - lastFlush >=
      Math.max(1, config.intervalSeconds || 5) * 1000
    ) {
      lastFlush = Date.now();
      void flush();
    }
  }, 1000);
  const refresh = setInterval(() => void sync(), 30000);
  void sync().then(() => {
    const n = performance.getEntriesByType("navigation")[0];
    if (n)
      track("performance_event", {
        metric: "domContentLoaded",
        value: Math.max(0, n.domContentLoadedEventEnd),
      });
  });
  return {
    track,
    flush,
    stop() {
      stopped = true;
      config.enabled = false;
      queue = [];
      controller?.abort();
      clearInterval(timer);
      clearInterval(refresh);
      listeners.forEach((off) => off());
      if (history.pushState === wrappedPush) history.pushState = originalPush;
      if (history.replaceState === wrappedReplace)
        history.replaceState = originalReplace;
    },
    get enabled() {
      return !stopped && config.enabled;
    },
  };
}
