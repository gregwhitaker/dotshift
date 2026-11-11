import { useEffect, useRef, useState, type FormEvent } from "react";
import { createRoot } from "react-dom/client";
import { api, APIError, connect } from "./api";
import {
  active,
  attention,
  cleanLog,
  elapsed,
  filterFromSearch,
  modelLabel,
  resumable,
  selectJobs,
  statusLabel,
  title,
  waiting,
  type Filter,
  type Job,
} from "./model";
import { Icon, Modal } from "./components";
import { Models } from "./models";
import "./style.css";

type Info = {
  execution_mode: string;
  mcp_enabled: boolean;
  started_at: string;
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

const isModels = () => location.pathname === "/models";

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
      className={`badge ${job.status === "done" && job.test_result === "failed" ? "failed" : job.status}`}
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
function StartHelp({ onClose }: { onClose: () => void }) {
  return (
    <Modal title="Start work with Dot" onClose={onClose}>
      <p>
        Ask Dot to use dotshift on a local project. Include what to change,
        how to check it, and any model preference. The job will appear here.
      </p>
      <div className="guide-step">
        <span>01</span>
        <div>
          <h3>Connect Dot once</h3>
          <p>
            Run <code>dotshift connect</code> for your connection details. Dot
            can list your allowed repositories, create projects inside your
            configured folder, and discover your model choices.
          </p>
        </div>
      </div>
      <div className="guide-step">
        <span>02</span>
        <div>
          <h3>Give Dot the coding job</h3>
          <p>
            “Use dotshift to add search to my app, test it, and review it.
            Use OpenAI first and continue with Claude if OpenAI hits its limit.”
          </p>
          <p>You can also start a job from the terminal:</p>
          <div className="command-block">
            <code>dotshift run -repo REPO "Describe the work"</code>
            <Copy value={'dotshift run -repo REPO "Describe the work"'} />
          </div>
          <p>
            Every new job includes fallback, tests, and a separate review.
            This monitor shows progress and lets you cancel or resume work.
          </p>
        </div>
      </div>
      <div className="dialog-actions">
        <button className="primary" onClick={onClose}>
          Got it <Icon name="check" size={16} />
        </button>
      </div>
    </Modal>
  );
}

function App() {
  const [id, setID] = useState(currentID);
  const [models, setModels] = useState(isModels);
  const [jobs, setJobs] = useState<Job[]>([]);
  const [info, setInfo] = useState<Info | null>(null);
  const [error, setError] = useState("");
  const [connected, setConnected] = useState(false);
  const [loaded, setLoaded] = useState(false);
  const [filter, setFilter] = useState<Filter>(() => filterFromSearch(location.search));
  const [repo, setRepo] = useState("");
  const [help, setHelp] = useState(false);
  const [theme, setTheme] = useState(
    () => localStorage.getItem("dotshift-theme") || "light",
  );
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
              : "The local service is unavailable. Run dotshift ui to reconnect.",
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
      setModels(isModels());
      setFilter(filterFromSearch(location.search));
    };
    window.addEventListener("popstate", listener);
    return () => window.removeEventListener("popstate", listener);
  }, []);
  function navigate(next: string) {
    history.pushState(null, "", next ? jobPath(next) : "/");
    setID(next);
    setModels(false);
    window.scrollTo(0, 0);
  }
  function showModels() {
    history.pushState(null, "", "/models");
    setID("");
    setModels(true);
    setRepo("");
    window.scrollTo(0, 0);
  }
  const repos = [...new Set(jobs.map((job) => job.repo))].sort();
  return (
    <div className="app-shell">
      <aside className="sidebar">
        <a
          className="brand"
          href="/"
          onClick={(e) => {
            e.preventDefault();
            navigate("");
          }}
        >
          <img className="brand-logo" src="/brand/dotshift-logo-dark.svg" width="167" height="32" alt="dotshift" />
        </a>
        <div className="workspace-caption">LOCAL CODING & REVIEW</div>
        <button
          className={`nav-item ${!id && !repo && !models ? "selected" : ""}`}
          onClick={() => {
            setRepo("");
            navigate("");
          }}
        >
          <Icon name="grid" />
          Work<span className="nav-count">{jobs.length}</span>
        </button>
        <button
          className={`nav-item ${models ? "selected" : ""}`}
          onClick={showModels}
        >
          <Icon name="activity" />
          Models
        </button>
        <div className="sidebar-section">
          <span>REPOSITORIES</span>
          <span>{repos.length.toString().padStart(2, "0")}</span>
        </div>
        <div className="repo-list">
          {repos.map((name) => (
            <button
              className={`repo-link ${repo === name && !models ? "selected" : ""}`}
              key={name}
              onClick={() => {
                setRepo(name);
                navigate("");
              }}
              aria-pressed={repo === name}
            >
              <Icon name="folder" size={16} />
              <span>{name}</span>
              {jobs.some((job) => job.repo === name && active(job)) && (
                <i className="tiny-live" />
              )}
            </button>
          ))}
          {!repos.length && (
            <p className="sidebar-note">
              Repositories appear here as work starts.
            </p>
          )}
        </div>
        <div className="sidebar-bottom">
          <div className="local-card">
            <span className={`connection-dot ${connected ? "online" : ""}`} />
            <div>
              <strong>
                {connected ? "Service connected" : "Connecting to service"}
              </strong>
              <small>
                {info
                  ? `${info.execution_mode === "subscription" ? "Subscription" : "API"} mode`
                  : "On your machine"}
              </small>
            </div>
          </div>
          <button className="sidebar-help" onClick={() => setHelp(true)}>
            <Icon name="terminal" size={16} />
            Start work with Dot
            <Icon name="arrow" size={14} />
          </button>
          <div className="sidebar-footer">
            <span>YOUR MACHINE. YOUR CODE.</span>
            <button
              className="icon-button"
              onClick={() => setTheme(theme === "light" ? "dark" : "light")}
              aria-label={`Switch to ${theme === "light" ? "dark" : "light"} theme`}
              title="Toggle theme"
            >
              <Icon name="sun" size={16} />
            </button>
          </div>
        </div>
      </aside>
      <main className="main">
        <header className="topbar">
          <span className="breadcrumbs">
            Local runner <span>/</span>{" "}
            <strong>
              {id ? "Job detail" : models ? "Models" : "Work"}
            </strong>
          </span>
          <span className="topbar-live">
            <span className={`connection-dot ${connected ? "online" : ""}`} />
            {connected ? "Live updates" : "Disconnected"}
            <span className="local-pill">LOCAL</span>
          </span>
        </header>
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
                ? "Open the authenticated link printed by dotshift ui."
                : "Loading your coding jobs."}
            </p>
          </div>
        ) : models ? (
          <Models />
        ) : id ? (
          <GoalDetail
            key={id}
            id={id}
            connected={connected}
            onBack={() => navigate("")}
          />
        ) : (
          <Overview
            jobs={jobs}
            filter={filter}
            setFilter={setFilter}
            repo={repo}
            clearRepo={() => setRepo("")}
            navigate={navigate}
            onHelp={() => setHelp(true)}
          />
        )}
        <footer className="page-footer">
          <span>
            <Icon name="branch" size={14} />
            Work happens on separate branches.
          </span>
          <span>dotshift / local runner</span>
        </footer>
      </main>
      {help && <StartHelp onClose={() => setHelp(false)} />}
    </div>
  );
}

function Overview({
  jobs,
  filter,
  setFilter,
  repo,
  clearRepo,
  navigate,
  onHelp,
}: {
  jobs: Job[];
  filter: Filter;
  setFilter: (v: Filter) => void;
  repo: string;
  clearRepo: () => void;
  navigate: (id: string) => void;
  onHelp: () => void;
}) {
  const [query, setQuery] = useState("");
  useEffect(() => {
    document.title = "dotshift · Work";
  }, []);
  const scoped = jobs.filter((job) => !repo || job.repo === repo);
  const shown = selectJobs(jobs, filter, repo, query);
  const countActive = scoped.filter(active).length,
    countAttention = scoped.filter(attention).length;
  return (
    <div className="page overview">
      <div className="page-heading">
        <div>
          <h1>{repo || "Local coding jobs"}</h1>
          <p>
            {countActive
              ? `${countActive} ${countActive === 1 ? "job is" : "jobs are"} running. `
              : "No jobs running. "}
            {countAttention
              ? `${countAttention} ${countAttention === 1 ? "needs" : "need"} your attention.`
              : "Follow the progress. Review the results."}
          </p>
        </div>
        <button className="secondary" onClick={onHelp}>
          <Icon name="terminal" size={17} />
          Start with Dot
        </button>
      </div>
      <section className="work-section" aria-label="Jobs and goals">
        <div className="section-title">
          <div>
            <h2>
              {repo ? "Repository work" : "All your work"}
              <span>{scoped.length}</span>
            </h2>
            {repo && (
              <button className="text-button" onClick={clearRepo}>
                Show all repositories
              </button>
            )}
          </div>
          <span className="section-note">
            Newest first · updates every 2 seconds
          </span>
        </div>
        <div className="work-toolbar">
          <div className="filters" aria-label="Filter jobs">
            {filters.map((item) => (
              <button
                key={item.key}
                aria-pressed={filter === item.key}
                className={filter === item.key ? "current" : ""}
                onClick={() => setFilter(item.key)}
              >
                {item.label}
              </button>
            ))}
          </div>
          <label className="search">
            <Icon name="search" size={16} />
            <input
              aria-label="Search jobs"
              placeholder="Search work…"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
            {query && (
              <button
                className="icon-button"
                onClick={() => setQuery("")}
                aria-label="Clear search"
              >
                <Icon name="close" size={14} />
              </button>
            )}
          </label>
        </div>
        {shown.length ? (
          <div className="job-table">
            <div className="table-head">
              <span>JOB</span>
              <span>STATUS</span>
              <span>MODEL</span>
              <span>CREATED</span>
              <span />
            </div>
            {shown.map((job) => (
              <a
                className="job-row"
                key={job.id}
                href={jobPath(job.id)}
                onClick={(e) => {
                  if (!e.metaKey && !e.ctrlKey) {
                    e.preventDefault();
                    navigate(job.id);
                  }
                }}
              >
                <div className="job-title">
                  <span className={`job-glyph ${job.status}`}>
                    <Icon
                      name={
                        attention(job)
                          ? "alert"
                          : job.status === "done"
                            ? "check"
                            : active(job)
                              ? "activity"
                              : "stop"
                      }
                      size={18}
                    />
                  </span>
                  <div>
                    <strong>{title(job)}</strong>
                    <div className="job-subtitle">
                      <span>{job.repo}</span>
                      <span>·</span>
                      <span>
                        {job.goal
                          ? `${job.goal.iterations} ${job.goal.iterations === 1 ? "iteration" : "iterations"}`
                          : `${job.commits} commits`}
                      </span>
                    </div>
                  </div>
                </div>
                <Badge job={job} />
                <div className="model-cell">
                  <span>
                    {modelLabel(
                      job.goal?.model_selection?.active_model || job.model,
                    )}
                  </span>
                  <small>
                    {job.goal?.model_selection?.strategy?.replaceAll(
                      "_",
                      " ",
                    ) || job.execution_mode}
                  </small>
                </div>
                <span className="created-cell">{date(job.created_at)}</span>
                <Icon name="arrow" size={16} />
              </a>
            ))}
          </div>
        ) : (
          <div className="empty-state">
            <div className="empty-icon">
              <Icon name={jobs.length ? "search" : "terminal"} size={28} />
            </div>
            <h3>
              {jobs.length
                ? "No work matches this view"
                : "Ready for a coding job"}
            </h3>
            <p>
              {jobs.length
                ? "Try another filter or search to find your work."
                : "Ask Dot to use dotshift on a local project. Coding, tests, model switches, and review will appear here."}
            </p>
            {jobs.length ? (
              <button
                className="secondary"
                onClick={() => {
                  setQuery("");
                  setFilter("all");
                  clearRepo();
                }}
              >
                Clear filters
              </button>
            ) : (
              <button className="primary" onClick={onHelp}>
                How to start
                <Icon name="arrow" size={16} />
              </button>
            )}
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
}: {
  id: string;
  connected: boolean;
  onBack: () => void;
}) {
  const [job, setJob] = useState<Job | null>(null);
  const [output, setOutput] = useState<Output>({
    output: "",
    truncated: false,
  });
  const [tab, setTab] = useState("activity");
  const [error, setError] = useState("");
  const [outputError, setOutputError] = useState("");
  const [dialog, setDialog] = useState<"resume" | "cancel" | null>(null);
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
          document.title = `dotshift · ${title(next)}`;
        }
      } catch (e) {
        if (!stopped)
          setError(
            e instanceof APIError
              ? e.message
              : "Could not refresh this goal. Check the service connection.",
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
    if (tab === "tests") return;
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
  }, [id, tab, revision]);
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
  const selection =
    job.goal?.phase === "reviewing"
      ? job.goal.reviewer_selection || job.goal.model_selection
      : job.goal?.model_selection;
  const wait = waiting(job);
  const phase = job.goal?.phase || "working";
  return (
    <div className="page detail">
      <button className="back-link" onClick={onBack}>
        <Icon name="back" size={16} />
        All work
      </button>
      <div className="detail-heading">
        <div>
          <div className="eyebrow">
            {job.repo} <span>/</span>{" "}
            {job.goal ? "CODING JOB" : "CODING TASK"} <span>/</span> {job.id}
          </div>
          <h1>{title(job)}</h1>
          <div className="detail-subheading">
            <Badge job={job} />
            <span>
              <Icon name="clock" size={15} />
              {elapsed(job)}
            </span>
            <span>Started {date(job.started_at || job.created_at)}</span>
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
              Cancel {job.goal ? "goal" : "task"}
            </button>
          )}
          {resumable(job) && (
            <button
              className="primary"
              disabled={pending || !connected}
              onClick={() => {
                setActionError("");
                setDialog("resume");
              }}
            >
              <Icon name="play" size={16} />
              Resume goal
            </button>
          )}
        </div>
      </div>
      {error && (
        <div className="notice warning" role="alert">
          {error}
        </div>
      )}
      {(job.error || wait) && (
        <div className={`notice ${attention(job) ? "warning" : ""}`}>
          <Icon name={attention(job) ? "alert" : "clock"} />
          <div>
            <strong>
              {wait && active(job)
                ? "Waiting for model availability"
                : statusLabel(job)}
            </strong>
            <p>
              {job.error || selection?.reason}
              {wait && active(job) && <> · Next retry {date(wait)}</>}
            </p>
          </div>
        </div>
      )}
      {job.goal && (
        <div className="phase-strip">
          {[
            { key: "working", name: "Implement", icon: "terminal" },
            { key: "testing", name: "Test", icon: "activity" },
            { key: "reviewing", name: "Review", icon: "check" },
          ].map((step, index) => (
            <div
              key={step.key}
              className={`phase ${job.status === "done" ? "complete" : phase === step.key ? "present" : ""}`}
            >
              <span className="phase-icon">
                <Icon
                  name={job.status === "done" ? "check" : step.icon}
                  size={17}
                />
              </span>
              <div>
                <small>0{index + 1}</small>
                <strong>{step.name}</strong>
              </div>
              {phase === step.key && active(job) && (
                <span className="phase-current">
                  {wait ? "Waiting" : "In progress"}
                </span>
              )}
            </div>
          ))}
          <div className="iteration">
            <strong>{job.goal.iterations.toString().padStart(2, "0")}</strong>
            <span>
              completed iterations
              {job.goal.max_iterations > 0 && (
                <small>of {job.goal.max_iterations} allowed</small>
              )}
            </span>
          </div>
        </div>
      )}
      <div className="detail-grid">
        <div className="detail-content">
          <section className="panel objective">
            <div className="panel-heading">
              <h2>The objective</h2>
              <span className="micro-label">ORIGINAL INSTRUCTIONS</span>
            </div>
            <p>{job.prompt}</p>
            {job.goal?.additional_instructions && (
              <details>
                <summary>Additional instructions</summary>
                <p>{job.goal.additional_instructions}</p>
              </details>
            )}
          </section>
          <div className="detail-tabs" role="tablist" aria-label="Goal output">
            {[
              ["activity", "Activity", "terminal"],
              ["changes", "Changes", "branch"],
              ["tests", "Tests", "check"],
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
                  const tabs = ["activity", "changes", "tests"];
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
                {(job.summary || job.goal?.report.summary) && (
                  <section className="panel assessment">
                    <div className="panel-heading">
                      <h3>
                        {job.status === "done"
                          ? "Completion notes"
                          : "Latest assessment"}
                      </h3>
                      <Icon name="activity" size={16} />
                    </div>
                    <p>{job.summary || job.goal?.report.summary}</p>
                    {job.goal?.report.next_steps && job.status !== "done" && (
                      <div className="next-steps">
                        <strong>Next steps</strong>
                        <p>{job.goal.report.next_steps}</p>
                      </div>
                    )}
                  </section>
                )}
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
                        ? "Committed changes appear after an iteration finishes."
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
        </div>
        <aside className="detail-aside">
          <section className="panel">
            <div className="panel-heading">
              <h2>Run details</h2>
              <Icon name="activity" size={16} />
            </div>
            <dl>
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
          </section>
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
          <div className="branch-note">
            <Icon name="branch" size={17} />
            <p>
              Your working copy stays separate.
              <br />
              Review this branch before merging.
            </p>
          </div>
        </aside>
      </div>
      {dialog === "cancel" && (
        <Modal
          title={`Cancel this ${job.goal ? "goal" : "task"}?`}
          onClose={() => {
            if (!pending) setDialog(null);
          }}
        >
          <p>
            The current agent or test process will stop. The branch and any
            remaining worktree will be preserved.
            {job.goal && " You can resume the goal later."}
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
      {dialog === "resume" && (
        <ResumeDialog
          job={job}
          pending={pending}
          error={actionError}
          onClose={() => {
            if (!pending) setDialog(null);
          }}
          onSubmit={action}
        />
      )}
    </div>
  );
}

function ResumeDialog({
  job,
  pending,
  error,
  onClose,
  onSubmit,
}: {
  job: Job;
  pending: boolean;
  error: string;
  onClose: () => void;
  onSubmit: (body: object) => Promise<void>;
}) {
  const [input, setInput] = useState(""),
    [retry, setRetry] = useState(false),
    [limit, setLimit] = useState(String(job.goal?.max_iterations || 0));
  function submit(event: FormEvent) {
    event.preventDefault();
    void onSubmit({
      input,
      max_iterations: Number(limit),
      retry_unavailable: retry,
    });
  }
  return (
    <Modal title="Pick up where it left off" onClose={onClose}>
      <p>Continue on the same branch with the existing work and progress.</p>
      <form onSubmit={submit}>
        <label className="form-field">
          Additional instructions
          <textarea
            autoFocus
            rows={4}
            placeholder="Resolve a blocker or clarify what to do next…"
            value={input}
            onChange={(e) => setInput(e.target.value)}
            disabled={pending}
          />
        </label>
        <label className="form-field">
          Total iteration limit
          <input
            type="number"
            min={0}
            step={1}
            required
            value={limit}
            onChange={(e) => setLimit(e.target.value)}
            disabled={pending}
          />
          <small>
            {job.goal?.iterations} completed so far. Use 0 for unlimited.
          </small>
        </label>
        {job.goal?.model_selection && (
          <label className="checkbox-field">
            <input
              type="checkbox"
              checked={retry}
              onChange={(e) => setRetry(e.target.checked)}
              disabled={pending}
            />
            <span>
              Retry unavailable models
              <small>
                Use after allowance resets or access is restored. Clears cached
                availability for these models across goals.
              </small>
            </span>
          </label>
        )}
        {error && (
          <div className="notice warning" role="alert">
            {error}
          </div>
        )}
        <div className="dialog-actions">
          <button
            type="button"
            className="secondary"
            onClick={onClose}
            disabled={pending}
          >
            Back
          </button>
          <button className="primary" disabled={pending}>
            {pending ? "Resuming…" : "Resume goal"}
            <Icon name="play" size={16} />
          </button>
        </div>
      </form>
    </Modal>
  );
}

createRoot(document.getElementById("root")!).render(<App />);
