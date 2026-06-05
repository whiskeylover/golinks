package store

import (
	"context"
	"database/sql"
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
	if link.IsFavorite {
		t.Fatal("new link is favorite by default")
	}
	if link.LastUsedAt == nil {
		t.Fatal("last used time was not recorded")
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
	if restored.LastUsedAt == nil {
		t.Fatal("backup last used time was not preserved")
	}
}

func TestStorePing(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "golinks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.Ping(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestStoreMigratesExistingLinksToNonFavorite(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "golinks.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `
CREATE TABLE schema_migrations (
	name TEXT PRIMARY KEY,
	applied_at TEXT NOT NULL
);
CREATE TABLE links (
	shortcut TEXT PRIMARY KEY,
	destination_url TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	use_count INTEGER NOT NULL DEFAULT 0
);
INSERT INTO schema_migrations (name, applied_at) VALUES
	('001_create_links.sql', '2026-01-01T00:00:00Z'),
	('002_add_use_count.sql', '2026-01-01T00:00:00Z');
INSERT INTO links (shortcut, destination_url, use_count, created_at, updated_at)
VALUES ('docs', 'https://example.com/docs', 7, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	link, err := s.Get(ctx, "docs")
	if err != nil {
		t.Fatal(err)
	}
	if link.IsFavorite {
		t.Fatal("migrated link is favorite by default")
	}
	if link.LastUsedAt != nil {
		t.Fatalf("migrated link last used = %v, want nil", link.LastUsedAt)
	}
	if link.UseCount != 7 {
		t.Fatalf("use count = %d, want 7", link.UseCount)
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

func TestStoreFavorites(t *testing.T) {
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
	if err := s.SetFavorite(ctx, "gamma", true); err != nil {
		t.Fatal(err)
	}

	favoriteCount, err := s.CountFavorites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if favoriteCount != 1 {
		t.Fatalf("favorite count = %d, want 1", favoriteCount)
	}
	favorites, err := s.ListFavorites(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(favorites) != 1 || favorites[0].Shortcut != "gamma" || !favorites[0].IsFavorite {
		t.Fatalf("favorites = %#v", favorites)
	}

	topLinks, err := s.ListTop(ctx, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(topLinks) != 2 || topLinks[0].Shortcut != "beta" || topLinks[1].Shortcut != "alpha" {
		t.Fatalf("top links = %#v", topLinks)
	}

	if err := s.SetFavorite(ctx, "gamma", false); err != nil {
		t.Fatal(err)
	}
	link, err := s.Get(ctx, "gamma")
	if err != nil {
		t.Fatal(err)
	}
	if link.IsFavorite {
		t.Fatal("link remained favorite after unpin")
	}
	if err := s.SetFavorite(ctx, "missing", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetFavorite() error = %v, want ErrNotFound", err)
	}
}

func TestStoreSearchesShortcutLiterally(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "golinks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	for shortcut, destination := range map[string]string{
		"docs/onboarding": "https://example.com/onboarding",
		"docs/100%":       "https://example.com/percent",
		"docs/a_b":        "https://example.com/underscore",
	} {
		if err := s.Upsert(ctx, shortcut, destination); err != nil {
			t.Fatal(err)
		}
	}

	for query, want := range map[string]string{
		"board": "docs/onboarding",
		"%":     "docs/100%",
		"_":     "docs/a_b",
	} {
		links, err := s.Search(ctx, query, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(links) != 1 || links[0].Shortcut != want {
			t.Fatalf("Search(%q) = %#v, want %q", query, links, want)
		}
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

func TestStoreDelete(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "golinks.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.Upsert(ctx, "docs", "https://example.com/docs"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "docs"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "docs"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
	if err := s.Delete(ctx, "docs"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete() error = %v, want ErrNotFound", err)
	}
}
