package tracker

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestAgentActivityVendorsSharedTasksAndExpiry(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 13, 10, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { return now }
	p := project(t, s, "Activity")
	q := project(t, s, "Other")
	pid := Text(p, "id")
	task, err := s.SaveTask(ctx, pid, "", Doc{"name": "Shared work", "status": "active"})
	task = must(t, task, err)
	tid := Text(task, "id")
	for _, agent := range []string{"builder-1", "builder-2"} {
		_, err = s.ReportWork(ctx, pid, Doc{"agent": agent, "provider": "openai", "tasks": []any{map[string]any{"taskId": tid, "workingOn": "Implementing core logic"}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = s.ReportWork(ctx, pid, Doc{"agent": "reviewer", "provider": "Claude", "workingOn": "Reviewing project scope"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ReportWork(ctx, Text(q, "id"), Doc{"agent": "builder-1", "provider": "OpenAI"})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := s.Summary(ctx, pid, "", "")
	summary = must(t, summary, err)
	if len(summary["tasks"].([]Doc)[0]["activeAgents"].([]Doc)) != 2 {
		t.Fatal("shared task must list both active agents")
	}
	view, err := s.ProjectAgents(ctx, pid)
	view = must(t, view, err)
	counts := map[string]int{}
	for _, vendor := range view["vendors"].([]Doc) {
		counts[Text(vendor, "provider")] = int(Num(vendor, "activeCount"))
	}
	if counts["OpenAI"] != 2 || counts["Anthropic"] != 1 {
		t.Fatal(counts)
	}
	now = now.Add(59 * time.Minute)
	view, err = s.ProjectAgents(ctx, pid)
	view = must(t, view, err)
	if view["agents"].([]Doc)[0]["active"] != true {
		t.Fatal("expired early")
	}
	now = now.Add(time.Minute)
	summary, err = s.Summary(ctx, pid, "", "")
	summary = must(t, summary, err)
	if len(summary["tasks"].([]Doc)[0]["activeAgents"].([]Doc)) != 0 {
		t.Fatal("activity did not expire at exact hour")
	}
	f, _ := s.Feed(ctx, FeedOptions{Limit: 100})
	s.Sweep(ctx)
	after, _ := s.Feed(ctx, FeedOptions{Limit: 100})
	if len(after.Notifications)-len(f.Notifications) != 4 {
		t.Fatal("expiry should notify once per project/agent")
	}
	s.Sweep(ctx)
	again, _ := s.Feed(ctx, FeedOptions{Limit: 100})
	if again.LatestCursor != after.LatestCursor {
		t.Fatal("duplicate expiry notifications")
	}
}

func TestBatchedWorkAtomicityStopAndRenewal(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	p := project(t, s, "Batch")
	q := project(t, s, "Foreign")
	pid := Text(p, "id")
	task, err := s.SaveTask(ctx, pid, "", Doc{"name": "Build"})
	task = must(t, task, err)
	tid := Text(task, "id")
	foreign, err := s.SaveTask(ctx, Text(q, "id"), "", Doc{"name": "Other"})
	foreign = must(t, foreign, err)
	report := Doc{"agent": "builder", "provider": "OpenAI", "workingOn": "Building the API", "taskUpdates": []any{map[string]any{"taskId": tid, "percent": float64(40), "status": "active", "detail": "API implementation"}}, "usage": map[string]any{"tokensRemaining": float64(5000)}, "message": "Ready for initial review"}
	result, err := s.ReportWork(ctx, pid, report)
	result = must(t, result, err)
	if Num(result, "cursor") == 0 {
		t.Fatal(result)
	}
	updated, err := s.Task(ctx, pid, tid)
	updated = must(t, updated, err)
	if Num(updated, "percent") != 40 {
		t.Fatal(updated)
	}
	activity, err := s.ProjectAgents(ctx, pid)
	activity = must(t, activity, err)
	if len(activity["agents"].([]Doc)[0]["tasks"].([]any)) != 1 {
		t.Fatal("batch did not attach task")
	}
	head, _ := s.Feed(ctx, FeedOptions{Limit: 100})
	report["taskUpdates"] = []any{map[string]any{"taskId": tid, "percent": float64(70)}, map[string]any{"taskId": Text(foreign, "id"), "percent": float64(70)}}
	if _, err = s.ReportWork(ctx, pid, report); err == nil {
		t.Fatal("cross-project batch accepted")
	}
	updated, err = s.Task(ctx, pid, tid)
	updated = must(t, updated, err)
	if Num(updated, "percent") != 40 {
		t.Fatal("failed batch partially committed")
	}
	after, _ := s.Feed(ctx, FeedOptions{Limit: 100})
	if head.LatestCursor != after.LatestCursor {
		t.Fatal("failed batch emitted notifications")
	}
	_, err = s.ReportWork(ctx, pid, Doc{"agent": "builder", "active": false})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := s.Summary(ctx, pid, "", "")
	summary = must(t, summary, err)
	if len(summary["tasks"].([]Doc)[0]["activeAgents"].([]Doc)) != 0 {
		t.Fatal("stopped agent remained on task")
	}
	_, err = s.ReportWork(ctx, pid, Doc{"agent": "builder"})
	if err != nil {
		t.Fatal(err)
	}
	summary, err = s.Summary(ctx, pid, "", "")
	summary = must(t, summary, err)
	if len(summary["tasks"].([]Doc)[0]["activeAgents"].([]Doc)) != 1 {
		t.Fatal("renewal lost task assignments")
	}
	_, err = s.SaveTask(ctx, pid, tid, Doc{"status": "done"})
	if err != nil {
		t.Fatal(err)
	}
	summary, err = s.Summary(ctx, pid, "", "")
	summary = must(t, summary, err)
	if len(summary["tasks"].([]Doc)[0]["activeAgents"].([]Doc)) != 0 {
		t.Fatal("completed task throbber still active")
	}
}

func TestPartialBatchKeepsOtherAssignmentsAndRevisionConflictRollsBack(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	pid := Text(project(t, s, "Partial"), "id")
	a, e := s.SaveTask(ctx, pid, "", Doc{"name": "First"})
	a = must(t, a, e)
	b, e := s.SaveTask(ctx, pid, "", Doc{"name": "Second"})
	b = must(t, b, e)
	_, e = s.ReportWork(ctx, pid, Doc{"agent": "worker", "tasks": []any{map[string]any{"taskId": Text(a, "id")}, map[string]any{"taskId": Text(b, "id")}}})
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.ReportWork(ctx, pid, Doc{"agent": "worker", "taskUpdates": []any{map[string]any{"taskId": Text(a, "id"), "percent": float64(30)}}})
	if e != nil {
		t.Fatal(e)
	}
	agents, e := s.ProjectAgents(ctx, pid)
	agents = must(t, agents, e)
	if len(agents["agents"].([]Doc)[0]["tasks"].([]any)) != 2 {
		t.Fatal("partial batch dropped other work")
	}
	head, _ := s.Feed(ctx, FeedOptions{Limit: 100})
	_, e = s.ReportWork(ctx, pid, Doc{"agent": "worker", "usage": map[string]any{"tokensRemaining": float64(10)}, "taskUpdates": []any{map[string]any{"taskId": Text(a, "id"), "percent": float64(90), "expectedRevision": float64(1)}}})
	if e == nil {
		t.Fatal("stale batch revision accepted")
	}
	if _, e = s.Get(ctx, "agent", "worker"); e == nil {
		t.Fatal("usage committed despite conflict")
	}
	after, _ := s.Feed(ctx, FeedOptions{Limit: 100})
	if after.LatestCursor != head.LatestCursor {
		t.Fatal("conflict emitted events")
	}
}

func TestAgentActivityPersistenceAndNoOwnershipInference(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db")
	now := time.Now()
	s, err := Open(path, 90*time.Minute, 5*time.Minute, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	p := project(t, s, "Persist")
	pid := Text(p, "id")
	task, err := s.SaveTask(ctx, pid, "", Doc{"name": "Owned", "agent": "builder", "status": "active"})
	task = must(t, task, err)
	summary, err := s.Summary(ctx, pid, "", "")
	summary = must(t, summary, err)
	if len(summary["tasks"].([]Doc)[0]["activeAgents"].([]Doc)) != 0 {
		t.Fatal("static ownership inferred active presence")
	}
	_, err = s.ReportWork(ctx, pid, Doc{"agent": "builder", "tasks": []any{map[string]any{"taskId": Text(task, "id")}}})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path, 90*time.Minute, 5*time.Minute, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	summary, err = s.Summary(ctx, pid, "", "")
	summary = must(t, summary, err)
	if len(summary["tasks"].([]Doc)[0]["activeAgents"].([]Doc)) != 1 {
		t.Fatal("presence lost across restart")
	}
	s.Close()
	now = now.Add(time.Hour)
	s, err = Open(path, 90*time.Minute, 5*time.Minute, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	agents, err := s.ProjectAgents(ctx, pid)
	agents = must(t, agents, err)
	if agents["agents"].([]Doc)[0]["active"] != false {
		t.Fatal("restart did not reconcile expired agent")
	}
	feed, err := s.Feed(ctx, FeedOptions{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	expired := 0
	for _, n := range feed.Notifications {
		if Text(n, "kind") == "agent.inactive" {
			expired++
		}
	}
	if expired != 1 {
		t.Fatal("restart must publish one inactivity event", expired)
	}
}

func TestProjectUsageIncludesPresenceAndUnknownTaskOwners(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	pid := Text(project(t, s, "Limits"), "id")
	if _, e := s.SaveTask(ctx, pid, "", Doc{"name": "Owned", "agent": "unknown-owner"}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ReportWork(ctx, pid, Doc{"agent": "reviewer", "provider": "OpenAI", "usage": map[string]any{"tokenBudget": float64(10000), "tokensUsed": float64(2000), "contextTokens": float64(1000), "contextLimit": float64(5000), "accountRemainingPercent": float64(45)}}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SaveUsage(ctx, "unrelated-agent", Doc{"tokensRemaining": float64(99)}); e != nil {
		t.Fatal(e)
	}
	view, e := s.Summary(ctx, pid, "", "")
	view = must(t, view, e)
	agents := view["projects"].([]Doc)[0]["agents"].([]Doc)
	if len(agents) != 2 || Text(agents[0], "agent") != "reviewer" || Num(agents[0], "tokensRemaining") != 8000 || Num(agents[0], "accountRemainingPercent") != 45 {
		t.Fatal("presence-only agent usage missing or unrelated usage leaked", agents)
	}
	if agents[1]["reported"] != false || agents[1]["tokensRemaining"] != nil {
		t.Fatal("missing usage was not explicitly unknown", agents[1])
	}
	if _, e = s.Get(ctx, "agent", "unknown-owner"); e == nil {
		t.Fatal("summary persisted a fabricated usage observation")
	}
	if _, e = s.ReportWork(ctx, pid, Doc{"agent": "reviewer", "active": false}); e != nil {
		t.Fatal(e)
	}
	view, e = s.Summary(ctx, pid, "", "")
	view = must(t, view, e)
	if Num(view["projects"].([]Doc)[0]["agents"].([]Doc)[0], "tokensRemaining") != 8000 {
		t.Fatal("stopping activity hid the last usage reading")
	}
}
