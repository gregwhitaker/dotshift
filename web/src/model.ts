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
  ["blocked", "failed", "waiting_input"].includes(job.status) ||
  (job.status === "done" && job.test_result === "failed");
export const resumable = (job: Job) =>
  !!job.goal && ["blocked", "cancelled", "failed"].includes(job.status);
export const waiting = (job: Job) =>
  job.goal?.phase === "reviewing"
    ? job.goal?.reviewer_selection?.waiting_until ||
      job.goal?.model_selection?.waiting_until
    : job.goal?.model_selection?.waiting_until;
export function statusLabel(job: Job) {
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
  return jobs.filter(
    (job) =>
      (!repo || job.repo === repo) &&
      (filter === "all" ||
        (filter === "active" && active(job)) ||
        (filter === "attention" && attention(job)) ||
        (filter === "done" && job.status === "done")) &&
      `${job.prompt} ${job.repo} ${job.id} ${job.branch} ${job.model}`
        .toLowerCase()
        .includes(term),
  );
}
export const title = (job: Job) =>
  job.prompt.trim().split("\n")[0] || "Untitled job";
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
