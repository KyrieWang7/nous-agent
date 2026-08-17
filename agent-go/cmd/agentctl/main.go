package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/KyrieWang7/nous-agent/agent-go/pkg/config"
	"github.com/KyrieWang7/nous-agent/agent-go/pkg/migrations"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) < 2 {
		return usage()
	}
	switch os.Args[1] {
	case "config":
		return runConfig()
	case "migrate":
		return runMigrate()
	case "run":
		return runAgent(os.Args[2:], os.Stdout)
	default:
		return fmt.Errorf("unknown command %q", os.Args[1])
	}
}
func usage() error {
	return fmt.Errorf("usage: agentctl <run --prompt TEXT|config validate|migrate up|migrate down|migrate version>")
}

func runAgent(args []string, output io.Writer) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	server := fs.String("server", "http://127.0.0.1:2024", "agentd base URL")
	threadID := fs.String("thread", "", "thread ID (generated when empty)")
	assistantID := fs.String("assistant", "lead_agent", "assistant ID")
	prompt := fs.String("prompt", "", "user prompt")
	onDisconnect := fs.String("on-disconnect", "continue", "cancel or continue")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*prompt) == "" && fs.NArg() > 0 {
		*prompt = strings.Join(fs.Args(), " ")
	}
	if strings.TrimSpace(*prompt) == "" {
		return errors.New("run requires --prompt or a positional prompt")
	}
	if *onDisconnect != "cancel" && *onDisconnect != "continue" {
		return errors.New("run --on-disconnect must be cancel or continue")
	}
	if *threadID == "" {
		*threadID = fmt.Sprintf("cli-%d", time.Now().UTC().UnixNano())
	}
	payload := map[string]any{
		"assistant_id":  *assistantID,
		"input":         map[string]any{"messages": []map[string]any{{"role": "user", "content": *prompt}}},
		"on_disconnect": *onDisconnect,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encoding run request: %w", err)
	}
	endpoint := strings.TrimRight(*server, "/") + "/api/v1/threads/" + *threadID + "/runs"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building run request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("starting run: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		return fmt.Errorf("starting run: HTTP %s: %s", response.Status, strings.TrimSpace(string(data)))
	}
	if _, err := io.Copy(output, response.Body); err != nil {
		return fmt.Errorf("streaming run: %w", err)
	}
	return nil
}
func runConfig() error {
	if len(os.Args) < 3 || os.Args[2] != "validate" {
		return usage()
	}
	fs := flag.NewFlagSet("config validate", flag.ContinueOnError)
	path := fs.String("config", "config.yaml", "path to YAML configuration")
	if err := fs.Parse(os.Args[3:]); err != nil {
		return err
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	m, _ := cfg.SelectedModel()
	fmt.Printf("configuration valid: model=%s provider=%s address=%s\n", m.Name, m.Provider, cfg.Server.Address)
	return nil
}
func runMigrate() error {
	if len(os.Args) < 3 {
		return usage()
	}
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	databaseURL := fs.String("database-url", os.Getenv("DATABASE_URL"), "PostgreSQL URL")
	dir := fs.String("dir", "migrations", "migration directory")
	steps := fs.Int("steps", 1, "number of down migrations")
	forceVersion := fs.Int("version", -2, "migration version for force recovery (-1 resets to no version)")
	if err := fs.Parse(os.Args[3:]); err != nil {
		return err
	}
	switch os.Args[2] {
	case "up":
		if err := migrations.Up(*databaseURL, *dir); err != nil {
			return err
		}
		fmt.Println("migrations applied")
	case "down":
		if err := migrations.Down(*databaseURL, *dir, *steps); err != nil {
			return err
		}
		fmt.Println("migration rolled back")
	case "version":
		v, dirty, err := migrations.Version(*databaseURL, *dir)
		if err != nil {
			return err
		}
		fmt.Printf("version=%d dirty=%t\n", v, dirty)
	case "force":
		if *forceVersion < -1 {
			return errors.New("migrate force requires --version >= -1")
		}
		if err := migrations.Force(*databaseURL, *dir, *forceVersion); err != nil {
			return err
		}
		fmt.Printf("migration version forced to %d\n", *forceVersion)
	default:
		return usage()
	}
	return nil
}
