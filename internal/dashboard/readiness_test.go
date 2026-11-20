package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

type fakeReadiness struct {
	forced bool
	calls  int
}

func (f *fakeReadiness) Current() *Readiness {
	return &Readiness{State: "unchecked", Label: "Waiting for Dot"}
}
func (f *fakeReadiness) Check(_ context.Context, force bool) (*Readiness, error) {
	f.forced = force
	f.calls++
	return f.Current(), nil
}

func TestReadinessSharesSnapshotAndKeepsAuthenticatedBoundary(t *testing.T) {
	_, service, record := testHandler(t)
	checks := &fakeReadiness{}
	h := newHandler(Options{Jobs: service, Readiness: checks}, record)
	for _, method := range []string{"GET", "POST"} {
		path := "/api/readiness"
		if method == "POST" {
			path += "/check"
		}
		w := request(h, record, method, path, "{}", func(r *http.Request) { r.Header.Del("Authorization") })
		if w.Code != http.StatusUnauthorized || checks.calls != 0 {
			t.Fatal("unauthenticated probe ran")
		}
	}
	for _, method := range []string{"GET", "POST"} {
		path := "/api/readiness"
		if method == "POST" {
			path += "/check"
		}
		w := request(h, record, method, path, "{}", nil)
		if w.Code != http.StatusOK || checks.forced != (method == "POST") {
			t.Fatalf("probe %s: %d", method, w.Code)
		}
	}
	var info struct {
		Readiness *Readiness `json:"readiness"`
	}
	w := request(h, record, "GET", "/api/info", "", nil)
	if json.Unmarshal(w.Body.Bytes(), &info) != nil || info.Readiness == nil || info.Readiness.State != "unchecked" || checks.calls != 2 {
		t.Fatal("status did not use the shared cache")
	}
	if service.resumed != nil || service.cancelled != "" {
		t.Fatal("readiness changed running work")
	}
}
