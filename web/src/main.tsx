import { useCallback, useEffect, useRef, useState } from "react";
import { createRoot } from "react-dom/client";
import { api, APIError, connect } from "./api";
import {
  active,
  attention,
  badgeClass,
  currentWork,
  needsYou,
  ongoingWork,
  cleanLog,
  elapsed,
  filterFromSearch,
  selectJobs,
  prioritizeJobs,
  progressText,
  statusLabel,
  title,
  workTitle,
  waiting,
  type Filter,
  type Job,
} from "./model";
import { Icon, Modal } from "./components";
import { Markdown } from "./Markdown";
import "./style.css";

type Info = {
  execution_mode: string;
  mcp_enabled: boolean;
  started_at: string;
  readiness?: Readiness | null;
};
type Readiness = {
  state: string;
  label: string;
  detail: string;
  message?: string;
  checked_at: string;
  last_contact?: string;
  checks: { name: string; state: string; label: string; detail: string }[];
  setup_command?: string;
};
type Output = { output: string; truncated: boolean };
const filters: { key: Filter; label: string }[] = [
  { key: "all", label: "All work" },
  { key: "active", label: "In progress" },
  { key: "attention", label: "Needs attention" },
  { key: "done", label: "Completed" },
];
const date = (value: string) =>
  new Date(value).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  });
const jobPath = (id: string) => `/goals/${encodeURIComponent(id)}`;
function currentID() {
  const match = location.pathname.match(/^\/goals\/([^/]+)$/);
  return match ? decodeURIComponent(match[1]) : "";
}


function Mark() {
  return (
    <svg
      className="mark"
      viewBox="0 0 64 64"
      fill="currentColor"
      aria-hidden="true"
    >
      <circle cx="13" cy="31" r="9" />
      <circle cx="33" cy="31" r="9" />
      <circle cx="13" cy="51" r="9" />
      <circle cx="33" cy="51" r="9" />
      <circle cx="49" cy="15" r="9" />
    </svg>
  );
}
function Badge({ job }: { job: Job }) {
  return (
    <span
      className={`badge ${badgeClass(job)}`}
    >
      <span className="status-dot" />
      {statusLabel(job)}
    </span>
  );
}
function Copy({ value }: { value: string }) {
  const [label, setLabel] = useState("Copy");
  return (
    <button
      className="icon-button"
      aria-label={label}
      title={label}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(value);
          setLabel("Copied");
          setTimeout(() => setLabel("Copy"), 1800);
        } catch {
          setLabel("Select the text to copy");
        }
      }}
    >
      <Icon name={label === "Copied" ? "check" : "copy"} size={15} />
    </button>
  );
}
function StartHelp({ onClose, readiness, checking, error, connected, onCheck }: {
  onClose: () => void;
  readiness?: Readiness | null;
  checking: boolean;
  error: string;
  connected: boolean;
  onCheck: () => void;
}) {
  return (
    <Modal title="Work with Dot" onClose={onClose}>
      <section className="readiness-panel" aria-label="Connection readiness">
        <div className="readiness-heading"><h3>{!connected ? "Local service unavailable" : checking ? "Checking local access…" : error ? "Connection check needed" : readiness?.label || "Connection not checked"}</h3>
          <button className="secondary" disabled={checking || !connected} onClick={onCheck}>{checking ? "Checking…" : "Check again"}</button></div>
        <p>{!connected ? "Open Dotshift in the menu bar to check the connection. Your saved work stays on this computer." : error || readiness?.detail || "Checking the workspace, coding access, and Dot connection."}</p>
        {connected && readiness && !error && <>
          <ul className="readiness-checks">{readiness.checks.map(check => <li key={check.name}>
            <Icon name={check.state === "ready" ? "check" : check.state === "attention" ? "alert" : "clock"} size={16} />
            <details><summary>{check.name} <span>{check.label}</span></summary><p>{check.detail}</p></details>
          </li>)}</ul>
          <p className="checked-at">Checked {date(readiness.checked_at)}{readiness.last_contact && <> · Dot last reached this service {date(readiness.last_contact)}</>}</p>
          {readiness.message && <MessageForDot message={readiness.message} />}
        </>}
      </section>
      <p>Tell Dot what you want changed. Dot finds the local code, implements the change,
        runs tests and review, and applies the result when that is part of your request.</p>
      <div className="command-block">
        <p>“Use dotshift to add search to my app, test it, and apply the finished changes locally.”</p>
      </div>
      <p>Keep follow-ups in your conversation with Dot. This window is optional; closing it leaves local work running.</p>
      <details className="setup-help">
        <summary>First connection or reconnect</summary>
        <p>Ask Dot to check its dotshift connection and list your local repositories. If the integration needs connecting, run this on your computer for its connection instructions:</p>
        <div className="setup-command"><code>{readiness?.setup_command || "dotshift connect"}</code><Copy value={readiness?.setup_command || "dotshift connect"} /></div>
        <p>Complete any required sign-in, then ask Dot to check again. Checking here refreshes the status; it does not change your sign-in or restart work.</p>
      </details>
      <div className="dialog-actions">
        <button className="primary" onClick={onClose}>Got it</button>
      </div>
    </Modal>
  );
}

function MessageForDot({ message }: { message: string }) {
  const [copied, setCopied] = useState(false);
  const [fallback, setFallback] = useState(false);
  useEffect(() => { setCopied(false); setFallback(false); }, [message]);
  return <div className="message-for-dot">
    <button className="secondary" onClick={async () => {
      try { await navigator.clipboard.writeText(message); setCopied(true); }
      catch { setFallback(true); }
    }}>
      <Icon name={copied ? "check" : "copy"} size={15} />
      {copied ? "Copied — paste into Dot" : "Copy message for Dot"}
    </button>
    <span className="handoff-hint" role="status">{copied ? "The message includes this request’s context." : "Paste it into your conversation with Dot."}</span>
    {fallback && <textarea aria-label="Message for Dot" readOnly value={message} onFocus={e => e.target.select()} />}
  </div>;
}

function App() {
  const [id, setID] = useState(currentID);
  const [historyView, setHistoryView] = useState(() => location.pathname === "/history" || new URLSearchParams(location.search).has("filter"));
  const [jobs, setJobs] = useState<Job[]>([]);
  const [info, setInfo] = useState<Info | null>(null);
  const [error, setError] = useState("");
  const [connected, setConnected] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [filter, setFilter] = useState<Filter>(() => filterFromSearch(location.search));
  const [help, setHelp] = useState(false);
  const [checking, setChecking] = useState(false);
  const [readinessError, setReadinessError] = useState("");
  const checkReadiness = useCallback(async (force = false, signal?: AbortSignal) => {
    setChecking(true);
    setReadinessError("");
    try {
      const readiness = await api<Readiness>(force ? "/readiness/check" : "/readiness", {
        method: force ? "POST" : "GET", ...(force ? { body: "{}" } : {}), signal,
      });
      if (!signal?.aborted) setInfo(info => info ? { ...info, readiness } : info);
    } catch {
      if (!signal?.aborted) setReadinessError("Could not finish checking local access. Check again; your saved work is unchanged.");
    } finally {
      if (!signal?.aborted) setChecking(false);
    }
  }, []);
  useEffect(() => {
    if (!connected) { setChecking(false); return; }
    const controller = new AbortController();
    void checkReadiness(false, controller.signal);
    // The service caches healthy probes for a minute and retries unavailable
    // connections sooner, so a recovered tunnel does not leave a stale warning.
    const timer = setInterval(() => { void checkReadiness(false, controller.signal); }, 5000);
    return () => { controller.abort(); clearInterval(timer); };
  }, [connected, checkReadiness]);
  const [theme, setTheme] = useState(
    () => localStorage.getItem("dotshift-theme") || "light",
  );
  useEffect(() => {
    if (!id) document.title = historyView ? "dotshift · History" : "dotshift · Status";
  }, [id, historyView]);
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    localStorage.setItem("dotshift-theme", theme);
  }, [theme]);
  useEffect(() => {
    let stopped = false,
      timer: ReturnType<typeof setTimeout>;
    const controller = new AbortController();
    async function refresh() {
      try {
        const [nextInfo, nextJobs] = await Promise.all([
          api<Info>("/info", { signal: controller.signal }),
          api<Job[]>("/jobs", { signal: controller.signal }),
        ]);
        if (stopped) return;
        setInfo(nextInfo);
        setJobs(nextJobs);
        setError("");
        setConnected(true);
        setLoaded(true);
      } catch (e) {
        if (!stopped) {
          setConnected(false);
          setError(
            e instanceof APIError
              ? e.message
              : "The local service is unavailable. Open Dotshift in the menu bar to check the connection.",
          );
        }
      }
      if (!stopped) timer = setTimeout(refresh, document.hidden ? 10000 : 2000);
    }
    connect()
      .then(() => {
        if (!stopped) void refresh();
      })
      .catch((e) => {
        if (!stopped) setError(String(e.message || e));
      });
    return () => {
      stopped = true;
      clearTimeout(timer);
      controller.abort();
    };
  }, []);
  useEffect(() => {
    const listener = () => {
      setID(currentID());
      setHistoryView(location.pathname === "/history" || new URLSearchParams(location.search).has("filter"));
      setFilter(filterFromSearch(location.search));
    };
    window.addEventListener("popstate", listener);
    return () => window.removeEventListener("popstate", listener);
  }, []);
  function navigate(next: string) {
    const showHistory = next === "/history";
    const query = showHistory && filter !== "all" ? `?filter=${filter}` : "";
    history.pushState(null, "", showHistory ? "/history" + query : next ? jobPath(next) : "/");
    setID(showHistory ? "" : next);
    setHistoryView(showHistory);
    window.scrollTo(0, 0);
  }
  return (
    <div className="app-shell monitor">
      <main className="main">
        <header className="topbar">
          <a href="/" className="monitor-brand" onClick={(e) => { e.preventDefault(); navigate(""); }}>
            <img className="brand-logo" src={`/brand/dotshift-logo-${theme === "dark" ? "dark" : "light"}.svg`} width="167" height="32" alt="dotshift" />
          </a>
          <div className="monitor-tools">
            <button className="topbar-live readiness-button" onClick={() => setHelp(true)} aria-label="Connection and help">
              <span className={`connection-dot ${connected && !readinessError && info?.readiness?.state === "ready" ? "online" : ""}`} />
              {!connected ? error ? "Reconnect local service" : "Connecting…" : readinessError ? "Check connection" : info?.readiness?.label || "Checking local access…"}
              <Icon name="arrow" size={12} />
            </button>
            <button className="icon-button" onClick={() => setTheme(theme === "light" ? "dark" : "light")}
              aria-label={`Switch to ${theme === "light" ? "dark" : "light"} theme`}>
              <Icon name="sun" size={16} />
            </button>
          </div>
        </header>
        {connected && info?.readiness?.state === "attention" && (
          <div className="connection-error" role="status">
            <Icon name="alert" />
            <span>{info.readiness.label}. {info.readiness.detail}</span>
            <button className="text-button" onClick={() => setHelp(true)}>Connection help</button>
          </div>
        )}
        {error && (
          <div className="connection-error" role="alert">
            <Icon name="alert" />
            {error}
            {loaded && " Showing the last received state."}
          </div>
        )}
        {!loaded ? (
          <div className="loading-state">
            <Mark />
            <h2>
              {error
                ? "Connect to the local service"
                : "Connecting to the local service…"}
            </h2>
            <p>
              {error
                ? "Open Dotshift from the menu bar, or run dotshift ui, to check the connection."
                : "Loading your local work."}
            </p>
          </div>
        ) : id ? (
          <GoalDetail
            key={id}
            id={id}
            connected={connected}
            jobs={jobs}
            navigate={navigate}
            onBack={() => navigate("/history")}
          />
        ) : historyView ? (
          <Overview
            jobs={jobs}
            filter={filter}
            setFilter={setFilter}
            navigate={navigate}
            onHelp={() => setHelp(true)}
            ready={info?.readiness?.state === "ready"}
          />
        ) : (
          <StatusOverview jobs={jobs} readiness={info?.readiness} connected={connected}
            navigate={navigate} onHelp={() => setHelp(true)} />
        )}
        <footer className="page-footer">
          <span>
            <Icon name="branch" size={14} />
            You can close this window. Local work keeps running.
          </span>
          <span>On your computer</span>
        </footer>
      </main>
      {help && <StartHelp onClose={() => setHelp(false)} readiness={info?.readiness} checking={checking} error={readinessError}
        connected={connected} onCheck={() => void checkReadiness(true)} />}
    </div>
  );
}

function StatusOverview({ jobs, readiness, connected, navigate, onHelp }: {
  jobs: Job[];
  readiness?: Readiness | null;
  connected: boolean;
  navigate: (id: string) => void;
  onHelp: () => void;
}) {
  const input = currentWork(jobs).filter(needsYou);
  const work = ongoingWork(jobs);
  return <div className="page status-overview">
    <div className="page-heading"><div>
      <div className="eyebrow">ON THIS COMPUTER</div>
      <h1>{!connected ? "Connection unavailable" : readiness?.label || "Checking local access…"}</h1>
      <p>{connected && readiness?.state === "ready" ? "Ask Dot for a change. Coding, tests, and review happen here." : "Open Dotshift in the menu bar to check the connection."}</p>
    </div></div>
    {input.length > 0 && <section className="status-section" aria-label="Needs you">
      <h2>Needs you</h2>
      {input.map(job => <article className="status-request needs-input" key={job.id}>
        <h3>{workTitle(job)}</h3><p>{job.display?.detail || "Dot needs your input to continue."}</p>
        <p className="status-hint">Answer in your conversation with Dot.</p>
        <button className="text-button" onClick={() => navigate(job.id)}>Details</button>
      </article>)}
    </section>}
    <section className="status-section" aria-label="Current work">
      <h2>{work.length ? "Current work" : input.length ? "Work is waiting for your input." : "Nothing running."}</h2>
      {work.map(job => <article className="status-request" key={job.id}>
        <h3>{workTitle(job)}</h3>
        <p>{active(job) ? statusLabel(job) : "Waiting for Dot to continue"}</p>
        <button className="text-button" onClick={() => navigate(job.id)}>Details</button>
      </article>)}
      {input.length === 0 && <p className="status-hint">No work needs your input.</p>}
    </section>
    <p className="status-hint">Dotshift lives in your menu bar. Requests, results, and follow-ups stay with Dot.</p>
    <details className="status-troubleshooting"><summary>Troubleshooting</summary>
      <button className="secondary" onClick={() => navigate("/history")}>History and details</button>
      <button className="text-button" onClick={onHelp}>Connection details</button>
    </details>
  </div>;
}

function Overview({ jobs, filter, setFilter, navigate, onHelp, ready }: {
  jobs: Job[];
  filter: Filter;
  setFilter: (filter: Filter) => void;
  navigate: (id: string) => void;
  onHelp: () => void;
  ready: boolean;
}) {
  const [query, setQuery] = useState("");
  const shown = prioritizeJobs(selectJobs(jobs, filter, "", query));
  const requests = currentWork(jobs);
  const running = requests.filter(active).length;
  const needsAttention = requests.filter(attention).length;
  return (
    <div className="page overview">
      <button className="back-link" onClick={() => navigate("")}><Icon name="back" size={16} /> Back to status</button>
      <div className="page-heading">
        <div>
          <div className="eyebrow">TROUBLESHOOTING</div>
          <h1>History and details</h1>
          <p>Saved requests, test results, and logs. Continue the work in your conversation with Dot.</p>
        </div>
      </div>
      <section className="work-section" aria-label="Local work">
        {jobs.length > 0 && <div className="work-toolbar">
          <div className="filters" aria-label="Filter work">
            {filters.map((item) => (
              <button key={item.key} aria-pressed={filter === item.key}
                className={filter === item.key ? "current" : ""} onClick={() => {
                  setFilter(item.key);
                  history.replaceState(null, "", item.key === "all" ? "/history" : `/history?filter=${item.key}`);
                }}>
                {item.label}
                {item.key === "active" && running > 0 ? ` (${running})` : ""}
                {item.key === "attention" && needsAttention > 0 ? ` (${needsAttention})` : ""}
              </button>
            ))}
          </div>
          <label className="search">
            <Icon name="search" size={16} />
            <input aria-label="Search work" placeholder="Find work…" value={query} onChange={(e) => setQuery(e.target.value)} />
            {query && <button className="icon-button" onClick={() => setQuery("")} aria-label="Clear search"><Icon name="close" size={14} /></button>}
          </label>
        </div>}
        {shown.length ? (
          <div className="job-table">
            {shown.map((job) => (
              <a className="job-row" key={job.id} href={jobPath(job.id)} onClick={(e) => {
                if (!e.metaKey && !e.ctrlKey) { e.preventDefault(); navigate(job.id); }
              }}>
                <div className="job-title">
                  <span className={`job-glyph ${job.status}`}>
                    <Icon name={attention(job) ? "alert" : job.status === "done" ? "check" : active(job) ? "activity" : "stop"} size={18} />
                  </span>
                  <div>
                    <strong>{workTitle(job)}</strong>
                    <div className="job-subtitle"><span>{job.repo}</span><span>·</span><span>{progressText(job)}</span></div>
                  </div>
                </div>
                <Badge job={job} />
                <span className="created-cell">{date(job.created_at)}</span>
                <Icon name="arrow" size={16} />
              </a>
            ))}
          </div>
        ) : (
          <div className="empty-state">
            <div className="empty-icon"><Icon name={jobs.length ? "search" : "terminal"} size={28} /></div>
            <h3>{jobs.length ? "No work in this view" : ready ? "Your next idea starts with Dot." : "Connect once. Then ask Dot."}</h3>
            <p>{jobs.length ? "Choose another view or search to find your work." : ready ? "Ask Dot to change a local app or build something new. Progress and results will appear here." : "Check the connection, then tell Dot what to build. Dot handles the code, tests, and follow-ups."}</p>
            {jobs.length ? <button className="secondary" onClick={() => { setQuery(""); setFilter("all"); history.replaceState(null, "", "/history"); }}>Show all work</button>
              : <button className="secondary" onClick={onHelp}>{ready ? "How to work with Dot" : "Check connection"} <Icon name="arrow" size={16} /></button>}
          </div>
        )}
      </section>
    </div>
  );
}

function GoalDetail({
  id,
  connected,
  onBack,
  jobs,
  navigate,
}: {
  id: string;
  connected: boolean;
  onBack: () => void;
  jobs: Job[];
  navigate: (id: string) => void;
}) {
  const [job, setJob] = useState<Job | null>(null);
  const [output, setOutput] = useState<Output>({
    output: "",
    truncated: false,
  });
  const [tab, setTab] = useState("changes");
  const [showOutput, setShowOutput] = useState(false);
  const [error, setError] = useState("");
  const [outputError, setOutputError] = useState("");
  const [dialog, setDialog] = useState<"cancel" | null>(null);
  const [pending, setPending] = useState(false);
  const [actionError, setActionError] = useState("");
  const [revision, setRevision] = useState(0);
  const [follow, setFollow] = useState(true);
  const logRef = useRef<HTMLPreElement>(null);
  useEffect(() => {
    let stopped = false,
      timer: ReturnType<typeof setTimeout>;
    const controller = new AbortController();
    async function refresh() {
      try {
        const next = await api<Job>(`/jobs/${encodeURIComponent(id)}`, {
          signal: controller.signal,
        });
        if (!stopped) {
          setJob(next);
          setError("");
          document.title = `dotshift · ${!next.current_id || next.current_id === next.id ? workTitle(next) : title(next)}`;
        }
      } catch (e) {
        if (!stopped)
          setError(
            e instanceof APIError
              ? e.message
              : "Could not refresh this job. Check the service connection.",
          );
      }
      if (!stopped) timer = setTimeout(refresh, document.hidden ? 10000 : 2000);
    }
    void refresh();
    return () => {
      stopped = true;
      clearTimeout(timer);
      controller.abort();
    };
  }, [id, revision]);
  useEffect(() => {
    setOutput({ output: "", truncated: false });
    setOutputError("");
    if (!showOutput || tab === "tests") return;
    let stopped = false,
      timer: ReturnType<typeof setTimeout>;
    const controller = new AbortController();
    async function refresh() {
      try {
        const next = await api<Output>(
          `/jobs/${encodeURIComponent(id)}/${tab === "changes" ? "diff" : "logs"}`,
          { signal: controller.signal },
        );
        if (!stopped) {
          setOutput(next);
          setOutputError("");
        }
      } catch (e) {
        if (!stopped)
          setOutputError(
            e instanceof APIError ? e.message : "Could not load the output.",
          );
      }
      if (!stopped)
        timer = setTimeout(refresh, tab === "changes" ? 5000 : 2000);
    }
    void refresh();
    return () => {
      stopped = true;
      clearTimeout(timer);
      controller.abort();
    };
  }, [id, tab, revision, showOutput]);
  useEffect(() => {
    if (follow && tab === "activity" && logRef.current)
      logRef.current.scrollTop = logRef.current.scrollHeight;
  }, [output, follow, tab]);
  async function action(body: object = {}) {
    if (!dialog || pending) return;
    setPending(true);
    setActionError("");
    try {
      await api(`/jobs/${encodeURIComponent(id)}/${dialog}`, {
        method: "POST",
        body: JSON.stringify(body),
      });
      setDialog(null);
      setRevision((v) => v + 1);
    } catch (e) {
      setActionError(
        e instanceof Error ? e.message : "The action failed. Please try again.",
      );
    } finally {
      setPending(false);
    }
  }
  async function setAttentionDismissed(dismissed: boolean) {
    if (!job || pending) return;
    setPending(true);
    setActionError("");
    try {
      const updated = await api<Job>(`/jobs/${encodeURIComponent(id)}/attention`, {
        method: "POST",
        body: JSON.stringify({ version: job.attention_version, dismissed }),
      });
      setJob(updated);
    } catch (e) {
      setActionError(
        e instanceof Error ? e.message : "Could not update attention.",
      );
    } finally {
      setRevision((v) => v + 1);
      setPending(false);
    }
  }
  if (!job)
    return (
      <div className="page">
        <button className="text-button" onClick={onBack}>
          <Icon name="back" />
          Back to overview
        </button>
        <div className="empty-state">
          <h2>{error || "Loading job…"}</h2>
        </div>
      </div>
    );
  const wait = waiting(job);
  const selection = job.goal?.phase === "reviewing"
    ? job.goal.reviewer_selection || job.goal.model_selection
    : job.goal?.model_selection;
  const historical = !!job.current_id && job.current_id !== job.id;
  const historyItems = jobs.filter(item => item.work_id && item.work_id === job.work_id);
  return (
    <div className="page detail">
      <button className="back-link" onClick={onBack}>
        <Icon name="back" size={16} />
        Back to history
      </button>
      <div className="detail-heading">
        <div>
          <div className="eyebrow">
            {job.repo}
          </div>
          <h1>{historical ? title(job) : workTitle(job)}</h1>
          <div className="detail-subheading">
            <Badge job={job} />
            <span>
              <Icon name="clock" size={15} />
              {elapsed(job)}
            </span>
            <span>{job.started_at ? "Started" : "Requested"} {date(job.started_at || job.created_at)}</span>
          </div>
        </div>
        <div className="detail-actions">
          {active(job) && (
            <button
              className="secondary"
              disabled={pending || !connected}
              onClick={() => {
                setActionError("");
                setDialog("cancel");
              }}
            >
              <Icon name="stop" size={16} />
              Stop work
            </button>
          )}

        </div>
      </div>
      {error && (
        <div className="notice warning" role="alert">
          {error}
        </div>
      )}
      {actionError && !dialog && (
        <div className="notice warning" role="alert">
          {actionError}
        </div>
      )}
      {job.attention_dismissed && (
        <div className="notice" role="status">
          <Icon name="check" />
          <div>
            <strong>Dismissed from attention</strong>
            <p>
              This job stays in All work with its files and history. A new issue
              after resuming will need attention again.
            </p>
          </div>
        </div>
      )}
      {historical && <div className="notice history-notice">
        <Icon name="clock" />
        <div><strong>{job.resolved_by ? "Resolved by later work" : "Earlier work on this request"}</strong>
          <p>{job.resolved_by ? "A later linked result passed validation. This earlier check is kept in history." : "Open the current request for its latest result and next step."}</p>
          <button className="text-button" onClick={() => navigate(job.current_id!)}>View current request <Icon name="arrow" size={14} /></button>
        </div>
      </div>}
      {job.display && <section className={`outcome-card ${attention(job) && !historical ? "needs-attention" : ""}`} aria-label="Work status">
        <div><h2>{statusLabel(job)}</h2><p>{job.display.detail}</p>
          {wait && active(job) && <p>Next retry {date(wait)}</p>}
        </div>
        {!historical && job.display.message && <MessageForDot message={job.display.message} />}
        {job.error && <details className="issue-details"><summary>What happened</summary><p>{job.error}</p></details>}
      </section>}
      <div className="detail-grid">
        <div className="detail-content">
          {(job.summary || job.goal?.report.summary) && <section className="panel assessment" aria-label="Work result">
            <div className="panel-heading"><h2>{job.status === "done"
              ? job.test_result === "failed" ? "Saved findings" : "Result"
              : "Progress so far"}</h2></div>
            <Markdown>{job.summary || job.goal?.report.summary || ""}</Markdown>
            {job.goal?.report.next_steps && job.status !== "done" && !historical && job.goal.report.next_steps !== job.display?.detail && (
              <div className="next-steps"><strong>{attention(job) ? "What is needed" : "Dot’s next step"}</strong><Markdown>{job.goal.report.next_steps}</Markdown></div>
            )}
          </section>}
          <details className="panel output-details" onToggle={e => setShowOutput(e.currentTarget.open)}>
            <summary>Changes, tests, and logs</summary>
          <div className="detail-tabs" role="tablist" aria-label="Job output">
            {[
              ["changes", "Changes", "branch"],
              ["tests", "Tests", "check"],
              ["activity", "Activity", "terminal"],
            ].map(([key, label, icon]) => (
              <button
                key={key}
                id={`tab-${key}`}
                role="tab"
                aria-selected={tab === key}
                aria-controls="output-panel"
                tabIndex={tab === key ? 0 : -1}
                onClick={() => setTab(key)}
                onKeyDown={(e) => {
                  const tabs = ["changes", "tests", "activity"];
                  let next = tab;
                  if (e.key === "ArrowRight")
                    next = tabs[(tabs.indexOf(tab) + 1) % 3];
                  else if (e.key === "ArrowLeft")
                    next = tabs[(tabs.indexOf(tab) + 2) % 3];
                  else if (e.key === "Home") next = tabs[0];
                  else if (e.key === "End") next = tabs[2];
                  else return;
                  e.preventDefault();
                  setTab(next);
                  document.getElementById(`tab-${next}`)?.focus();
                }}
              >
                <Icon name={icon} size={16} />
                {label}
                {key === "changes" && job.commits > 0 && (
                  <span>{job.commits}</span>
                )}
                {key === "tests" && job.test_result && (
                  <i className={`test-dot ${job.test_result}`} />
                )}
              </button>
            ))}
          </div>
          <div
            id="output-panel"
            role="tabpanel"
            aria-labelledby={`tab-${tab}`}
            tabIndex={0}
          >
            {tab === "activity" && (
              <>
                <div className="execution-details">
                  <div className="log-panel">
                  <div className="log-heading">
                    <span>
                      <Icon name="terminal" size={16} />
                      Agent output
                    </span>
                    <label>
                      <input
                        type="checkbox"
                        checked={follow}
                        onChange={(e) => setFollow(e.target.checked)}
                      />
                      Follow output
                    </label>
                  </div>
                  {outputError ? (
                    <p className="output-error" role="alert">
                      {outputError}
                    </p>
                  ) : (
                    <pre ref={logRef}>
                      {cleanLog(output.output) ||
                        "No agent output yet. Output appears here when the agent starts."}
                    </pre>
                  )}
                  <div className="log-footer">
                    <span>
                      {output.truncated
                        ? "Showing the most recent 64 KiB"
                        : "Recorded agent output"}
                    </span>
                    <span>
                      {active(job)
                        ? "Refreshing every 2s"
                        : "Work preserved locally"}
                    </span>
                  </div>
                  </div>
                </div>
              </>
            )}
            {tab === "changes" && (
              <section className="panel changes">
                <div className="panel-heading">
                  <h3>Branch changes</h3>
                  <span className="micro-label">
                    {job.commits} {job.commits === 1 ? "COMMIT" : "COMMITS"}
                  </span>
                </div>
                <p className="panel-description">
                  Committed changes from the starting commit to this job’s
                  branch.
                </p>
                {job.diffstat && <pre className="diffstat">{job.diffstat}</pre>}
                {outputError ? (
                  <div className="notice warning" role="alert">
                    {outputError}
                  </div>
                ) : output.output ? (
                  <pre className="diff-code">
                    {cleanLog(output.output)
                      .split("\n")
                      .map((line, i) => (
                        <span
                          key={i}
                          className={
                            line.startsWith("+") && !line.startsWith("+++")
                              ? "added"
                              : line.startsWith("-") && !line.startsWith("---")
                                ? "removed"
                                : line.startsWith("@@")
                                  ? "hunk"
                                  : ""
                          }
                        >
                          {line}
                          {"\n"}
                        </span>
                      ))}
                  </pre>
                ) : (
                  <div className="inline-empty">
                    <Icon name="branch" size={25} />
                    <p>
                      {active(job)
                        ? "Changes appear as work is saved."
                        : "No committed changes to display."}
                    </p>
                  </div>
                )}
                {output.truncated && (
                  <p className="panel-description">
                    Diff limited to 256 KiB. Inspect the branch locally for the
                    full patch.
                  </p>
                )}
              </section>
            )}
            {tab === "tests" && (
              <section className="panel tests">
                <div className="panel-heading">
                  <h3>Test results</h3>
                  <span className={`test-result ${job.test_result}`}>
                    {job.test_result || "Not run"}
                  </span>
                </div>
                {job.goal?.test_command && (
                  <div className="command-block">
                    <code>{job.goal.test_command}</code>
                    <Copy value={job.goal.test_command} />
                  </div>
                )}
                {job.goal?.tested_commit && (
                  <p className="panel-description">
                    Last tested commit{" "}
                    <code>{job.goal.tested_commit.slice(0, 12)}</code>
                  </p>
                )}
                {job.test_output ? (
                  <pre className="test-output">{cleanLog(job.test_output)}</pre>
                ) : (
                  <div className="inline-empty">
                    <Icon name="check" size={25} />
                    <p>
                      {job.test_result === "passed"
                        ? "The configured tests passed. No test output was recorded."
                        : job.test_result === "failed"
                          ? "The configured tests failed. No test output was recorded."
                          : "Test results appear after the configured test command runs."}
                    </p>
                  </div>
                )}
              </section>
            )}
          </div>
          </details>
          {historyItems.length > 1 && <details className="panel request-history">
            <summary>Request history ({historyItems.length})</summary>
            <p className="panel-description">Related work stays together. Dot uses this history to continue your request.</p>
            <ol>{[...historyItems].sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at)).map(item => <li key={item.id}>
              <a href={jobPath(item.id)} aria-current={item.id === job.id ? "page" : undefined} onClick={e => {
                if (!e.metaKey && !e.ctrlKey) { e.preventDefault(); navigate(item.id); }
              }}><span>{title(item)}</span><small>{item.resolved_by ? "Resolved in later work" : statusLabel(item)} · {date(item.created_at)}</small></a>
            </li>)}</ol>
          </details>}
          <details className="panel objective request-instructions">
            <summary>Original instructions</summary>
            <p>{job.prompt}</p>
            {job.goal?.additional_instructions && <details><summary>Additional instructions</summary><p>{job.goal.additional_instructions}</p></details>}
          </details>
        </div>
        <aside className="detail-aside">
          <details className="panel technical-details">
            <summary>Technical details</summary>
            {!historical && (attention(job) || job.attention_dismissed) && <button
              className="text-button attention-dismiss"
              disabled={pending || !connected}
              onClick={() => void setAttentionDismissed(!job.attention_dismissed)}>
              {job.attention_dismissed ? "Show this notice again" : "Dismiss this notice"}
            </button>}
            <dl>
              <dt>Task reference</dt>
              <dd><code>{job.id}</code><Copy value={job.id} /></dd>
              <dt>Repository</dt>
              <dd>
                <Icon name="folder" size={14} />
                {job.repo}
              </dd>
              <dt>Working branch</dt>
              <dd className="branch-value">
                <code>{job.branch || "No branch"}</code>
                {job.branch && <Copy value={job.branch} />}
              </dd>
              <dt>Implementation model</dt>
              <dd>{job.goal?.model_selection?.active_model || job.model}</dd>
              {job.goal?.model_selection && (
                <>
                  <dt>Strategy</dt>
                  <dd>
                    {job.goal.model_selection.strategy.replaceAll("_", " ")}
                  </dd>
                  <dt>Model switches</dt>
                  <dd>{job.goal.model_selection.switches}</dd>
                </>
              )}
              {job.goal?.reviewer_model && (
                <>
                  <dt>Reviewer</dt>
                  <dd>
                    {job.goal.reviewer_selection?.active_model ||
                      job.goal.reviewer_model}
                  </dd>
                </>
              )}
              <dt>Execution</dt>
              <dd>
                {job.execution_mode === "subscription" ? "Subscription" : "API"}{" "}
                mode
              </dd>
              <dt>Created</dt>
              <dd>{date(job.created_at)}</dd>
              {job.finished_at && (
                <>
                  <dt>Finished</dt>
                  <dd>{date(job.finished_at)}</dd>
                </>
              )}
            </dl>

          {selection?.reason && (
            <section className="model-note">
              <Icon name="activity" size={16} />
              <div>
                <h3>Model selection</h3>
                <p>{selection.reason}</p>
                {selection.last_failure && (
                  <details>
                    <summary>Last model issue</summary>
                    <p>{selection.last_failure}</p>
                  </details>
                )}
              </div>
            </section>
          )}
          {job.goal?.budget && (
            <section className="panel budget">
              <h3>Reported API cost</h3>
              <strong>${job.goal.budget.spent_usd.toFixed(4)}</strong>
              <p>
                {job.goal.budget.limit_usd > 0
                  ? `$${job.goal.budget.limit_usd.toFixed(2)} goal cap`
                  : "No spending cap"}
                {job.goal.budget.pending_requests > 0 &&
                  ` · ${job.goal.budget.pending_requests} pending receipts`}
              </p>
            </section>
          )}
            <p className="panel-description">Coding stays on a separate branch. Dot can apply the tested result when you ask.</p>
          </details>
        </aside>
      </div>
      {dialog === "cancel" && (
        <Modal
          title="Stop this work?"
          onClose={() => {
            if (!pending) setDialog(null);
          }}
        >
          <p>
            Work will stop and your progress will be saved. Ask Dot to continue whenever you are ready.
          </p>
          {actionError && (
            <div className="notice warning" role="alert">
              {actionError}
            </div>
          )}
          <div className="dialog-actions">
            <button
              className="secondary"
              disabled={pending}
              onClick={() => setDialog(null)}
            >
              Keep running
            </button>
            <button
              className="danger"
              disabled={pending}
              onClick={() => void action()}
            >
              {pending ? "Cancelling…" : "Cancel work"}
            </button>
          </div>
        </Modal>
      )}

    </div>
  );
}

createRoot(document.getElementById("root")!).render(<App />);
