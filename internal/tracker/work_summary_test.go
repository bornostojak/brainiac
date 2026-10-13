package tracker

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestWorkSummaryReportsReadsAndAtomicRollback(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	p, e := s.CreateProject(ctx, Doc{"name": "Live summary", "workSummary": "Preparing validation", "actor": "lead"})
	p = must(t, p, e)
	pid := Text(p, "id")
	if Text(p, "workSummaryBy") != "lead" || Text(p, "workSummaryAt") != stamp(now) {
		t.Fatal("missing summary provenance", p)
	}
	now = now.Add(time.Minute)
	_, e = s.ReportWork(ctx, pid, Doc{"agent": "lead", "workSummary": "Fixing request validation; checking edge cases next."})
	if e != nil {
		t.Fatal(e)
	}
	p, e = s.Get(ctx, "project", pid)
	p = must(t, p, e)
	at := Text(p, "workSummaryAt")
	now = now.Add(time.Minute)
	if _, e = s.ReportWork(ctx, pid, Doc{"agent": "worker", "workingOn": "Reviewing"}); e != nil {
		t.Fatal(e)
	}
	view, e := s.Summary(ctx, pid, "", "")
	view = must(t, view, e)
	current := view["projects"].([]Doc)[0]
	if Text(current, "workSummaryAt") != at || Text(current, "workSummaryBy") != "lead" {
		t.Fatal("reads or unrelated reports rewrote summary provenance")
	}
	head, _ := s.Feed(ctx, FeedOptions{Limit: 100})
	_, e = s.ReportWork(ctx, pid, Doc{"agent": "lead", "workSummary": "Must roll back", "inputRequest": map[string]any{"title": "Bad", "message": "Cross project", "taskId": "unknown"}})
	if e == nil {
		t.Fatal("invalid batch accepted")
	}
	after, _ := s.Get(ctx, "project", pid)
	feed, _ := s.Feed(ctx, FeedOptions{Limit: 100})
	if Text(after, "workSummary") != Text(p, "workSummary") || feed.LatestCursor != head.LatestCursor {
		t.Fatal("failed batch partially committed summary or events")
	}
	for _, value := range []any{true, strings.Repeat("x", 1001)} {
		if _, e = s.ReportWork(ctx, pid, Doc{"agent": "lead", "workSummary": value}); e == nil {
			t.Fatal("invalid summary accepted")
		}
	}
	if _, e = s.UpdateProject(ctx, pid, Doc{"workSummary": "Finished", "actor": "lead", "expectedRevision": Num(after, "revision")}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.UpdateProject(ctx, pid, Doc{"workSummary": "Overwrite", "expectedRevision": float64(1)}); e == nil {
		t.Fatal("stale project patch accepted")
	}
	if _, e = s.UpdateProject(ctx, pid, Doc{"workSummaryBy": "fake"}); e == nil {
		t.Fatal("server-managed summary author accepted")
	}
}
