import React, { createContext, useContext, useState, useEffect, useMemo, useRef } from "react";
import { createRoot } from "react-dom/client";
import {
  Activity,
  Globe,
  Search,
  Settings,
  ShieldCheck,
  LogOut,
  RefreshCw,
  ArrowRight,
  ChevronLeft,
  ChevronRight,
  Download,
  Info,
  Upload,
  LoaderCircle,
  Plus,
  X,
} from "lucide-react";
import { api, backup, setCSRF, parse, when as formatTime, number, Row } from "./api";
import { Select } from "./components/Select";
import "./style.css";
const PAGE_SIZE = 20;
const TimezoneContext = createContext("UTC");

function useTimeFormatter() {
  const timezone = useContext(TimezoneContext);
  return (value: unknown) => formatTime(value, timezone);
}

function listTotal(rows: Row[] | null | undefined) {
  return (rows as (Row[] & { total?: number }) | null | undefined)?.total ?? rows?.length ?? 0;
}
const sections = [
  { id: "sites", name: "站点" },
  { id: "overview", name: "概览" },
  { id: "pages", name: "页面" },
  { id: "clicks", name: "点击行为" },
  { id: "sessions", name: "访问轨迹" },
  { id: "events", name: "事件记录" },
  { id: "devices", name: "设备" },
  { id: "collection", name: "探针管理" },
  { id: "security", name: "安全审计" },
  { id: "system", name: "运行设置" },
  { id: "system-info", name: "系统信息" },
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
  manual = false,
  loading,
  keepPage = false,
  emptyLabel = "暂无数据",
}: {
  columns: {
    key: string;
    label: string;
    width?: string;
    render?: (r: Row) => React.ReactNode;
  }[];
  rows: Row[];
  onRow?: (r: Row) => void;
  manual?: boolean;
  loading?: boolean;
  keepPage?: boolean;
  emptyLabel?: string;
}) {
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(PAGE_SIZE);
  useEffect(() => {
    setPage((previous) => keepPage ? Math.min(previous, Math.max(1, Math.ceil(rows.length / pageSize))) : 1);
  }, [rows, pageSize, keepPage]);
  const currentPage = Math.min(page, Math.max(1, Math.ceil(rows.length / pageSize)));
  const visible = manual ? rows : rows.slice((currentPage - 1) * pageSize, currentPage * pageSize);
  if (!rows.length && loading === undefined) return <Empty>{emptyLabel}</Empty>;
  const grid = (
    <div className="table-wrap">
      <table>
        {columns.some((column) => column.width) && (
          <colgroup>
            {columns.map((column) => <col key={column.key} style={{ width: column.width }} />)}
          </colgroup>
        )}
        <thead>
          <tr>
            {columns.map((c) => (
              <th key={c.key}>{c.label}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {!rows.length && <tr><td colSpan={columns.length} className="table-refresh-empty">{emptyLabel}</td></tr>}
          {visible.map((r, i) => (
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
  return (
    <>
    {loading === undefined ? grid : (
      <div className="table-refresh-frame" aria-busy={loading}>
        {grid}
        {loading && (
          <div className="table-refresh-overlay" role="status" aria-label="正在刷新站点">
            <LoaderCircle size={20} aria-hidden="true" />
          </div>
        )}
      </div>
    )}
    {!manual && rows.length > 0 && <Pager page={currentPage} setPage={setPage} total={rows.length}
      pageSize={pageSize} setPageSize={setPageSize} />}
    </>
  );
}
function Pager({ page, setPage, total, pageSize, setPageSize }: {
  page: number;
  setPage: (n: number) => void;
  total: number;
  pageSize: number;
  setPageSize: (n: number) => void;
}) {
  const pages = Math.max(1, Math.ceil(total / pageSize));
  const [jump, setJump] = useState("");
  useEffect(() => setJump(""), [page, pageSize, total]);
  useEffect(() => { if (page > pages) setPage(pages); }, [page, pages, setPage]);
  const start = Math.max(1, Math.min(page - 2, pages - 4));
  const end = Math.min(pages, start + 4);
  const items: (number | string)[] = [];
  if (start > 1) items.push(1);
  if (start > 2) items.push("before");
  for (let value = start; value <= end; value++) items.push(value);
  if (end < pages - 1) items.push("after");
  if (end < pages) items.push(pages);
  function go(value: number) {
    setPage(Math.min(pages, Math.max(1, value)));
  }
  function commitJump() {
    if (/^\d+$/.test(jump)) go(Number(jump));
    setJump("");
  }
  return (
    <div className="pager" aria-label="分页">
      <span className="pager-total">共 {number(total)} 条</span>
      <div className="pager-pages">
        <button disabled={page <= 1} onClick={() => go(page - 1)} aria-label="上一页"><ChevronLeft size={16} /></button>
        {items.map((item) => typeof item === "number"
          ? <button key={item} className={item === page ? "current" : ""}
              aria-label={`第 ${item} 页`} aria-current={item === page ? "page" : undefined}
              onClick={() => go(item)}>{item}</button>
          : <span key={item} className="pager-ellipsis">…</span>)}
        <button disabled={page >= pages} onClick={() => go(page + 1)} aria-label="下一页"><ChevronRight size={16} /></button>
      </div>
      <Select ariaLabel="每页条数" value={String(pageSize)}
        options={[20, 50, 100].map((size) => ({ value: String(size), label: `${size} 条/页` }))}
        onChange={(value) => { setPageSize(Number(value)); setPage(1); }} />
      <label className="pager-jump">跳至<input aria-label="跳转页码" inputMode="numeric" value={jump}
        onChange={(event) => setJump(event.target.value.replace(/[^0-9]/g, ""))}
        onBlur={commitJump} onKeyDown={(event) => { if (event.key === "Enter") commitJump(); }} />页</label>
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
        <img className="brand-logo" src="/assets/sailfin-logo.svg" alt="" />
        <span className="brand-name"><span>帆鳍</span><small>Sailfin</small></span>
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
      </form>
    </div>
  );
}
function SiteAssets({ state, selected, siteHref, onNavigate }: {
  state: { data: Row[] | null; loading: boolean; error: string };
  selected: string;
  siteHref: (id: string) => string;
  onNavigate: React.MouseEventHandler<HTMLAnchorElement>;
}) {
  const [search, setSearch] = useState("");
  const sites = state.data || [];
  const term = search.trim().toLocaleLowerCase();
  const matches = sites.filter((site) => String(site.name).toLocaleLowerCase().includes(term));
  return (
    <aside className="site-assets" aria-label="站点资产">
      <div className="asset-search">
        <Search size={16} aria-hidden="true" />
        <input type="search" aria-label="搜索站点" placeholder="搜索站点" value={search}
          onChange={(event) => setSearch(event.target.value)} />
      </div>
      <ErrorBox message={state.error} />
      {state.loading && !state.data ? <div className="loading">加载中…</div> : (
        <>
          <a className={`asset-all ${!selected ? "selected" : ""}`} href={siteHref("")}
            onClick={onNavigate} aria-current={!selected ? "page" : undefined}>
            <Globe size={16} aria-hidden="true" /><span>全部站点</span><span className="asset-count">{state.data ? sites.length : "—"}</span>
          </a>
          <nav className="asset-items" aria-label="站点列表">
            {matches.map((site) => (
              <a key={site.id} href={siteHref(site.id)} onClick={onNavigate}
                className={`asset-item ${selected === site.id ? "selected" : ""}`}
                title={site.name} aria-current={selected === site.id ? "page" : undefined}>
                <Globe size={15} aria-hidden="true" /><span>{site.name}</span>
              </a>
            ))}
            {!matches.length && <div className="asset-empty">{term ? "无匹配站点" : "暂无站点"}</div>}
          </nav>
        </>
      )}
    </aside>
  );
}
function App() {
  const [authenticated, setAuthenticated] = useState<boolean | null>(null),
    [revision, setRevision] = useState(0),
    [error, setError] = useState("");
  const { current, params, href, navigate } = useDashboardLocation();
  const section = current.id === "sites" ? "overview" : current.id;
  const project = params.get("projectId") ?? "";
  const selectedRange = params.get("range") ?? "";
  const range = ["24", "168", "720", "custom"].includes(selectedRange)
    ? selectedRange
    : "24";
  const customFrom = params.get("from") ?? "";
  const customTo = params.get("to") ?? "";
  const selectedEventType = params.get("eventType") ?? "";
  const eventType = Object.hasOwn(eventLabels, selectedEventType) ? selectedEventType : "";
  const projects = useData(
    () => (authenticated ? api("/sites") : Promise.resolve([])),
    [authenticated, revision],
    authenticated ? 10000 : 0,
  );
  const system = useData(
    () => authenticated ? api<Row>("/system/status") : Promise.resolve(null),
    [authenticated, revision],
  );
  const timezone = system.data?.timezone ?? "UTC";
  useEffect(() => {
    api("/auth/me")
      .then((rows) => {
        setCSRF(rows[0].csrf);
        setAuthenticated(true);
      })
      .catch(() => setAuthenticated(false));
    const expired = () => setAuthenticated(false);
    window.addEventListener("sailfin:unauthorized", expired);
    return () => window.removeEventListener("sailfin:unauthorized", expired);
  }, []);
  const query = useMemo(() => {
    const filter = new URLSearchParams({ projectId: project });
    if (range === "custom") {
      if (customFrom) filter.set("fromLocal", customFrom);
      if (customTo) filter.set("toLocal", customTo);
    } else {
      const to = Date.now();
      filter.set("to", String(to));
      filter.set("from", String(to - Number(range) * 3600000));
    }
    return `?${filter}`;
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
  const settingsView = section === "system-info" || section === "system" || section === "security";
  const probeView = section === "collection";
  const probePane = params.get("view") === "probes" ? "probes" : "sites";
  const selectedSite = projects.data?.find((item) => item.id === project);
  const primarySection = settingsView ? "system-info" : section === "collection" ? "collection" : "sites";
  return (
    <TimezoneContext.Provider value={timezone}>
    <div className="shell site-shell">
      <header className="topbar">
        <a className="brand" href={href("sites", { projectId: "" })} onClick={followLink}>
          <img className="brand-logo" src="/assets/sailfin-logo.svg" alt="" /><span className="brand-name"><span>帆鳍</span><small>Sailfin</small></span>
        </a>
        <nav className="primary-nav" aria-label="主导航">
          {[
            { id: "sites", name: "站点" },
            { id: "collection", name: "探针管理" },
            { id: "system-info", name: "系统设置" },
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
      <main className="site-main" aria-label={siteView ? selectedSite?.name || "全部站点" : current.name}>
        <div className="workspace site-workspace">
          {siteView && <SiteAssets state={projects} selected={project}
            siteHref={(id) => href(section, { projectId: id })} onNavigate={followLink} />}
          {probeView && <aside className="site-assets module-menu" aria-label="探针导航">
            <nav>
              {[{ id: "sites", name: "站点接入", icon: Globe }, { id: "probes", name: "探针管理", icon: Activity }].map((item) => (
                <a key={item.id} className={`asset-item ${probePane === item.id ? "selected" : ""}`}
                  href={href("collection", { view: item.id })} onClick={followLink}
                  aria-current={probePane === item.id ? "page" : undefined}>
                  <item.icon size={16} aria-hidden="true" /><span>{item.name}</span>
                </a>
              ))}
            </nav>
          </aside>}
          {settingsView && <aside className="site-assets module-menu" aria-label="设置导航">
            <nav>
              {[{ id: "system-info", name: "系统信息", icon: Info }, { id: "system", name: "运行设置", icon: Settings }, { id: "security", name: "安全审计", icon: ShieldCheck }].map((item) => (
                <a key={item.id} className={`asset-item ${section === item.id ? "selected" : ""}`}
                  href={href(item.id)} onClick={followLink}
                  aria-current={section === item.id ? "page" : undefined}>
                  <item.icon size={16} aria-hidden="true" /><span>{item.name}</span>
                </a>
              ))}
            </nav>
          </aside>}
          <div className="detail-workspace">
          {siteView && <div className="site-sticky-header">
          {siteView && (
              <nav className="tabs" aria-label="站点导航">
                {siteSections.map((item) => (
                  <a key={item.id} href={href(item.id)} onClick={followLink}
                    className={section === item.id ? "active" : ""}
                    aria-current={section === item.id ? "page" : undefined}>{item.name}</a>
                ))}
              </nav>
          )}
          </div>}
          <div className={`detail-scroll ${["pages", "devices", "clicks", "events", "sessions"].includes(section) || probeView ? "list-workspace" : ""}${section === "system-info" ? " system-info-workspace" : ""}`}>
          {siteView && (
              <div className="filterbar">
                <div className="filters">
                  <Select label="时间" ariaLabel="时间范围" value={range}
                    options={[
                      { value: "24", label: "最近 24 小时" }, { value: "168", label: "最近 7 天" },
                      { value: "720", label: "最近 30 天" }, { value: "custom", label: "自定义" },
                    ]}
                    onChange={(value) => navigate(href(section, { range: value === "24" ? "" : value }), true)} />
                  {section === "events" && <Select label="事件类型" ariaLabel="事件类型" value={eventType}
                    options={[{ value: "", label: "全部事件" }, ...Object.entries(eventLabels).map(([value, label]) => ({ value, label }))]}
                    onChange={(value) => navigate(href(section, { eventType: value }), true)} />}
                  {range === "custom" && <>
                    <label>开始：<input type="datetime-local" value={customFrom} onChange={(e) => navigate(href(section, { from: e.target.value }), true)} /></label>
                    <label>结束：<input type="datetime-local" value={customTo} onChange={(e) => navigate(href(section, { to: e.target.value }), true)} /></label>
                    <span className="site-meta">{timezone}</span>
                  </>}
                  {selectedSite && <span className="site-meta">{selectedSite.last_event ? `最近上报：${formatTime(selectedSite.last_event, timezone)}` : "等待首批数据"}</span>}
                </div>
              </div>
          )}
          <ErrorBox message={error} />
          {section === "overview" && <Overview query={query} revision={revision} />}
          {section === "pages" && <DataTable path="/stats/pages" query={query} revision={revision} columns={[
            { key: "path", label: "页面 / 请求路径" }, { key: "pv", label: "浏览量" },
            { key: "requests", label: "HTTP 请求" },
          ]} />}
          {section === "devices" && <DataTable path="/stats/devices" query={query} revision={revision} columns={[
            { key: "device", label: "设备类型" }, { key: "browser", label: "浏览器" }, { key: "os", label: "操作系统" },
            { key: "requests", label: "HTTP 请求" }, { key: "pv", label: "浏览量" }, { key: "uv", label: "匿名访客" }, { key: "events", label: "事件总数" },
          ]} />}
          {section === "events" && <Events key="events" query={query} revision={revision} eventType={eventType} />}
          {section === "clicks" && <Clicks query={query} revision={revision} />}
          {section === "sessions" && <Sessions query={query} revision={revision} />}
          {probeView && (probePane === "sites" ? <NginxCollection revision={revision} onChange={refresh} /> : <LocalProbe revision={revision} />)}
          {section === "security" && <Security revision={revision} />}
          {section === "system-info" && <SystemInfo state={system} revision={revision} onChange={refresh} />}
          {section === "system" && <SystemSettings />}
          </div>
          </div>
        </div>
      </main>
    </div>
    </TimezoneContext.Provider>
  );
}
function Overview({ query, revision }: { query: string; revision: number }) {
  const timezone = useContext(TimezoneContext);
  const state = useData(async () => {
    const [overview, trends] = await Promise.all([
      api("/stats/overview" + query),
      api("/stats/trends" + query),
    ]);
    return { overview: overview[0], trends };
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
              <span className="muted">{timezone} · 每小时</span>
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
              </div>
              <DataTable bare path="/stats/pages" query={query} revision={revision}
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
  const when = useTimeFormatter();
  const hour = (row: Row) => when(Date.parse(String(row.time).replace(" ", "T") + ":00Z"));
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
          {hour(rows[0])}
        </text>
        <text x="970" y="212" textAnchor="end">
          {hour(rows[rows.length - 1])}
        </text>
      </svg>
      <details>
        <summary>查看趋势数据</summary>
        <Table
          rows={rows}
          columns={[
            { key: "time", label: "小时", render: hour },
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
function DataTable({ path, query, revision, columns, bare = false, emptyLabel }: {
  path: string;
  query: string;
  revision: number;
  columns: Parameters<typeof Table>[0]["columns"];
  bare?: boolean;
  emptyLabel?: string;
}) {
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(PAGE_SIZE);
  useEffect(() => setPage(1), [path, query]);
  const state = useData(
    () => api(path + query + `&page=${page}&limit=${pageSize}`),
    [path, query, page, pageSize, revision],
  );
  const content = <AsyncPanel state={state}>
    <Table manual rows={state.data || []} columns={columns} emptyLabel={emptyLabel} />
    <Pager page={page} setPage={setPage} total={listTotal(state.data)} pageSize={pageSize} setPageSize={setPageSize} />
  </AsyncPanel>;
  return bare ? content : <section className="panel list-panel">{content}</section>;
}
function Clicks({ query, revision }: { query: string; revision: number }) {
  const when = useTimeFormatter();
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(PAGE_SIZE);
  useEffect(() => setPage(1), [query]);
  const state = useData(
    () => api("/stats/clicks" + query + `&page=${page}&limit=${pageSize}`),
    [query, page, pageSize, revision],
  );
  const allSites = !new URLSearchParams(query).get("projectId");
  return (
    <section className="panel list-panel">
      <AsyncPanel state={state}>
        <Table manual rows={state.data || []} columns={[
          ...(allSites ? [{ key: "site_name", label: "站点" }] : []),
          { key: "path", label: "页面" },
          { key: "label", label: "元素名称", render: (row) => row.label || "未命名元素" },
          { key: "region", label: "区域", render: (row) => row.region || "—" },
          { key: "clicks", label: "点击次数", render: (row) => number(row.clicks) },
          { key: "visitors", label: "匿名访客", render: (row) => number(row.visitors) },
          { key: "last_click", label: "最近点击", render: (row) => when(row.last_click) },
        ]} />
        <Pager page={page} setPage={setPage} total={listTotal(state.data)} pageSize={pageSize} setPageSize={setPageSize} />
      </AsyncPanel>
    </section>
  );
}
function Events({ query, revision, eventType = "" }: { query: string; revision: number; eventType?: string }) {
  const when = useTimeFormatter();
  const type = eventType;
  const [page, setPage] = useState(1),
    [detail, setDetail] = useState<Row | null>(null);
  const [pageSize, setPageSize] = useState(PAGE_SIZE);
  useEffect(() => { setPage(1); setDetail(null); }, [query, type]);
  const state = useData(
    () =>
      api(
        "/events" +
          query +
          `&page=${page}&limit=${pageSize}&eventType=${encodeURIComponent(type)}`,
      ),
    [query, type, page, pageSize, revision],
  );
  return (
    <div className="list-view">
      <section className="panel list-panel">
        <AsyncPanel state={state}>
          <Table manual
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
            total={listTotal(state.data)} pageSize={pageSize} setPageSize={setPageSize}
          />
        </AsyncPanel>
      </section>
      {detail && (
        <section className="panel record-details" aria-label="事件详情">
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
    </div>
  );
}
function Sessions({ query, revision }: { query: string; revision: number }) {
  const when = useTimeFormatter();
  const [page, setPage] = useState(1),
    [selected, setSelected] = useState<Row | null>(null),
    [eventPage, setEventPage] = useState(1);
  const [eventPageSize, setEventPageSize] = useState(PAGE_SIZE);
  const [pageSize, setPageSize] = useState(PAGE_SIZE);
  useEffect(() => {
    setPage(1);
    setSelected(null);
  }, [query]);
  const state = useData(
    () => api("/sessions" + query + `&page=${page}&limit=${pageSize}`),
    [query, page, pageSize, revision],
  );
  const timeline = useData(
    () =>
      selected
        ? api(
            `/sessions/${encodeURIComponent(selected.session_id)}?projectId=${encodeURIComponent(selected.project_id)}&page=${eventPage}&limit=${eventPageSize}`,
          )
        : Promise.resolve([]),
    [selected, eventPage, eventPageSize, revision],
  );
  return (
    <div className="list-view">
      <section className="panel list-panel">
        <AsyncPanel state={state}>
          <Table manual
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
            total={listTotal(state.data)} pageSize={pageSize} setPageSize={setPageSize}
          />
        </AsyncPanel>
      </section>
      {selected && (
        <section className="panel record-details" aria-label="会话时间线">
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
              total={listTotal(timeline.data)} pageSize={eventPageSize} setPageSize={setEventPageSize}
            />
          </AsyncPanel>
        </section>
      )}
    </div>
  );
}
function Security({ revision }: { revision: number }) {
  const when = useTimeFormatter();
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
            <DataTable bare path="/security/audit" query="?" revision={revision}
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
  const when = useTimeFormatter();
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
    <section className="panel list-panel">
      <AsyncPanel state={state}>
        <ErrorBox message={error || probe?.error || ""} />
        <Table rows={probe ? [probe] : []} columns={[
          { key: "name", label: "探针", render: (row) => row.name || "本机探针" },
          { key: "hostname", label: "主机", render: (row) => row.hostname || "—" },
          { key: "status", label: "健康状态", render: (row) => (
            <span className={`probe-health ${row.status === "Online" ? "healthy" : "unhealthy"}`}>
              <span aria-hidden="true" />{row.status === "Online" ? "正常" : labels[row.status] || "未就绪"}
            </span>
          ) },
          { key: "platform", label: "平台", render: (row) => [row.os, row.arch].filter(Boolean).join(" / ") || "—" },
          { key: "version", label: "版本", render: (row) => row.version || "—" },
          { key: "sites", label: "配置站点数", render: (row) => parse<Row>(row.metrics, {}).configuredSites ?? "—" },
          { key: "last_seen", label: "最后心跳", render: (row) => when(row.last_seen) },
          { key: "actions", label: "操作", render: (row) => (
            <button disabled={busy || row.status === "Failed"} onClick={() => void toggle(!row.enabled)}>
              {busy ? "处理中…" : row.enabled ? "暂停全部采集" : "恢复全部采集"}
            </button>
          ) },
        ]} />
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
    siteId?: string;
    origin?: string;
    added?: boolean;
  }[];
};

type SiteDraft = {
  id: string;
  name: string;
  accessLog: string;
  browserOrigin: string;
  requireConsent: boolean;
};

const emptySite: SiteDraft = {
  id: "", name: "", accessLog: "", browserOrigin: "", requireConsent: true,
};

function NginxCollection({ revision, onChange }: { revision: number; onChange: () => void }) {
  const when = useTimeFormatter();
  const dialog = useRef<HTMLDialogElement>(null);
  const [drawer, setDrawer] = useState<"add" | "edit" | null>(null);
  const [tab, setTab] = useState<"manual" | "scan">("manual");
  const [plan, setPlan] = useState<NginxScan | null>(null);
  const [pending, setPending] = useState<string | null>(null);
  const [error, setError] = useState("");
  const [drawerError, setDrawerError] = useState("");
  const [chosen, setChosen] = useState<string[]>([]);
  const [draft, setDraft] = useState<SiteDraft>(emptySite);
  const registered = useData(() => api("/system/sites"), [revision], 10000);
  const showLogs = drawer !== null && tab === "manual";
  const logPaths = useData(() => showLogs ? api<string[]>("/system/site-logs") : Promise.resolve([]), [showLogs, revision]);
  const rows = (registered.data ?? []).filter((row) => row.source !== "scan");

  useEffect(() => {
    const panel = dialog.current;
    if (drawer && panel && !panel.open) panel.showModal();
    if (!drawer && panel?.open) panel.close();
  }, [drawer]);

  function openDrawer(site?: Row) {
    setTab("manual");
    setPlan(null);
    setChosen([]);
    setDrawerError("");
    if (site) {
      setDraft({
        id: site.siteId, name: site.name, accessLog: site.accessLog,
        browserOrigin: site.browserOrigin, requireConsent: Boolean(site.requireConsent),
      });
      setDrawer("edit");
    } else {
      setDraft(emptySite);
      setDrawer("add");
    }
  }

  async function scan() {
    setPlan(null);
    setChosen([]);
    setDrawerError("");
    setPending("scan");
    try {
      setPlan(await api<NginxScan>("/system/nginx/scan", { method: "POST" }));
      onChange();
    } catch (error) {
      setDrawerError((error as Error).message);
    } finally {
      setPending(null);
    }
  }

  const selectable = (row: NginxScan["sites"][number]) =>
    Boolean(row.siteId && row.status === "ready" && !row.added);

  async function apply() {
    if (!plan || chosen.length === 0) return;
    setDrawerError("");
    setPending("apply");
    try {
      setPlan(await api<NginxScan>("/system/nginx/apply", {
        method: "POST",
        body: JSON.stringify({ id: plan.id, siteIds: chosen }),
      }));
      setChosen([]);
      onChange();
    } catch (error) {
      setPlan(null);
      setDrawerError((error as Error).message);
    } finally {
      setPending(null);
    }
  }

  async function toggle(row: Row) {
    setPending(row.id);
    setError("");
    try {
      await api(`/system/sites/${encodeURIComponent(row.id)}`, {
        method: "PATCH",
        body: JSON.stringify({ enabled: !Boolean(row.enabled) }),
      });
      onChange();
    } catch (error) {
      setError((error as Error).message);
    } finally {
      setPending(null);
    }
  }

  async function save(event: React.FormEvent) {
    event.preventDefault();
    setDrawerError("");
    if (drawer === "add" && rows.some((row) => row.siteId === draft.id.trim())) {
      setDrawerError("此站点已添加，可在列表中编辑。");
      return;
    }
    setPending("save");
    try {
      await api("/system/sites", { method: "POST", body: JSON.stringify(draft) });
      setDrawer(null);
      onChange();
    } catch (error) {
      setDrawerError((error as Error).message);
    } finally {
      setPending(null);
    }
  }

  return (
    <section className="panel list-panel" aria-label="站点接入">
      <div className="panel-title site-management-toolbar">
        <button className="primary" disabled={pending !== null} onClick={() => openDrawer()}>
          <Plus size={16} aria-hidden="true" />添加站点
        </button>
      </div>
      <ErrorBox message={error} />
      <ErrorBox message={registered.error} />
        <Table rows={rows} loading={registered.loading} keepPage
          emptyLabel={registered.data !== null || !registered.loading ? "暂无站点" : ""}
          columns={[
          { key: "name", label: "站点", width: "18%" },
          { key: "source", label: "采集来源", width: "17%", render: (row) => [row.accessLog && "访问日志", row.browserOrigin && "浏览器事件"].filter(Boolean).join("、") || "已有采集" },
          { key: "last_event", label: "最近上报", width: "35%", render: (row) => row.enabled && !row.last_event ? "等待首批数据" : when(row.last_event) },
          {
            key: "enabled", label: "采集状态", width: "20%", render: (row) => (
              <button className="site-collection-switch" type="button" role="switch"
                aria-checked={Boolean(row.enabled)} aria-label={`${row.name} 采集状态`}
                aria-busy={pending === row.id} disabled={pending !== null}
                onClick={() => void toggle(row)}>
                <span aria-hidden="true" />
              </button>
            ),
          },
          {
            key: "edit", label: "操作", width: "10%", render: (row) => row.siteId ? (
              <button disabled={pending !== null} onClick={() => openDrawer(row)}>编辑</button>
            ) : null,
          },
        ]} />
      <dialog ref={dialog} className="site-drawer" aria-labelledby="site-drawer-title"
        onCancel={(event) => {
          event.preventDefault();
          if (pending === null) setDrawer(null);
        }}>
        {drawer && (
          <div className="site-drawer-layout">
            <header className="site-drawer-header">
              <h2 id="site-drawer-title">{drawer === "edit" ? "编辑站点" : "添加站点"}</h2>
              <button type="button" aria-label="关闭" className="site-drawer-close"
                disabled={pending !== null} onClick={() => setDrawer(null)}><X size={20} aria-hidden="true" /></button>
            </header>
            {drawer === "add" && (
              <div className="site-drawer-tabs" role="tablist" aria-label="添加方式"
                onKeyDown={(event) => {
                  if (pending !== null || !["ArrowLeft", "ArrowRight"].includes(event.key)) return;
                  event.preventDefault();
                  const next = tab === "manual" ? "scan" : "manual";
                  setTab(next);
                  setDrawerError("");
                  dialog.current?.querySelector<HTMLElement>(`#site-tab-${next}`)?.focus();
                }}>
                {([["manual", "手动添加"], ["scan", "一键扫描"]] as const).map(([key, label]) => (
                  <button type="button" key={key} id={`site-tab-${key}`} role="tab" aria-selected={tab === key}
                    aria-controls="site-drawer-panel" tabIndex={tab === key ? 0 : -1}
                    disabled={pending !== null} onClick={() => { setTab(key); setDrawerError(""); }}>
                    {label}
                  </button>
                ))}
              </div>
            )}
            <div id="site-drawer-panel" className="site-drawer-body"
              role={drawer === "add" ? "tabpanel" : undefined}
              aria-labelledby={drawer === "add" ? `site-tab-${tab}` : undefined}>
              <ErrorBox message={drawerError} />
              {tab === "manual" ? (
                <div className="form-panel">
                  <form id="site-drawer-form" onSubmit={save}>
                    <div className="form-grid">
                      <label>站点标识<input required disabled={drawer === "edit" || pending !== null} pattern={"[a-z0-9][a-z0-9._\\-]*"} maxLength={120} placeholder="example.com" value={draft.id} onChange={(e) => setDraft({ ...draft, id: e.target.value })} /></label>
                      <label>站点名称<input required maxLength={120} disabled={pending !== null} value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} /></label>
                    </div>
                    <label>访问日志路径<input placeholder="/path/to/access.log" disabled={pending !== null} value={draft.accessLog} onChange={(e) => setDraft({ ...draft, accessLog: e.target.value })} /></label>
                    {!!logPaths.data?.length && <Select label="已授权日志" ariaLabel="选择已授权的访问日志文件"
                      value={logPaths.data.includes(draft.accessLog) ? draft.accessLog : ""}
                      options={[{ value: "", label: "选择日志文件" }, ...logPaths.data.map((path) => ({ value: path, label: path }))]}
                      disabled={pending !== null}
                      onChange={(path) => setDraft({ ...draft, accessLog: path })} />}
                    <label>浏览器采集地址<input type="url" placeholder="https://example.com" disabled={pending !== null} value={draft.browserOrigin} onChange={(e) => setDraft({ ...draft, browserOrigin: e.target.value })} /></label>
                    <p className="site-drawer-hint">至少填写一个采集来源。浏览器采集需要站点已配置同源 SDK 路由。</p>
                    {draft.browserOrigin && <label className="checkbox"><input type="checkbox" checked={draft.requireConsent} disabled={pending !== null} onChange={(e) => setDraft({ ...draft, requireConsent: e.target.checked })} />等待站点同意信号</label>}
                  </form>
                </div>
              ) : (
                <>
                  <button type="button" className={plan ? "" : "primary"} disabled={pending !== null} onClick={() => void scan()}>
                    <Search size={16} aria-hidden="true" />
                    {pending === "scan" ? "扫描中…" : plan ? "重新扫描" : "扫描站点"}
                  </button>
                  {plan && (
                    <div className="site-scan-results" aria-live="polite">
                      {!plan.sites.length && <p className="site-drawer-hint">未扫描到站点</p>}
                      <ul aria-label="扫描结果">
                        {plan.sites.map((row, index) => {
                          const added = row.added || row.status === "configured" || row.status === "applied";
                          const id = row.siteId;
                          return (
                            <li key={id || `${row.file || row.site}-${index}`}>
                              <div className="site-scan-name">
                                {id && selectable(row) && <input type="checkbox" className="site-select"
                                  aria-label={`选择 ${row.site}`} checked={chosen.includes(id)} disabled={pending !== null}
                                  onChange={(event) => setChosen((ids) => event.target.checked ? [...ids, id] : ids.filter((selected) => selected !== id))} />}
                                <div>
                                  <strong>{row.origin || row.site}</strong>
                                  {row.reason && !added && <p className="site-drawer-hint">{row.reason}</p>}
                                </div>
                              </div>
                              <span className={added ? "site-scan-added" : "site-scan-status"}>
                                {added ? "已添加" : row.status === "ready" ? "可添加" : "已跳过"}
                              </span>
                            </li>
                          );
                        })}
                      </ul>
                      {plan.sites.some(selectable) && (
                        <div className="actions site-scan-actions">
                          <button type="button" className="primary" disabled={pending !== null || !chosen.length}
                            onClick={() => void apply()}>
                            {pending === "apply" ? "应用中…" : chosen.length ? `接入并启用 ${chosen.length} 个站点` : "接入并启用"}
                          </button>
                        </div>
                      )}
                    </div>
                  )}
                </>
              )}
            </div>
            {tab === "manual" && (
              <footer className="site-drawer-footer">
                <button type="button" disabled={pending !== null} onClick={() => setDrawer(null)}>取消</button>
                <button type="submit" form="site-drawer-form" className="primary"
                  disabled={pending !== null || (!draft.accessLog.trim() && !draft.browserOrigin.trim())}>
                  {pending === "save" ? "保存中…" : "保存站点"}
                </button>
              </footer>
            )}
          </div>
        )}
      </dialog>
    </section>
  );
}

function SystemInfo({ state, revision, onChange }: {
  state: ReturnType<typeof useData<Row | null>>;
  revision: number;
  onChange: () => void;
}) {
  const when = useTimeFormatter();
  const d = state.data;
  return (
    <>
      <AsyncPanel state={state}>
        {d && (
          <section className="panel system-info-panel">
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
              <dt>统计时区</dt>
              <dd><TimezoneSettings key={d.timezone} timezone={d.timezone} onChange={onChange} /></dd>
            </dl>
          </section>
        )}
      </AsyncPanel>
      <div className="split system-update-grid">
        <UpdatePackage />
        <section className="panel list-panel">
          <h2>更新日志</h2>
          <DataTable bare path="/system/update/history" query="?" revision={revision}
            emptyLabel="暂无更新记录" columns={[
              { key: "created_at", label: "更新时间", render: (row) => when(row.created_at) },
              { key: "action", label: "状态", render: (row) => {
                const status = updateHistoryStatuses[row.action];
                return status ? <span className={`badge ${status.tone}`}>{status.label}</span> : "—";
              } },
            ]} />
        </section>
      </div>
    </>
  );
}

const commonTimezones = [
  { value: "UTC", label: "UTC（协调世界时）" },
  { value: "Asia/Shanghai", label: "Asia/Shanghai（北京时间）" },
];

function TimezoneSettings({ timezone, onChange }: { timezone: string; onChange: () => void }) {
  const [selected, setSelected] = useState(timezone);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const values = Intl.supportedValuesOf?.("timeZone") ?? ["Asia/Tokyo", "Europe/London", "America/New_York"];
  const options = [
    ...commonTimezones,
    ...[...new Set([...values, timezone])]
      .filter((value) => !commonTimezones.some((option) => option.value === value))
      .map((value) => ({ value, label: value })),
  ];
  return (
    <>
      <form className="timezone-settings" onSubmit={async (event) => {
        event.preventDefault();
        if (busy || selected === timezone) return;
        setBusy(true);
        setError("");
        try {
          await api("/system/timezone", {
            method: "PATCH",
            body: JSON.stringify({ timezone: selected }),
          });
          onChange();
        } catch (error) {
          setError((error as Error).message);
        } finally {
          setBusy(false);
        }
      }}>
        <Select ariaLabel="统计时区" value={selected} options={options} onChange={setSelected} disabled={busy} />
        <button type="submit" className="primary" disabled={busy || selected === timezone}>
          {busy ? "保存中…" : "保存"}
        </button>
      </form>
      <ErrorBox message={error} />
    </>
  );
}

const updateHistoryStatuses: Record<string, { label: string; tone: string }> = {
  "system.update.started": { label: "开始更新", tone: "neutral" },
  "system.update.succeeded": { label: "更新成功", tone: "good" },
  "system.update.failed": { label: "更新失败", tone: "warn" },
  "system.update.rolled_back": { label: "已回退", tone: "neutral" },
};

function UpdatePackageDrop({ onChoose, onFiles }: {
  onChoose: () => void;
  onFiles: (files: FileList) => void;
}) {
  const [dragging, setDragging] = useState(false);
  return (
    <button type="button" className="update-package-drop" data-dragging={dragging} onClick={onChoose}
      onDragEnter={(event) => {
        if (event.dataTransfer.types.includes("Files")) setDragging(true);
      }}
      onDragOver={(event) => {
        if (!event.dataTransfer.types.includes("Files")) return;
        event.preventDefault();
        event.dataTransfer.dropEffect = "copy";
      }}
      onDragLeave={() => setDragging(false)}
      onDrop={(event) => {
        event.preventDefault();
        setDragging(false);
        if (event.dataTransfer.files.length) onFiles(event.dataTransfer.files);
      }}>
      <Upload size={22} aria-hidden="true" />
      <span>拖入或选择更新包</span>
      <small>.bin 文件</small>
    </button>
  );
}

async function readUpdatePackage(file: File) {
  if (!/^Sailfin-update-[a-f0-9]{32}\.bin$/i.test(file.name)) {
    throw new Error("请选择名为 Sailfin-update-<md5>.bin 的安装包。");
  }
  const bytes = new Uint8Array(await file.slice(0, 64 * 1024).arrayBuffer());
  const decoder = new TextDecoder();
  const prefix = decoder.decode(bytes.subarray(0, 512));
  const payloadLine = Number(/^payload_line='(\d+)'$/m.exec(prefix)?.[1]);
  if (!prefix.startsWith("#!/bin/bash\n") || !Number.isInteger(payloadLine) || payloadLine < 2 || payloadLine > bytes.length) {
    throw new Error("无法识别 Sailfin 安装包格式。");
  }
  let payload = 0;
  for (let line = 1; line < payloadLine; line++) {
    const newline = bytes.indexOf(10, payload);
    if (newline < 0) throw new Error("安装包头部不完整。");
    payload = newline + 1;
  }
  const header = decoder.decode(bytes.subarray(0, payload));
  if (!/^package_kind='update'$/m.test(header)) {
    throw new Error("此文件不是 Sailfin 更新包。");
  }
  const platform = /^platform='((?:linux|darwin)\/(?:amd64|arm64))'$/m.exec(header)?.[1];
  const packageName = /^package='(sailfin-[0-9][A-Za-z0-9._-]*)'$/m.exec(header)?.[1];
  const suffix = platform && `-${platform.replace("/", "-")}`;
  if (!suffix || !packageName?.endsWith(suffix) || bytes[payload] !== 0x1f || bytes[payload + 1] !== 0x8b) {
    throw new Error("安装包的平台信息或压缩内容无效。");
  }
  const version = packageName.slice("sailfin-".length, -suffix.length);
  if (!/^\d/.test(version)) throw new Error("无法识别安装包版本。");
  const notes = /^# sailfin-release-notes: ([A-Za-z0-9+/=]+)$/m.exec(header)?.[1];
  return {
    file, platform, version,
    notes: notes ? decoder.decode(Uint8Array.from(atob(notes), (character) => character.charCodeAt(0))).trim() : "",
  };
}

function UpdatePackage() {
  const drawer = useRef<HTMLDialogElement>(null);
  const title = useRef<HTMLHeadingElement>(null);
  const picker = useRef<HTMLInputElement>(null);
  const [password, setPassword] = useState("");
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [passwordError, setPasswordError] = useState("");
  const [fileError, setFileError] = useState("");
  const [preview, setPreview] = useState<Awaited<ReturnType<typeof readUpdatePackage>> | null>(null);

  useEffect(() => {
    if (confirming) title.current?.focus();
  }, [confirming]);

  async function selectPackage(files: FileList) {
    if (busy) return;
    if (!drawer.current?.open) drawer.current?.showModal();
    setPreview(null);
    setPassword("");
    setPasswordError("");
    setFileError("");
    setConfirming(false);
    setBusy(true);
    try {
      if (files.length !== 1) throw new Error("每次请选择一个更新包。");
      setPreview(await readUpdatePackage(files[0]));
    } catch (error) {
      setFileError((error as Error).message);
    } finally {
      setBusy(false);
    }
  }

  async function verify(event: React.FormEvent) {
    event.preventDefault();
    if (busy || !preview || !password) return;
    setBusy(true);
    setPasswordError("");
    try {
      await api("/system/update/verify-password", {
        method: "POST",
        body: JSON.stringify({ password }),
      });
      setPassword("");
      setConfirming(true);
    } catch (error) {
      setPasswordError((error as Error).message);
    } finally {
      setBusy(false);
    }
  }

  function reset() {
    setPassword("");
    setConfirming(false);
    setPasswordError("");
    setFileError("");
    setPreview(null);
  }

  return (
    <section className="panel">
      <h2>软件更新</h2>
      <input ref={picker} type="file" accept=".bin" hidden onChange={(event) => {
        if (event.currentTarget.files?.length) void selectPackage(event.currentTarget.files);
        event.currentTarget.value = "";
      }} />
      <UpdatePackageDrop onChoose={() => picker.current?.click()} onFiles={selectPackage} />
      <dialog ref={drawer} className="site-drawer" aria-labelledby="update-drawer-title" onClose={reset}
        onCancel={(event) => { if (busy) event.preventDefault(); }}>
        <div className="site-drawer-layout">
          <header className="site-drawer-header">
            <h2 ref={title} id="update-drawer-title" tabIndex={-1}>{confirming ? "确认安装" : "软件更新"}</h2>
            <button type="button" aria-label="关闭" className="site-drawer-close" disabled={busy}
              onClick={() => drawer.current?.close()}><X size={20} aria-hidden="true" /></button>
          </header>
          <div className="site-drawer-body">
            {preview ? (
              <>
                <dl className="facts">
                  <dt>文件名</dt><dd>{preview.file.name}</dd>
                  <dt>目标版本</dt><dd>{preview.version}</dd>
                  <dt>适用平台</dt><dd>{preview.platform}</dd>
                </dl>
                {confirming ? (
                  <>
                    <p className="notice update-package-message">
                      安装将由更新包执行，可能替换程序文件并自动重启 Sailfin。管理页面和采集服务可能暂时中断。
                    </p>
                    <p className="muted update-package-message">当前安装尚未接入更新包执行功能。</p>
                  </>
                ) : (
                  <>
                    <p className="notice update-package-message">
                      更新包会安排服务重启，期间管理页面和采集服务可能暂时中断。
                    </p>
                    <section aria-labelledby="update-release-notes">
                      <h3 id="update-release-notes">更新说明</h3>
                      <p className="update-package-notes">{preview.notes || "此包未附更新说明。"}</p>
                    </section>
                    <form id="update-password-form" onSubmit={verify}>
                      <label>当前管理员密码
                        <input type="password" autoComplete="current-password" required maxLength={256}
                          value={password} disabled={busy} onChange={(event) => setPassword(event.target.value)} />
                      </label>
                      <ErrorBox message={passwordError} />
                    </form>
                  </>
                )}
              </>
            ) : busy ? (
              <p className="update-package-message" role="status">正在检查更新包…</p>
            ) : (
              <>
                <ErrorBox message={fileError} />
                <UpdatePackageDrop onChoose={() => picker.current?.click()} onFiles={selectPackage} />
              </>
            )}
          </div>
          <footer className="site-drawer-footer">
            <button type="button" disabled={busy} onClick={() => drawer.current?.close()}>
              取消
            </button>
            {confirming ? <button type="button" className="primary" disabled>确认安装</button> : preview && (
              <button type="submit" form="update-password-form" className="primary" disabled={busy || !password}>
                {busy ? "验证中…" : "确认"}
              </button>
            )}
          </footer>
        </div>
      </dialog>
    </section>
  );
}

function SystemSettings() {
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <section className="panel">
      <h2>数据备份</h2>
      <p className="muted">备份包含账户与采集数据，请妥善保存。</p>
      <ErrorBox message={error} />
      <button disabled={busy} onClick={async () => {
        setBusy(true);
        setError("");
        try {
          await backup();
        } catch (error) {
          setError((error as Error).message);
        } finally {
          setBusy(false);
        }
      }}>
        <Download size={16} />
        {busy ? "生成备份中…" : "下载备份"}
      </button>
      <PasswordSettings />
    </section>
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
          新密码
          <input
            type="password"
            autoComplete="new-password"
            required
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
