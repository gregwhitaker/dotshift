package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrAttentionChanged prevents a stale browser action from acknowledging a
// different issue, including a new failure after the same goal was resumed.
var ErrAttentionChanged = errors.New("the job's attention state changed; refresh and try again")

// AttentionVersion identifies the issue being acknowledged without exposing
// the worker's ownership token. Ordinary progress and cleanup of process IDs
// do not create a new issue. A new run, status, error, or test outcome does.
func (j *Job) AttentionVersion() string {
	if j.Status != Blocked && j.Status != Failed && j.Status != WaitingInput && !(j.Status == Done && j.TestResult == "failed") {
		return ""
	}
	data, _ := json.Marshal([]any{j.ID, j.RunToken, j.Status, j.Error, j.TestResult, unix(j.FinishedAt)})
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func (j *Job) AttentionDismissed() bool {
	version := j.AttentionVersion()
	return version != "" && j.dismissedAttentionVersion == version
}

// SetAttentionDismissed acknowledges or restores only the issue the user saw.
// It never changes execution status, starts work, or removes saved work.
func (r *Runner) SetAttentionDismissed(ctx context.Context, id, version string, dismissed bool) (*Job, error) {
	j, err := r.Store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if version == "" || version != j.AttentionVersion() {
		return nil, ErrAttentionChanged
	}
	if err := r.Store.setAttentionDismissed(ctx, j, dismissed); err != nil {
		return nil, err
	}
	return r.Store.Get(ctx, id)
}

func (s *Store) setAttentionDismissed(ctx context.Context, j *Job, dismissed bool) error {
	version := j.AttentionVersion()
	if version == "" {
		return ErrAttentionChanged
	}
	if !dismissed {
		version = ""
	}
	// Compare the same fields used by AttentionVersion inside this update. A
	// concurrent resume or new issue must not inherit an old acknowledgment.
	res, err := s.db.ExecContext(ctx, `UPDATE jobs SET attention_dismissed = ?
		WHERE id = ? AND run_token = ? AND status = ? AND error = ? AND test_result = ? AND finished_at = ?`,
		version, j.ID, j.RunToken, j.Status, j.Error, j.TestResult, unix(j.FinishedAt))
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return ErrAttentionChanged
	}
	return err
}
