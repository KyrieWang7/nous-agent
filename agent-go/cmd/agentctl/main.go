package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

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
	default:
		return fmt.Errorf("unknown command %q", os.Args[1])
	}
}
func usage() error {
	return fmt.Errorf("usage: agentctl <config validate|migrate up|migrate down|migrate version>")
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
