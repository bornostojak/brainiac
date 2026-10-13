package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io/fs"
	"net/http"
	"regexp"
	"strings"

	assets "brainiac"
	"brainiac/internal/tracker"
)

var safeHost = regexp.MustCompile(`^[a-zA-Z0-9.\[\]:-]+$`)

func (a *App) EnableDistribution() {
	a.mux.HandleFunc("GET /install.sh", func(w http.ResponseWriter, r *http.Request) {
		if !safeHost.MatchString(r.Host) {
			fail(w, &tracker.Error{Status: 400, Message: "Invalid service host"})
			return
		}
		origin := "http://" + r.Host
		if r.TLS != nil {
			origin = "https://" + r.Host
		}
		w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
		w.Write([]byte(strings.ReplaceAll(assets.Installer, "__BASE_URL__", origin)))
	})
	a.mux.HandleFunc("GET /skills/{package}", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSuffix(r.PathValue("package"), ".tar.gz")
		if r.PathValue("package") != name+".tar.gz" || name != "progress-tracker" && name != "live-progress-monitor" {
			fail(w, &tracker.Error{Status: 404, Message: "Unknown skill package"})
			return
		}
		var buffer bytes.Buffer
		zip := gzip.NewWriter(&buffer)
		archive := tar.NewWriter(zip)
		err := fs.WalkDir(assets.Skills, "skills/"+name, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			content, err := assets.Skills.ReadFile(path)
			if err != nil {
				return err
			}
			header := &tar.Header{Name: strings.TrimPrefix(path, "skills/"), Mode: 0644, Size: int64(len(content)), Typeflag: tar.TypeReg}
			if err = archive.WriteHeader(header); err != nil {
				return err
			}
			_, err = archive.Write(content)
			return err
		})
		if err != nil {
			fail(w, err)
			return
		}
		if err = archive.Close(); err != nil {
			fail(w, err)
			return
		}
		if err = zip.Close(); err != nil {
			fail(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", "attachment; filename="+name+".tar.gz")
		w.Write(buffer.Bytes())
	})
}
