import { clickProperties, pagePath, safePath, safeProps, shouldCollect } from "./policy.js";
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
  const currentPage = () => ({ path: safePath(location.href), pagePath: pagePath(location.href) });
  let activePage = currentPage();
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
  function on(target, type, fn, capture = false) {
    target.addEventListener(type, fn, capture);
    listeners.push(() => target.removeEventListener(type, fn, capture));
  }
  function track(type, properties = {}, page = currentPage()) {
    if (
      stopped ||
      !config.enabled ||
      navigator.doNotTrack === "1" ||
      window.TRACELITE_DISABLED
    )
      return;
    const path = page.path;
    if (!shouldCollect(type, path, config)) return;
    const fresh = identity();
    if (
      fresh &&
      type !== "session_start" &&
      shouldCollect("session_start", path, config)
    )
      push("session_start", { pagePath: page.pagePath }, page);
    push(type, safeProps({ ...properties, pagePath: page.pagePath }), page);
  }
  function push(type, properties, page) {
    if (queue.length >= 200) queue.shift();
    queue.push({
      eventId: id(),
      projectId: project,
      sessionId: session,
      visitorId: visitor,
      eventType: type,
      timestamp: Date.now(),
      path: page.path,
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
    inflight = true;
    try {
      while (queue.length && !stopped && config.enabled && !window.TRACELITE_DISABLED && navigator.doNotTrack !== "1") {
        const batch = [];
        let bytes = 32;
        const limit = Math.max(1, Math.min(config.batchSize || 20, 100));
        while (queue.length && batch.length < limit) {
          const size = new Blob([JSON.stringify(queue[0])]).size + 1;
          if (batch.length && bytes + size > 48 * 1024) break;
          bytes += size;
          batch.push(queue.shift());
        }
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
            break;
          }
          if (res.status === 429 || res.status >= 500) {
            if (!stopped && config.enabled) queue = [...batch, ...queue].slice(0, 200);
            break;
          }
        } catch {
          if (!stopped && config.enabled) queue = [...batch, ...queue].slice(0, 200);
          break;
        } finally {
          clearTimeout(timeout);
          controller = null;
        }
      }
    } finally {
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
        activePage = currentPage();
        activeStart = document.visibilityState === "visible" ? Date.now() : 0;
        track("page_view");
      }
    } catch {
      config.enabled = false;
      queue = [];
    }
  }
  const originalPush = history.pushState,
    originalReplace = history.replaceState;
  function endVisiblePeriod() {
    if (!activeStart) return;
    const durationMs = Math.max(0, Math.min(Date.now() - activeStart, 1800000));
    activeStart = 0;
    track("page_hidden", { durationMs }, activePage);
  }
  function route() {
    const next = currentPage();
    if (next.pagePath === activePage.pagePath) return;
    endVisiblePeriod();
    activePage = next;
    activeStart = config.enabled && document.visibilityState === "visible" ? Date.now() : 0;
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
    if (e.target.closest('input:not([type="button"]):not([type="submit"]):not([type="reset"]),textarea,select,[contenteditable],[data-tracelite-ignore],[hidden],[aria-hidden="true"]')) return;
    const el = e.target.closest('button,a,[role="button"],[role="link"],input[type="button"],input[type="submit"],input[type="reset"],[data-tracelite-id]');
    if (
      !el ||
      el.closest(
        'input:not([type="button"]):not([type="submit"]):not([type="reset"]),textarea,select,[contenteditable],[data-tracelite-ignore]',
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
    try {
      track("click", clickProperties(el));
    } catch {
      // Instrumentation must never interrupt the site's own click handler.
    }
  }, true);
  on(document, "visibilitychange", () => {
    if (document.visibilityState === "hidden") {
      endVisiblePeriod();
      void flush();
    } else if (config.enabled && !activeStart) activeStart = Date.now();
  });
  on(window, "pagehide", () => {
    endVisiblePeriod();
    void flush();
  });
  on(window, "pageshow", () => {
    if (config.enabled && document.visibilityState === "visible" && !activeStart) activeStart = Date.now();
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
