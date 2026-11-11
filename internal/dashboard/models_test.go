package dashboard

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gregwhitaker/dotshift/internal/config"
	"github.com/gregwhitaker/dotshift/internal/jobs"
)

func TestModelCatalogPreservesRunnerChoices(t *testing.T) {
	cfg, err := config.Parse([]byte(config.SubscriptionSample))
	if err != nil {
		t.Fatal(err)
	}
	store, err := jobs.OpenStore(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runner := &jobs.Runner{Config: cfg, Store: store}
	r := Record{URL: "http://127.0.0.1:8123", Token: strings.Repeat("a", 64), Instance: "test"}
	h := newHandler(Options{Jobs: runner, DefaultModel: cfg.Jobs.Model}, r)
	w := request(h, r, "GET", "/api/models", "", nil)
	var catalog struct {
		DefaultModel string               `json:"default_model"`
		Strategies   jobs.StrategyCatalog `json:"strategies"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &catalog); err != nil || w.Code != 200 {
		t.Fatalf("models: %d %s", w.Code, w.Body.String())
	}
	if catalog.DefaultModel != "smart" || catalog.Strategies.DefaultStrategy != "prefer_openai" || catalog.Strategies.DefaultReviewer != "auto" {
		t.Fatalf("lost defaults: %+v", catalog)
	}
	aliases := map[string][]string{}
	policies := map[string]bool{}
	for _, policy := range catalog.Strategies.Strategies {
		policies[policy.Name] = true
		if policy.Name == "alias" {
			for _, alias := range policy.Aliases {
				for _, model := range alias.Models {
					aliases[alias.Alias] = append(aliases[alias.Alias], model.Model)
				}
			}
		}
	}
	for _, policy := range []string{"alias", "prefer_openai", "adaptive"} {
		if !policies[policy] {
			t.Fatalf("missing policy %s", policy)
		}
	}
	if len(aliases["smart"]) != 2 || strings.Join(aliases["codex"], ",") != "openai/default" || strings.Join(aliases["claude"], ",") != "anthropic/default" {
		t.Fatalf("lost model choices: %+v", aliases)
	}
	w = request(h, r, "GET", "/api/models", "", func(r *http.Request) { r.Header.Del("Authorization") })
	if w.Code != http.StatusUnauthorized {
		t.Fatal("model catalog requires a local authenticated session")
	}
}

func TestMonitorHasNoProjectOrSubmissionAPI(t *testing.T) {
	h, _, r := testHandler(t)
	for _, path := range []string{"/api/projects/create", "/api/projects/register", "/api/goals"} {
		w := request(h, r, "POST", path, `{}`, nil)
		if w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("unexpected submission route %s: %d", path, w.Code)
		}
	}
	w := request(h, r, "GET", "/projects?new=1", "", nil)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/" {
		t.Fatal("old app links should lead to the work monitor")
	}
}
