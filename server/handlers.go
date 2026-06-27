package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// NewMux wires the kanban routes to a Board.
// Routes:
//
//	GET    /api/cards         list all cards
//	POST   /api/cards         create
//	PATCH  /api/cards/{id}    sparse update
//	DELETE /api/cards/{id}    remove
//	GET    /                  static frontend (served by caller separately)
func NewMux(b *Board) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/cards", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, b.ListCards())
		case http.MethodPost:
			handleCreate(w, r, b)
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/cards/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/cards/")
		if id == "" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodPatch:
			handleUpdate(w, r, b, id)
		case http.MethodDelete:
			handleDelete(w, r, b, id)
		default:
			w.Header().Set("Allow", "PATCH, DELETE")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/columns", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, b.Columns())
		case http.MethodPost:
			handleAddColumn(w, r, b)
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/columns/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/columns/")
		if id == "" {
			http.NotFound(w, r)
			return
		}
		switch r.Method {
		case http.MethodPatch:
			handleRenameColumn(w, r, b, id)
		case http.MethodDelete:
			handleDeleteColumn(w, r, b, id)
		default:
			w.Header().Set("Allow", "PATCH, DELETE")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	return mux
}

// columnLabelMaxLen caps a custom column label so the header stays sane.
const columnLabelMaxLen = 60

// decodeColumnLabel reads and validates a {label} body.
func decodeColumnLabel(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req struct {
		Label string `json:"label"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return "", false
	}
	label := strings.TrimSpace(req.Label)
	if label == "" {
		http.Error(w, "label is required", http.StatusBadRequest)
		return "", false
	}
	if len(label) > columnLabelMaxLen {
		http.Error(w, "label too long", http.StatusBadRequest)
		return "", false
	}
	return label, true
}

func handleAddColumn(w http.ResponseWriter, r *http.Request, b *Board) {
	label, ok := decodeColumnLabel(w, r)
	if !ok {
		return
	}
	c, err := b.AddColumn(label)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func handleRenameColumn(w http.ResponseWriter, r *http.Request, b *Board, id string) {
	label, ok := decodeColumnLabel(w, r)
	if !ok {
		return
	}
	if err := b.RenameColumn(id, label); errors.Is(err, ErrColumnNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, Column{ID: id, Label: label})
}

func handleDeleteColumn(w http.ResponseWriter, r *http.Request, b *Board, id string) {
	if err := b.DeleteColumn(id); errors.Is(err, ErrColumnNotFound) {
		http.NotFound(w, r)
		return
	} else if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type createRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Column      string `json:"column"`
	Color       string `json:"color"`
}

// validColors is the allowlist for the Card.Color field. Empty string
// means "no colour" and is always allowed. Keep this in sync with the
// CSS palette in static/style.css.
var validColors = map[string]struct{}{
	"":       {},
	"red":    {},
	"orange": {},
	"yellow": {},
	"green":  {},
	"blue":   {},
	"purple": {},
	"grey":   {},
}

func handleCreate(w http.ResponseWriter, r *http.Request, b *Board) {
	var req createRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}
	if req.Column == "" {
		req.Column = "to-do"
	}
	if _, ok := validColors[req.Color]; !ok {
		http.Error(w, "invalid color (allowed: red, orange, yellow, green, blue, purple, grey, or empty)", http.StatusBadRequest)
		return
	}
	c, err := b.AddCard(req.Title, req.Description, req.Column, req.Color)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func handleUpdate(w http.ResponseWriter, r *http.Request, b *Board, id string) {
	var u CardUpdate
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&u); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if u.Color != nil {
		if _, ok := validColors[*u.Color]; !ok {
			http.Error(w, "invalid color (allowed: red, orange, yellow, green, blue, purple, grey, or empty)", http.StatusBadRequest)
			return
		}
	}
	c, err := b.UpdateCard(id, u)
	if errors.Is(err, ErrCardNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func handleDelete(w http.ResponseWriter, r *http.Request, b *Board, id string) {
	err := b.DeleteCard(id)
	if errors.Is(err, ErrCardNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
