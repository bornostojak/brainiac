package server

import (
	"encoding/json"
	"strings"
	"testing"

	"brainiac/internal/tracker"
)

func TestLessonAPIAndReadableReview(t *testing.T) {
	a, s := testApp(t)
	a.EnableUI()
	code, body := call(t, s, "POST", "/api/v1/projects", `{"name":"Learning project"}`, nil)
	if code != 201 {
		t.Fatal(code, string(body))
	}
	var p tracker.Doc
	json.Unmarshal(body, &p)
	pid := tracker.Text(p, "id")
	base := "/api/v1/projects/" + pid + "/lessons"
	code, body = call(t, s, "POST", base, `{"title":"Wrong API assumption","summary":"Verify reference behavior","kind":"misunderstanding","correction":"Read **the contract** before implementing.\n\n<script>alert(1)</script>\n\n[unsafe](javascript:alert(1))","tags":["api"],"agent":"builder"}`, nil)
	if code != 201 {
		t.Fatal(code, string(body))
	}
	var d tracker.Doc
	json.Unmarshal(body, &d)
	id := tracker.Text(d, "id")
	code, body = call(t, s, "GET", base+"?q=reference&tag=api&kind=misunderstanding", "", nil)
	var index struct{ Lessons []tracker.Doc }
	json.Unmarshal(body, &index)
	if code != 200 || len(index.Lessons) != 1 || index.Lessons[0]["correction"] != nil {
		t.Fatal(code, string(body))
	}
	code, _ = call(t, s, "PATCH", base+"/"+id, `{"status":"superseded"}`, nil)
	if code != 400 {
		t.Fatal("unguarded patch", code)
	}
	code, _ = call(t, s, "PATCH", base+"/"+id, `{"status":"superseded","expectedRevision":2}`, nil)
	if code != 409 {
		t.Fatal("stale patch", code)
	}
	readme := "/projects/" + pid + "/lessons/" + id + "/README.md"
	code, body = call(t, s, "GET", readme, "", map[string]string{"Accept": "text/html"})
	if code != 200 || !strings.Contains(string(body), "<strong>the contract</strong>") || strings.Contains(string(body), "<script>alert") || strings.Contains(string(body), `href="javascript:`) {
		t.Fatal(code, string(body))
	}
	code, body = call(t, s, "GET", readme, "", map[string]string{"Accept": "text/markdown", "User-Agent": "Mozilla/5.0"})
	if code != 200 || !strings.Contains(string(body), "**the contract**") || strings.Contains(string(body), "<!doctype") {
		t.Fatal(code, string(body))
	}
	code, body = call(t, s, "GET", "/projects/"+pid+"/README.md", "", map[string]string{"Accept": "text/markdown"})
	if code != 200 || !strings.Contains(string(body), "/lessons/README.md") || !strings.Contains(string(body), "/notifications?projectId=") {
		t.Fatal(code, string(body))
	}
	code, _ = call(t, s, "PATCH", base+"/"+id, `{"status":"superseded","expectedRevision":1}`, nil)
	if code != 200 {
		t.Fatal(code)
	}
	code, body = call(t, s, "GET", base, "", nil)
	json.Unmarshal(body, &index)
	if code != 200 || len(index.Lessons) != 0 {
		t.Fatal("default exposed superseded lesson", code, string(body))
	}
	code, body = call(t, s, "GET", base+"?status=all", "", nil)
	json.Unmarshal(body, &index)
	if code != 200 || len(index.Lessons) != 1 {
		t.Fatal(code, string(body))
	}
	code, body = call(t, s, "GET", base+"/"+id+"/revisions/1", "", nil)
	if code != 200 || !strings.Contains(string(body), `"status":"active"`) {
		t.Fatal(code, string(body))
	}
	code, _ = call(t, s, "GET", base+"/"+id+"/revisions/0", "", nil)
	if code != 400 {
		t.Fatal(code)
	}
	code, _ = call(t, s, "DELETE", base+"/"+id, "{}", nil)
	if code != 405 {
		t.Fatal(code)
	}
	for _, page := range []string{"/notifications", "/projects/" + pid + "/lessons"} {
		code, body = call(t, s, "GET", page, "", nil)
		if code != 200 || !strings.Contains(string(body), "review.js") {
			t.Fatal(code, string(body))
		}
	}
	code, body = call(t, s, "GET", "/api/v1/notifications?lessonId="+id, "", nil)
	var feed tracker.Feed
	json.Unmarshal(body, &feed)
	if code != 200 || len(feed.Notifications) != 2 || tracker.Text(feed.Notifications[0], "lessonId") != id {
		t.Fatal(code, string(body))
	}
}
