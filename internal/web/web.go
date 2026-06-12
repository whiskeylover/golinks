package web

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golinks/internal/store"
)

//go:embed templates/*.html static/*
var assets embed.FS

const topLinksLimit = 10
const favoriteLinksLimit = 10
const fallbackLinksLimit = 5
const searchLinksLimit = 50

type linkStore interface {
	Ping(ctx context.Context) error
	Get(ctx context.Context, shortcut string) (store.Link, error)
	CountFavorites(ctx context.Context) (int, error)
	ListFavorites(ctx context.Context, limit int) ([]store.Link, error)
	ListTop(ctx context.Context, limit int) ([]store.Link, error)
	Search(ctx context.Context, query string, limit int) ([]store.Link, error)
	RecordUse(ctx context.Context, shortcut string) error
	Delete(ctx context.Context, shortcut string) error
	SetFavorite(ctx context.Context, shortcut string, favorite bool) error
	Upsert(ctx context.Context, shortcut, destinationURL string) error
}

type Server struct {
	store     linkStore
	logger    *slog.Logger
	templates *template.Template
}

type pageData struct {
	FavoriteLinks  []store.Link
	FavoriteMore   int
	TopLinks       []store.Link
	SuggestedLinks []store.Link
	ShowLinks      bool
	Shortcut       string
	DestinationURL string
	Error          string
	Message        string
}

type searchLink struct {
	Shortcut       string `json:"shortcut"`
	DestinationURL string `json:"destination_url"`
	UseCount       int64  `json:"use_count"`
	IsFavorite     bool   `json:"is_favorite"`
	LastUsedAt     string `json:"last_used_at,omitempty"`
}

func New(linkStore linkStore, logger *slog.Logger) (*Server, error) {
	templates, err := template.ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{store: linkStore, logger: logger, templates: templates}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	static, err := fs.Sub(assets, "static")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /api/links", s.searchLinks)
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("POST /{$}", s.create)
	mux.HandleFunc("GET /edit/{shortcut...}", s.edit)
	mux.HandleFunc("POST /edit/{shortcut...}", s.save)
	mux.HandleFunc("GET /delete/{shortcut...}", s.confirmDelete)
	mux.HandleFunc("POST /delete/{shortcut...}", s.delete)
	mux.HandleFunc("POST /favorite/{shortcut...}", s.favorite)
	mux.HandleFunc("GET /{shortcut...}", s.redirect)
	return mux
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		s.logger.Error("health check failed", "error", err)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintln(w, "unhealthy")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "ok")
}

func (s *Server) searchLinks(w http.ResponseWriter, r *http.Request) {
	links, err := s.store.Search(r.Context(), r.URL.Query().Get("q"), searchLinksLimit)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	results := make([]searchLink, 0, len(links))
	for _, link := range links {
		var lastUsedAt string
		if link.LastUsedAt != nil {
			lastUsedAt = link.LastUsedAt.Format(time.RFC3339Nano)
		}
		results = append(results, searchLink{
			Shortcut:       link.Shortcut,
			DestinationURL: link.DestinationURL,
			UseCount:       link.UseCount,
			IsFavorite:     link.IsFavorite,
			LastUsedAt:     lastUsedAt,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(results); err != nil {
		s.logger.Error("encode searched links", "error", err)
	}
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	s.renderHome(w, r, pageData{}, http.StatusOK)
}

func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	shortcut, shortcutErr := normalizeShortcut(r.FormValue("shortcut"))
	destinationURL := strings.TrimSpace(r.FormValue("destination_url"))
	urlErr := validateDestinationURL(destinationURL)
	if shortcutErr != nil || urlErr != nil {
		s.renderHome(w, r, pageData{
			Shortcut:       shortcut,
			DestinationURL: destinationURL,
			Error:          errorMessage(shortcutErr, urlErr),
		}, http.StatusBadRequest)
		return
	}

	if err := s.store.Upsert(r.Context(), shortcut, destinationURL); err != nil {
		s.internalError(w, r, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) renderHome(w http.ResponseWriter, r *http.Request, data pageData, status int) {
	data.ShowLinks = r.URL.Query().Has("links")
	if !data.ShowLinks {
		s.renderStatus(w, r, "home.html", data, status)
		return
	}

	favoriteCount, err := s.store.CountFavorites(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	favorites, err := s.store.ListFavorites(r.Context(), favoriteLinksLimit)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	links, err := s.store.ListTop(r.Context(), topLinksLimit)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	data.FavoriteLinks = favorites
	if favoriteCount > len(favorites) {
		data.FavoriteMore = favoriteCount - len(favorites)
	}
	data.TopLinks = links
	s.renderStatus(w, r, "home.html", data, status)
}

func (s *Server) edit(w http.ResponseWriter, r *http.Request) {
	shortcut, err := normalizeShortcut(r.PathValue("shortcut"))
	if err != nil {
		s.render(w, r, "edit.html", pageData{Shortcut: shortcut, Error: err.Error()})
		return
	}

	link, err := s.store.Get(r.Context(), shortcut)
	if errors.Is(err, store.ErrNotFound) {
		s.render(w, r, "edit.html", pageData{Shortcut: shortcut})
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.render(w, r, "edit.html", pageData{
		Shortcut:       link.Shortcut,
		DestinationURL: link.DestinationURL,
		Message:        savedMessage(r),
	})
}

func (s *Server) save(w http.ResponseWriter, r *http.Request) {
	shortcut, shortcutErr := normalizeShortcut(r.PathValue("shortcut"))
	destinationURL := strings.TrimSpace(r.FormValue("destination_url"))
	urlErr := validateDestinationURL(destinationURL)
	if shortcutErr != nil || urlErr != nil {
		message := errorMessage(shortcutErr, urlErr)
		s.renderStatus(w, r, "edit.html", pageData{
			Shortcut:       shortcut,
			DestinationURL: destinationURL,
			Error:          message,
		}, http.StatusBadRequest)
		return
	}

	if err := s.store.Upsert(r.Context(), shortcut, destinationURL); err != nil {
		s.internalError(w, r, err)
		return
	}
	http.Redirect(w, r, "/edit/"+shortcut+"?saved=1", http.StatusSeeOther)
}

func (s *Server) confirmDelete(w http.ResponseWriter, r *http.Request) {
	shortcut, err := normalizeShortcut(r.PathValue("shortcut"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	link, err := s.store.Get(r.Context(), shortcut)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	s.render(w, r, "delete.html", pageData{
		Shortcut:       link.Shortcut,
		DestinationURL: link.DestinationURL,
	})
}

func (s *Server) delete(w http.ResponseWriter, r *http.Request) {
	shortcut, err := normalizeShortcut(r.PathValue("shortcut"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.store.Delete(r.Context(), shortcut); errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.internalError(w, r, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) favorite(w http.ResponseWriter, r *http.Request) {
	shortcut, err := normalizeShortcut(r.PathValue("shortcut"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var favorite bool
	switch r.FormValue("favorite") {
	case "1":
		favorite = true
	case "0":
		favorite = false
	default:
		http.Error(w, "favorite must be 0 or 1", http.StatusBadRequest)
		return
	}
	if err := s.store.SetFavorite(r.Context(), shortcut, favorite); errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		s.internalError(w, r, err)
		return
	}
	http.Redirect(w, r, "/?links=1", http.StatusSeeOther)
}

func (s *Server) redirect(w http.ResponseWriter, r *http.Request) {
	shortcut, err := normalizeShortcut(r.PathValue("shortcut"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	link, err := s.store.Get(r.Context(), shortcut)
	if errors.Is(err, store.ErrNotFound) {
		suggestions, err := s.suggestLinks(r.Context(), shortcut, fallbackLinksLimit)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		s.render(w, r, "edit.html", pageData{
			Shortcut:       shortcut,
			SuggestedLinks: suggestions,
			Message:        "This shortcut doesn't exist yet. Add a destination URL to create it.",
		})
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if err := s.store.RecordUse(r.Context(), shortcut); err != nil {
		s.internalError(w, r, err)
		return
	}
	http.Redirect(w, r, link.DestinationURL, http.StatusTemporaryRedirect)
}

func (s *Server) suggestLinks(ctx context.Context, shortcut string, limit int) ([]store.Link, error) {
	if limit <= 0 {
		return nil, nil
	}

	var suggestions []store.Link
	seenLinks := make(map[string]bool)
	for _, query := range suggestionQueries(shortcut) {
		links, err := s.store.Search(ctx, query, limit)
		if err != nil {
			return nil, err
		}
		for _, link := range links {
			if seenLinks[link.Shortcut] {
				continue
			}
			seenLinks[link.Shortcut] = true
			suggestions = append(suggestions, link)
			if len(suggestions) == limit {
				return suggestions, nil
			}
		}
	}
	return suggestions, nil
}

func suggestionQueries(shortcut string) []string {
	var queries []string
	seen := make(map[string]bool)
	add := func(query string) {
		query = strings.TrimSpace(query)
		if query == "" || seen[query] {
			return
		}
		seen[query] = true
		queries = append(queries, query)
	}

	add(shortcut)
	for _, part := range strings.FieldsFunc(shortcut, func(char rune) bool {
		return char == '/' || char == '-' || char == '_' || char == '.'
	}) {
		add(part)
	}
	return queries
}

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, data pageData) {
	s.renderStatus(w, r, name, data, http.StatusOK)
}

func (s *Server) renderStatus(w http.ResponseWriter, r *http.Request, name string, data pageData, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := s.templates.ExecuteTemplate(w, name, data); err != nil {
		s.logger.Error("render template", "path", r.URL.Path, "error", err)
	}
}

func (s *Server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	s.logger.Error("request failed", "method", r.Method, "path", r.URL.Path, "error", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}

func normalizeShortcut(value string) (string, error) {
	shortcut := strings.Trim(value, "/")
	if shortcut == "" {
		return shortcut, errors.New("shortcut is required")
	}
	if hasReservedPrefix(shortcut) {
		return shortcut, errors.New("shortcut uses a reserved path")
	}
	for _, part := range strings.Split(shortcut, "/") {
		if part == "" || part == "." || part == ".." || !isValidShortcutSegment(part) {
			return shortcut, errors.New("shortcut contains an invalid path segment")
		}
	}
	return shortcut, nil
}

func hasReservedPrefix(shortcut string) bool {
	for _, reserved := range []string{"api", "delete", "edit", "favorite", "healthz", "static"} {
		if shortcut == reserved || strings.HasPrefix(shortcut, reserved+"/") {
			return true
		}
	}
	return false
}

func isValidShortcutSegment(segment string) bool {
	for _, char := range segment {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.' {
			continue
		}
		return false
	}
	return true
}

func validateDestinationURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("destination URL must be an absolute http:// or https:// URL")
	}
	return nil
}

func savedMessage(r *http.Request) string {
	if r.URL.Query().Has("saved") {
		return "Saved."
	}
	return ""
}

func errorMessage(errs ...error) string {
	var messages []string
	for _, err := range errs {
		if err != nil {
			messages = append(messages, err.Error())
		}
	}
	return strings.Join(messages, ". ")
}
