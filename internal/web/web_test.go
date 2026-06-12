package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golinks/internal/store"
)

type memoryStore struct {
	links              map[string]store.Link
	favoriteCount      int
	favoriteLimit      int
	countFavoriteCalls int
	listFavoriteCalls  int
	listTopCalls       int
	listTopLimit       int
	searchCalls        int
	searchQuery        string
	searchLimit        int
	healthErr          error
}

func (s *memoryStore) Ping(_ context.Context) error {
	return s.healthErr
}

func (s *memoryStore) Get(_ context.Context, shortcut string) (store.Link, error) {
	link, ok := s.links[shortcut]
	if !ok {
		return store.Link{}, store.ErrNotFound
	}
	if link.ExpiresAt != nil && !link.ExpiresAt.After(time.Now().UTC()) {
		delete(s.links, shortcut)
		return store.Link{}, store.ErrNotFound
	}
	return link, nil
}

func (s *memoryStore) CountFavorites(_ context.Context) (int, error) {
	s.countFavoriteCalls++
	count := 0
	for _, link := range s.links {
		if link.ExpiresAt != nil && !link.ExpiresAt.After(time.Now().UTC()) {
			continue
		}
		if link.IsFavorite {
			count++
		}
	}
	return count, nil
}

func (s *memoryStore) ListFavorites(_ context.Context, limit int) ([]store.Link, error) {
	s.listFavoriteCalls++
	s.favoriteLimit = limit
	var links []store.Link
	for _, link := range s.links {
		if link.ExpiresAt != nil && !link.ExpiresAt.After(time.Now().UTC()) {
			continue
		}
		if link.IsFavorite {
			links = append(links, link)
		}
	}
	if len(links) > limit {
		links = links[:limit]
	}
	return links, nil
}

func (s *memoryStore) ListTop(_ context.Context, limit int) ([]store.Link, error) {
	s.listTopCalls++
	s.listTopLimit = limit
	var links []store.Link
	for _, link := range s.links {
		if link.ExpiresAt != nil && !link.ExpiresAt.After(time.Now().UTC()) {
			continue
		}
		if link.IsFavorite {
			continue
		}
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
		if link.ExpiresAt != nil && !link.ExpiresAt.After(time.Now().UTC()) {
			continue
		}
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
	if link.ExpiresAt != nil && !link.ExpiresAt.After(time.Now().UTC()) {
		delete(s.links, shortcut)
		return store.ErrNotFound
	}
	link.UseCount++
	now := time.Now().UTC()
	link.LastUsedAt = &now
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

func (s *memoryStore) SetFavorite(_ context.Context, shortcut string, favorite bool) error {
	link, ok := s.links[shortcut]
	if !ok {
		return store.ErrNotFound
	}
	link.IsFavorite = favorite
	s.links[shortcut] = link
	return nil
}

func (s *memoryStore) Upsert(_ context.Context, shortcut, destinationURL string, expiresAt *time.Time) error {
	s.links[shortcut] = store.Link{Shortcut: shortcut, DestinationURL: destinationURL, ExpiresAt: expiresAt}
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

func TestHealthEndpoint(t *testing.T) {
	handler, _ := newTestHandler(t)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "text/plain; charset=utf-8" {
		t.Fatalf("content type = %q", contentType)
	}
	if body := response.Body.String(); body != "ok\n" {
		t.Fatalf("body = %q", body)
	}
}

func TestHealthEndpointReportsStoreFailure(t *testing.T) {
	handler, linkStore := newTestHandler(t)
	linkStore.healthErr = errors.New("database unavailable")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", response.Code)
	}
	if body := response.Body.String(); body != "unhealthy\n" {
		t.Fatalf("body = %q", body)
	}
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
	if linkStore.links["docs/onboarding"].LastUsedAt == nil {
		t.Fatal("last used time was not recorded")
	}
}

func TestEditCanMakeLinkTemporary(t *testing.T) {
	handler, linkStore := newTestHandler(t)
	expiresDate := time.Now().UTC().AddDate(0, 0, 7).Format("2006-01-02")
	form := url.Values{
		"destination_url": {"https://example.com/event"},
		"is_temporary":    {"1"},
		"expires_date":    {expiresDate},
	}
	save := httptest.NewRequest(http.MethodPost, "/edit/event", strings.NewReader(form.Encode()))
	save.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	saveResponse := httptest.NewRecorder()

	handler.ServeHTTP(saveResponse, save)

	if saveResponse.Code != http.StatusSeeOther {
		t.Fatalf("save status = %d", saveResponse.Code)
	}
	link := linkStore.links["event"]
	if link.ExpiresAt == nil || link.ExpiresAt.Format("2006-01-02") <= expiresDate {
		t.Fatalf("expires at = %v, want after %s", link.ExpiresAt, expiresDate)
	}

	edit := httptest.NewRecorder()
	handler.ServeHTTP(edit, httptest.NewRequest(http.MethodGet, "/edit/event", nil))
	editBody := edit.Body.String()
	if !strings.Contains(editBody, `name="is_temporary" type="checkbox" value="1" checked`) || !strings.Contains(editBody, `name="expires_date" type="date" value="`+expiresDate+`"`) {
		t.Fatalf("edit body = %q", editBody)
	}

	home := httptest.NewRecorder()
	handler.ServeHTTP(home, httptest.NewRequest(http.MethodGet, "/?links=1", nil))
	if !strings.Contains(home.Body.String(), `class="temp-indicator icon-button"`) || !strings.Contains(home.Body.String(), `title="Expires on `+expiresDate+`"`) {
		t.Fatalf("home body = %q", home.Body.String())
	}

	search := httptest.NewRecorder()
	handler.ServeHTTP(search, httptest.NewRequest(http.MethodGet, "/api/links?q=event", nil))
	if !strings.Contains(search.Body.String(), `"expires_date":"`+expiresDate+`"`) {
		t.Fatalf("search body = %q", search.Body.String())
	}
}

func TestEditRejectsTemporaryLinkWithoutExpirationDate(t *testing.T) {
	handler, linkStore := newTestHandler(t)
	form := url.Values{
		"destination_url": {"https://example.com/event"},
		"is_temporary":    {"1"},
	}
	request := httptest.NewRequest(http.MethodPost, "/edit/event", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), "expiration date is required") {
		t.Fatalf("body = %q", response.Body.String())
	}
	if _, ok := linkStore.links["event"]; ok {
		t.Fatal("stored temporary link without expiration date")
	}
}

func TestCreateLinkFromHome(t *testing.T) {
	handler, linkStore := newTestHandler(t)
	home := httptest.NewRecorder()
	handler.ServeHTTP(home, httptest.NewRequest(http.MethodGet, "/", nil))
	homeBody := home.Body.String()
	if !strings.Contains(homeBody, `class="temp-icon-button icon-button"`) || !strings.Contains(homeBody, `name="is_temporary" type="checkbox" value="1"`) || !strings.Contains(homeBody, `name="expires_date" type="date"`) {
		t.Fatalf("home form does not include temporary controls: %q", homeBody)
	}

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
	if linkStore.links["docs/onboarding"].ExpiresAt != nil {
		t.Fatal("home-created link is temporary by default")
	}
}

func TestCreateTemporaryLinkFromHome(t *testing.T) {
	handler, linkStore := newTestHandler(t)
	expiresDate := time.Now().UTC().AddDate(0, 0, 3).Format("2006-01-02")
	form := url.Values{
		"shortcut":        {"campaign"},
		"destination_url": {"https://example.com/campaign"},
		"is_temporary":    {"1"},
		"expires_date":    {expiresDate},
	}
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", response.Code)
	}
	link := linkStore.links["campaign"]
	if link.ExpiresAt == nil || link.ExpiresAt.Format("2006-01-02") <= expiresDate {
		t.Fatalf("expires at = %v, want after %s", link.ExpiresAt, expiresDate)
	}
}

func TestCreateRejectsTemporaryLinkWithoutExpirationDate(t *testing.T) {
	handler, linkStore := newTestHandler(t)
	form := url.Values{
		"shortcut":        {"campaign"},
		"destination_url": {"https://example.com/campaign"},
		"is_temporary":    {"1"},
	}
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, "expiration date is required") || !strings.Contains(body, `name="is_temporary" type="checkbox" value="1" checked`) {
		t.Fatalf("body = %q", body)
	}
	if _, ok := linkStore.links["campaign"]; ok {
		t.Fatal("stored temporary link without expiration date")
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
	if linkStore.listFavoriteCalls != 0 {
		t.Fatalf("ListFavorites() called %d times for default home", linkStore.listFavoriteCalls)
	}
	if linkStore.countFavoriteCalls != 0 {
		t.Fatalf("CountFavorites() called %d times for default home", linkStore.countFavoriteCalls)
	}

	shown := httptest.NewRecorder()
	handler.ServeHTTP(shown, httptest.NewRequest(http.MethodGet, "/?links=1", nil))
	body := shown.Body.String()
	if !strings.Contains(body, "Top links") || !strings.Contains(body, "go/docs") || !strings.Contains(body, `class="usage-count">3</span>`) || !strings.Contains(body, "/edit/docs") || !strings.Contains(body, `action="/favorite/docs"`) || !strings.Contains(body, `data-copy-path="/docs"`) || !strings.Contains(body, `data-qr-path="/docs"`) || !strings.Contains(body, `/static/qrcode-generator.js`) {
		t.Fatalf("response body = %q", body)
	}
	if linkStore.listFavoriteCalls != 1 {
		t.Fatalf("ListFavorites() called %d times, want 1", linkStore.listFavoriteCalls)
	}
	if linkStore.countFavoriteCalls != 1 {
		t.Fatalf("CountFavorites() called %d times, want 1", linkStore.countFavoriteCalls)
	}
	if linkStore.favoriteLimit != 10 {
		t.Fatalf("ListFavorites() limit = %d, want 10", linkStore.favoriteLimit)
	}
	if linkStore.listTopCalls != 1 {
		t.Fatalf("ListTop() called %d times, want 1", linkStore.listTopCalls)
	}
	if linkStore.listTopLimit != 10 {
		t.Fatalf("ListTop() limit = %d, want 10", linkStore.listTopLimit)
	}
}

func TestHomeShowsFavoritesAboveTopLinks(t *testing.T) {
	handler, linkStore := newTestHandler(t)
	linkStore.links["docs"] = store.Link{Shortcut: "docs", DestinationURL: "https://example.com/docs", UseCount: 9, IsFavorite: true}
	linkStore.links["calendar"] = store.Link{Shortcut: "calendar", DestinationURL: "https://example.com/calendar", UseCount: 4}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/?links=1", nil))
	body := response.Body.String()
	if !strings.Contains(body, "Favorites") || !strings.Contains(body, "Top links") {
		t.Fatalf("response body = %q", body)
	}
	if strings.Index(body, "Favorites") > strings.Index(body, "Top links") {
		t.Fatalf("favorites were not shown before top links: %q", body)
	}
	if !strings.Contains(body, `<span class="row-actions">`) || !strings.Contains(body, `class="edit icon-link"`) || !strings.Contains(body, `aria-label="Edit go/docs"`) {
		t.Fatalf("favorite row does not include compact icon actions: %q", body)
	}
	if !strings.Contains(body, `name="favorite" type="hidden" value="0"`) || !strings.Contains(body, `class="pin-button is-pinned"`) || !strings.Contains(body, `aria-label="Unfavorite go/docs"`) {
		t.Fatalf("favorite row does not include unfavorite button: %q", body)
	}
	if !strings.Contains(body, `name="favorite" type="hidden" value="1"`) || !strings.Contains(body, `aria-label="Favorite go/calendar"`) {
		t.Fatalf("top row does not include favorite button: %q", body)
	}
}

func TestHomeShowsFavoriteOverflowHint(t *testing.T) {
	handler, linkStore := newTestHandler(t)
	for i := 0; i < 12; i++ {
		shortcut := fmt.Sprintf("fav-%02d", i)
		linkStore.links[shortcut] = store.Link{Shortcut: shortcut, DestinationURL: "https://example.com", IsFavorite: true}
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/?links=1", nil))
	body := response.Body.String()
	if strings.Count(body, `class="pin-button is-pinned"`) != 10 {
		t.Fatalf("favorite rows rendered = %d, want 10, body = %q", strings.Count(body, `class="pin-button is-pinned"`), body)
	}
	if !strings.Contains(body, "+2 more. Search to find.") {
		t.Fatalf("response body = %q", body)
	}
}

func TestFaviconIsLinkedAndServed(t *testing.T) {
	handler, _ := newTestHandler(t)
	home := httptest.NewRecorder()
	handler.ServeHTTP(home, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(home.Body.String(), `href="/static/favicon.svg"`) {
		t.Fatal("home page does not link favicon")
	}

	icon := httptest.NewRecorder()
	handler.ServeHTTP(icon, httptest.NewRequest(http.MethodGet, "/static/favicon.svg", nil))
	if icon.Code != http.StatusOK {
		t.Fatalf("favicon status = %d", icon.Code)
	}
	if !strings.Contains(icon.Body.String(), "<svg") {
		t.Fatalf("favicon body = %q", icon.Body.String())
	}
}

func TestSearchLinks(t *testing.T) {
	handler, linkStore := newTestHandler(t)
	lastUsedAt := time.Date(2026, 6, 5, 21, 45, 0, 0, time.UTC)
	linkStore.links["docs/onboarding"] = store.Link{Shortcut: "docs/onboarding", DestinationURL: "https://example.com/docs", UseCount: 3, IsFavorite: true, LastUsedAt: &lastUsedAt}
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
	if !strings.Contains(body, `"shortcut":"docs/onboarding"`) || !strings.Contains(body, `"is_favorite":true`) || !strings.Contains(body, `"last_used_at":"2026-06-05T21:45:00Z"`) || strings.Contains(body, `"calendar"`) {
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
	if !strings.Contains(body, "const topLinksHTML = results.innerHTML") || !strings.Contains(body, "results.innerHTML = topLinksHTML") || !strings.Contains(body, "/api/links?q=") || !strings.Contains(body, "/favorite/${path}") || !strings.Contains(body, "Unfavorite") || !strings.Contains(body, "Favorite") || !strings.Contains(body, "data-qr-path") || !strings.Contains(body, "new URL(button.dataset.qrPath, window.location.origin).href") || !strings.Contains(body, "`go${button.dataset.qrPath}`") || !strings.Contains(body, "qrcode(0, \"M\")") || !strings.Contains(body, "title.textContent = label") || !strings.Contains(body, "link.is_favorite") || !strings.Contains(body, "temp-indicator") || !strings.Contains(body, "Expires on") || !strings.Contains(body, "navigator.clipboard") || !strings.Contains(body, "data-copy-path") || strings.Contains(body, "data-qr-url") {
		t.Fatalf("search script = %q", body)
	}
}

func TestQRCodeGeneratorIsServed(t *testing.T) {
	handler, _ := newTestHandler(t)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/static/qrcode-generator.js", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if !strings.Contains(response.Body.String(), "QR Code Generator for JavaScript") || !strings.Contains(response.Body.String(), "MIT license") {
		t.Fatalf("QR script body = %q", response.Body.String())
	}
}

func TestFavoriteRoutePinsAndUnpins(t *testing.T) {
	handler, linkStore := newTestHandler(t)
	linkStore.links["docs/onboarding"] = store.Link{Shortcut: "docs/onboarding", DestinationURL: "https://example.com/docs"}

	pinForm := url.Values{"favorite": {"1"}}
	pin := httptest.NewRequest(http.MethodPost, "/favorite/docs/onboarding", strings.NewReader(pinForm.Encode()))
	pin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	pinResponse := httptest.NewRecorder()
	handler.ServeHTTP(pinResponse, pin)
	if pinResponse.Code != http.StatusSeeOther {
		t.Fatalf("pin status = %d", pinResponse.Code)
	}
	if location := pinResponse.Header().Get("Location"); location != "/?links=1" {
		t.Fatalf("pin location = %q", location)
	}
	if !linkStore.links["docs/onboarding"].IsFavorite {
		t.Fatal("link was not pinned")
	}

	unpinForm := url.Values{"favorite": {"0"}}
	unpin := httptest.NewRequest(http.MethodPost, "/favorite/docs/onboarding", strings.NewReader(unpinForm.Encode()))
	unpin.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	unpinResponse := httptest.NewRecorder()
	handler.ServeHTTP(unpinResponse, unpin)
	if unpinResponse.Code != http.StatusSeeOther {
		t.Fatalf("unpin status = %d", unpinResponse.Code)
	}
	if linkStore.links["docs/onboarding"].IsFavorite {
		t.Fatal("link was not unpinned")
	}
}

func TestFavoriteRouteRejectsMissingAndInvalidShortcut(t *testing.T) {
	handler, _ := newTestHandler(t)
	form := url.Values{"favorite": {"1"}}
	for _, path := range []string{"/favorite/missing", "/favorite/has%20space"} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusNotFound {
				t.Fatalf("status = %d", response.Code)
			}
		})
	}
}

func TestUnknownShortcutOffersToCreateLink(t *testing.T) {
	handler, linkStore := newTestHandler(t)
	linkStore.links["missing-docs"] = store.Link{Shortcut: "missing-docs", DestinationURL: "https://example.com/docs", UseCount: 4}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/missing", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, "This shortcut doesn&#39;t exist yet.") || !strings.Contains(body, `value="missing" disabled`) || !strings.Contains(body, `action="/edit/missing"`) || !strings.Contains(body, "Likely matches") || !strings.Contains(body, `href="/missing-docs"`) || !strings.Contains(body, "https://example.com/docs") || !strings.Contains(body, `class="usage-count">4</span>`) {
		t.Fatalf("response body = %q", body)
	}
	if linkStore.searchCalls != 1 || linkStore.searchQuery != "missing" || linkStore.searchLimit != 5 {
		t.Fatalf("search calls = %d, query = %q, limit = %d", linkStore.searchCalls, linkStore.searchQuery, linkStore.searchLimit)
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

func TestUnknownNestedShortcutShowsTokenFallbackMatches(t *testing.T) {
	handler, linkStore := newTestHandler(t)
	linkStore.links["docs/onboarding"] = store.Link{Shortcut: "docs/onboarding", DestinationURL: "https://example.com/docs", UseCount: 8}
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/docs/onboardng", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, "Likely matches") || !strings.Contains(body, `href="/docs/onboarding"`) || !strings.Contains(body, "https://example.com/docs") {
		t.Fatalf("response body = %q", body)
	}
	if linkStore.searchCalls != 3 || linkStore.searchQuery != "onboardng" || linkStore.searchLimit != 5 {
		t.Fatalf("search calls = %d, last query = %q, limit = %d", linkStore.searchCalls, linkStore.searchQuery, linkStore.searchLimit)
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
	for _, shortcut := range []string{"has space", "has@symbol", "api/links", "delete/admin", "favorite/docs", "healthz"} {
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
