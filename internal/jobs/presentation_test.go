package jobs

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTitlesKeepFullInstructionsOutOfHeadings(t *testing.T) {
	for _, j := range []*Job{
		{Repo: "house-manual", Prompt: "Implement the following user-authorized House Manual project completely in this repository. More details follow."},
		{Repo: "house-manual", Prompt: "Read-only provenance checkpoint on the same branch. " + strings.Repeat("Never turn these instructions into a heading. ", 20)},
		{Prompt: strings.Repeat("界", 200)},
		{Prompt: strings.Repeat("Long instructions ", 200)},
	} {
		title := DisplayTitle(j)
		if !utf8.ValidString(title) || len([]rune(title)) > 80 || title == "" {
			t.Fatalf("unbounded or broken title: %q", title)
		}
	}
	if title := DisplayTitle(&Job{Title: "Add search to House Manual", Prompt: "Detailed instructions"}); title != "Add search to House Manual" {
		t.Fatalf("Dot's supplied title was lost: %q", title)
	}
}

func TestTitleMigrationPreservesExistingHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	oldSchema := strings.Replace(schema, ",\n\ttitle TEXT NOT NULL DEFAULT ''", "", 1)
	if _, err := db.Exec(oldSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO jobs(id,repo,repo_path,prompt,model,branch,base_commit,worktree,status,created_at,summary) VALUES('old','app','/app','Original request','smart','work','base','','failed',1,'Kept findings')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	j, err := store.Get(context.Background(), "old")
	if err != nil || j.Title != "" || j.Prompt != "Original request" || j.Summary != "Kept findings" {
		t.Fatalf("migration lost history: %+v %v", j, err)
	}
	j.Title = "Fix export"
	if err := store.Save(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Get(context.Background(), "old")
	if err != nil || saved.Title != j.Title || saved.Prompt != j.Prompt {
		t.Fatal("short title did not persist separately from instructions")
	}
}

func TestOmittedTitlePreservesPreUpgradeSubmissionRetries(t *testing.T) {
	r, _ := setup(t, "goal_finish", "test -f hello.txt")
	// This is the request serialization before the title field existed.
	const oldJSON = `{"RequestID":"legacy-retry","Repo":"","Prompt":"Finish this change","Model":"","Strategy":"","ReviewerModel":"","EscalateAfter":0,"MaxCostUSD":null,"Branch":"","Base":"","Continue":"","Goal":true,"MaxIterations":0}`
	j := &Job{ID: "already-finished", RequestID: "legacy-retry", RequestHash: fmt.Sprintf("%x", sha256.Sum256([]byte(oldJSON))), Prompt: "Finish this change", Status: Done}
	if err := r.Store.Save(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	got, err := r.Submit(context.Background(), Request{RequestID: j.RequestID, Prompt: j.Prompt})
	if err != nil || got.ID != j.ID {
		t.Fatalf("an upgrade broke retry recovery: %+v %v", got, err)
	}
}
