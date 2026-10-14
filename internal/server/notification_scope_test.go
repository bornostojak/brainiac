package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"brainiac/internal/tracker"
)

func TestProjectNotificationsRemoveGlobalRoutes(t *testing.T) {
	a, s := testApp(t)
	ctx := context.Background()
	job, err := a.Store.CreateProject(ctx, tracker.Doc{"name": "Job"})
	if err != nil {
		t.Fatal(err)
	}
	weekend, err := a.Store.CreateProject(ctx, tracker.Doc{"name": "Weekend"})
	if err != nil {
		t.Fatal(err)
	}
	pid := tracker.Text(job, "id")
	other := tracker.Text(weekend, "id")
	base := "/api/v1/projects/" + pid + "/notifications"
	for _, id := range []string{pid, other} {
		code, body := call(t, s, "POST", "/api/v1/projects/"+id+"/notifications", `{"actor":"same-agent","recipient":"worker","message":"Scoped handoff"}`, nil)
		if code != 201 {
			t.Fatal(code, string(body))
		}
	}
	code, body := call(t, s, "GET", base+"/since/0?actor=same-agent&recipient=worker&limit=1", "", nil)
	var feed tracker.Feed
	json.Unmarshal(body, &feed)
	if code != 200 || len(feed.Notifications) != 1 || tracker.Text(feed.Notifications[0], "projectId") != pid || feed.HasMore {
		t.Fatal(code, string(body))
	}
	for _, url := range []string{"/api/v1/notifications", "/api/v1/notifications?projectId=" + pid, "/api/v1/notifications/since/0"} {
		code, _ = call(t, s, "GET", url, "", nil)
		if code != 404 {
			t.Fatal("global read still available", url, code)
		}
	}
	code, _ = call(t, s, "POST", "/api/v1/notifications", `{"message":"Global"}`, nil)
	if code != 404 {
		t.Fatal("global write still available", code)
	}
	code, _ = call(t, s, "POST", base, `{"projectId":"`+other+`","message":"Wrong scope"}`, nil)
	if code != 400 {
		t.Fatal("cross-project payload", code)
	}
	code, _ = call(t, s, "GET", base+"?projectId="+other, "", nil)
	if code != 400 {
		t.Fatal("cross-project query", code)
	}
	code, _ = call(t, s, "GET", "/api/v1/projects/unknown/notifications", "", nil)
	if code != 404 {
		t.Fatal(code)
	}
	code, _ = call(t, s, "GET", "/events", "", nil)
	if code != 400 {
		t.Fatal("implicit global stream", code)
	}
	if _, ok := OpenAPI()["paths"].(map[string]any)["/notifications"]; ok {
		t.Fatal("removed route advertised")
	}
	// A weekend write must not complete a job agent's pending read.
	head, err := a.Store.NotificationCursor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	requestCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	result := make(chan tracker.Feed, 1)
	failure := make(chan error, 1)
	go func() {
		req, err := http.NewRequestWithContext(requestCtx, "GET", s.URL+base+"?wait=2&since="+jsonNumber(head), nil)
		if err != nil {
			failure <- err
			return
		}
		r, err := s.Client().Do(req)
		if err != nil {
			failure <- err
			return
		}
		defer r.Body.Close()
		var feed tracker.Feed
		if err = json.NewDecoder(r.Body).Decode(&feed); err != nil {
			failure <- err
			return
		}
		result <- feed
	}()
	if _, err = a.Store.Message(ctx, tracker.Doc{"projectId": other, "message": "Weekend update"}); err != nil {
		t.Fatal(err)
	}
	select {
	case feed := <-result:
		t.Fatal("foreign update completed wait", feed)
	case err := <-failure:
		t.Fatal(err)
	case <-time.After(75 * time.Millisecond):
	}
	if _, err = a.Store.Message(ctx, tracker.Doc{"projectId": pid, "message": "Job update"}); err != nil {
		t.Fatal(err)
	}
	select {
	case feed := <-result:
		if len(feed.Notifications) != 1 || tracker.Text(feed.Notifications[0], "projectId") != pid {
			t.Fatal(feed)
		}
	case err := <-failure:
		t.Fatal(err)
	case <-requestCtx.Done():
		t.Fatal(requestCtx.Err())
	}
}

func jsonNumber(n int64) string { b, _ := json.Marshal(n); return string(b) }
