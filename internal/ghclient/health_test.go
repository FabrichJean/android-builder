package ghclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeGH struct {
	rateRemaining int
	repoCode      int
	private       bool
	archived      bool
	branchCode    int
	wfCode        int
	wfState       string
	billing       map[string]any // nil = 404
	runs          []fakeRun
}

type fakeRun struct {
	conclusion string
	age        time.Duration
	steps      int
}

func okGH() *fakeGH {
	return &fakeGH{rateRemaining: 4000, repoCode: 200, branchCode: 200, wfCode: 200, wfState: "active"}
}

func (f *fakeGH) server(t *testing.T) *Client {
	t.Helper()
	mux := http.NewServeMux()
	j := func(w http.ResponseWriter, code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(v)
	}
	mux.HandleFunc("/rate_limit", func(w http.ResponseWriter, r *http.Request) {
		j(w, 200, map[string]any{"resources": map[string]any{"core": map[string]any{"remaining": f.rateRemaining, "reset": time.Now().Add(time.Hour).Unix()}}})
	})
	mux.HandleFunc("/repos/o/r", func(w http.ResponseWriter, r *http.Request) {
		j(w, f.repoCode, map[string]any{"private": f.private, "archived": f.archived, "owner": map[string]any{"login": "o", "type": "User"}})
	})
	mux.HandleFunc("/repos/o/r/branches/main", func(w http.ResponseWriter, r *http.Request) { j(w, f.branchCode, map[string]any{}) })
	mux.HandleFunc("/repos/o/r/actions/workflows/build-apk.yml", func(w http.ResponseWriter, r *http.Request) {
		j(w, f.wfCode, map[string]any{"state": f.wfState})
	})
	mux.HandleFunc("/repos/o/r/actions/workflows/build-apk.yml/runs", func(w http.ResponseWriter, r *http.Request) {
		var rs []map[string]any
		for i, run := range f.runs {
			rs = append(rs, map[string]any{"id": i + 1, "conclusion": run.conclusion, "created_at": time.Now().Add(-run.age)})
		}
		j(w, 200, map[string]any{"workflow_runs": rs})
	})
	mux.HandleFunc("/repos/o/r/actions/runs/", func(w http.ResponseWriter, r *http.Request) {
		id := int(r.URL.Path[strings.LastIndex(r.URL.Path[:len(r.URL.Path)-5], "/")+1:][0] - '0')
		steps := make([]any, f.runs[id-1].steps)
		for i := range steps {
			steps[i] = map[string]any{"name": "x"}
		}
		j(w, 200, map[string]any{"jobs": []any{map[string]any{"steps": steps}}})
	})
	mux.HandleFunc("/users/o/settings/billing/actions", func(w http.ResponseWriter, r *http.Request) {
		if f.billing == nil {
			j(w, 404, map[string]any{})
			return
		}
		j(w, 200, f.billing)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := New("tok", "o", "r", "build-apk.yml")
	c.baseURL = srv.URL
	return c
}

func TestCheckHealth(t *testing.T) {
	startDead := fakeRun{conclusion: "failure", age: time.Hour, steps: 0}
	cases := []struct {
		name   string
		mutate func(f *fakeGH)
		wantOK bool
		want   string
	}{
		{"ok", func(f *fakeGH) {}, true, ""},
		{"token invalide", func(f *fakeGH) { f.repoCode = 401 }, false, "token GitHub invalide"},
		{"dépôt introuvable", func(f *fakeGH) { f.repoCode = 404 }, false, "introuvable"},
		{"dépôt archivé", func(f *fakeGH) { f.archived = true }, false, "archivé"},
		{"branche absente", func(f *fakeGH) { f.branchCode = 404 }, false, "branche"},
		{"workflow absent", func(f *fakeGH) { f.wfCode = 404 }, false, "introuvable dans le dépôt"},
		{"workflow désactivé", func(f *fakeGH) { f.wfState = "disabled_manually" }, false, "désactivé"},
		{"quota API bas", func(f *fakeGH) { f.rateRemaining = 10 }, false, "quota de l'API"},
		{"minutes épuisées (privé)", func(f *fakeGH) {
			f.private = true
			f.billing = map[string]any{"total_minutes_used": 2000, "total_paid_minutes_used": 0, "included_minutes": 2000}
		}, false, "minutes GitHub Actions"},
		{"minutes épuisées mais budget payant", func(f *fakeGH) {
			f.private = true
			f.billing = map[string]any{"total_minutes_used": 2100, "total_paid_minutes_used": 100, "included_minutes": 2000}
		}, true, ""},
		{"facturation inconnue (404) : on laisse passer", func(f *fakeGH) { f.private = true }, true, ""},
		{"public : minutes ignorées", func(f *fakeGH) {
			f.billing = map[string]any{"total_minutes_used": 9999, "included_minutes": 10}
		}, true, ""},
		{"2 runs morts au démarrage", func(f *fakeGH) { f.runs = []fakeRun{startDead, startDead} }, false, "avant de démarrer"},
		{"startup_failure x2", func(f *fakeGH) {
			f.runs = []fakeRun{{conclusion: "startup_failure", age: time.Hour}, {conclusion: "startup_failure", age: time.Hour}}
		}, false, "avant de démarrer"},
		{"1 seul run mort + 1 ok", func(f *fakeGH) { f.runs = []fakeRun{startDead, {conclusion: "success", age: time.Hour}} }, true, ""},
		{"échec normal (étapes exécutées)", func(f *fakeGH) {
			f.runs = []fakeRun{{conclusion: "failure", age: time.Hour, steps: 5}, {conclusion: "failure", age: time.Hour, steps: 5}}
		}, true, ""},
		{"runs morts mais anciens", func(f *fakeGH) {
			f.runs = []fakeRun{{conclusion: "startup_failure", age: 24 * time.Hour}, {conclusion: "startup_failure", age: 25 * time.Hour}}
		}, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := okGH()
			tc.mutate(f)
			h := f.server(t).CheckHealth(context.Background(), "main")
			if h.OK != tc.wantOK {
				t.Fatalf("OK=%v (raison: %q), attendu %v", h.OK, h.Reason, tc.wantOK)
			}
			if !tc.wantOK && !strings.Contains(h.Reason, tc.want) {
				t.Fatalf("raison %q ne contient pas %q", h.Reason, tc.want)
			}
		})
	}
}

func TestCheckHealthUnreachable(t *testing.T) {
	c := New("tok", "o", "r", "build-apk.yml")
	c.baseURL = "http://127.0.0.1:1"
	if h := c.CheckHealth(context.Background(), "main"); h.OK || !strings.Contains(h.Reason, "injoignable") {
		t.Fatalf("attendu injoignable, obtenu %+v", h)
	}
}
