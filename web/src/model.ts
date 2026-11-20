export type Status =
  | "queued"
  | "running"
  | "waiting_input"
  | "done"
  | "failed"
  | "cancelled"
  | "blocked";
export type Selection = {
  strategy: string;
  active_model?: string;
  reason?: string;
  switches: number;
  waiting_until?: string;
  last_failure?: string;
};
export type Goal = {
  phase: string;
  iterations: number;
  max_iterations: number;
  additional_instructions?: string;
  test_command?: string;
  tested_commit?: string;
  report: { status: string; summary: string; next_steps: string };
  model_selection?: Selection;
  reviewer_selection?: Selection;
  reviewer_model?: string;
  adaptive?: { level: number; escalations: number; reason?: string };
  budget?: { limit_usd: number; spent_usd: number; pending_requests: number };
};
export type Job = {
  id: string;
  title?: string;
  parent?: string;
  work_id?: string;
  work_title?: string;
  current_id?: string;
  resolved_by?: string;
  display?: {
    kind: string;
    label: string;
    detail: string;
    attention: boolean;
    completed: boolean;
    message?: string;
  };
  repo: string;
  prompt: string;
  model: string;
  execution_mode: string;
  branch: string;
  base_commit: string;
  status: Status;
  error: string;
  summary: string;
  commits: number;
  diffstat: string;
  test_result: string;
  test_output?: string;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
  goal: Goal | null;
  delivery?: { ready: boolean; applied: boolean; blocker?: string; blocker_code?: string };
  attention_version: string;
  attention_dismissed: boolean;
};
export type Filter = "all" | "active" | "attention" | "done";
export function filterFromSearch(search: string): Filter {
  const value = new URLSearchParams(search).get("filter");
  return value === "active" || value === "attention" || value === "done"
    ? value
    : "all";
}
export const active = (job: Job) =>
  job.status === "queued" || job.status === "running";
export const attention = (job: Job) =>
  !job.attention_dismissed && !job.resolved_by &&
  (job.display?.attention ?? (["blocked", "failed", "waiting_input"].includes(job.status) ||
    (job.status === "done" && job.test_result === "failed")));
export const currentWork = (jobs: Job[]) => jobs.filter(job => !job.current_id || job.current_id === job.id);
// Only a specific input/access request belongs in the user's attention list.
// Routine failures remain visible as work waiting for Dot, without an alert.
export const needsYou = (job: Job) => attention(job) &&
  (job.display?.kind === "needs_input" || (!job.display && job.status === "waiting_input"));
export const ongoingWork = (jobs: Job[]) => currentWork(jobs).filter(job => active(job) || (attention(job) && !needsYou(job)));
export const completed = (job: Job) => job.display?.completed ?? (job.status === "done" && job.test_result !== "failed");
export const resumable = (job: Job) =>
  !!job.goal && ["blocked", "cancelled", "failed"].includes(job.status);
export const waiting = (job: Job) =>
  job.goal?.phase === "reviewing"
    ? job.goal?.reviewer_selection?.waiting_until ||
      job.goal?.model_selection?.waiting_until
    : job.goal?.model_selection?.waiting_until;

export function prioritizeJobs(jobs: Job[]) {
  const rank = (job: Job) => attention(job) ? 0 : active(job) ? 1 : 2;
  return [...jobs].sort((a, b) => rank(a) - rank(b) || Date.parse(b.created_at) - Date.parse(a.created_at));
}

export function progressText(job: Job): string {
  if (job.display) {
    const labels: Record<string, string> = {
      ready: "Tests and review passed · saved separately",
      applied: "Tests and review passed · in your local app",
      verified: "Tests and review passed · no changes needed",
      delivery_blocked: "Changes saved · local checkout needs checking",
      unverified: "Saved result · review not recorded",
    };
    const text = labels[job.display.kind] || job.display.detail;
    return text.length > 130 ? text.slice(0, 127).trimEnd() + "…" : text;
  }
  if (attention(job)) return "Progress saved · Dot can help continue";
  if (active(job) && waiting(job)) return "Will retry automatically";
  if (active(job)) return "Working locally";
  if (job.status === "done" && job.test_result === "passed") return "Tests passed";
  if (job.status === "cancelled") return "Stopped · progress saved";
  return "Saved locally";
}
export function statusLabel(job: Job) {
  if (job.display) return job.display.label;
  if (job.status === "done" && job.test_result === "failed")
    return "Tests failed";
  if (job.status === "running" && waiting(job)) return "Waiting for model";
  if (job.status === "running")
    return (
      { working: "Implementing", testing: "Testing", reviewing: "Reviewing" }[
        job.goal?.phase || ""
      ] || "Running"
    );
  return (
    {
      queued: "Queued",
      blocked: "Needs input",
      failed: "Failed",
      cancelled: "Cancelled",
      done: "Complete",
      waiting_input: "Needs input",
    }[job.status] || job.status
  );
}
export function selectJobs(
  jobs: Job[],
  filter: Filter,
  repo: string,
  query: string,
) {
  const term = query.trim().toLowerCase();
  return currentWork(jobs).filter(
    (job) =>
      (!repo || job.repo === repo) &&
      (filter === "all" ||
        (filter === "active" && active(job)) ||
        (filter === "attention" && attention(job)) ||
        (filter === "done" && completed(job))) &&
      jobs.some(entry => (entry.id === job.id || (!!job.work_id && entry.work_id === job.work_id)) &&
        `${entry.title || ""} ${entry.work_title || ""} ${entry.prompt} ${entry.repo} ${entry.id} ${entry.branch}`.toLowerCase().includes(term)),
  );
}
export function title(job: Job) {
  const text = job.title || job.prompt.trim().split("\n")[0] || "Local coding request";
  return text.length > 80 ? text.slice(0, 77).trimEnd() + "…" : text;
}
export const workTitle = (job: Job) => job.work_title || title(job);
export const badgeClass = (job: Job) => {
  if (job.test_result === "failed" && job.status === "done") return "failed";
  if (job.display?.kind === "unverified") return "saved";
  if (job.display?.kind === "delivery_blocked") return "blocked";
  return job.status;
};
export function modelLabel(model: string) {
  if (model === "openai/default") return "Codex default";
  if (model === "anthropic/default") return "Claude default";
  return model.includes("/") ? model.slice(model.indexOf("/") + 1) : model;
}
export function elapsed(job: Job, now = Date.now()) {
  if (!job.started_at) return "Not started";
  const seconds = Math.max(
    0,
    Math.floor(
      ((job.finished_at ? new Date(job.finished_at).getTime() : now) -
        new Date(job.started_at).getTime()) /
        1000,
    ),
  );
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
  return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`;
}
export const cleanLog = (text: string) =>
  text.replace(/\u001b\[[0-?]*[ -/]*[@-~]/g, "").replace(/\r/g, "");
