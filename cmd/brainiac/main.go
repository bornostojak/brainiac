package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"brainiac/internal/server"
	"brainiac/internal/tracker"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		flags := flag.NewFlagSet("healthcheck", flag.ExitOnError)
		url := flags.String("url", "http://127.0.0.1:3011", "service URL")
		flags.Parse(os.Args[2:])
		client := http.Client{Timeout: 2 * time.Second}
		response, err := client.Get(*url + "/healthz")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		slog.Error("brainiac stopped", "error", err)
		os.Exit(1)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if len(os.Args) > 1 && os.Args[1] == "mcp-stdio" {
		flags := flag.NewFlagSet("mcp-stdio", flag.ContinueOnError)
		base := flags.String("url", env("PROGRESS_TRACKER_URL", "http://127.0.0.1:3011"), "shared service URL")
		if err := flags.Parse(os.Args[2:]); err != nil {
			return err
		}
		remote, err := server.NewRemote(*base)
		if err != nil {
			return err
		}
		return server.MCPServer(remote).Run(ctx, &mcp.StdioTransport{})
	}
	if len(os.Args) > 1 {
		return fmt.Errorf("usage: brainiac [healthcheck --url URL | mcp-stdio --url URL]")
	}
	timeout, err := time.ParseDuration(env("ACTIVITY_TIMEOUT", "90m"))
	if err != nil {
		return err
	}
	stale, err := time.ParseDuration(env("USAGE_STALE_AFTER", "5m"))
	if err != nil {
		return err
	}
	port, err := strconv.Atoi(env("LISTEN_PORT", "3011"))
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("LISTEN_PORT must be 1..65535")
	}
	ip := env("LISTEN_IP", "127.0.0.1")
	if net.ParseIP(ip) == nil {
		return fmt.Errorf("LISTEN_IP must be an IP address")
	}
	store, err := tracker.Open(env("DATA_PATH", "data/brainiac.db"), timeout, stale, nil)
	if err != nil {
		return err
	}
	defer store.Close()
	app := server.New(store)
	app.EnableUI()
	app.EnableMCP()
	app.EnableDistribution()
	listener, err := net.Listen("tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: app, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 * 1024}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(listener) }()
	slog.Info("Brainiac ready", "url", "http://"+listener.Addr().String(), "database", env("DATA_PATH", "data/brainiac.db"))
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case err = <-done:
			if err == http.ErrServerClosed {
				return nil
			}
			return err
		case <-tick.C:
			if err = store.Sweep(ctx); err != nil && ctx.Err() == nil {
				slog.Error("activity sweep", "error", err)
			}
		case <-ctx.Done():
			shutdown, stop := context.WithTimeout(context.Background(), 8*time.Second)
			defer stop()
			return srv.Shutdown(shutdown)
		}
	}
}
