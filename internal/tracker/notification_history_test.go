package tracker

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestClearNotificationsPreservesEntitiesAndSequence(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "history.db")
	s, err := Open(path, 90*time.Minute, 5*time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := project(t, s, "History")
	pid := Text(p, "id")
	task, err := s.SaveTask(ctx, pid, "", Doc{"name": "Keep this task"})
	task = must(t, task, err)
	lesson, err := s.SaveLesson(ctx, pid, "", Doc{"title": "Keep this lesson", "correction": "Retain entity state"})
	lesson = must(t, lesson, err)
	article, err := s.SaveKnowledge(ctx, "", Doc{"slug": "keep", "title": "Keep knowledge", "body": "Retain revisions"})
	article = must(t, article, err)
	notice, err := s.RequestInput(ctx, Doc{"projectId": pid, "agent": "builder"})
	notice = must(t, notice, err)
	instruction, err := s.SaveInstruction(ctx, "", Doc{"key": "keep-guidance", "title": "Keep", "message": "Keep independent instructions"})
	instruction = must(t, instruction, err)
	entities := []struct{ kind, id string }{{"project", pid}, {"task", Text(task, "id")}, {"lesson", Text(lesson, "id")}, {"knowledge", Text(article, "slug")}, {"input", Text(notice, "id")}, {"instruction", Text(instruction, "key")}}
	before := [][]byte{}
	for _, entry := range entities {
		d, err := s.Get(ctx, entry.kind, entry.id)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(d)
		before = append(before, b)
	}
	head, err := s.Feed(ctx, FeedOptions{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	count, err := ClearNotificationHistory(ctx, path)
	if err != nil || count != int64(len(head.Notifications)) {
		t.Fatal(count, err)
	}
	cleared, err := s.Feed(ctx, FeedOptions{Since: head.LatestCursor, Limit: 100, ProjectID: pid})
	if err != nil || len(cleared.Notifications) != 0 || cleared.LatestCursor != head.LatestCursor || cleared.NextCursor != head.LatestCursor {
		t.Fatal("clear broke held cursor", cleared, err)
	}
	all, err := s.Feed(ctx, FeedOptions{Limit: 100})
	if err != nil || len(all.Notifications) != 0 {
		t.Fatal("history not empty", all, err)
	}
	for i, entry := range entities {
		d, err := s.Get(ctx, entry.kind, entry.id)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(d)
		if string(b) != string(before[i]) {
			t.Fatal("clear changed entity", entry.kind)
		}
	}
	if _, err = s.LessonHistory(ctx, pid, Text(lesson, "id"), 1); err != nil {
		t.Fatal(err)
	}
	if _, err = s.KnowledgeHistory(ctx, "keep", 1); err != nil {
		t.Fatal(err)
	}
	message, err := s.Message(ctx, Doc{"projectId": pid, "message": "New project update"})
	message = must(t, message, err)
	if Num(message, "number") <= float64(head.LatestCursor) {
		t.Fatal("notification numbers were reused")
	}
	newFeed, err := s.Feed(ctx, FeedOptions{Since: head.LatestCursor, Limit: 100, ProjectID: pid})
	if err != nil || len(newFeed.Notifications) != 1 {
		t.Fatal(newFeed, err)
	}
	missing := filepath.Join(t.TempDir(), "missing.db")
	if _, err = ClearNotificationHistory(ctx, missing); err == nil {
		t.Fatal("clear created a new database")
	}
}
