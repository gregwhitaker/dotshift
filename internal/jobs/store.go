package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// ErrNotFound is returned for an unknown job ID.
var ErrNotFound = errors.New("no such job")

// Store keeps jobs in a SQLite file. Several dotshift processes may share it.
type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS jobs (
	id          TEXT PRIMARY KEY,
	repo        TEXT NOT NULL,
	repo_path   TEXT NOT NULL,
	prompt      TEXT NOT NULL,
	model       TEXT NOT NULL,
	branch      TEXT NOT NULL,
	base_commit TEXT NOT NULL,
	worktree    TEXT NOT NULL,
	status      TEXT NOT NULL,
	error       TEXT NOT NULL DEFAULT '',
	summary     TEXT NOT NULL DEFAULT '',
	commits     INTEGER NOT NULL DEFAULT 0,
	diffstat    TEXT NOT NULL DEFAULT '',
	test_result TEXT NOT NULL DEFAULT '',
	test_output TEXT NOT NULL DEFAULT '',
	exit_code   INTEGER NOT NULL DEFAULT 0,
	log_path    TEXT NOT NULL DEFAULT '',
	runner_pid  INTEGER NOT NULL DEFAULT 0,
	agent_pid   INTEGER NOT NULL DEFAULT 0,
	created_at  INTEGER NOT NULL,
	started_at  INTEGER NOT NULL DEFAULT 0,
	finished_at INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS jobs_created ON jobs (created_at);
`

// OpenStore opens (creating if needed) the job database at path.
func OpenStore(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("open job database %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

const columns = `id, repo, repo_path, prompt, model, branch, base_commit, worktree, status,
	error, summary, commits, diffstat, test_result, test_output, exit_code, log_path,
	runner_pid, agent_pid, created_at, started_at, finished_at`

// Save inserts or replaces a job.
func (s *Store) Save(ctx context.Context, j *Job) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO jobs (`+columns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.ID, j.Repo, j.RepoPath, j.Prompt, j.Model, j.Branch, j.BaseCommit, j.Worktree, string(j.Status),
		j.Error, j.Summary, j.Commits, j.DiffStat, j.TestResult, j.TestOutput, j.ExitCode, j.LogPath,
		j.RunnerPID, j.AgentPID, unix(j.CreatedAt), unix(j.StartedAt), unix(j.FinishedAt))
	return err
}

// Get returns one job.
func (s *Store) Get(ctx context.Context, id string) (*Job, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM jobs WHERE id = ?`, id)
	j, err := scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w %q", ErrNotFound, id)
	}
	return j, err
}

// List returns all jobs, newest first.
func (s *Store) List(ctx context.Context) ([]*Job, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+columns+` FROM jobs ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Job
	for rows.Next() {
		j, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// SetStatus changes only a job's status and error, leaving fields the
// runner owns alone. It's how another process marks a job cancelled.
func (s *Store) SetStatus(ctx context.Context, id string, st Status, msg string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET status = ?, error = ? WHERE id = ?`, string(st), msg, id)
	return err
}

// MarkRunning moves a queued job to running. It reports false if the job is
// no longer queued, which means it was cancelled before its agent started.
func (s *Store) MarkRunning(ctx context.Context, j *Job) (bool, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE jobs SET status = ?, started_at = ?, agent_pid = ? WHERE id = ? AND status = ?`,
		string(Running), unix(j.StartedAt), j.AgentPID, j.ID, string(Queued))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

type scanner interface{ Scan(dest ...any) error }

func scan(r scanner) (*Job, error) {
	var j Job
	var status string
	var created, started, finished int64
	err := r.Scan(&j.ID, &j.Repo, &j.RepoPath, &j.Prompt, &j.Model, &j.Branch, &j.BaseCommit, &j.Worktree, &status,
		&j.Error, &j.Summary, &j.Commits, &j.DiffStat, &j.TestResult, &j.TestOutput, &j.ExitCode, &j.LogPath,
		&j.RunnerPID, &j.AgentPID, &created, &started, &finished)
	if err != nil {
		return nil, err
	}
	j.Status = Status(status)
	j.CreatedAt, j.StartedAt, j.FinishedAt = fromUnix(created), fromUnix(started), fromUnix(finished)
	return &j, nil
}

func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromUnix(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}
