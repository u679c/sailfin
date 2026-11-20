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
// Keep hash-router pages separate without collecting arbitrary URL fragments or queries.
export function pagePath(value) {
  const url = new URL(value, "http://localhost");
  const route = url.hash.replace(/^#!?/, "");
  return (url.pathname + (route.startsWith("/") ? "#" + route.split(/[?#]/)[0] : "")).slice(0, 200);
}
const privateElements = "input,textarea,select,[contenteditable],[data-tracelite-ignore],script,style,[hidden],[aria-hidden='true']";
export function clickProperties(el) {
  const clean = (value) => (value || "").replace(/\s+/g, " ").trim().slice(0, 200);
  function text(node) {
    const copy = node.cloneNode(true);
    copy.querySelectorAll(privateElements).forEach((child) => child.remove());
    return clean(node.querySelector(privateElements) ? copy.textContent : (node.innerText || copy.textContent));
  }
  let label = clean(el.getAttribute("aria-label"));
  if (!label) {
    const refs = (el.getAttribute("aria-labelledby") || "").split(/\s+/).filter(Boolean).slice(0, 5);
    label = clean(refs.map((ref) => {
      const node = el.ownerDocument.getElementById(ref);
      return node && !node.closest(privateElements) ? text(node) : "";
    }).join(" "));
  }
  const isInput = el.tagName.toLowerCase() === "input";
  label ||= isInput ? clean(el.value) : text(el);
  label ||= clean(el.getAttribute("title"));
  if (!label) {
    label = clean(Array.from(el.querySelectorAll("img[alt]")).filter((img) => !img.closest(privateElements)).map((img) => img.getAttribute("alt")).join(" "));
  }
  const parts = [];
  for (let node = el; node && parts.length < 32; node = node.parentElement) {
    const tag = node.tagName.toLowerCase();
    let position = 1;
    for (let sibling = node.previousElementSibling; sibling; sibling = sibling.previousElementSibling) {
      if (sibling.tagName === node.tagName) position++;
    }
    parts.unshift(`${tag}:nth-of-type(${position})`);
    if (tag === "body") break;
  }
  const selectorParts = [];
  for (const part of [...parts].reverse()) {
    if ([part, ...selectorParts].join(" > ").length > 200) break;
    selectorParts.unshift(part);
  }
  const selector = selectorParts.join(" > ");
  const region = el.closest("header,nav,main,aside,footer,section,form,[role='dialog'],[role='region']");
  const explicitID = clean(el.getAttribute("data-tracelite-id"));
  // Structural identity does not change when a button's label changes.
  const source = pagePath(el.ownerDocument.location.href) + "|" + parts.join(" > ");
  let hash = 2166136261;
  for (let i = 0; i < source.length; i++) hash = Math.imul(hash ^ source.charCodeAt(i), 16777619);
  return {
    element: el.tagName.toLowerCase(),
    elementId: explicitID || `auto_${(hash >>> 0).toString(16)}`,
    label,
    selector,
    region: region ? clean(region.getAttribute("aria-label") || region.getAttribute("role") || region.tagName.toLowerCase()) : "",
  };
}
export function safeProps(properties = {}) {
  const out = {};
  for (const [k, v] of Object.entries(properties)) {
    if (!["element", "elementId", "label", "selector", "region", "pagePath", "durationMs", "metric", "value"].includes(k))
      continue;
    if (typeof v === "string") {
      // Core limits strings in UTF-8 bytes; Chinese labels must fit that limit too.
      const chars = Array.from(v.replace(/[\r\n]/g, " ").slice(0, 200));
      while (new TextEncoder().encode(chars.join("")).length > 480) chars.pop();
      out[k] = chars.join("");
    }
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
