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

	"golinks/internal/store"
)

//go:embed templates/*.html static/*
var assets embed.FS

const topLinksLimit = 10
const searchLinksLimit = 50

type linkStore interface {
	Get(ctx context.Context, shortcut string) (store.Link, error)
	ListTop(ctx context.Context, limit int) ([]store.Link, error)
	Search(ctx context.Context, query string, limit int) ([]store.Link, error)
	RecordUse(ctx context.Context, shortcut string) error
	Delete(ctx context.Context, shortcut string) error
	Upsert(ctx context.Context, shortcut, destinationURL string) error
}

type Server struct {
	store     linkStore
	logger    *slog.Logger
	templates *template.Template
}

type pageData struct {
	TopLinks       []store.Link
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
	mux.HandleFunc("GET /api/links", s.searchLinks)
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("POST /{$}", s.create)
	mux.HandleFunc("GET /edit/{shortcut...}", s.edit)
	mux.HandleFunc("POST /edit/{shortcut...}", s.save)
	mux.HandleFunc("GET /delete/{shortcut...}", s.confirmDelete)
	mux.HandleFunc("POST /delete/{shortcut...}", s.delete)
	mux.HandleFunc("GET /{shortcut...}", s.redirect)
	return mux
}

func (s *Server) searchLinks(w http.ResponseWriter, r *http.Request) {
	links, err := s.store.Search(r.Context(), r.URL.Query().Get("q"), searchLinksLimit)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	results := make([]searchLink, 0, len(links))
	for _, link := range links {
		results = append(results, searchLink{
			Shortcut:       link.Shortcut,
			DestinationURL: link.DestinationURL,
			UseCount:       link.UseCount,
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

	links, err := s.store.ListTop(r.Context(), topLinksLimit)
	if err != nil {
		s.internalError(w, r, err)
		return
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

func (s *Server) redirect(w http.ResponseWriter, r *http.Request) {
	shortcut, err := normalizeShortcut(r.PathValue("shortcut"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	link, err := s.store.Get(r.Context(), shortcut)
	if errors.Is(err, store.ErrNotFound) {
		s.render(w, r, "edit.html", pageData{
			Shortcut: shortcut,
			Message:  "This shortcut doesn't exist yet. Add a destination URL to create it.",
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
	for _, reserved := range []string{"api", "delete", "edit", "static"} {
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
