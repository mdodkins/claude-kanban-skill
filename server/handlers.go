package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// NewMux wires the kanban routes to a Board.
// Routes:
//
//	GET    /api/agents                          list agent names
//	GET    /api/cards                           list all cards
//	POST   /api/cards                           create
//	PATCH  /api/cards/{id}                      sparse update
//	DELETE /api/cards/{id}                      remove
//	POST   /api/cards/{id}/attachments          upload file (multipart "file" field, max 10 MB)
//	GET    /api/cards/{id}/attachments/{aid}    download file
//	DELETE /api/cards/{id}/attachments/{aid}    remove file
func NewMux(b *Board, agents []string, attachDir string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/agents", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		list := agents
		if list == nil {
			list = []string{}
		}
		writeJSON(w, http.StatusOK, list)
	})
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
		path := strings.TrimPrefix(r.URL.Path, "/api/cards/")
		if path == "" {
			http.NotFound(w, r)
			return
		}
		// Split into at most 3 segments: cardID / "attachments" / attachmentID
		parts := strings.SplitN(path, "/", 3)
		cardID := parts[0]
		if cardID == "" {
			http.NotFound(w, r)
			return
		}
		if len(parts) == 1 {
			switch r.Method {
			case http.MethodPatch:
				handleUpdate(w, r, b, cardID)
			case http.MethodDelete:
				handleDelete(w, r, b, cardID)
			default:
				w.Header().Set("Allow", "PATCH, DELETE")
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
			return
		}
		if parts[1] != "attachments" {
			http.NotFound(w, r)
			return
		}
		if len(parts) == 2 {
			if r.Method == http.MethodPost {
				handleAttachmentUpload(w, r, b, cardID, attachDir)
			} else {
				w.Header().Set("Allow", "POST")
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
			return
		}
		// /api/cards/{id}/attachments/{aid}
		attachmentID := parts[2]
		switch r.Method {
		case http.MethodGet:
			handleAttachmentServe(w, r, b, cardID, attachmentID, attachDir)
		case http.MethodDelete:
			handleAttachmentDelete(w, r, b, cardID, attachmentID, attachDir)
		default:
			w.Header().Set("Allow", "GET, DELETE")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	return mux
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

const maxAttachBytes = 10 << 20 // 10 MiB

func handleAttachmentUpload(w http.ResponseWriter, r *http.Request, b *Board, cardID, attachDir string) {
	r.Body = http.MaxBytesReader(w, r.Body, maxAttachBytes+4096)
	if err := r.ParseMultipartForm(maxAttachBytes); err != nil {
		http.Error(w, "file too large or bad form (max 10 MB)", http.StatusBadRequest)
		return
	}
	f, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing 'file' field in form", http.StatusBadRequest)
		return
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxAttachBytes+1))
	if err != nil {
		http.Error(w, "read error", http.StatusInternalServerError)
		return
	}
	if int64(len(data)) > maxAttachBytes {
		http.Error(w, "file too large (max 10 MB)", http.StatusBadRequest)
		return
	}

	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}

	a, err := b.AddAttachment(cardID, attachDir, header.Filename, mimeType, data)
	if errors.Is(err, ErrCardNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func handleAttachmentServe(w http.ResponseWriter, r *http.Request, b *Board, cardID, attachmentID, attachDir string) {
	a, err := b.GetAttachmentInfo(cardID, attachmentID)
	if errors.Is(err, ErrCardNotFound) || errors.Is(err, ErrAttachmentNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	f, err := os.Open(filepath.Join(attachDir, cardID, attachmentID))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	safeName := strings.ReplaceAll(a.Filename, `"`, "'")
	w.Header().Set("Content-Type", a.MimeType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+safeName+`"`)
	http.ServeContent(w, r, a.Filename, time.Time{}, f)
}

func handleAttachmentDelete(w http.ResponseWriter, r *http.Request, b *Board, cardID, attachmentID, attachDir string) {
	err := b.DeleteAttachment(cardID, attachmentID, attachDir)
	if errors.Is(err, ErrCardNotFound) || errors.Is(err, ErrAttachmentNotFound) {
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
