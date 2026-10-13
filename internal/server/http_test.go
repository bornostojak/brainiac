package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"brainiac/internal/tracker"
)

func testApp(t *testing.T) (*App, *httptest.Server) {
	t.Helper()
	s, e := tracker.Open(filepath.Join(t.TempDir(), "db"), 90*time.Minute, 5*time.Minute, nil)
	if e != nil {
		t.Fatal(e)
	}
	a := New(s)
	server := httptest.NewServer(a)
	t.Cleanup(func() { server.Close(); s.Close() })
	return a, server
}
func call(t *testing.T, server *httptest.Server, method, path, body string, headers map[string]string) (int, []byte) {
	t.Helper()
	r, e := http.NewRequest(method, server.URL+path, strings.NewReader(body))
	if e != nil {
		t.Fatal(e)
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	response, e := server.Client().Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	b, e := io.ReadAll(response.Body)
	if e != nil {
		t.Fatal(e)
	}
	return response.StatusCode, b
}
func TestAPIValidationAndKnowledge(t *testing.T) {
	_, server := testApp(t)
	code, b := call(t, server, "POST", "/api/v1/projects", `{"name":"HTTP project","externalKey":"workspace"}`, nil)
	if code != 201 {
		t.Fatal(code, string(b))
	}
	var p tracker.Doc
	json.Unmarshal(b, &p)
	pid := tracker.Text(p, "id")
	code, _ = call(t, server, "POST", "/api/v1/projects", `{"name":"Duplicate","externalKey":"workspace"}`, nil)
	if code != 409 {
		t.Fatal("duplicate accepted", code)
	}
	code, b = call(t, server, "GET", "/api/v1/projects?externalKey=workspace", "", nil)
	if code != 200 || !strings.Contains(string(b), pid) {
		t.Fatal(code, string(b))
	}
	code, _ = call(t, server, "POST", "/api/v1/projects", `{"name":"Bad","unknown":true}`, nil)
	if code != 400 {
		t.Fatal(code)
	}
	code, _ = call(t, server, "POST", "/api/v1/projects", `null`, nil)
	if code != 400 {
		t.Fatal(code)
	}
	code, _ = call(t, server, "POST", "/api/v1/projects", `{"name":"Origin"}`, map[string]string{"Origin": "http://evil.example"})
	if code != 403 {
		t.Fatal(code)
	}
	code, _ = call(t, server, "GET", "/api/v1/notifications/since/0?wait=31", "", nil)
	if code != 400 {
		t.Fatal(code)
	}
	code, _ = call(t, server, "PATCH", "/api/v1/projects/"+pid, `{"readme":"edit"}`, nil)
	if code != 400 {
		t.Fatal(code)
	}
	code, _ = call(t, server, "POST", "/api/v1/knowledge", `{"slug":"tips","title":"Go tips","body":"Very large private body","tags":["go"]}`, nil)
	if code != 201 {
		t.Fatal(code)
	}
	code, b = call(t, server, "GET", "/api/v1/knowledge?q=Go&tag=go", "", nil)
	if code != 200 || strings.Contains(string(b), "Very large private body") || !strings.Contains(string(b), "tips") {
		t.Fatal(code, string(b))
	}
	code, b = call(t, server, "GET", "/api/v1/knowledge/tips", "", nil)
	if code != 200 || !strings.Contains(string(b), "Very large private body") {
		t.Fatal(code, string(b))
	}
}
func TestREADMEAcceptAndSafeHTML(t *testing.T) {
	a, server := testApp(t)
	d, e := a.Store.SaveKnowledge(context.Background(), "", tracker.Doc{"slug": "safety", "title": "Safety", "body": "<script>alert(1)</script>\n\n[bad](javascript:alert(1))\n\n**Good**"})
	if e != nil {
		t.Fatal(e)
	}
	_ = d
	path := "/knowledge/safety/README.md"
	code, b := call(t, server, "GET", path, "", map[string]string{"Accept": "text/html"})
	if code != 200 || strings.Contains(string(b), "<script>") || strings.Contains(string(b), "href=\"javascript:") || !strings.Contains(string(b), "<strong>Good</strong>") {
		t.Fatal(code, string(b))
	}
	_, b = call(t, server, "GET", path, "", map[string]string{"Accept": "text/plain", "User-Agent": "Mozilla/5.0"})
	if !strings.Contains(string(b), "# Safety") || strings.Contains(string(b), "<!doctype") {
		t.Fatal(string(b))
	}
	_, b = call(t, server, "GET", path, "", map[string]string{"Accept": "text/html;q=0.2,text/markdown;q=0.8", "User-Agent": "Mozilla/5.0"})
	if !strings.Contains(string(b), "# Safety") {
		t.Fatal(string(b))
	}
	_, b = call(t, server, "GET", path, "", map[string]string{"Accept": "*/*", "User-Agent": "Mozilla/5.0"})
	if !strings.Contains(string(b), "<!doctype") {
		t.Fatal(string(b))
	}
}
func TestSSEReplay(t *testing.T) {
	a, server := testApp(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, e := a.Store.Message(ctx, tracker.Doc{"message": "hello"}); e != nil {
			t.Fatal(e)
		}
	}
	req, e := http.NewRequest("GET", server.URL+"/events", nil)
	if e != nil {
		t.Fatal(e)
	}
	req.Header.Set("Last-Event-ID", "2")
	cancelCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	response, e := server.Client().Do(req.WithContext(cancelCtx))
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	var lines []string
	for scanner.Scan() {
		line := scanner.Text()
		lines = append(lines, line)
		if line == "" {
			break
		}
	}
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "id: 3") || !strings.Contains(joined, "event: update") {
		t.Fatal(joined)
	}
}
