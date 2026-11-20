import test from "node:test";
import assert from "node:assert/strict";
import {
  attention,
  selectJobs,
  statusLabel,
  resumable,
  elapsed,
  waiting,
  filterFromSearch,
  prioritizeJobs,
  progressText,
  currentWork,
  title,
  workTitle,
  completed,
  needsYou,
  ongoingWork,
  type Job,
} from "./model.ts";

test("menu bar deep links select only supported work filters", () => {
  assert.equal(filterFromSearch("?filter=attention"), "attention");
  assert.equal(filterFromSearch("?filter=active"), "active");
  assert.equal(filterFromSearch("?filter=done"), "done");
  assert.equal(filterFromSearch("?filter=unexpected"), "all");
  assert.equal(filterFromSearch(""), "all");
});

test("the status view shows current work and only actual input requests", () => {
  const question = job({ id: "question", status: "blocked", display: { kind: "needs_input", label: "Dot needs your input", detail: "Which account should receive the export?", attention: true, completed: false } });
  const repair = job({ id: "repair", status: "done", test_result: "failed", display: { kind: "needs_dot", label: "Tests failed", detail: "Dot can fix this", attention: true, completed: false } });
  const old = { ...question, id: "old", current_id: "question" };
  const jobs = [job({ id: "running" }), question, repair, old, job({ id: "done", status: "done" }), job({ id: "stopped", status: "cancelled" })];
  assert.deepEqual(currentWork(jobs).filter(needsYou).map(j => j.id), ["question"]);
  assert.deepEqual(ongoingWork(jobs).map(j => j.id), ["running", "repair"]);
  assert.equal(needsYou({ ...question, attention_dismissed: true }), false);
  assert.equal(needsYou({ ...question, resolved_by: "later" }), false);
  assert.equal(jobs.length, 6);
});

test("finished tasks with failed tests still need attention", () => {
  const failedTests = job({
    status: "done",
    test_result: "failed",
    goal: null,
  });
  assert.equal(attention(failedTests), true);
  assert.equal(statusLabel(failedTests), "Tests failed");
  assert.equal(selectJobs([failedTests], "attention", "", "").length, 1);
  assert.equal(selectJobs([failedTests], "done", "", "").length, 0);
  assert.equal(
    attention(job({ status: "running", test_result: "failed" })),
    false,
  );
});

const job = (values: Partial<Job>): Job => ({
  id: "abc",
  repo: "app",
  prompt: "Build search",
  model: "smart",
  execution_mode: "subscription",
  branch: "dotshift/abc",
  base_commit: "base",
  status: "running",
  error: "",
  summary: "",
  commits: 0,
  diffstat: "",
  test_result: "",
  created_at: "2026-11-06T10:00:00Z",
  started_at: "2026-11-06T10:00:00Z",
  finished_at: null,
  goal: {
    phase: "working",
    iterations: 1,
    max_iterations: 10,
    report: { status: "continue", summary: "", next_steps: "" },
  },
  attention_version: "issue-1",
  attention_dismissed: false,
  ...values,
});

test("the overview keeps current work ahead of history without hiding dismissed jobs", () => {
  const done = job({ id: "done", status: "done", created_at: "2026-11-16T10:00:00Z" });
  const running = job({ id: "running" });
  const blocked = job({ id: "blocked", status: "blocked" });
  const dismissed = job({ id: "dismissed", status: "blocked", attention_dismissed: true });
  const original = [done, dismissed, running, blocked];
  assert.deepEqual(prioritizeJobs(original).map(j => j.id), ["blocked", "running", "done", "dismissed"]);
  assert.equal(original[0], done);
  assert.equal(progressText(running), "Working locally");
  assert.equal(progressText(blocked), "Progress saved · Dot can help continue");
});

test("dismissed issues leave attention but remain in history and can resume", () => {
  for (const status of ["blocked", "failed", "waiting_input", "done"] as const) {
    const dismissed = job({
      status,
      test_result: "failed",
      attention_dismissed: true,
    });
    assert.equal(attention(dismissed), false);
    assert.equal(selectJobs([dismissed], "attention", "", "").length, 0);
    assert.deepEqual(selectJobs([dismissed], "all", "", ""), [dismissed]);
    assert.equal(
      statusLabel(dismissed),
      status === "done"
        ? "Tests failed"
        : status === "failed"
          ? "Failed"
          : "Needs input",
    );
    assert.equal(attention({ ...dismissed, attention_dismissed: false }), true);
  }
  assert.equal(
    resumable(job({ status: "blocked", attention_dismissed: true })),
    true,
  );
});

test("overview filters intersect repository, status, and search", () => {
  const jobs = [
    job({ id: "one" }),
    job({ id: "two", status: "blocked" }),
    job({ id: "three", status: "done", repo: "other" }),
    job({ id: "four", status: "cancelled" }),
  ];
  assert.deepEqual(
    selectJobs(jobs, "active", "", "").map((j) => j.id),
    ["one"],
  );
  assert.deepEqual(
    selectJobs(jobs, "attention", "app", "search").map((j) => j.id),
    ["two"],
  );
  assert.equal(selectJobs(jobs, "done", "app", "").length, 0);
  assert.equal(selectJobs(jobs, "all", "", "DOTSHIFT/ABC").length, 4);
});
test("cooldowns identify the active phase and never override terminal status", () => {
  const j = job({});
  j.goal!.model_selection = {
    strategy: "alias",
    switches: 1,
    waiting_until: "2026-11-06T11:00:00Z",
  };
  assert.equal(statusLabel(j), "Waiting for model");
  j.status = "blocked";
  assert.equal(statusLabel(j), "Needs input");
  j.status = "running";
  j.goal!.phase = "reviewing";
  j.goal!.reviewer_selection = {
    strategy: "alias",
    switches: 0,
    waiting_until: "2026-11-06T12:00:00Z",
  };
  assert.equal(waiting(j), "2026-11-06T12:00:00Z");
});
test("only stopped unfinished goals can resume", () => {
  for (const status of ["blocked", "failed", "cancelled"] as const)
    assert.equal(resumable(job({ status })), true);
  for (const status of ["running", "queued", "done"] as const)
    assert.equal(resumable(job({ status })), false);
  assert.equal(resumable(job({ status: "failed", goal: null })), false);
});
test("elapsed time freezes when a job finishes and queued work has no duration", () => {
  assert.equal(elapsed(job({ finished_at: "2026-11-06T10:02:12Z" })), "2m 12s");
  assert.equal(elapsed(job({ started_at: null })), "Not started");
});

test("one request shows its current result and remains searchable through its history", () => {
  const old = job({ id: "old", work_id: "old", current_id: "new", status: "done", test_result: "failed", resolved_by: "new", prompt: "Fix the offline export", display: { kind: "needs_dot", label: "Tests failed", detail: "Earlier check", attention: false, completed: false } });
  const latest = job({ id: "new", parent: "old", work_id: "old", current_id: "new", work_title: "Improve House Manual", title: "Verify the export fix", status: "done", display: { kind: "ready", label: "Ready to apply", detail: "Tests and review passed", attention: false, completed: true } });
  const unrelated = job({ id: "other", work_id: "other", current_id: "other", status: "blocked" });
  const all = [old, latest, unrelated];
  assert.deepEqual(currentWork(all).map(j => j.id), ["new", "other"]);
  assert.deepEqual(selectJobs(all, "all", "", "offline export").map(j => j.id), ["new"]);
  assert.deepEqual(selectJobs(all, "attention", "", "").map(j => j.id), ["other"]);
  assert.equal(workTitle(latest), "Improve House Manual");
  assert.equal(title(latest), "Verify the export fix");
  assert.equal(attention(old), false);
});

test("list badges preserve the server's ready applied and blocked delivery states", () => {
  for (const [kind, label] of [["ready", "Ready to apply"], ["applied", "Applied locally"], ["delivery_blocked", "Not applied"]]) {
    const item = job({ status: "done", display: { kind, label, detail: "Saved result", attention: false, completed: true } });
    assert.equal(statusLabel(item), label);
    assert.equal(completed(item), true);
    assert.equal(attention(item), false);
  }
  assert.ok(title(job({ prompt: "Long instruction ".repeat(50) })).length <= 80);
});
