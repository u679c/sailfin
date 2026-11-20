let trace;
const status = document.querySelector("#status");
document.querySelector("#connect").addEventListener("submit", async (event) => {
  event.preventDefault();
  trace?.stop();
  const server = new URL(document.querySelector("#server").value);
  const projectId = document.querySelector("#project").value.trim();
  try {
    await new Promise((resolve, reject) => {
      const script = document.createElement("script");
      script.src = new URL("/sdk/tracelite.browser.min.js", server).href;
      script.onload = resolve;
      script.onerror = reject;
      document.head.append(script);
    });
    trace = window.TraceLite.init({
      projectId,
      endpoint: new URL("/api/v1/collect/browser", server).href,
    });
    status.textContent =
      "SDK 已加载；等待配置。请在 Core 中启用该项目并允许本站 Origin。";
    setTimeout(
      () =>
        (status.textContent = trace.enabled
          ? "采集已启用，可在管理后台查看真实事件。"
          : "未启用，请检查项目设置、Origin 或浏览器 DNT。"),
      2000,
    );
  } catch {
    status.textContent = "SDK 加载失败，请先启动 make dev 并检查 Core 地址。";
  }
});
document.querySelectorAll("[data-path]").forEach((button) =>
  button.addEventListener("click", () => {
    history.pushState({}, "", button.dataset.path);
    document.querySelector("#route").textContent = location.pathname;
  }),
);
document
  .querySelector("#action")
  .addEventListener(
    "click",
    () => (status.textContent = "已点击，请到事件查询中检查 click。"),
  );
document.querySelector("#error").addEventListener("click", () => {
  setTimeout(() => {
    throw new Error("Deliberate TraceLite verification error");
  }, 0);
});
document.querySelector("#stop").addEventListener("click", () => {
  trace?.stop();
  status.textContent = "采集已停止。";
});
