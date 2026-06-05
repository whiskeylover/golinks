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
	listTopLimit int
	searchCalls  int
	searchQuery  string
	searchLimit  int
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
	s.listTopLimit = limit
	var links []store.Link
	for _, link := range s.links {
		links = append(links, link)
	}
	if len(links) > limit {
		links = links[:limit]
	}
	return links, nil
}

func (s *memoryStore) Search(_ context.Context, query string, limit int) ([]store.Link, error) {
	s.searchCalls++
	s.searchQuery = query
	s.searchLimit = limit
	var links []store.Link
	for _, link := range s.links {
		if strings.Contains(link.Shortcut, query) {
			links = append(links, link)
		}
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

func (s *memoryStore) Delete(_ context.Context, shortcut string) error {
	if _, ok := s.links[shortcut]; !ok {
		return store.ErrNotFound
	}
	delete(s.links, shortcut)
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
	if location := saveResponse.Header().Get("Location"); location != "/edit/docs/onboarding?saved=1" {
		t.Fatalf("save location = %q", location)
	}
	if got := linkStore.links["docs/onboarding"].DestinationURL; got != "https://example.com/start" {
		t.Fatalf("stored URL = %q", got)
	}

	edit := httptest.NewRequest(http.MethodGet, "/edit/docs/onboarding?saved=1", nil)
	editResponse := httptest.NewRecorder()
	handler.ServeHTTP(editResponse, edit)
	editBody := editResponse.Body.String()
	if !strings.Contains(editBody, "https://example.com/start") || !strings.Contains(editBody, `value="docs/onboarding" disabled`) || !strings.Contains(editBody, "Saved.") {
		t.Fatal("edit response does not include stored URL, disabled shortcut, and saved message")
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
	if !strings.Contains(body, "Top links") || !strings.Contains(body, "go/docs") || !strings.Contains(body, `class="usage-count">3</span>`) || !strings.Contains(body, "/edit/docs") {
		t.Fatalf("response body = %q", body)
	}
	if linkStore.listTopCalls != 1 {
		t.Fatalf("ListTop() called %d times, want 1", linkStore.listTopCalls)
	}
	if linkStore.listTopLimit != 10 {
		t.Fatalf("ListTop() limit = %d, want 10", linkStore.listTopLimit)
	}
}

func TestSearchLinks(t *testing.T) {
	handler, linkStore := newTestHandler(t)
	linkStore.links["docs/onboarding"] = store.Link{Shortcut: "docs/onboarding", DestinationURL: "https://example.com/docs", UseCount: 3}
	linkStore.links["calendar"] = store.Link{Shortcut: "calendar", DestinationURL: "https://example.com/calendar", UseCount: 1}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/links?q=board", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/json" {
		t.Fatalf("content type = %q", contentType)
	}
	body := response.Body.String()
	if !strings.Contains(body, `"shortcut":"docs/onboarding"`) || strings.Contains(body, `"calendar"`) {
		t.Fatalf("response body = %q", body)
	}
	if linkStore.searchCalls != 1 || linkStore.searchQuery != "board" || linkStore.searchLimit != 50 {
		t.Fatalf("search calls = %d, query = %q, limit = %d", linkStore.searchCalls, linkStore.searchQuery, linkStore.searchLimit)
	}
}

func TestSearchScriptCachesTopLinks(t *testing.T) {
	handler, _ := newTestHandler(t)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/static/search.js", nil))
	body := response.Body.String()
	if !strings.Contains(body, "const topLinksHTML = results.innerHTML") || !strings.Contains(body, "results.innerHTML = topLinksHTML") || !strings.Contains(body, "/api/links?q=") {
		t.Fatalf("search script = %q", body)
	}
}

func TestUnknownShortcutOffersToCreateLink(t *testing.T) {
	handler, linkStore := newTestHandler(t)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/missing", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, "This shortcut doesn&#39;t exist yet.") || !strings.Contains(body, `value="missing" disabled`) || !strings.Contains(body, `action="/edit/missing"`) {
		t.Fatalf("response body = %q", body)
	}

	form := url.Values{"destination_url": {"https://example.com/missing"}}
	save := httptest.NewRequest(http.MethodPost, "/edit/missing", strings.NewReader(form.Encode()))
	save.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	saveResponse := httptest.NewRecorder()
	handler.ServeHTTP(saveResponse, save)
	if saveResponse.Code != http.StatusSeeOther {
		t.Fatalf("save status = %d", saveResponse.Code)
	}
	if location := saveResponse.Header().Get("Location"); location != "/edit/missing?saved=1" {
		t.Fatalf("save location = %q", location)
	}
	if got := linkStore.links["missing"].DestinationURL; got != "https://example.com/missing" {
		t.Fatalf("stored URL = %q", got)
	}
}

func TestInvalidShortcutReturnsNotFound(t *testing.T) {
	handler, _ := newTestHandler(t)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/has%20space", nil))
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

func TestCreateRejectsInvalidShortcutCharacters(t *testing.T) {
	for _, shortcut := range []string{"has space", "has@symbol", "api/links", "delete/admin"} {
		t.Run(shortcut, func(t *testing.T) {
			handler, linkStore := newTestHandler(t)
			form := url.Values{
				"shortcut":        {shortcut},
				"destination_url": {"https://example.com"},
			}
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			response := httptest.NewRecorder()

			handler.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d", response.Code)
			}
			if len(linkStore.links) != 0 {
				t.Fatalf("stored invalid shortcut %q", shortcut)
			}
		})
	}
}

func TestDeleteRequiresConfirmationPost(t *testing.T) {
	handler, linkStore := newTestHandler(t)
	linkStore.links["docs/onboarding"] = store.Link{
		Shortcut:       "docs/onboarding",
		DestinationURL: "https://example.com/docs",
	}

	confirm := httptest.NewRecorder()
	handler.ServeHTTP(confirm, httptest.NewRequest(http.MethodGet, "/delete/docs/onboarding", nil))
	if confirm.Code != http.StatusOK {
		t.Fatalf("confirmation status = %d", confirm.Code)
	}
	body := confirm.Body.String()
	if !strings.Contains(body, "Delete shortcut") || !strings.Contains(body, "go/docs/onboarding") || !strings.Contains(body, "https://example.com/docs") {
		t.Fatalf("confirmation body = %q", body)
	}
	if _, ok := linkStore.links["docs/onboarding"]; !ok {
		t.Fatal("GET confirmation deleted link")
	}

	remove := httptest.NewRecorder()
	handler.ServeHTTP(remove, httptest.NewRequest(http.MethodPost, "/delete/docs/onboarding", nil))
	if remove.Code != http.StatusSeeOther {
		t.Fatalf("delete status = %d", remove.Code)
	}
	if location := remove.Header().Get("Location"); location != "/" {
		t.Fatalf("delete location = %q", location)
	}
	if _, ok := linkStore.links["docs/onboarding"]; ok {
		t.Fatal("POST delete did not remove link")
	}
}
