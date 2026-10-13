package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIPAllowlistCIDRsAndAddresses(t *testing.T) {
	for _, test := range []struct {
		name, config, peer string
		allowed            bool
	}{
		{"empty denies IPv4 peers", DefaultIPAllowlist, "127.0.0.1:1000", false},
		{"empty denies IPv6 peers", DefaultIPAllowlist, "[::1]:1000", false},
		{"explicit loopback IPv4", "127.0.0.1", "127.0.0.1:1000", true},
		{"explicit loopback IPv6", "::1", "[::1]:1000", true},
		{"mapped loopback", "127.0.0.1", "[::ffff:127.0.0.1]:1000", true},
		{"default rejects LAN", DefaultIPAllowlist, "192.0.2.100:1000", false},
		{"default exact loopback", DefaultIPAllowlist, "127.0.0.2:1000", false},
		{"IPv4 network", "127.0.0.1,192.0.2.0/24", "192.0.2.42:1000", true},
		{"IPv4 outside network", "192.0.2.0/24", "198.51.100.42:1000", false},
		{"masked CIDR", "192.0.2.50/24", "192.0.2.1:1000", true},
		{"IPv6 network", "2001:db8::/32", "[2001:db8:abc::1]:1000", true},
		{"IPv6 scoped peer", "fe80::/10", "[fe80::42%eth0]:1000", true},
		{"IPv6 outside network", "2001:db8::/32", "[2001:db9::1]:1000", false},
		{"mapped IPv4 network", "::ffff:192.0.2.0/120", "192.0.2.42:1000", true},
		{"IPv4 CIDR explicit all", "0.0.0.0/0", "203.0.113.9:1000", true},
		{"IPv6 CIDR explicit all", "::/0", "[2001:db8::1]:1000", true},
		{"malformed peer", DefaultIPAllowlist, "127.0.0.1", false},
		{"not an IP", DefaultIPAllowlist, "localhost:1000", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			list, err := ParseIPAllowlist(test.config)
			if err != nil {
				t.Fatal(err)
			}
			if list.Allows(test.peer) != test.allowed {
				t.Fatalf("%s allowed=%v", test.peer, !test.allowed)
			}
		})
	}
}

func TestIPAllowlistInvalidConfiguration(t *testing.T) {
	for _, value := range []string{",", "127.0.0.1,", "localhost", "203.0.113.0/33", "::/129", "not-an-ip", "fe80::1%eth0"} {
		if _, err := ParseIPAllowlist(value); err == nil {
			t.Fatalf("accepted invalid allowlist %q", value)
		}
	}
}

func TestIPAllowlistProtectsEveryRouteAndIgnoresForwardedHeaders(t *testing.T) {
	app := New(nil)
	app.mux.HandleFunc("GET /allowed", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for _, path := range []string{"/healthz", "/", "/api/v1/projects", "/mcp", "/events", "/install.sh", "/skills/progress-tracker.tar.gz", "/assets/app.js", "/README.md"} {
		r := httptest.NewRequest("GET", path, nil)
		r.RemoteAddr = "192.0.2.100:4000"
		r.Header.Set("X-Forwarded-For", "127.0.0.1")
		r.Header.Set("Forwarded", "for=127.0.0.1")
		r.Header.Set("X-Real-IP", "127.0.0.1")
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Fatalf("%s returned %d", path, w.Code)
		}
	}
	for _, peer := range []string{"127.0.0.1:4000", "[::1]:4000"} {
		r := httptest.NewRequest("GET", "/allowed", nil)
		r.RemoteAddr = peer
		r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 3011}))
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != http.StatusNoContent {
			t.Fatal("loopback was blocked")
		}
	}
	if err := app.ConfigureIPAllowlist("127.0.0.1,192.0.2.0/24"); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/allowed", nil)
	r.RemoteAddr = "192.0.2.42:4000"
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatal("CIDR client was blocked")
	}
}

func TestLocalDestinationBypassCannotBeSpoofed(t *testing.T) {
	list, err := ParseIPAllowlist("")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		destination, peer string
		allowed           bool
	}{
		{"127.0.0.1", "127.0.0.1:4000", true},
		{"127.0.0.254", "192.0.2.42:4000", true},
		{"::ffff:127.0.0.1", "127.0.0.1:4000", true},
		{"127.0.1.1", "127.0.0.1:4000", false},
		{"::1", "[::1]:4000", false},
		{"192.0.2.100", "127.0.0.1:4000", false},
		{"192.0.2.100", "192.0.2.42:4000", false},
	} {
		r := httptest.NewRequest("GET", "http://127.0.0.1/allowed", nil)
		r.RemoteAddr = test.peer
		r.Header.Set("Forwarded", "for=127.0.0.1;host=127.0.0.1")
		r.Header.Set("X-Forwarded-For", "127.0.0.1")
		r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP(test.destination), Port: 3011}))
		if list.allowsRequest(r) != test.allowed {
			t.Fatalf("destination %s peer %s", test.destination, test.peer)
		}
	}
	app := New(nil)
	app.mux.HandleFunc("GET /allowed", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	r := httptest.NewRequest("GET", "http://127.0.0.1/allowed", nil)
	r.RemoteAddr = "127.0.0.1:4000"
	r = r.WithContext(context.WithValue(r.Context(), http.LocalAddrContextKey, &net.TCPAddr{IP: net.ParseIP("192.0.2.100"), Port: 3011}))
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatal("Host header bypassed destination policy")
	}
}
