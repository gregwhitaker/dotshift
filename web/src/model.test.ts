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
  type Job,
} from "./model.ts";

test("menu bar deep links select only supported work filters", () => {
  assert.equal(filterFromSearch("?filter=attention"), "attention");
  assert.equal(filterFromSearch("?filter=active"), "active");
  assert.equal(filterFromSearch("?filter=done"), "done");
  assert.equal(filterFromSearch("?filter=unexpected"), "all");
  assert.equal(filterFromSearch(""), "all");
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
  ...values,
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
