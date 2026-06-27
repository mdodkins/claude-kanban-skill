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
//	GET    /api/config                          board config (agents list)
//	GET    /api/projects                        list all projects
//	POST   /api/projects                        create project
//	DELETE /api/projects/{id}                   remove project
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
	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		list := agents
		if list == nil {
			list = []string{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"agents": list})
	})
	mux.HandleFunc("/api/projects", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, b.ListProjects())
		case http.MethodPost:
			var req struct {
				Name string `json:"name"`
			}
			if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
				http.Error(w, "invalid JSON", http.StatusBadRequest)
				return
			}
			if req.Name == "" {
				http.Error(w, "name is required", http.StatusBadRequest)
				return
			}
			p, err := b.AddProject(req.Name)
			if err != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusCreated, p)
		default:
			w.Header().Set("Allow", "GET, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/api/projects/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/projects/")
		if id == "" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodDelete {
			w.Header().Set("Allow", "DELETE")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := b.DeleteProject(id); err != nil {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
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
			handlePatchColumn(w, r, b, id)
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

// handlePatchColumn applies a sparse column update: {label} renames,
// {position} moves. Either or both may be present.
func handlePatchColumn(w http.ResponseWriter, r *http.Request, b *Board, id string) {
	var req struct {
		Label    *string `json:"label"`
		Position *int    `json:"position"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Label == nil && req.Position == nil {
		http.Error(w, "label or position is required", http.StatusBadRequest)
		return
	}
	if req.Label != nil {
		label := strings.TrimSpace(*req.Label)
		if label == "" {
			http.Error(w, "label is required", http.StatusBadRequest)
			return
		}
		if len(label) > columnLabelMaxLen {
			http.Error(w, "label too long", http.StatusBadRequest)
			return
		}
		if err := b.RenameColumn(id, label); errors.Is(err, ErrColumnNotFound) {
			http.NotFound(w, r)
			return
		} else if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if req.Position != nil {
		if err := b.MoveColumn(id, *req.Position); errors.Is(err, ErrColumnNotFound) {
			http.NotFound(w, r)
			return
		} else if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	writeJSON(w, http.StatusOK, b.Columns())
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
	ProjectID   string `json:"projectId"`
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
	c, err := b.AddCard(req.Title, req.Description, req.Column, req.Color, req.ProjectID)
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
