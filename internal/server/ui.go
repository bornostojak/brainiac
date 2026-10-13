package server

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed web/*
var web embed.FS

func (a *App) EnableUI() {
	assets, _ := fs.Sub(web, "web")
	a.mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assets))))
	page := func(w http.ResponseWriter, r *http.Request) {
		b, _ := web.ReadFile("web/index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	}
	a.mux.HandleFunc("GET /{$}", page)
	a.mux.HandleFunc("GET /projects/{projectId}", page)
	a.mux.HandleFunc("GET /projects/{projectId}/tasks/{taskId}", page)
	a.mux.HandleFunc("GET /knowledge", page)
}
