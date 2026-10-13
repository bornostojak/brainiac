package server

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestServedInstallerTargetsAndMCP(t *testing.T) {
	app, s := testApp(t)
	app.EnableDistribution()
	code, script := call(t, s, "GET", "/install.sh", "", nil)
	if code != 200 || !strings.Contains(string(script), s.URL) {
		t.Fatal("installer origin missing")
	}
	dir := t.TempDir()
	fakebin := filepath.Join(dir, "bin")
	os.Mkdir(fakebin, 0700)
	if e := os.WriteFile(filepath.Join(fakebin, "opencode"), []byte("#!/bin/sh\nexit 0\n"), 0700); e != nil {
		t.Fatal(e)
	}
	installer := filepath.Join(dir, "install.sh")
	os.WriteFile(installer, script, 0600)
	configRoot := filepath.Join(dir, "config")
	configDir := filepath.Join(configRoot, "opencode")
	os.MkdirAll(configDir, 0700)
	os.WriteFile(filepath.Join(configDir, "opencode.jsonc"), []byte("// preserve\n{\"model\":\"test\",\"mcp\":{\"other\":{\"type\":\"remote\",\"url\":\"http://other\"}}}"), 0600)
	cmd := exec.Command("sh", installer, "--harness", "opencode", "--mcp")
	cmd.Env = append(os.Environ(), "PATH="+fakebin+":"+os.Getenv("PATH"), "XDG_CONFIG_HOME="+configRoot)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	for _, skill := range []string{"progress-tracker", "live-progress-monitor"} {
		if _, e = os.Stat(filepath.Join(configDir, "skills", skill, "SKILL.md")); e != nil {
			t.Fatal(e)
		}
	}
	saved, e := os.ReadFile(filepath.Join(configDir, "skills", "progress-tracker", "references", "server-url.txt"))
	if e != nil || strings.TrimSpace(string(saved)) != s.URL {
		t.Fatal("installed discovery origin lost")
	}
	config, e := os.ReadFile(filepath.Join(configDir, "opencode.jsonc"))
	if e != nil || !strings.Contains(string(config), s.URL+"/mcp") || !strings.Contains(string(config), "other") || !strings.Contains(string(config), "// preserve") {
		t.Fatal("configuration merge", e)
	}
	cmd = exec.Command("sh", installer, "--harness", "not-installed", "--no-mcp")
	cmd.Env = append(os.Environ(), "PATH="+fakebin+":"+os.Getenv("PATH"))
	if e = cmd.Run(); e == nil {
		t.Fatal("unknown harness accepted")
	}
	code, _ = call(t, s, "GET", "/skills/unknown.tar.gz", "", nil)
	if code != 404 {
		t.Fatal(code)
	}
}
