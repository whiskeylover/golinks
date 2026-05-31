package web

import (
	"context"
	"embed"
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

type linkStore interface {
	Get(ctx context.Context, shortcut string) (store.Link, error)
	ListTop(ctx context.Context, limit int) ([]store.Link, error)
	RecordUse(ctx context.Context, shortcut string) error
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
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("POST /{$}", s.create)
	mux.HandleFunc("GET /edit/{shortcut...}", s.edit)
	mux.HandleFunc("POST /edit/{shortcut...}", s.save)
	mux.HandleFunc("GET /{shortcut...}", s.redirect)
	return mux
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

	links, err := s.store.ListTop(r.Context(), 5)
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
	http.Redirect(w, r, "/edit/"+shortcut, http.StatusSeeOther)
}

func (s *Server) redirect(w http.ResponseWriter, r *http.Request) {
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
	if strings.HasPrefix(shortcut, "edit/") || shortcut == "edit" || strings.HasPrefix(shortcut, "static/") || shortcut == "static" {
		return shortcut, errors.New("shortcut uses a reserved path")
	}
	for _, part := range strings.Split(shortcut, "/") {
		if part == "" || part == "." || part == ".." {
			return shortcut, errors.New("shortcut contains an invalid path segment")
		}
	}
	return shortcut, nil
}

func validateDestinationURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("destination URL must be an absolute http:// or https:// URL")
	}
	return nil
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
