package jobs

import (
	"context"
	"fmt"
	"time"
)

func (s *Store) projects(ctx context.Context, root string) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, root, path, test_profile FROM projects WHERE root = ? ORDER BY name`, root)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var projects []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.Name, &p.root, &p.Path, &p.TestProfile); err != nil {
			return nil, err
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}

func (s *Store) projectExists(ctx context.Context, name string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE name = ?)`, name).Scan(&exists)
	return exists, err
}

func (s *Store) registerProject(ctx context.Context, p Project) error {
	// An identical retry is idempotent. Never silently change an existing
	// project's location or test profile, including from another process.
	_, err := s.db.ExecContext(ctx, `INSERT INTO projects(name, root, path, test_profile, created_at) VALUES(?,?,?,?,?) ON CONFLICT(name) DO NOTHING`, p.Name, p.root, p.Path, p.TestProfile, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	var root, path, profile string
	err = s.db.QueryRowContext(ctx, `SELECT root, path, test_profile FROM projects WHERE name = ?`, p.Name).Scan(&root, &path, &profile)
	if err != nil {
		return err
	}
	if root != p.root || path != p.Path || profile != p.TestProfile {
		return fmt.Errorf("project %q is already registered with a different path or test profile", p.Name)
	}
	return nil
}
