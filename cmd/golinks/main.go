package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golinks/internal/store"
	"golinks/internal/web"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("golinks failed", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return serve(args)
	}
	switch args[0] {
	case "serve":
		return serve(args[1:])
	case "backup":
		return backup(args[1:])
	case "delete":
		return deleteLink(args[1:])
	case "list":
		return listLinks(args[1:])
	default:
		return fmt.Errorf("unknown command %q; use serve, backup, delete, or list", args[0])
	}
}

func serve(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := flags.String("addr", envOrDefault("GOLINKS_ADDR", ":8080"), "HTTP listen address")
	dbPath := flags.String("db", envOrDefault("GOLINKS_DB", "./data/golinks.db"), "SQLite database path")
	if err := flags.Parse(args); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	linkStore, err := store.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer linkStore.Close()

	app, err := web.New(linkStore, slog.Default())
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:              *addr,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("golinks listening", "addr", *addr, "db", *dbPath)
		errCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shut down HTTP server: %w", err)
		}
	}
	return nil
}

func backup(args []string) error {
	flags := flag.NewFlagSet("backup", flag.ContinueOnError)
	dbPath := flags.String("db", envOrDefault("GOLINKS_DB", "./data/golinks.db"), "SQLite database path")
	outputPath := flags.String("output", "", "backup output path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *outputPath == "" {
		return errors.New("backup requires -output")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	linkStore, err := store.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer linkStore.Close()

	if err := linkStore.Backup(ctx, *outputPath); err != nil {
		return err
	}
	slog.Info("backup created", "output", *outputPath)
	return nil
}

func deleteLink(args []string) error {
	flags := flag.NewFlagSet("delete", flag.ContinueOnError)
	dbPath := flags.String("db", envOrDefault("GOLINKS_DB", "./data/golinks.db"), "SQLite database path")
	shortcut := flags.String("shortcut", "", "shortcut to delete")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *shortcut == "" {
		return errors.New("delete requires -shortcut")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	linkStore, err := store.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer linkStore.Close()

	if err := linkStore.Delete(ctx, *shortcut); err != nil {
		return err
	}
	slog.Info("link deleted", "shortcut", *shortcut)
	return nil
}

func listLinks(args []string) error {
	flags := flag.NewFlagSet("list", flag.ContinueOnError)
	dbPath := flags.String("db", envOrDefault("GOLINKS_DB", "./data/golinks.db"), "SQLite database path")
	if err := flags.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	linkStore, err := store.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer linkStore.Close()

	links, err := linkStore.List(ctx)
	if err != nil {
		return err
	}
	for _, link := range links {
		fmt.Printf("%q -> %s\n", link.Shortcut, link.DestinationURL)
	}
	return nil
}

func envOrDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
