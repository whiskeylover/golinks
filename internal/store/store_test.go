package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestStoreUpsertGetListAndBackup(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "golinks.db")
	s, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.Upsert(ctx, "docs/onboarding", "https://example.com/start"); err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert(ctx, "docs/onboarding", "https://example.com/updated"); err != nil {
		t.Fatal(err)
	}

	link, err := s.Get(ctx, "docs/onboarding")
	if err != nil {
		t.Fatal(err)
	}
	if link.DestinationURL != "https://example.com/updated" {
		t.Fatalf("destination URL = %q", link.DestinationURL)
	}
	if err := s.RecordUse(ctx, "docs/onboarding"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordUse(ctx, "docs/onboarding"); err != nil {
		t.Fatal(err)
	}
	link, err = s.Get(ctx, "docs/onboarding")
	if err != nil {
		t.Fatal(err)
	}
	if link.UseCount != 2 {
		t.Fatalf("use count = %d, want 2", link.UseCount)
	}

	links, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 {
		t.Fatalf("got %d links, want 1", len(links))
	}
	topLinks, err := s.ListTop(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(topLinks) != 1 || topLinks[0].Shortcut != "docs/onboarding" {
		t.Fatalf("top links = %#v", topLinks)
	}

	backupPath := filepath.Join(t.TempDir(), "backups", "golinks.db")
	if err := s.Backup(ctx, backupPath); err != nil {
		t.Fatal(err)
	}

	backup, err := Open(ctx, backupPath)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	restored, err := backup.Get(ctx, "docs/onboarding")
	if err != nil {
		t.Fatal(err)
	}
	if restored.DestinationURL != "https://example.com/updated" {
		t.Fatalf("backup destination URL = %q", restored.DestinationURL)
	}
	if restored.UseCount != 2 {
		t.Fatalf("backup use count = %d, want 2", restored.UseCount)
	}
}

func TestStoreListTopOrdersByUsage(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "golinks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for shortcut, destination := range map[string]string{
		"alpha": "https://example.com/alpha",
		"beta":  "https://example.com/beta",
		"gamma": "https://example.com/gamma",
	} {
		if err := s.Upsert(ctx, shortcut, destination); err != nil {
			t.Fatal(err)
		}
	}
	for _, shortcut := range []string{"gamma", "beta", "gamma"} {
		if err := s.RecordUse(ctx, shortcut); err != nil {
			t.Fatal(err)
		}
	}

	links, err := s.ListTop(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 2 || links[0].Shortcut != "gamma" || links[1].Shortcut != "beta" {
		t.Fatalf("top links = %#v", links)
	}
}

func TestStoreGetMissing(t *testing.T) {
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "golinks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	_, err = s.Get(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
}
