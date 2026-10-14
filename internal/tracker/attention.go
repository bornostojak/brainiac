package tracker

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"net/url"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// Input requests are attention notices, never a question or answer channel.
// Legacy question bodies stay in storage but are excluded from all public reads.
func inputNotice(d Doc) Doc {
	out := Doc{}
	for _, key := range strings.Fields("id status revision projectId projectName taskId agent source sourceUrl externalKey createdAt updatedAt resolvedBy") {
		if value, ok := d[key]; ok {
			out[key] = value
		}
	}
	return out
}

func attentionMessage(kind string) string {
	if kind == "input.required" {
		return "Your attention is needed in the originating agent conversation."
	}
	return "Attention notice " + strings.TrimPrefix(kind, "input.") + "."
}

// Input requests are durable state. Their events use the ordinary progress feed,
// so browser reconnects and agent coordination share the same commit boundary.
func (s *Store) RequestInput(ctx context.Context, payload Doc) (Doc, error) {
	return s.write(ctx, func(tx *sql.Tx) (Doc, error) { return s.requestInputTx(ctx, tx, payload) })
}

func (s *Store) requestInputTx(ctx context.Context, tx *sql.Tx, payload Doc) (Doc, error) {
	if err := fields(payload, "projectId taskId agent source sourceUrl title message externalKey"); err != nil {
		return nil, err
	}
	if err := texts(payload, "projectId agent", map[string]int{"source": 128, "sourceUrl": 2048, "projectId": 64, "taskId": 64, "agent": 128, "title": 200, "message": 8000, "externalKey": 128}); err != nil {
		return nil, err
	}
	if strings.Contains(Text(payload, "agent"), "/") {
		return nil, bad("agent cannot contain slashes")
	}
	if link := Text(payload, "sourceUrl"); link != "" {
		u, err := url.Parse(link)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
			return nil, bad("sourceUrl must be an absolute HTTP(S) conversation URL without credentials")
		}
	}
	pid := Text(payload, "projectId")
	p, err := get(ctx, tx, "project", pid)
	if err != nil {
		return nil, err
	}
	if tid := Text(payload, "taskId"); tid != "" {
		task, err := get(ctx, tx, "task", tid)
		if err != nil {
			return nil, err
		}
		if Text(task, "projectId") != pid {
			return nil, bad("Input request task belongs to another project")
		}
	}
	if key := Text(payload, "externalKey"); key != "" {
		var data []byte
		err := tx.QueryRowContext(ctx, `SELECT document FROM entities WHERE kind='input' AND project_id=? AND json_extract(document,'$.agent')=? AND json_extract(document,'$.externalKey')=?`, pid, Text(payload, "agent"), key).Scan(&data)
		if err == nil {
			var d Doc
			if err = json.Unmarshal(data, &d); err != nil {
				return nil, err
			}
			return inputNotice(d), nil // Retry the same notice without another alert.
		}
		if err != sql.ErrNoRows {
			return nil, err
		}
	}
	d := Doc{"id": uuid.NewString(), "status": "open", "revision": float64(1), "createdAt": stamp(s.Now()), "updatedAt": stamp(s.Now()), "taskId": "", "externalKey": "", "projectName": Text(p, "name")}
	merge(d, inputNotice(payload)) // Discard deprecated title/message, including from old clients.
	if err = put(ctx, tx, "input", Text(d, "id"), pid, d); err != nil {
		return nil, err
	}
	s.touch(p)
	if err = put(ctx, tx, "project", pid, "", p); err != nil {
		return nil, err
	}
	return d, s.inputEvent(ctx, tx, "input.required", payload, d)
}

func (s *Store) inputEvent(ctx context.Context, tx *sql.Tx, kind string, payload, d Doc) error {
	message := attentionMessage(kind)
	actor := Text(payload, "actor")
	if actor == "" && kind == "input.required" {
		actor = Text(d, "agent")
	}
	n, err := s.event(ctx, tx, kind, actor, Text(d, "projectId"), Text(d, "taskId"), "", message, Text(d, "agent"))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "UPDATE notifications SET document=json_set(document,'$.inputRequestId',?) WHERE number=?", Text(d, "id"), n)
	return err
}

func (s *Store) CloseInput(ctx context.Context, id string, payload Doc) (Doc, error) {
	if err := fields(payload, "status"); err != nil {
		return nil, err
	}
	if err := texts(payload, "", map[string]int{"status": 16}); err != nil {
		return nil, err
	}
	return s.write(ctx, func(tx *sql.Tx) (Doc, error) {
		d, err := get(ctx, tx, "input", id)
		if err != nil {
			return nil, err
		}
		if err = revision(d, payload, true); err != nil {
			return nil, err
		}
		if Text(d, "status") != "open" {
			return nil, conflict("Input request is already closed")
		}
		merge(d, payload)
		if err = oneOf(d, "status", "resolved cancelled"); err != nil {
			return nil, err
		}
		d["revision"] = Num(d, "revision") + 1
		d["updatedAt"] = stamp(s.Now())
		d["resolvedBy"] = Text(payload, "actor")
		if err = put(ctx, tx, "input", id, Text(d, "projectId"), d); err != nil {
			return nil, err
		}
		p, err := get(ctx, tx, "project", Text(d, "projectId"))
		if err != nil {
			return nil, err
		}
		s.touch(p)
		if err = put(ctx, tx, "project", Text(p, "id"), "", p); err != nil {
			return nil, err
		}
		return inputNotice(d), s.inputEvent(ctx, tx, "input."+Text(d, "status"), payload, d)
	})
}

var instructionKey = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

// Instructions and per-agent receipts intentionally never create numbered events
// or refresh project activity. Their revisions are local to each instruction.
func (s *Store) SaveInstruction(ctx context.Context, key string, payload Doc) (Doc, error) {
	create := key == ""
	allowed := "title message kind recipient projectId active"
	if create {
		allowed += " key"
		key = Text(payload, "key")
	}
	if !instructionKey.MatchString(key) || strings.HasPrefix(key, "brainiac.") {
		return nil, bad("Use a non-reserved instruction key (letters, digits, dots, underscores or hyphens)")
	}
	if err := fields(payload, allowed); err != nil {
		return nil, err
	}
	if err := texts(payload, "", map[string]int{"key": 128, "title": 200, "message": 8000, "kind": 32, "recipient": 128, "projectId": 64}); err != nil {
		return nil, err
	}
	return s.write(ctx, func(tx *sql.Tx) (Doc, error) {
		d, err := get(ctx, tx, "instruction", key)
		if create {
			if err == nil {
				return nil, conflict("Instruction key already exists")
			}
			if e, ok := err.(*Error); !ok || e.Status != 404 {
				return nil, err
			}
			d = Doc{"key": key, "title": "", "message": "", "kind": "guidance", "recipient": "", "projectId": "", "active": true, "revision": float64(0), "createdAt": stamp(s.Now())}
		} else {
			if err != nil {
				return nil, err
			}
			if err = revision(d, payload, true); err != nil {
				return nil, err
			}
		}
		merge(d, payload)
		if err = texts(d, "title message", nil); err != nil {
			return nil, err
		}
		if err = oneOf(d, "kind", "guidance reread_skill refresh_tools"); err != nil {
			return nil, err
		}
		if _, valid := d["active"].(bool); !valid {
			return nil, bad("active must be boolean")
		}
		if strings.Contains(Text(d, "recipient"), "/") {
			return nil, bad("recipient cannot contain slashes")
		}
		if pid := Text(d, "projectId"); pid != "" {
			if _, err = get(ctx, tx, "project", pid); err != nil {
				return nil, err
			}
		}
		d["updatedAt"] = stamp(s.Now())
		d["revision"] = Num(d, "revision") + 1
		return d, put(ctx, tx, "instruction", key, Text(d, "projectId"), d)
	})
}

type instructionReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func pendingInstructions(ctx context.Context, q instructionReader, agent, pid string) ([]Doc, error) {
	rows, err := q.QueryContext(ctx, `SELECT i.document FROM entities i
 LEFT JOIN entities ack ON ack.kind='instruction-ack' AND ack.id=i.id||'/'||?
 WHERE i.kind='instruction' AND json_extract(i.document,'$.active')=1
 AND (json_extract(i.document,'$.recipient')='' OR json_extract(i.document,'$.recipient')=?)
 AND (i.project_id='' OR i.project_id=?)
 AND (ack.id IS NULL OR json_extract(ack.document,'$.instructionRevision')<json_extract(i.document,'$.revision'))
 ORDER BY i.id`, agent, agent, pid)
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
		out = append(out, d)
	}
	return out, rows.Err()
}

func validAgent(agent string) error {
	if strings.TrimSpace(agent) == "" || len(agent) > 128 || strings.Contains(agent, "/") {
		return bad("agent must be nonempty, up to 128 bytes, without slashes")
	}
	return nil
}

func (s *Store) Instructions(ctx context.Context, agent, pid string) ([]Doc, error) {
	if err := validAgent(agent); err != nil {
		return nil, err
	}
	if pid != "" {
		if _, err := s.Get(ctx, "project", pid); err != nil {
			return nil, err
		}
	}
	return pendingInstructions(ctx, s.db, agent, pid)
}

func (s *Store) AcknowledgeInstruction(ctx context.Context, key string, payload Doc) (Doc, error) {
	if err := fields(payload, "agent revision"); err != nil {
		return nil, err
	}
	agent := Text(payload, "agent")
	if err := validAgent(agent); err != nil {
		return nil, err
	}
	n, valid := payload["revision"].(float64)
	if !valid || n < 1 || n != math.Trunc(n) {
		return nil, bad("revision must be a positive integer")
	}
	return s.write(ctx, func(tx *sql.Tx) (Doc, error) {
		d, err := get(ctx, tx, "instruction", key)
		if err != nil {
			return nil, err
		}
		if Num(d, "revision") != n || d["active"] != true {
			return nil, conflict("Instruction changed or was retired; reread before acknowledging")
		}
		if recipient := Text(d, "recipient"); recipient != "" && recipient != agent {
			return nil, bad("Instruction is addressed to another agent")
		}
		receipt := Doc{"key": key, "agent": agent, "instructionRevision": n, "acknowledgedAt": stamp(s.Now())}
		return receipt, put(ctx, tx, "instruction-ack", key+"/"+agent, "", receipt)
	})
}

// SyncSkillInstruction creates/revises one stable service notice per package.
// Content fingerprints prevent repeated notices on an unchanged restart.
func (s *Store) SyncSkillInstruction(ctx context.Context, name, version string) (Doc, error) {
	return s.write(ctx, func(tx *sql.Tx) (Doc, error) {
		key := "brainiac.skill." + name
		d, err := get(ctx, tx, "instruction", key)
		if err != nil {
			if e, ok := err.(*Error); !ok || e.Status != 404 {
				return nil, err
			}
			d = Doc{"key": key, "revision": float64(0), "createdAt": stamp(s.Now()), "recipient": "", "projectId": "", "active": true}
		}
		if Text(d, "contentVersion") == version {
			return d, nil
		}
		d["kind"] = "reread_skill"
		d["title"] = "Read updated " + name + " skill"
		d["message"] = "Brainiac's " + name + " package changed. Re-read it with get_skill or GET /api/v1/skills/" + name + ", then acknowledge this instruction revision. Update the installed package when appropriate; do not start another progress backend."
		d["skill"] = name
		d["contentVersion"] = version
		d["updatedAt"] = stamp(s.Now())
		d["revision"] = Num(d, "revision") + 1
		return d, put(ctx, tx, "instruction", key, "", d)
	})
}
