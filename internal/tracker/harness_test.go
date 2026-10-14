package tracker

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHarnessIdentityRetentionIsolationAndPersistence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "harness.db")
	s, err := Open(path, 90*time.Minute, 5*time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	pid := Text(project(t, s, "Desktop project"), "id")
	other := Text(project(t, s, "OpenCode project"), "id")
	if _, err = s.ReportWork(ctx, pid, Doc{"agent": "worker", "provider": "Z.ai", "model": "reported-glm", "harness": " Codex Desktop "}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReportWork(ctx, other, Doc{"agent": "worker", "provider": "Z.ai", "harness": "OpenCode"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReportWork(ctx, pid, Doc{"agent": "worker", "workingOn": "New milestone"}); err != nil {
		t.Fatal(err)
	}
	// A shared usage reading must not replace project-specific harness identity.
	if _, err = s.SaveUsage(ctx, "worker", Doc{"harness": "Claude Code", "tokensRemaining": float64(2000)}); err != nil {
		t.Fatal(err)
	}
	for projectID, want := range map[string]string{pid: "Codex Desktop", other: "OpenCode"} {
		view, err := s.Summary(ctx, projectID, "", "")
		if err != nil {
			t.Fatal(err)
		}
		d := view["projects"].([]Doc)[0]
		identity, err := s.Get(ctx, "presence", projectID+"/worker")
		if err != nil {
			t.Fatal(err)
		}
		if Text(d["agents"].([]Doc)[0], "harness") != want || Text(identity, "harness") != want {
			t.Fatal("harness crossed project boundary", d)
		}
	}
	if _, err = s.SaveUsage(ctx, "usage-only", Doc{"harness": "Codex CLI"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReportWork(ctx, pid, Doc{"agent": "usage-only"}); err != nil {
		t.Fatal(err)
	}
	activity, err := s.Get(ctx, "presence", pid+"/usage-only")
	activity = must(t, activity, err)
	if Text(activity, "harness") != "Codex CLI" {
		t.Fatal("usage fallback lost", activity)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path, 90*time.Minute, 5*time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	activity, err = s.Get(ctx, "presence", pid+"/worker")
	activity = must(t, activity, err)
	if Text(activity, "harness") != "Codex Desktop" || Text(activity, "provider") != "Z.ai" {
		t.Fatal(activity)
	}
	head, _ := s.Feed(ctx, FeedOptions{Limit: 100})
	for _, harness := range []any{true, strings.Repeat("x", 129)} {
		if _, err = s.ReportWork(ctx, pid, Doc{"agent": "worker", "harness": harness}); err == nil {
			t.Fatal("invalid harness accepted")
		}
		if _, err = s.SaveUsage(ctx, "worker", Doc{"harness": harness}); err == nil {
			t.Fatal("invalid usage harness accepted")
		}
	}
	after, _ := s.Feed(ctx, FeedOptions{Limit: 100})
	if after.LatestCursor != head.LatestCursor {
		t.Fatal("invalid identity published updates")
	}
}
