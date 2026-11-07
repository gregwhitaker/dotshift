package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"
)

var errGoalStopped = errors.New("goal was cancelled or claimed by another worker")

func encodeGoal(goal *GoalProgress) (string, error) {
	if goal == nil {
		return "", nil
	}
	data, err := json.Marshal(goal)
	return string(data), err
}

// saveGoal checkpoints only the current owner's progress. In particular, a
// late agent exit must never overwrite a cancellation from another process.
func (s *Store) saveGoal(ctx context.Context, j *Job) error {
	goal, err := encodeGoal(j.Goal)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE jobs SET
		status = ?, error = ?, summary = ?, commits = ?, diffstat = ?,
		test_result = ?, test_output = ?, exit_code = ?, agent_pid = ?,
		worktree = ?, started_at = ?, finished_at = ?, runner_pid = ?, goal = ?
		WHERE id = ? AND run_token = ? AND status != ?`,
		j.Status, j.Error, j.Summary, j.Commits, j.DiffStat,
		j.TestResult, j.TestOutput, j.ExitCode, j.AgentPID,
		j.Worktree, unix(j.StartedAt), unix(j.FinishedAt), j.RunnerPID, goal,
		j.ID, j.RunToken, Cancelled)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return errGoalStopped
	}
	return err
}

// claimGoal atomically takes ownership of a stopped goal. Comparing both
// status and token makes concurrent resumes and cancellation safe.
func (s *Store) claimGoal(ctx context.Context, j *Job) error {
	token, err := newID()
	if err != nil {
		return err
	}
	goal, err := encodeGoal(j.Goal)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE jobs SET
		status = ?, error = '', finished_at = 0, runner_pid = ?, run_token = ?, goal = ?, model = ?
		WHERE id = ? AND run_token = ? AND runner_pid = ? AND status = ?`,
		Queued, os.Getpid(), token, goal, j.Model, j.ID, j.RunToken, j.RunnerPID, j.Status)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return errGoalStopped
	}
	// A cap change shares the ownership claim's transaction. A losing or
	// stale resume must never alter the winning worker's spending limit.
	if j.Goal.Budget != nil {
		limit, err := budgetUnits(j.Goal.Budget.LimitUSD)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE goal_budgets SET limit_units = ? WHERE job_id = ?`, limit, j.ID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	j.RunToken, j.RunnerPID, j.Status, j.Error = token, os.Getpid(), Queued, ""
	j.FinishedAt = fromUnix(0)
	return nil
}

func (s *Store) cancelGoal(ctx context.Context, j *Job) error {
	res, err := s.db.ExecContext(ctx, `UPDATE jobs SET status = ?, error = 'cancelled', finished_at = ?
		WHERE id = ? AND run_token = ? AND status IN (?, ?, ?)`, Cancelled, unix(time.Now()), j.ID, j.RunToken, Queued, Running, Blocked)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return errGoalStopped
	}
	return err
}
