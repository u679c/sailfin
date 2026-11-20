export const TYPES = [
  "page_view",
  "route_change",
  "click",
  "session_start",
  "page_hidden",
  "js_error",
  "unhandled_rejection",
  "performance_event",
];
export function safePath(value, base = "http://localhost") {
  try {
    return new URL(value, base).pathname.slice(0, 2048);
  } catch {
    return "/";
  }
}
export function safeProps(properties = {}) {
  const out = {};
  for (const [k, v] of Object.entries(properties)) {
    if (!["element", "elementId", "durationMs", "metric", "value"].includes(k))
      continue;
    if (typeof v === "string") out[k] = v.replace(/[\r\n]/g, " ").slice(0, 200);
    if (typeof v === "number" && Number.isFinite(v) && v >= 0 && v <= 1e12)
      out[k] = v;
  }
  return out;
}
export function shouldCollect(type, path, config, random = Math.random) {
  return (
    config.enabled &&
    TYPES.includes(type) &&
    (!config.eventTypes || config.eventTypes.includes(type)) &&
    !(config.excludePaths || []).some((p) => path.startsWith(p)) &&
    random() < (config.sampleRate ?? 1)
  );
}
