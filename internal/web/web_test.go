package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golinks/internal/store"
)

type memoryStore struct {
	links        map[string]store.Link
	listTopCalls int
}

func (s *memoryStore) Get(_ context.Context, shortcut string) (store.Link, error) {
	link, ok := s.links[shortcut]
	if !ok {
		return store.Link{}, store.ErrNotFound
	}
	return link, nil
}

func (s *memoryStore) ListTop(_ context.Context, limit int) ([]store.Link, error) {
	s.listTopCalls++
	var links []store.Link
	for _, link := range s.links {
		links = append(links, link)
	}
	if len(links) > limit {
		links = links[:limit]
	}
	return links, nil
}

func (s *memoryStore) RecordUse(_ context.Context, shortcut string) error {
	link, ok := s.links[shortcut]
	if !ok {
		return store.ErrNotFound
	}
	link.UseCount++
	s.links[shortcut] = link
	return nil
}

func (s *memoryStore) Upsert(_ context.Context, shortcut, destinationURL string) error {
	s.links[shortcut] = store.Link{Shortcut: shortcut, DestinationURL: destinationURL}
	return nil
}

func newTestHandler(t *testing.T) (http.Handler, *memoryStore) {
	t.Helper()
	linkStore := &memoryStore{links: make(map[string]store.Link)}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server, err := New(linkStore, logger)
	if err != nil {
		t.Fatal(err)
	}
	return server.Handler(), linkStore
}

func TestSaveEditAndRedirectNestedShortcut(t *testing.T) {
	handler, linkStore := newTestHandler(t)
	form := url.Values{"destination_url": {"https://example.com/start"}}
	save := httptest.NewRequest(http.MethodPost, "/edit/docs/onboarding", strings.NewReader(form.Encode()))
	save.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	saveResponse := httptest.NewRecorder()

	handler.ServeHTTP(saveResponse, save)
	if saveResponse.Code != http.StatusSeeOther {
		t.Fatalf("save status = %d", saveResponse.Code)
	}
	if got := linkStore.links["docs/onboarding"].DestinationURL; got != "https://example.com/start" {
		t.Fatalf("stored URL = %q", got)
	}

	edit := httptest.NewRequest(http.MethodGet, "/edit/docs/onboarding", nil)
	editResponse := httptest.NewRecorder()
	handler.ServeHTTP(editResponse, edit)
	if !strings.Contains(editResponse.Body.String(), "https://example.com/start") {
		t.Fatal("edit response does not include stored URL")
	}

	redirect := httptest.NewRequest(http.MethodGet, "/docs/onboarding", nil)
	redirectResponse := httptest.NewRecorder()
	handler.ServeHTTP(redirectResponse, redirect)
	if redirectResponse.Code != http.StatusTemporaryRedirect {
		t.Fatalf("redirect status = %d", redirectResponse.Code)
	}
	if location := redirectResponse.Header().Get("Location"); location != "https://example.com/start" {
		t.Fatalf("redirect location = %q", location)
	}
	if count := linkStore.links["docs/onboarding"].UseCount; count != 1 {
		t.Fatalf("use count = %d, want 1", count)
	}
}

func TestCreateLinkFromHome(t *testing.T) {
	handler, linkStore := newTestHandler(t)
	form := url.Values{
		"shortcut":        {"docs/onboarding"},
		"destination_url": {"https://example.com/start"},
	}
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", response.Code)
	}
	if location := response.Header().Get("Location"); location != "/" {
		t.Fatalf("location = %q", location)
	}
	if got := linkStore.links["docs/onboarding"].DestinationURL; got != "https://example.com/start" {
		t.Fatalf("stored URL = %q", got)
	}
}

func TestHomeOnlyShowsTopLinksWhenRequested(t *testing.T) {
	handler, linkStore := newTestHandler(t)
	linkStore.links["docs"] = store.Link{Shortcut: "docs", DestinationURL: "https://example.com/docs", UseCount: 3}

	hidden := httptest.NewRecorder()
	handler.ServeHTTP(hidden, httptest.NewRequest(http.MethodGet, "/", nil))
	if strings.Contains(hidden.Body.String(), "Top links") || strings.Contains(hidden.Body.String(), "go/docs") {
		t.Fatalf("default home response includes links: %q", hidden.Body.String())
	}
	if linkStore.listTopCalls != 0 {
		t.Fatalf("ListTop() called %d times for default home", linkStore.listTopCalls)
	}

	shown := httptest.NewRecorder()
	handler.ServeHTTP(shown, httptest.NewRequest(http.MethodGet, "/?links=1", nil))
	body := shown.Body.String()
	if !strings.Contains(body, "Top links") || !strings.Contains(body, "go/docs") || !strings.Contains(body, "3x") || !strings.Contains(body, "/edit/docs") {
		t.Fatalf("response body = %q", body)
	}
	if linkStore.listTopCalls != 1 {
		t.Fatalf("ListTop() called %d times, want 1", linkStore.listTopCalls)
	}
}

func TestUnknownShortcutReturnsNotFound(t *testing.T) {
	handler, _ := newTestHandler(t)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/missing", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestSaveRejectsInvalidValuesAndPreservesURL(t *testing.T) {
	handler, _ := newTestHandler(t)
	form := url.Values{"destination_url": {"not-a-url"}}
	request := httptest.NewRequest(http.MethodPost, "/edit/edit/admin", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, "reserved path") || !strings.Contains(body, "not-a-url") {
		t.Fatalf("response body = %q", body)
	}
}

func TestCreateRejectsInvalidValuesAndPreservesFields(t *testing.T) {
	handler, _ := newTestHandler(t)
	form := url.Values{
		"shortcut":        {"edit/admin"},
		"destination_url": {"not-a-url"},
	}
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, "reserved path") || !strings.Contains(body, "edit/admin") || !strings.Contains(body, "not-a-url") {
		t.Fatalf("response body = %q", body)
	}
}
