package tracker

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

const LessonKinds = "failed_approach miscalculation misunderstanding user_preference"

func lessonIndex(d Doc) Doc {
	out := clone(d)
	for _, key := range []string{"attempt", "outcome", "correction", "evidence"} {
		delete(out, key)
	}
	return out
}

func (s *Store) Lesson(ctx context.Context, pid, id string) (Doc, error) {
	d, err := s.Get(ctx, "lesson", id)
	if err == nil && Text(d, "projectId") != pid {
		return nil, &Error{404, "Unknown project lesson"}
	}
	return d, err
}

func (s *Store) SaveLesson(ctx context.Context, pid, id string, payload Doc) (Doc, error) {
	return s.write(ctx, func(tx *sql.Tx) (Doc, error) { return s.saveLessonTx(ctx, tx, pid, id, payload) })
}

func (s *Store) saveLessonTx(ctx context.Context, tx *sql.Tx, pid, id string, payload Doc) (Doc, error) {
	allowed := "title summary kind status confidence attempt outcome correction evidence tags taskId agent"
	create := id == ""
	if create {
		allowed += " externalKey"
	}
	if err := fields(payload, allowed); err != nil {
		return nil, err
	}
	if err := texts(payload, "", map[string]int{"title": 200, "summary": 2000, "attempt": 32768, "outcome": 32768, "correction": 32768, "evidence": 32768, "kind": 32, "status": 16, "confidence": 16, "taskId": 64, "agent": 128, "externalKey": 128}); err != nil {
		return nil, err
	}
	p, err := get(ctx, tx, "project", pid)
	if err != nil {
		return nil, err
	}
	var d Doc
	if create {
		// One serialized transaction makes retries idempotent for this project.
		if key := Text(payload, "externalKey"); key != "" {
			var data []byte
			err = tx.QueryRowContext(ctx, `SELECT document FROM entities WHERE kind='lesson' AND project_id=? AND json_extract(document,'$.externalKey')=?`, pid, key).Scan(&data)
			if err == nil {
				err = json.Unmarshal(data, &d)
				return d, err
			}
			if err != sql.ErrNoRows {
				return nil, err
			}
		}
		id = uuid.NewString()
		d = Doc{"id": id, "projectId": pid, "title": "", "summary": "", "kind": "failed_approach", "status": "active", "confidence": "observed", "attempt": "", "outcome": "", "correction": "", "evidence": "", "tags": []any{}, "taskId": "", "agent": "", "externalKey": "", "revision": float64(0), "createdAt": stamp(s.Now())}
	} else {
		d, err = get(ctx, tx, "lesson", id)
		if err != nil {
			return nil, err
		}
		if Text(d, "projectId") != pid {
			return nil, &Error{404, "Unknown project lesson"}
		}
		if _, ok := payload["expectedRevision"]; !ok {
			return nil, bad("expectedRevision is required to update a lesson")
		}
		if err = revision(d, payload, false); err != nil {
			return nil, err
		}
	}
	merge(d, payload)
	if err = texts(d, "title correction", nil); err != nil {
		return nil, err
	}
	for key, values := range map[string]string{"kind": LessonKinds, "status": "active superseded", "confidence": "observed confirmed"} {
		if err = oneOf(d, key, values); err != nil {
			return nil, err
		}
	}
	tags, valid := d["tags"].([]any)
	if !valid || len(tags) > 32 {
		return nil, bad("tags must be an array of at most 32 strings")
	}
	for _, value := range tags {
		tag, ok := value.(string)
		if !ok || strings.TrimSpace(tag) == "" || len(tag) > 64 {
			return nil, bad("tags must contain nonempty strings up to 64 bytes")
		}
	}
	if tid := Text(d, "taskId"); tid != "" {
		task, err := get(ctx, tx, "task", tid)
		if err != nil {
			return nil, err
		}
		if Text(task, "projectId") != pid {
			return nil, bad("Lesson task must belong to this project")
		}
	}
	actor := Text(payload, "actor")
	if actor == "" {
		actor = Text(d, "agent")
	}
	d["revision"] = Num(d, "revision") + 1
	d["updatedAt"], d["updatedBy"] = stamp(s.Now()), actor
	if err = put(ctx, tx, "lesson", id, pid, d); err != nil {
		return nil, err
	}
	if err = put(ctx, tx, "lesson-revision", fmt.Sprintf("%s/%d", id, int64(Num(d, "revision"))), pid, d); err != nil {
		return nil, err
	}
	s.touch(p)
	if err = put(ctx, tx, "project", pid, "", p); err != nil {
		return nil, err
	}
	kind := "lesson.updated"
	if create {
		kind = "lesson.created"
	}
	n, err := s.event(ctx, tx, kind, actor, pid, Text(d, "taskId"), "", Text(d, "title"), "")
	if err == nil {
		_, err = tx.ExecContext(ctx, "UPDATE notifications SET document=json_set(document,'$.lessonId',?) WHERE number=?", id, n)
	}
	return d, err
}

// Index and history queries are scoped before decoding and omit long text.
func (s *Store) Lessons(ctx context.Context, pid string, filters map[string]string) ([]Doc, error) {
	if _, err := s.Get(ctx, "project", pid); err != nil {
		return nil, err
	}
	for key, values := range map[string]string{"kind": LessonKinds, "status": "active superseded", "confidence": "observed confirmed"} {
		if v := filters[key]; v != "" && v != "all" {
			if err := oneOf(Doc{key: v}, key, values); err != nil {
				return nil, err
			}
		}
	}
	rows, err := s.db.QueryContext(ctx, "SELECT document FROM entities WHERE kind='lesson' AND project_id=? ORDER BY json_extract(document,'$.updatedAt') DESC,id", pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Doc{}
	for rows.Next() {
		var data []byte
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		var d Doc
		if err = json.Unmarshal(data, &d); err != nil {
			return nil, err
		}
		match := true
		for _, key := range []string{"kind", "status", "confidence", "agent", "taskId"} {
			if v := filters[key]; v != "" && !(v == "all" && (key == "kind" || key == "status" || key == "confidence")) && Text(d, key) != v {
				match = false
			}
		}
		tags := []string{}
		for _, value := range d["tags"].([]any) {
			tags = append(tags, value.(string))
		}
		if tag := filters["tag"]; tag != "" && !containsTag(tags, tag) {
			match = false
		}
		hay := strings.ToLower(Text(d, "title") + " " + Text(d, "summary") + " " + strings.Join(tags, " "))
		if !strings.Contains(hay, strings.ToLower(filters["q"])) {
			match = false
		}
		if match {
			out = append(out, lessonIndex(d))
		}
	}
	return out, rows.Err()
}

func containsTag(tags []string, tag string) bool {
	for _, value := range tags {
		if value == tag {
			return true
		}
	}
	return false
}

func (s *Store) LessonHistory(ctx context.Context, pid, id string, rev int64) (any, error) {
	if _, err := s.Lesson(ctx, pid, id); err != nil {
		return nil, err
	}
	if rev > 0 {
		return s.Get(ctx, "lesson-revision", fmt.Sprintf("%s/%d", id, rev))
	}
	rows, err := s.db.QueryContext(ctx, `SELECT document FROM entities WHERE kind='lesson-revision' AND project_id=? AND json_extract(document,'$.id')=? ORDER BY json_extract(document,'$.revision')`, pid, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Doc{}
	for rows.Next() {
		var data []byte
		if err = rows.Scan(&data); err != nil {
			return nil, err
		}
		var d Doc
		if err = json.Unmarshal(data, &d); err != nil {
			return nil, err
		}
		out = append(out, lessonIndex(d))
	}
	return out, rows.Err()
}
