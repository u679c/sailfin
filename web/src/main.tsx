import React, { useState, useEffect, useMemo } from "react";
import { createRoot } from "react-dom/client";
import {
  Activity,
  LogOut,
  RefreshCw,
  ArrowRight,
  ChevronLeft,
  ChevronRight,
  Download,
} from "lucide-react";
import { api, backup, setCSRF, parse, when, number, Row } from "./api";
import "./style.css";
const sections = [
  { id: "sites", name: "站点" },
  { id: "overview", name: "概览" },
  { id: "pages", name: "页面" },
  { id: "clicks", name: "点击行为" },
  { id: "sessions", name: "访问轨迹" },
  { id: "events", name: "事件记录" },
  { id: "devices", name: "设备" },
  { id: "collection", name: "采集管理" },
  { id: "security", name: "安全审计" },
  { id: "system", name: "系统设置" },
];
const siteSections = sections.filter((s) =>
  ["overview", "pages", "clicks", "sessions", "events", "devices"].includes(s.id),
);
const eventLabels: Record<string, string> = {
  http_request: "HTTP 请求", page_view: "页面浏览", route_change: "页面切换",
  click: "点击", session_start: "会话开始", page_hidden: "页面停留",
  js_error: "脚本错误", unhandled_rejection: "异步错误", performance_event: "性能记录",
};
function eventLabel(row: Row) {
  const props = parse<Row>(row.properties, {});
  return row.type === "click" && props.label
    ? `点击「${props.label}」`
    : eventLabels[row.type] || row.type;
}
function eventPath(row: Row) {
  return parse<Row>(row.properties, {}).pagePath || row.path;
}
function readLocation() {
  return {
    pathname: window.location.pathname,
    search: window.location.search,
  };
}

function useDashboardLocation() {
  const [location, setLocation] = useState(readLocation);
  useEffect(() => {
    const onPopState = () => setLocation(readLocation());
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, []);

  const current =
    sections.find(
      ({ id }) => location.pathname.replace(/\/$/, "") === `/${id}`,
    ) ?? sections[0];

  function href(section = current.id, changes: Record<string, string> = {}) {
    const params = new URLSearchParams(location.search);
    for (const [key, value] of Object.entries(changes)) {
      if (value) params.set(key, value);
      else params.delete(key);
    }
    const query = params.toString();
    return `/${section}${query ? `?${query}` : ""}`;
  }

  function navigate(url: string, replace = false) {
    const { pathname, search, hash } = window.location;
    if (url === pathname + search + hash) return;
    if (replace) window.history.replaceState(null, "", url);
    else window.history.pushState(null, "", url);
    setLocation(readLocation());
  }

  return {
    current,
    params: new URLSearchParams(location.search),
    href,
    navigate,
  };
}

function useData<T>(
  fn: () => Promise<T>,
  deps: React.DependencyList,
  pollMs = 0,
) {
  const [data, setData] = useState<T | null>(null),
    [error, setError] = useState(""),
    [loading, setLoading] = useState(true);
  useEffect(() => {
    let live = true;
    let timer: number | undefined;
    setLoading(true);
    setError("");
    async function load() {
      try {
        const value = await fn();
        if (live) {
          setData(value);
          setError("");
        }
      } catch (error) {
        if (live) setError((error as Error).message);
      } finally {
        if (live) {
          setLoading(false);
          if (pollMs) timer = window.setTimeout(load, pollMs);
        }
      }
    }
    void load();
    return () => {
      live = false;
      window.clearTimeout(timer);
    };
  }, [...deps, pollMs]);
  return { data, error, loading };
}
function ErrorBox({ message }: { message: string }) {
  return message ? (
    <div className="error" role="alert">
      {message}
    </div>
  ) : null;
}
function Empty({ children }: { children: React.ReactNode }) {
  return (
    <div className="empty">
      <Activity size={25} />
      <p>{children}</p>
    </div>
  );
}
function Badge({ value }: { value: string }) {
  return (
    <span
      className={`badge ${value === "Online" || value === "active" ? "good" : value === "Warning" || value === "paused" ? "warn" : "neutral"}`}
    >
      {({ browser: "浏览器", host: "访问日志", Online: "在线", active: "启用", paused: "暂停" } as Record<string, string>)[value] || value}
    </span>
  );
}
function Table({
  columns,
  rows,
  onRow,
}: {
  columns: {
    key: string;
    label: string;
    render?: (r: Row) => React.ReactNode;
  }[];
  rows: Row[];
  onRow?: (r: Row) => void;
}) {
  if (!rows.length) return <Empty>暂无数据</Empty>;
  return (
    <div className="table-wrap">
      <table>
        <thead>
          <tr>
            {columns.map((c) => (
              <th key={c.key}>{c.label}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={r.id || `${r.project_id}:${r.session_id || i}`}>
              {columns.map((c, j) => (
                <td key={c.key}>
                  {j === 0 && onRow ? (
                    <button className="text-button" onClick={() => onRow(r)}>
                      {c.render ? c.render(r) : String(r[c.key] ?? "—")}
                      <ArrowRight size={13} />
                    </button>
                  ) : c.render ? (
                    c.render(r)
                  ) : (
                    String(r[c.key] ?? "—")
                  )}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
function Pager({
  page,
  setPage,
  count,
}: {
  page: number;
  setPage: (n: number) => void;
  count: number;
}) {
  return (
    <div className="pager">
      <span>
        第 {page} 页 · 本页 {count} 条
      </span>
      <button
        disabled={page === 1}
        onClick={() => setPage(page - 1)}
        aria-label="上一页"
      >
        <ChevronLeft size={16} />
      </button>
      <button
        disabled={count < 50}
        onClick={() => setPage(page + 1)}
        aria-label="下一页"
      >
        <ChevronRight size={16} />
      </button>
    </div>
  );
}
function AsyncPanel({
  state,
  children,
}: {
  state: { loading: boolean; error: string };
  children: React.ReactNode;
}) {
  if (state.loading)
    return (
      <div className="loading" role="status">
        正在读取数据…
      </div>
    );
  if (state.error) return <ErrorBox message={state.error} />;
  return <>{children}</>;
}
function Login({ onLogin }: { onLogin: () => void }) {
  const [name, setName] = useState("admin"),
    [password, setPassword] = useState(""),
    [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  return (
    <div className="login">
      <div className="login-brand">
        <Activity size={28} />
        <span>TraceLite</span>
      </div>
      <form
        onSubmit={async (e) => {
          e.preventDefault();
          setBusy(true);
          setError("");
          try {
            const d = await api<Row>("/auth/login", {
              method: "POST",
              body: JSON.stringify({ name, password }),
            });
            setCSRF(d.csrf);
            onLogin();
          } catch (e) {
            setError((e as Error).message);
          } finally {
            setBusy(false);
          }
        }}
      >
        <h1>登录</h1>

        <label>
          用户名
          <input
            autoComplete="username"
            required
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </label>
        <label>
          密码
          <input
            type="password"
            autoComplete="current-password"
            required
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>
        <ErrorBox message={error} />
        <button className="primary" disabled={busy}>
          {busy ? "正在登录…" : "登录"}
          <ArrowRight size={16} />
        </button>
        <p className="hint">
          首次登录凭据：数据目录 bootstrap-admin.txt
        </p>
      </form>
    </div>
  );
}
function App() {
  const [authenticated, setAuthenticated] = useState<boolean | null>(null),
    [revision, setRevision] = useState(0),
    [error, setError] = useState("");
  const [showConnect, setShowConnect] = useState(false);
  const { current, params, href, navigate } = useDashboardLocation();
  const section = current.id;
  const project = params.get("projectId") ?? "";
  const selectedRange = params.get("range") ?? "";
  const range = ["24", "168", "720", "custom"].includes(selectedRange)
    ? selectedRange
    : "24";
  const customFrom = params.get("from") ?? "";
  const customTo = params.get("to") ?? "";
  const projects = useData(
    () => (authenticated ? api("/sites") : Promise.resolve([])),
    [authenticated, revision],
    authenticated ? 10000 : 0,
  );
  useEffect(() => {
    api("/auth/me")
      .then((rows) => {
        setCSRF(rows[0].csrf);
        setAuthenticated(true);
      })
      .catch(() => setAuthenticated(false));
    const expired = () => setAuthenticated(false);
    window.addEventListener("tracelite:unauthorized", expired);
    return () => window.removeEventListener("tracelite:unauthorized", expired);
  }, []);
  const query = useMemo(() => {
    const end = range === "custom" ? new Date(customTo).getTime() : NaN;
    const start = range === "custom" ? new Date(customFrom).getTime() : NaN;
    const to = Number.isFinite(end) ? end : Date.now();
    const hours = range === "custom" ? 24 : Number(range);
    const from = Number.isFinite(start) ? start : to - hours * 3600000;
    return `?projectId=${encodeURIComponent(project)}&from=${from}&to=${to}`;
  }, [project, range, customFrom, customTo, revision]);
  if (authenticated === null)
    return <div className="loading">正在连接 Core…</div>;
  if (!authenticated) return <Login onLogin={() => setAuthenticated(true)} />;
  const refresh = () => setRevision((v) => v + 1);
  function followLink(event: React.MouseEvent<HTMLAnchorElement>) {
    if (
      event.defaultPrevented ||
      event.button !== 0 ||
      event.metaKey ||
      event.ctrlKey ||
      event.shiftKey ||
      event.altKey
    )
      return;
    event.preventDefault();
    const link = event.currentTarget;
    navigate(link.pathname + link.search);
    setError("");
  }
  const siteView = siteSections.some((item) => item.id === section);
  const settingsView = section === "system" || section === "security";
  const selectedSite = projects.data?.find((item) => item.id === project);
  const primarySection = settingsView ? "system" : section === "collection" ? "collection" : "sites";
  return (
    <div className="shell">
      <header className="topbar">
        <a className="brand" href={href("sites", { projectId: "" })} onClick={followLink}>
          <Activity size={23} /><span>TraceLite</span>
        </a>
        <nav className="primary-nav" aria-label="主导航">
          {[
            { id: "sites", name: "站点" },
            { id: "collection", name: "采集管理" },
            { id: "system", name: "系统设置" },
          ].map((item) => (
            <a key={item.id} href={href(item.id, item.id === "sites" ? { projectId: "" } : {})}
              className={primarySection === item.id ? "selected" : ""}
              aria-current={primarySection === item.id ? "page" : undefined} onClick={followLink}>
              {item.name}
            </a>
          ))}
        </nav>
        <div className="header-actions">
          <button onClick={refresh} aria-label="刷新数据" title="刷新"><RefreshCw size={16} /></button>
          <button onClick={async () => {
            try {
              await api("/auth/logout", { method: "POST" });
              setAuthenticated(false);
              setCSRF("");
            } catch (e) { setError((e as Error).message); }
          }}><LogOut size={16} />退出</button>
        </div>
      </header>
      <main>
        <div className="workspace">
          {siteView && (
            <a className="back-link" href={href("sites", { projectId: "" })} onClick={followLink}>
              <ChevronLeft size={14} />站点
            </a>
          )}
          <div className="page-title">
            <h1>{siteView ? selectedSite?.name || (project ? "站点数据" : "全部站点") : settingsView ? "系统设置" : current.name}</h1>
            {section === "sites" && (
              <button className={showConnect ? "" : "primary"} aria-expanded={showConnect}
                onClick={() => setShowConnect((value) => !value)}>
                {showConnect ? "收起接入" : "接入站点"}
              </button>
            )}
          </div>
          {siteView && (
            <>
              <nav className="tabs" aria-label="站点导航">
                {siteSections.map((item) => (
                  <a key={item.id} href={href(item.id)} onClick={followLink}
                    className={section === item.id ? "active" : ""}
                    aria-current={section === item.id ? "page" : undefined}>{item.name}</a>
                ))}
              </nav>
              <div className="filterbar">
                <div className="filters">
                  <label>站点<select value={project} onChange={(e) => navigate(href(section, { projectId: e.target.value }), true)}>
                    <option value="">全部站点</option>
                    {projects.data?.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}
                  </select></label>
                  <label>时间<select value={range} onChange={(e) => navigate(href(section, { range: e.target.value === "24" ? "" : e.target.value }), true)}>
                    <option value="24">最近 24 小时</option><option value="168">最近 7 天</option>
                    <option value="720">最近 30 天</option><option value="custom">自定义</option>
                  </select></label>
                  {range === "custom" && <>
                    <label>开始<input type="datetime-local" value={customFrom} onChange={(e) => navigate(href(section, { from: e.target.value }), true)} /></label>
                    <label>结束<input type="datetime-local" value={customTo} onChange={(e) => navigate(href(section, { to: e.target.value }), true)} /></label>
                  </>}
                </div>
              </div>
            </>
          )}
          {settingsView && (
            <nav className="tabs" aria-label="设置导航">
              {[{ id: "system", name: "运行设置" }, { id: "security", name: "安全审计" }].map((item) => (
                <a key={item.id} href={href(item.id)} onClick={followLink} className={section === item.id ? "active" : ""}
                  aria-current={section === item.id ? "page" : undefined}>{item.name}</a>
              ))}
            </nav>
          )}
          <ErrorBox message={error || (section === "sites" || !siteView ? "" : projects.error)} />
          {section === "sites" && <>
            {showConnect && <NginxCollection />}
            <section className="panel">
              <AsyncPanel state={projects}>
                {projects.data?.length ? <Table rows={projects.data}
                  onRow={(row) => navigate(href("overview", { projectId: row.id }))}
                  columns={[
                    { key: "name", label: "站点名称" },
                    { key: "last_event", label: "数据状态", render: (row) => <span className={`badge ${row.last_event ? "good" : "neutral"}`}>{row.last_event ? "已收到数据" : "暂无数据"}</span> },
                    { key: "created_at", label: "首次接入", render: (row) => when(row.created_at) },
                    { key: "received", label: "最近上报", render: (row) => row.last_event ? when(row.last_event) : "—" },
                    { key: "actions", label: "操作", render: (row) => <a href={href("overview", { projectId: row.id })} onClick={followLink}>查看数据</a> },
                  ]} /> : <div className="empty"><p>暂无站点</p>
                    {!showConnect && <button className="primary" onClick={() => setShowConnect(true)}>接入站点</button>}
                  </div>}
              </AsyncPanel>
            </section>
          </>}
          {section === "overview" && <Overview query={query} revision={revision} />}
          {section === "pages" && <DataTable path="/stats/pages" query={query} revision={revision} columns={[
            { key: "path", label: "页面 / 请求路径" }, { key: "pv", label: "浏览量" },
            { key: "requests", label: "HTTP 请求" },
          ]} />}
          {section === "devices" && <DataTable path="/stats/devices" query={query} revision={revision} columns={[
            { key: "device", label: "设备类型" }, { key: "browser", label: "浏览器" }, { key: "os", label: "操作系统" },
            { key: "requests", label: "HTTP 请求" }, { key: "pv", label: "浏览量" }, { key: "uv", label: "匿名访客" }, { key: "events", label: "事件总数" },
          ]} />}
          {section === "events" && <Events key="events" query={query} revision={revision} />}
          {section === "clicks" && <Events key="clicks" query={query} revision={revision} eventType="click" />}
          {section === "sessions" && <Sessions query={query} revision={revision} />}
          {section === "collection" && <><NginxCollection /><LocalProbe revision={revision} /></>}
          {section === "security" && <Security revision={revision} />}
          {section === "system" && <System revision={revision} />}
        </div>
      </main>
    </div>
  );
}
function Overview({ query, revision }: { query: string; revision: number }) {
  const state = useData(async () => {
    const [overview, trends, pages] = await Promise.all([
      api("/stats/overview" + query),
      api("/stats/trends" + query),
      api("/stats/pages" + query),
    ]);
    return { overview: overview[0], trends, pages };
  }, [query, revision]);
  const d = state.data;
  return (
    <AsyncPanel state={state}>
      {d && (
        <>
          <div className="metrics">
            {[
              {
                label: "浏览量",
                v: d.overview.pv,
              },
              {
                label: "匿名访客",
                v: d.overview.uv,
              },
              {
                label: "HTTP 请求",
                v: d.overview.requests,
              },
              {
                label: "事件总数（含请求）",
                v: d.overview.events,
              },
            ].map((m) => (
              <div className="metric" key={m.label}>
                <span>{m.label}</span>
                <strong>{number(m.v)}</strong>
              </div>
            ))}
          </div>
          <div className="panel">
            <div className="panel-title">
              <h2>采集趋势</h2>
              <span className="muted">UTC · 每小时</span>
            </div>
            {d.trends.length ? (
              <>
                <Trend rows={d.trends} />
                <div className="legend">
                  <span>浏览量</span>
                  <span>HTTP 请求</span>
                </div>
              </>
            ) : (
              <Empty>暂无采集数据</Empty>
            )}
          </div>
          <div className="split">
            <section className="panel">
              <div className="panel-title">
                <h2>热门路径</h2>
                <span>最多 100 条</span>
              </div>
              <Table
                rows={d.pages}
                columns={[
                  { key: "path", label: "页面 / 请求路径" },
                  { key: "pv", label: "PV" },
                  { key: "requests", label: "请求" },
                ]}
              />
            </section>
            <section className="panel">
              <div className="panel-title">
                <h2>访问概况</h2>
              </div>
              <dl className="facts">
                <dt>活跃会话</dt>
                <dd>{number(d.overview.active_sessions)}</dd>
                <dt>JavaScript 错误</dt>
                <dd>{number(d.overview.js_errors)}</dd>
                <dt>前台平均停留</dt>
                <dd>
                  {(Number(d.overview.avg_duration_ms) / 1000).toFixed(1)} 秒
                </dd>
              </dl>
              <p className="hint">
                单段停留最多计 30 分钟。
              </p>
            </section>
          </div>
        </>
      )}
    </AsyncPanel>
  );
}
function Trend({ rows }: { rows: Row[] }) {
  const max = Math.max(
    1,
    ...rows.flatMap((r) => [Number(r.pv), Number(r.requests)]),
  );
  const point = (key: string) =>
    rows
      .map(
        (r, i) =>
          `${30 + (i / Math.max(1, rows.length - 1)) * 940},${180 - (Number(r[key]) / max) * 150}`,
      )
      .join(" ");
  return (
    <div className="trend">
      <svg
        viewBox="0 0 1000 220"
        role="img"
        aria-label={`小时趋势，最大值 ${max}。详情见下方数据表。`}
      >
        <line x1="30" y1="180" x2="970" y2="180" stroke="#dce4ec" />
        <text x="0" y="25">
          {max}
        </text>
        <text x="0" y="184">
          0
        </text>
        <polyline
          points={point("requests")}
          fill="none"
          stroke="#237f75"
          strokeWidth="2.5"
        />
        <polyline
          points={point("pv")}
          fill="none"
          stroke="#3267d6"
          strokeWidth="2.5"
        />
        <text x="30" y="212">
          {rows[0]?.time}
        </text>
        <text x="970" y="212" textAnchor="end">
          {rows.at(-1)?.time}
        </text>
      </svg>
      <details>
        <summary>查看趋势数据</summary>
        <Table
          rows={rows}
          columns={[
            { key: "time", label: "UTC 小时" },
            { key: "pv", label: "PV" },
            { key: "uv", label: "UV" },
            { key: "requests", label: "HTTP 请求" },
            { key: "events", label: "事件" },
            { key: "errors", label: "HTTP 错误" },
          ]}
        />
      </details>
    </div>
  );
}
function DataTable({
  path,
  query,
  revision,
  columns,
}: {
  path: string;
  query: string;
  revision: number;
  columns: Parameters<typeof Table>[0]["columns"];
}) {
  const state = useData(() => api(path + query), [path, query, revision]);
  return (
    <section className="panel">
      <AsyncPanel state={state}>
        <Table rows={state.data || []} columns={columns} />
      </AsyncPanel>
    </section>
  );
}
function Events({ query, revision, eventType = "" }: { query: string; revision: number; eventType?: string }) {
  const [type, setType] = useState(eventType),
    [page, setPage] = useState(1),
    [detail, setDetail] = useState<Row | null>(null);
  useEffect(() => { setPage(1); setDetail(null); }, [query, type]);
  const state = useData(
    () =>
      api(
        "/events" +
          query +
          `&page=${page}&eventType=${encodeURIComponent(type)}`,
      ),
    [query, type, page, revision],
  );
  return (
    <>
      {!eventType && <div className="toolbar">
        <label>
          事件类型
          <select value={type} onChange={(e) => setType(e.target.value)}>
            {[
              "",
              "http_request",
              "page_view",
              "route_change",
              "click",
              "session_start",
              "page_hidden",
              "js_error",
              "unhandled_rejection",
              "performance_event",
            ].map((t) => (
              <option key={t} value={t}>
                {eventLabels[t] || "全部事件"}
              </option>
            ))}
          </select>
        </label>
      </div>}
      <section className="panel">
        <AsyncPanel state={state}>
          <Table
            rows={state.data || []}
            onRow={setDetail}
            columns={[
              { key: "type", label: "行为", render: eventLabel },
              {
                key: "timestamp",
                label: "发生时间",
                render: (r) => when(r.timestamp),
              },
              { key: "path", label: "页面", render: eventPath },
              ...(eventType === "click" ? [{
                key: "region", label: "区域",
                render: (row: Row) => parse<Row>(row.properties, {}).region || "—",
              }] : []),
              { key: "source_type", label: "来源", render: (row) => row.source_type === "browser" ? "浏览器" : row.source_type === "host" ? "访问日志" : row.source_type },
              { key: "device", label: "设备" },
              { key: "browser", label: "浏览器" },
            ]}
          />
          <Pager
            page={page}
            setPage={setPage}
            count={state.data?.length || 0}
          />
        </AsyncPanel>
      </section>
      {detail && (
        <section className="panel">
          <div className="panel-title">
            <h2>事件详情</h2>
            <button onClick={() => setDetail(null)}>关闭详情</button>
          </div>
          <dl className="facts">
            <dt>行为</dt><dd>{eventLabel(detail)}</dd>
            <dt>时间</dt><dd>{when(detail.timestamp)}</dd>
            <dt>页面</dt><dd>{eventPath(detail)}</dd>
            <dt>所在区域</dt><dd>{parse<Row>(detail.properties, {}).region || "—"}</dd>
            <dt>设备</dt><dd>{[detail.device, detail.browser, detail.os].filter(Boolean).join(" / ") || "—"}</dd>
          </dl>
          <details><summary>原始数据</summary><pre>{JSON.stringify({
            ...detail, properties: parse(detail.properties, {}), device_info: parse(detail.device_info, {}),
          }, null, 2)}</pre></details>
        </section>
      )}
    </>
  );
}
function Sessions({ query, revision }: { query: string; revision: number }) {
  const [page, setPage] = useState(1),
    [selected, setSelected] = useState<Row | null>(null),
    [eventPage, setEventPage] = useState(1);
  useEffect(() => {
    setPage(1);
    setSelected(null);
  }, [query]);
  const state = useData(
    () => api("/sessions" + query + "&page=" + page),
    [query, page, revision],
  );
  const timeline = useData(
    () =>
      selected
        ? api(
            `/sessions/${encodeURIComponent(selected.session_id)}?projectId=${encodeURIComponent(selected.project_id)}&page=${eventPage}`,
          )
        : Promise.resolve([]),
    [selected, eventPage, revision],
  );
  return (
    <>
      <section className="panel">
        <AsyncPanel state={state}>
          <Table
            rows={state.data || []}
            onRow={(r) => {
              setSelected(r);
              setEventPage(1);
            }}
            columns={[
              { key: "session_id", label: "匿名会话" },
              { key: "device", label: "设备" },
              { key: "browser", label: "浏览器" },
              {
                key: "started_at",
                label: "开始时间",
                render: (r) => when(r.started_at),
              },
              { key: "pages", label: "页面数" },
              { key: "interactions", label: "点击数" },
              {
                key: "duration_ms",
                label: "前台停留",
                render: (r) =>
                  `${(Number(r.duration_ms) / 1000).toFixed(1)} 秒`,
              },
            ]}
          />
          <Pager
            page={page}
            setPage={setPage}
            count={state.data?.length || 0}
          />
        </AsyncPanel>
      </section>
      {selected && (
        <section className="panel">
          <div className="panel-title">
            <h2>会话时间线</h2>
            <button onClick={() => setSelected(null)}>关闭</button>
          </div>
          <code>{selected.session_id}</code>
          <AsyncPanel state={timeline}>
            <ol className="timeline">
              {timeline.data?.map((e) => (
                <li key={e.id}>
                  <time>{when(e.timestamp)}</time>
                  <div>
                    <strong>{eventLabel(e)}</strong>
                    <span>{eventPath(e)}</span>
                    <details>
                      <summary>详细属性</summary>
                      <pre>
                        {JSON.stringify(parse(e.properties, {}), null, 2)}
                      </pre>
                    </details>
                  </div>
                  <Badge value={e.source_type} />
                </li>
              ))}
            </ol>
            <Pager
              page={eventPage}
              setPage={setEventPage}
              count={timeline.data?.length || 0}
            />
          </AsyncPanel>
        </section>
      )}
    </>
  );
}
function Security({ revision }: { revision: number }) {
  const state = useData(() => api<Row>("/security/summary"), [revision]);
  const d = state.data;
  return (
    <AsyncPanel state={state}>
      {d && (
        <>
          <section className="panel">
            <h2>运行异常</h2>
            <p
              className={
                d.diskFreeBytes >= 0 && d.diskFreeBytes < 268435456
                  ? "error"
                  : "muted"
              }
            >
              {d.diskFreeBytes >= 0
                ? `磁盘可用 ${(d.diskFreeBytes / 1024 / 1024 / 1024).toFixed(2)} GB`
                : "无法读取磁盘可用空间"}
            </p>
            <Table
              rows={d.agents || []}
              columns={[
                { key: "name", label: "采集服务" },
                {
                  key: "last_seen",
                  label: "最后心跳",
                  render: (r) => when(r.last_seen),
                },
                {
                  key: "error",
                  label: "上报错误",
                  render: (r) => r.error || "已离线或等待首次心跳",
                },
                {
                  key: "metrics",
                  label: "采集丢弃数",
                  render: (r) => number(parse<Row>(r.metrics, {}).dropped),
                },
              ]}
            />
          </section>

          <section className="panel">
            <h2>安全计数</h2>
            {d.counters.length ? (
              <Table
                rows={d.counters}
                columns={[
                  { key: "name", label: "类别" },
                  { key: "count", label: "累计次数" },
                ]}
              />
            ) : (
              <Empty>暂无异常记录</Empty>
            )}
          </section>
          <section className="panel">
            <h2>今日写入配额 · UTC</h2>
            <Table
              rows={d.quotas}
              columns={[
                { key: "name", label: "站点" },
                { key: "used", label: "已写入" },
                { key: "daily_quota", label: "每日上限" },
              ]}
            />
          </section>
          <section className="panel">
            <h2>管理审计</h2>
            <Table
              rows={d.audit}
              columns={[
                {
                  key: "created_at",
                  label: "时间",
                  render: (r) => when(r.created_at),
                },
                { key: "action", label: "操作" },
                { key: "target", label: "目标" },
              ]}
            />
          </section>
        </>
      )}
    </AsyncPanel>
  );
}
function LocalProbe({ revision }: { revision: number }) {
  const [change, setChange] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const state = useData(
    () => api<Row>("/system/probe"),
    [revision, change],
    10000,
  );
  const probe = state.data;
  const labels: Record<string, string> = {
    Online: "运行中",
    Starting: "启动中",
    Disabled: "已暂停",
    Warning: "采集异常",
    Failed: "启动失败",
    Offline: "心跳超时",
  };

  async function toggle(enabled: boolean) {
    setBusy(true);
    setError("");
    try {
      await api("/system/probe", {
        method: "PATCH",
        body: JSON.stringify({ enabled }),
      });
      setChange((value) => value + 1);
    } catch (error) {
      setError((error as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="panel">
      <div className="panel-title">
        <h2>本机采集服务</h2>
        {probe && (
          <span role="status">{labels[probe.status] ?? probe.status}</span>
        )}
      </div>
      <AsyncPanel state={state}>
        {probe && (
          <>
            <dl className="facts">
              <dt>主机</dt>
              <dd>{probe.hostname || "—"}</dd>
              <dt>平台</dt>
              <dd>
                {[probe.os, probe.arch].filter(Boolean).join(" / ") || "—"}
              </dd>
              <dt>配置站点数</dt>
              <dd>{parse<Row>(probe.metrics, {}).configuredSites ?? "—"}</dd>
              <dt>最后心跳</dt>
              <dd>{when(probe.last_seen)}</dd>
            </dl>
            <ErrorBox message={error || probe.error} />
            <button
              disabled={busy || probe.status === "Failed"}
              onClick={() => void toggle(!probe.enabled)}
            >
              {busy ? "处理中…" : probe.enabled ? "暂停全部采集" : "恢复全部采集"}
            </button>
          </>
        )}
      </AsyncPanel>
    </section>
  );
}

type NginxScan = {
  id: string;
  ready: number;
  sites: {
    site: string;
    file?: string;
    status: "ready" | "configured" | "skipped" | "applied";
    reason?: string;
  }[];
};

function NginxCollection() {
  const [plan, setPlan] = useState<NginxScan | null>(null);
  const [pending, setPending] = useState<"scan" | "apply" | null>(null);
  const [error, setError] = useState("");
  const statusLabel: Record<NginxScan["sites"][number]["status"], string> = {
    ready: "可接入",
    configured: "已配置",
    skipped: "已跳过",
    applied: "已请求重载",
  };

  async function scan() {
    setPlan(null);
    setError("");
    setPending("scan");
    try {
      setPlan(await api<NginxScan>("/system/nginx/scan", { method: "POST" }));
    } catch (error) {
      setError((error as Error).message);
    } finally {
      setPending(null);
    }
  }

  async function apply() {
    if (!plan?.ready) return;
    setError("");
    setPending("apply");
    try {
      setPlan(
        await api<NginxScan>("/system/nginx/apply", {
          method: "POST",
          body: JSON.stringify({ id: plan.id }),
        }),
      );
    } catch (error) {
      setPlan(null);
      setError((error as Error).message);
    } finally {
      setPending(null);
    }
  }

  return (
    <section className="panel" aria-labelledby="nginx-collection-title">
      <div className="panel-title">
        <h2 id="nginx-collection-title">站点接入</h2>
        <button
          className="primary"
          disabled={pending !== null}
          onClick={() => void scan()}
        >
          {pending === "scan"
            ? "扫描中…"
            : plan
              ? "重新扫描"
              : "扫描站点"}
        </button>
      </div>
      <ErrorBox message={error} />
      {plan && (
        <>
          {plan.ready > 0 && (
            <p>
              接入 {plan.ready} 个站点，将备份配置并重载 Nginx。
            </p>
          )}
          {plan.sites.some((site) => site.status === "applied") && (
            <p role="status">
              已请求重载，收到数据后显示在站点列表。
            </p>
          )}
          <Table
            rows={plan.sites}
            columns={[
              { key: "site", label: "站点" },
              {
                key: "status",
                label: "状态",
                render: (row) =>
                  statusLabel[
                    row.status as NginxScan["sites"][number]["status"]
                  ],
              },
              { key: "reason", label: "说明" },
              { key: "file", label: "配置文件" },
            ]}
          />
          {plan.ready > 0 && (
            <div className="actions nginx-confirm-actions">
              <button
                className="primary"
                disabled={pending !== null}
                onClick={() => void apply()}
              >
                {pending === "apply" ? "应用中…" : "确认接入"}
              </button>
              <button disabled={pending !== null} onClick={() => setPlan(null)}>
                取消
              </button>
            </div>
          )}
        </>
      )}
    </section>
  );
}

function System({ revision }: { revision: number }) {
  const state = useData(() => api<Row>("/system/status"), [revision]);
  const [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  const d = state.data;
  return (
    <>
      <AsyncPanel state={state}>
        {d && (
          <section className="panel">
            <h2>运行信息</h2>
            <dl className="facts">
              <dt>版本</dt>
              <dd>{d.version}</dd>
              <dt>数据库</dt>
              <dd>{d.database}</dd>
              <dt>数据库及 WAL 体积</dt>
              <dd>{(Number(d.databaseBytes) / 1024 / 1024).toFixed(2)} MB</dd>
              <dt>磁盘可用空间</dt>
              <dd>
                {d.diskFreeBytes >= 0
                  ? `${(d.diskFreeBytes / 1024 / 1024 / 1024).toFixed(2)} GB`
                  : "Unknown"}
              </dd>
              <dt>管理端 Origin</dt>
              <dd>{d.publicUrl}</dd>
              <dt>统计时区</dt>
              <dd>{d.timezone}</dd>
            </dl>
            <h2>数据备份</h2>
            <p className="muted">
              备份包含账户与采集数据，请妥善保存。
            </p>
            <ErrorBox message={error} />
            <button
              disabled={busy}
              onClick={async () => {
                setBusy(true);
                setError("");
                try {
                  await backup();
                } catch (e) {
                  setError((e as Error).message);
                } finally {
                  setBusy(false);
                }
              }}
            >
              <Download size={16} />
              {busy ? "生成备份中…" : "下载备份"}
            </button>
            <PasswordSettings />
          </section>
        )}
      </AsyncPanel>
    </>
  );
}
createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);

function PasswordSettings() {
  const [current, setCurrent] = useState(""),
    [next, setNext] = useState(""),
    [error, setError] = useState(""),
    [notice, setNotice] = useState(""),
    [busy, setBusy] = useState(false);
  return (
    <form
      className="password-settings"
      onSubmit={async (e) => {
        e.preventDefault();
        setBusy(true);
        setError("");
        setNotice("");
        try {
          await api("/auth/password", {
            method: "POST",
            body: JSON.stringify({
              currentPassword: current,
              newPassword: next,
            }),
          });
          setCurrent("");
          setNext("");
          setNotice("密码已更新，其他登录会话已退出。");
        } catch (e) {
          setError((e as Error).message);
        } finally {
          setBusy(false);
        }
      }}
    >
      <h2>修改管理员密码</h2>
      <div className="form-grid">
        <label>
          当前密码
          <input
            type="password"
            autoComplete="current-password"
            required
            maxLength={256}
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
          />
        </label>
        <label>
          新密码（至少 12 字符）
          <input
            type="password"
            autoComplete="new-password"
            required
            minLength={12}
            maxLength={256}
            value={next}
            onChange={(e) => setNext(e.target.value)}
          />
        </label>
      </div>
      <ErrorBox message={error} />
      {notice && <p className="notice">{notice}</p>}
      <button disabled={busy}>{busy ? "保存中…" : "更新密码"}</button>
    </form>
  );
}
