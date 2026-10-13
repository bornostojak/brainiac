package tracker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

type Doc map[string]any
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string      { return e.Message }
func bad(message string) error      { return &Error{400, message} }
func conflict(message string) error { return &Error{409, message} }
func Text(d Doc, k string) string   { s, _ := d[k].(string); return s }
func Num(d Doc, k string) float64 {
	switch v := d[k].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	}
	return 0
}
func clone(d Doc) Doc          { b, _ := json.Marshal(d); var out Doc; _ = json.Unmarshal(b, &out); return out }
func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
func parse(s string) time.Time { t, _ := time.Parse(time.RFC3339Nano, s); return t }

type Store struct {
	db         *sql.DB
	mu         sync.Mutex
	changed    chan struct{}
	Now        func() time.Time
	Timeout    time.Duration
	StaleAfter time.Duration
}

func Open(path string, timeout, stale time.Duration, now func() time.Time) (*Store, error) {
	if timeout <= 0 || stale <= 0 {
		return nil, fmt.Errorf("timeouts must be positive")
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, changed: make(chan struct{}), Now: now, Timeout: timeout, StaleAfter: stale}
	if now == nil {
		s.Now = time.Now
	}
	if _, err = db.Exec(`PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL;`); err != nil {
		db.Close()
		return nil, err
	}
	tx, err := db.Begin()
	if err != nil {
		db.Close()
		return nil, err
	}
	defer tx.Rollback()
	var version int
	if err = tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		db.Close()
		return nil, err
	}
	if version > 1 {
		db.Close()
		return nil, fmt.Errorf("database schema %d is newer than this binary", version)
	}
	if version == 0 {
		_, err = tx.Exec(`CREATE TABLE entities(kind TEXT NOT NULL,id TEXT NOT NULL,project_id TEXT NOT NULL DEFAULT '',document TEXT NOT NULL CHECK(json_valid(document)),PRIMARY KEY(kind,id));
 CREATE INDEX entities_project ON entities(kind,project_id);
 CREATE UNIQUE INDEX project_external_key ON entities(json_extract(document,'$.externalKey')) WHERE kind='project' AND json_extract(document,'$.externalKey')!='';
 CREATE TABLE notifications(number INTEGER PRIMARY KEY AUTOINCREMENT,document TEXT NOT NULL CHECK(json_valid(document)));
 CREATE TABLE knowledge_revisions(slug TEXT NOT NULL,revision INTEGER NOT NULL,document TEXT NOT NULL CHECK(json_valid(document)),PRIMARY KEY(slug,revision));
 PRAGMA user_version=1;`)
		if err != nil {
			db.Close()
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		db.Close()
		return nil, err
	}
	if err = s.Sweep(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error                   { return s.db.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
func (s *Store) Signal() <-chan struct{}        { s.mu.Lock(); defer s.mu.Unlock(); return s.changed }

type reader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func get(ctx context.Context, q reader, kind, id string) (Doc, error) {
	var data []byte
	err := q.QueryRowContext(ctx, "SELECT document FROM entities WHERE kind=? AND id=?", kind, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, &Error{404, "Unknown " + kind}
	}
	if err != nil {
		return nil, err
	}
	var d Doc
	err = json.Unmarshal(data, &d)
	return d, err
}
func put(ctx context.Context, tx *sql.Tx, kind, id, pid string, d Doc) error {
	b, err := json.Marshal(d)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO entities(kind,id,project_id,document) VALUES(?,?,?,?) ON CONFLICT(kind,id) DO UPDATE SET document=excluded.document,project_id=excluded.project_id", kind, id, pid, string(b))
	return err
}
func (s *Store) Get(ctx context.Context, kind, id string) (Doc, error) {
	d, err := get(ctx, s.db, kind, id)
	if err == nil && kind == "agent" {
		s.decorateUsage(d)
	}
	return d, err
}
func (s *Store) List(ctx context.Context, kind string) ([]Doc, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT document FROM entities WHERE kind=? ORDER BY id", kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Doc{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		var d Doc
		if err = json.Unmarshal(b, &d); err != nil {
			return nil, err
		}
		if kind == "agent" {
			s.decorateUsage(d)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func (s *Store) write(ctx context.Context, fn func(*sql.Tx) (Doc, error)) (Doc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	out, err := fn(tx)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	close(s.changed)
	s.changed = make(chan struct{})
	return out, nil
}
func (s *Store) event(ctx context.Context, tx *sql.Tx, kind, actor, pid, tid, slug, message, recipient string) (int64, error) {
	d := Doc{"at": stamp(s.Now()), "kind": kind, "actor": actor, "projectId": pid, "taskId": tid, "knowledgeSlug": slug, "message": message, "recipient": recipient}
	b, _ := json.Marshal(d)
	res, err := tx.ExecContext(ctx, "INSERT INTO notifications(document) VALUES(?)", string(b))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
func (s *Store) touch(p Doc) {
	now := s.Now()
	p["lastActivityAt"] = stamp(now)
	p["activeUntil"] = stamp(now.Add(s.Timeout))
	if Text(p, "lifecycle") == "ongoing" {
		p["activity"] = "active"
	}
	p["updatedAt"] = stamp(now)
	p["revision"] = Num(p, "revision") + 1
}
func revision(d, payload Doc, markdown bool) error {
	v, ok := payload["expectedRevision"]
	if markdown && !ok {
		return bad("expectedRevision is required for Markdown replacements")
	}
	if ok {
		n, yes := v.(float64)
		if !yes || n < 1 || n != math.Trunc(n) {
			return bad("expectedRevision must be a positive integer")
		}
		if n != Num(d, "revision") {
			return conflict("Revision changed; fetch the latest entity and merge your update")
		}
	}
	return nil
}
func fields(payload Doc, allowed string) error {
	set := map[string]bool{"actor": true, "expectedRevision": true}
	for _, k := range strings.Fields(allowed) {
		set[k] = true
	}
	for k := range payload {
		if !set[k] {
			return bad("Unknown or immutable field: " + k)
		}
	}
	if actor, ok := payload["actor"]; ok {
		if _, ok = actor.(string); !ok || len(Text(payload, "actor")) > 128 {
			return bad("actor must be text up to 128 bytes")
		}
	}
	return nil
}
func texts(d Doc, required string, limits map[string]int) error {
	for k, max := range limits {
		v, ok := d[k]
		if !ok {
			continue
		}
		t, yes := v.(string)
		if !yes || len(t) > max {
			return bad(fmt.Sprintf("%s must be text up to %d bytes", k, max))
		}
	}
	for _, k := range strings.Fields(required) {
		if strings.TrimSpace(Text(d, k)) == "" {
			return bad(k + " must be nonempty text")
		}
	}
	return nil
}
func oneOf(d Doc, k string, values string) error {
	for _, v := range strings.Fields(values) {
		if Text(d, k) == v {
			return nil
		}
	}
	return bad("Invalid " + k)
}
func merge(d, p Doc) {
	for k, v := range p {
		if k != "actor" && k != "expectedRevision" {
			d[k] = v
		}
	}
}

var projectLimits = map[string]int{"name": 200, "description": 2000, "readme": 65536, "externalKey": 1024}
var taskLimits = map[string]int{"name": 200, "detail": 2000, "next": 2000, "verification": 2000, "readme": 65536, "agent": 128, "parentId": 64}

func (s *Store) CreateProject(ctx context.Context, payload Doc) (Doc, error) {
	if err := fields(payload, "name description readme externalKey lifecycle activity"); err != nil {
		return nil, err
	}
	return s.write(ctx, func(tx *sql.Tx) (Doc, error) {
		id := uuid.NewString()
		d := Doc{"id": id, "name": "", "description": "", "readme": "", "externalKey": "", "lifecycle": "ongoing", "activity": "inactive", "revision": float64(0), "createdAt": stamp(s.Now())}
		merge(d, payload)
		if err := validateProject(d); err != nil {
			return nil, err
		}
		if Text(d, "externalKey") != "" {
			var n int
			if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM entities WHERE kind='project' AND json_extract(document,'$.externalKey')=?", Text(d, "externalKey")).Scan(&n); err != nil {
				return nil, err
			}
			if n > 0 {
				return nil, conflict("externalKey already belongs to a project")
			}
		}
		s.touch(d)
		if _, explicit := payload["activity"]; explicit {
			d["activity"] = payload["activity"]
		}
		if Text(d, "lifecycle") == "archived" {
			d["activity"] = "inactive"
		}
		if err := put(ctx, tx, "project", id, "", d); err != nil {
			return nil, err
		}
		_, err := s.event(ctx, tx, "project.created", Text(payload, "actor"), id, "", "", Text(d, "name"), "")
		return d, err
	})
}
func validateProject(d Doc) error {
	if err := texts(d, "name", projectLimits); err != nil {
		return err
	}
	if err := oneOf(d, "lifecycle", "ongoing completed archived"); err != nil {
		return err
	}
	return oneOf(d, "activity", "active inactive")
}
func (s *Store) UpdateProject(ctx context.Context, id string, payload Doc) (Doc, error) {
	if err := fields(payload, "name description readme lifecycle activity"); err != nil {
		return nil, err
	}
	return s.write(ctx, func(tx *sql.Tx) (Doc, error) {
		d, err := get(ctx, tx, "project", id)
		if err != nil {
			return nil, err
		}
		_, md := payload["readme"]
		if err = revision(d, payload, md); err != nil {
			return nil, err
		}
		merge(d, payload)
		if err = validateProject(d); err != nil {
			return nil, err
		}
		s.touch(d)
		if _, ok := payload["activity"]; ok {
			d["activity"] = payload["activity"]
		}
		if Text(d, "lifecycle") == "archived" {
			d["activity"] = "inactive"
		}
		if err = put(ctx, tx, "project", id, "", d); err != nil {
			return nil, err
		}
		_, err = s.event(ctx, tx, "project.updated", Text(payload, "actor"), id, "", "", Text(d, "name"), "")
		return d, err
	})
}
func (s *Store) Heartbeat(ctx context.Context, id string, payload Doc) (Doc, error) {
	if err := fields(payload, ""); err != nil {
		return nil, err
	}
	return s.write(ctx, func(tx *sql.Tx) (Doc, error) {
		d, err := get(ctx, tx, "project", id)
		if err != nil {
			return nil, err
		}
		if err = revision(d, payload, false); err != nil {
			return nil, err
		}
		s.touch(d)
		if err = put(ctx, tx, "project", id, "", d); err != nil {
			return nil, err
		}
		_, err = s.event(ctx, tx, "project.heartbeat", Text(payload, "actor"), id, "", "", "Activity refreshed", "")
		return d, err
	})
}
func validateTask(d Doc) error {
	if err := texts(d, "name", taskLimits); err != nil {
		return err
	}
	if err := oneOf(d, "status", "pending partial active done blocked"); err != nil {
		return err
	}
	n, ok := d["percent"].(float64)
	if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 100 {
		return bad("percent must be a finite number from 0 through 100")
	}
	return nil
}
func (s *Store) Task(ctx context.Context, pid, tid string) (Doc, error) {
	d, err := s.Get(ctx, "task", tid)
	if err == nil && Text(d, "projectId") != pid {
		return nil, &Error{404, "Unknown task in this project"}
	}
	return d, err
}
func (s *Store) SaveTask(ctx context.Context, pid, tid string, payload Doc) (Doc, error) {
	allowed := "name status percent agent detail next verification readme"
	if tid == "" {
		allowed += " parentId"
	}
	if err := fields(payload, allowed); err != nil {
		return nil, err
	}
	return s.write(ctx, func(tx *sql.Tx) (Doc, error) {
		p, err := get(ctx, tx, "project", pid)
		if err != nil {
			return nil, err
		}
		create := tid == ""
		var d Doc
		if create {
			tid = uuid.NewString()
			d = Doc{"id": tid, "projectId": pid, "parentId": "", "name": "", "status": "pending", "percent": float64(0), "agent": "", "detail": "", "next": "", "verification": "", "readme": "", "revision": float64(0), "createdAt": stamp(s.Now())}
		} else {
			d, err = get(ctx, tx, "task", tid)
			if err != nil {
				return nil, err
			}
			if Text(d, "projectId") != pid {
				return nil, &Error{404, "Unknown task in this project"}
			}
			_, md := payload["readme"]
			if err = revision(d, payload, md); err != nil {
				return nil, err
			}
		}
		merge(d, payload)
		if err = validateTask(d); err != nil {
			return nil, err
		}
		if parent := Text(d, "parentId"); parent != "" {
			pd, err := get(ctx, tx, "task", parent)
			if err != nil {
				return nil, bad("Unknown parent task")
			}
			if Text(pd, "projectId") != pid {
				return nil, bad("Parent must belong to this project")
			}
		}
		d["revision"] = Num(d, "revision") + 1
		d["updatedAt"] = stamp(s.Now())
		s.touch(p)
		if err = put(ctx, tx, "task", tid, pid, d); err != nil {
			return nil, err
		}
		if err = put(ctx, tx, "project", pid, "", p); err != nil {
			return nil, err
		}
		kind := "task.updated"
		if create {
			kind = "task.created"
		}
		_, err = s.event(ctx, tx, kind, Text(payload, "actor"), pid, tid, "", fmt.Sprintf("%s · %s · %g%%", Text(d, "name"), Text(d, "status"), Num(d, "percent")), "")
		return d, err
	})
}

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,95}$`)

func validateKnowledge(d Doc) error {
	if !slugPattern.MatchString(Text(d, "slug")) {
		return bad("slug must be lowercase letters, digits and hyphens, up to 96 characters")
	}
	if err := texts(d, "title body", map[string]int{"title": 200, "summary": 2000, "body": 131072, "sourceProjectId": 64, "sourceTaskId": 64, "sourceAgent": 128}); err != nil {
		return err
	}
	tags, ok := d["tags"].([]any)
	if !ok || len(tags) > 32 {
		return bad("tags must be an array of at most 32 strings")
	}
	for _, tag := range tags {
		t, ok := tag.(string)
		if !ok || len(t) == 0 || len(t) > 64 {
			return bad("tags must contain nonempty strings up to 64 bytes")
		}
	}
	return nil
}
func (s *Store) SaveKnowledge(ctx context.Context, slug string, payload Doc) (Doc, error) {
	allowed := "title summary tags body sourceProjectId sourceTaskId sourceAgent"
	if slug == "" {
		allowed += " slug"
	}
	if err := fields(payload, allowed); err != nil {
		return nil, err
	}
	return s.write(ctx, func(tx *sql.Tx) (Doc, error) {
		create := slug == ""
		var d Doc
		var err error
		if create {
			slug = Text(payload, "slug")
			if _, err = get(ctx, tx, "knowledge", slug); err == nil {
				return nil, conflict("Knowledge slug already exists")
			} else if e, ok := err.(*Error); !ok || e.Status != 404 {
				return nil, err
			}
			d = Doc{"slug": slug, "title": "", "summary": "", "body": "", "tags": []any{}, "sourceProjectId": "", "sourceTaskId": "", "sourceAgent": "", "revision": float64(0), "createdAt": stamp(s.Now())}
		} else {
			d, err = get(ctx, tx, "knowledge", slug)
			if err != nil {
				return nil, err
			}
			if err = revision(d, payload, true); err != nil {
				return nil, err
			}
		}
		merge(d, payload)
		if err = validateKnowledge(d); err != nil {
			return nil, err
		}
		pid := Text(d, "sourceProjectId")
		tid := Text(d, "sourceTaskId")
		if pid != "" {
			if _, err = get(ctx, tx, "project", pid); err != nil {
				return nil, bad("Unknown source project")
			}
		}
		if tid != "" {
			t, err := get(ctx, tx, "task", tid)
			if err != nil {
				return nil, bad("Unknown source task")
			}
			if pid != "" && Text(t, "projectId") != pid {
				return nil, bad("Source task belongs to another project")
			}
		}
		d["revision"] = Num(d, "revision") + 1
		d["updatedAt"] = stamp(s.Now())
		if err = put(ctx, tx, "knowledge", slug, "", d); err != nil {
			return nil, err
		}
		b, _ := json.Marshal(d)
		if _, err = tx.ExecContext(ctx, "INSERT INTO knowledge_revisions(slug,revision,document) VALUES(?,?,?)", slug, Num(d, "revision"), string(b)); err != nil {
			return nil, err
		}
		kind := "knowledge.updated"
		if create {
			kind = "knowledge.created"
		}
		_, err = s.event(ctx, tx, kind, Text(payload, "actor"), "", "", slug, Text(d, "title"), "")
		return d, err
	})
}
func (s *Store) KnowledgeHistory(ctx context.Context, slug string, rev int64) (any, error) {
	if _, err := s.Get(ctx, "knowledge", slug); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT document FROM knowledge_revisions WHERE slug=? AND (?=0 OR revision=?) ORDER BY revision", slug, rev, rev)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Doc{}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		var d Doc
		if err = json.Unmarshal(b, &d); err != nil {
			return nil, err
		}
		if rev == 0 {
			delete(d, "body")
		}
		out = append(out, d)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if rev > 0 {
		if len(out) == 0 {
			return nil, &Error{404, "Unknown revision"}
		}
		return out[0], nil
	}
	return out, nil
}
func (s *Store) Message(ctx context.Context, payload Doc) (Doc, error) {
	if err := fields(payload, "projectId taskId message recipient"); err != nil {
		return nil, err
	}
	if err := texts(payload, "message", map[string]int{"projectId": 64, "taskId": 64, "message": 8000, "recipient": 128}); err != nil {
		return nil, err
	}
	return s.write(ctx, func(tx *sql.Tx) (Doc, error) {
		pid := Text(payload, "projectId")
		tid := Text(payload, "taskId")
		if tid != "" {
			t, err := get(ctx, tx, "task", tid)
			if err != nil {
				return nil, err
			}
			if pid != "" && Text(t, "projectId") != pid {
				return nil, bad("Task belongs to another project")
			}
			pid = Text(t, "projectId")
		}
		if pid != "" {
			p, err := get(ctx, tx, "project", pid)
			if err != nil {
				return nil, err
			}
			s.touch(p)
			if err = put(ctx, tx, "project", pid, "", p); err != nil {
				return nil, err
			}
		}
		n, err := s.event(ctx, tx, "message", Text(payload, "actor"), pid, tid, "", Text(payload, "message"), Text(payload, "recipient"))
		return Doc{"number": n}, err
	})
}

type FeedOptions struct {
	Since                                                    int64
	Limit                                                    int
	ProjectID, TaskID, Actor, Recipient, Kind, KnowledgeSlug string
}
type Feed struct {
	Notifications []Doc `json:"notifications"`
	NextCursor    int64 `json:"nextCursor"`
	LatestCursor  int64 `json:"latestCursor"`
	HasMore       bool  `json:"hasMore"`
}

func (s *Store) Feed(ctx context.Context, o FeedOptions) (Feed, error) {
	if o.Since < 0 || o.Limit < 1 || o.Limit > 500 {
		return Feed{}, bad("since must be nonnegative; limit must be 1..500")
	} // One snapshot: concurrent commits cannot move the head past unread rows.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Feed{}, err
	}
	defer tx.Rollback()
	f := Feed{Notifications: []Doc{}, NextCursor: o.Since}
	if err = tx.QueryRowContext(ctx, "SELECT coalesce(max(number),0) FROM notifications").Scan(&f.LatestCursor); err != nil {
		return f, err
	}
	if o.Since > f.LatestCursor {
		return f, conflict("Cursor is ahead of this database; reload the summary and restart at zero")
	}
	rows, err := tx.QueryContext(ctx, `SELECT number,document FROM notifications WHERE number>? AND number<=?
 AND (?='' OR json_extract(document,'$.projectId')=?) AND (?='' OR json_extract(document,'$.taskId')=?)
 AND (?='' OR json_extract(document,'$.actor')=?) AND (?='' OR json_extract(document,'$.kind')=?)
 AND (?='' OR json_extract(document,'$.knowledgeSlug')=?)
 AND (?='' OR json_extract(document,'$.recipient')='' OR json_extract(document,'$.recipient')=?) ORDER BY number LIMIT ?`, o.Since, f.LatestCursor, o.ProjectID, o.ProjectID, o.TaskID, o.TaskID, o.Actor, o.Actor, o.Kind, o.Kind, o.KnowledgeSlug, o.KnowledgeSlug, o.Recipient, o.Recipient, o.Limit+1)
	if err != nil {
		return f, err
	}
	defer rows.Close()
	for rows.Next() {
		var n int64
		var b []byte
		if err = rows.Scan(&n, &b); err != nil {
			return f, err
		}
		if len(f.Notifications) == o.Limit {
			f.HasMore = true
			break
		}
		var d Doc
		if err = json.Unmarshal(b, &d); err != nil {
			return f, err
		}
		d["number"] = n
		f.Notifications = append(f.Notifications, d)
		f.NextCursor = n
	}
	if err = rows.Err(); err != nil {
		return f, err
	}
	if !f.HasMore {
		f.NextCursor = f.LatestCursor
	}
	return f, nil
}
func (s *Store) WaitFeed(ctx context.Context, o FeedOptions, wait time.Duration) (Feed, error) {
	if wait < 0 || wait > 30*time.Second {
		return Feed{}, bad("wait must be 0..30 seconds")
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		signal := s.Signal()
		f, err := s.Feed(ctx, o)
		if err != nil || len(f.Notifications) > 0 || wait == 0 {
			return f, err
		}
		select {
		case <-ctx.Done():
			return f, ctx.Err()
		case <-timer.C:
			return f, nil
		case <-signal:
		}
	}
}
func (s *Store) Sweep(ctx context.Context) error {
	_, err := s.write(ctx, func(tx *sql.Tx) (Doc, error) {
		rows, err := tx.QueryContext(ctx, "SELECT document FROM entities WHERE kind='project' AND json_extract(document,'$.activity')='active'")
		if err != nil {
			return nil, err
		}
		pending := []Doc{}
		for rows.Next() {
			var b []byte
			if err = rows.Scan(&b); err != nil {
				rows.Close()
				return nil, err
			}
			var d Doc
			if err = json.Unmarshal(b, &d); err != nil {
				rows.Close()
				return nil, err
			}
			if !s.Now().Before(parse(Text(d, "activeUntil"))) {
				pending = append(pending, d)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		for _, p := range pending {
			p["activity"] = "inactive"
			p["revision"] = Num(p, "revision") + 1
			p["updatedAt"] = stamp(s.Now())
			if err = put(ctx, tx, "project", Text(p, "id"), "", p); err != nil {
				return nil, err
			}
			if _, err = s.event(ctx, tx, "project.inactive", "system", Text(p, "id"), "", "", "No updates within the activity timeout", ""); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	return err
}

func (s *Store) SaveUsage(ctx context.Context, agent string, payload Doc) (Doc, error) {
	if agent == "" || len(agent) > 128 || strings.Contains(agent, "/") {
		return nil, bad("agent must be nonempty, up to 128 bytes, without slashes")
	}
	if err := fields(payload, "provider model sessionId tokenBudget tokensUsed tokensRemaining contextTokens contextLimit accountRemainingPercent resetsAt observedAt source"); err != nil {
		return nil, err
	}
	if err := texts(payload, "", map[string]int{"provider": 128, "model": 128, "sessionId": 128, "resetsAt": 64, "observedAt": 64, "source": 256}); err != nil {
		return nil, err
	}
	for _, k := range strings.Fields("tokenBudget tokensUsed tokensRemaining contextTokens contextLimit accountRemainingPercent") {
		if v, ok := payload[k]; ok && v != nil {
			n, yes := v.(float64)
			if !yes || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 1e15 {
				return nil, bad(k + " must be a nonnegative finite number or null")
			}
			if k == "accountRemainingPercent" && n > 100 {
				return nil, bad("accountRemainingPercent must be 0..100")
			}
		}
	}
	for _, k := range []string{"observedAt", "resetsAt"} {
		if Text(payload, k) != "" {
			t, err := time.Parse(time.RFC3339Nano, Text(payload, k))
			if err != nil {
				return nil, bad(k + " must be RFC3339")
			}
			if k == "observedAt" && t.After(s.Now().Add(time.Minute)) {
				return nil, bad("observedAt cannot be in the future")
			}
		}
	}
	return s.write(ctx, func(tx *sql.Tx) (Doc, error) {
		old, err := get(ctx, tx, "agent", agent)
		if err != nil {
			if e, ok := err.(*Error); !ok || e.Status != 404 {
				return nil, err
			}
			old = Doc{"revision": float64(0)}
		}
		if _, ok := payload["expectedRevision"]; ok {
			if err = revision(old, payload, false); err != nil {
				return nil, err
			}
		} // Reports replace the observation, avoiding stale values leaking between sessions.
		d := Doc{"agent": agent, "revision": Num(old, "revision") + 1, "updatedAt": stamp(s.Now()), "observedAt": stamp(s.Now())}
		merge(d, payload)
		if err = put(ctx, tx, "agent", agent, "", d); err != nil {
			return nil, err
		}
		_, err = s.event(ctx, tx, "agent.usage", Text(payload, "actor"), "", "", "", "Usage reported for "+agent, "")
		s.decorateUsage(d)
		return d, err
	})
}
func (s *Store) decorateUsage(d Doc) {
	observed := parse(Text(d, "observedAt"))
	d["stale"] = observed.IsZero() || !s.Now().Before(observed.Add(s.StaleAfter))
	if d["tokensRemaining"] == nil && d["tokenBudget"] != nil && d["tokensUsed"] != nil {
		d["tokensRemaining"] = math.Max(0, Num(d, "tokenBudget")-Num(d, "tokensUsed"))
	}
	if d["contextTokens"] != nil && Num(d, "contextLimit") > 0 {
		d["contextPercent"] = Num(d, "contextTokens") / Num(d, "contextLimit") * 100
	}
}
func Filter(items []Doc, filters map[string]string) []Doc {
	out := []Doc{}
	for _, d := range items {
		match := true
		for k, v := range filters {
			if v != "" && Text(d, k) != v {
				match = false
			}
		}
		if match {
			out = append(out, d)
		}
	}
	return out
}
func (s *Store) Summary(ctx context.Context, pid, agent, status string) (Doc, error) {
	projects, err := s.List(ctx, "project")
	if err != nil {
		return nil, err
	}
	tasks, err := s.List(ctx, "task")
	if err != nil {
		return nil, err
	}
	usage, err := s.List(ctx, "agent")
	if err != nil {
		return nil, err
	}
	if pid != "" {
		if _, err = s.Get(ctx, "project", pid); err != nil {
			return nil, err
		}
	}
	tasks = Filter(tasks, map[string]string{"projectId": pid, "agent": agent, "status": status})
	projects = Filter(projects, map[string]string{"id": pid})
	reports := []Doc{}
	for _, p := range projects {
		p = clone(p)
		pt := Filter(tasks, map[string]string{"projectId": Text(p, "id")})
		count, blocked, active, n, total := 0, 0, 0, 0, 0.0
		owners := map[string]bool{}
		for _, t := range pt {
			count++
			if Text(t, "status") == "blocked" {
				blocked++
			}
			if Text(t, "status") == "active" {
				active++
			}
			if Text(t, "parentId") == "" {
				n++
				total += Num(t, "percent")
			}
			if Text(t, "agent") != "" {
				owners[Text(t, "agent")] = true
			}
		}
		p["percent"] = nil
		if n > 0 {
			p["percent"] = math.Round(total/float64(n)*10) / 10
		}
		p["taskCount"] = count
		p["blockedCount"] = blocked
		p["activeTaskCount"] = active
		p["agents"] = []Doc{}
		for _, u := range usage {
			if owners[Text(u, "agent")] {
				p["agents"] = append(p["agents"].([]Doc), u)
			}
		}
		reports = append(reports, p)
	}
	sort.SliceStable(reports, func(i, j int) bool {
		rank := func(p Doc) int {
			if Text(p, "lifecycle") == "ongoing" && Text(p, "activity") == "active" {
				return 0
			}
			if Text(p, "lifecycle") == "ongoing" {
				return 1
			}
			if Text(p, "lifecycle") == "completed" {
				return 2
			}
			return 3
		}
		a, b := rank(reports[i]), rank(reports[j])
		if a != b {
			return a < b
		}
		return Text(reports[i], "updatedAt") > Text(reports[j], "updatedAt")
	})
	return Doc{"projects": reports, "tasks": tasks, "agents": usage, "generatedAt": stamp(s.Now())}, nil
}
