package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Card is one item on the kanban board.
type Card struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Column      string   `json:"column"`
	Position    int      `json:"position"`
	// Color is an optional palette tag rendered by the frontend as a
	// left-border + tinted background. Empty string = no colour.
	// Allowed values are validated by the API layer (see handlers.go).
	Color       string       `json:"color,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Attachment is metadata for a file attached to a card.
// The file itself lives at <attachDir>/<cardID>/<ID> on disk.
type Attachment struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	MimeType string `json:"mime_type"`
	Size     int64  `json:"size"`
}

// Board owns the in-memory state and the JSON state file.
// Mutations write through to disk atomically.
type Board struct {
	path string

	mu    sync.Mutex
	cards []Card
}

// NewBoard loads (or creates) the board state at path.
// A missing file is treated as an empty board.
func NewBoard(path string) (*Board, error) {
	b := &Board{path: path}
	if err := b.load(); err != nil {
		return nil, err
	}
	return b, nil
}

// load reads the state file into b.cards. Missing file = empty.
func (b *Board) load() error {
	data, err := os.ReadFile(b.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read state: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	var cards []Card
	if err := json.Unmarshal(data, &cards); err != nil {
		return fmt.Errorf("parse state: %w", err)
	}
	b.cards = cards
	// Legacy state files may have all-zero positions. Renumber each column
	// 0..N-1 in load order so drag-and-drop has a stable basis.
	b.renumberAllColumns()
	return nil
}

// renumberAllColumns assigns positions 0..N-1 within each column,
// preserving the relative order implied by current (column, position) sort.
// Caller must hold b.mu (or hold no other writer, e.g. during load).
func (b *Board) renumberAllColumns() {
	byCol := map[string][]int{}
	for i := range b.cards {
		byCol[b.cards[i].Column] = append(byCol[b.cards[i].Column], i)
	}
	for _, idxs := range byCol {
		ids := idxs
		sort.SliceStable(ids, func(i, j int) bool {
			return b.cards[ids[i]].Position < b.cards[ids[j]].Position
		})
		for n, i := range ids {
			b.cards[i].Position = n
		}
	}
}

// save writes the current in-memory state to disk atomically.
// Caller must hold b.mu.
func (b *Board) save() error {
	if err := os.MkdirAll(filepath.Dir(b.path), 0o755); err != nil {
		return fmt.Errorf("mkdir state dir: %w", err)
	}
	data, err := json.MarshalIndent(b.cards, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	tmp := b.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, b.path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// ListCards returns a copy of all cards (caller-safe to mutate).
func (b *Board) ListCards() []Card {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Card, len(b.cards))
	copy(out, b.cards)
	return out
}

// AddCard creates a card on the board and persists. The new card is appended
// to the end of its column (Position = current count in that column).
func (b *Board) AddCard(title, description, column, color string) (Card, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	pos := 0
	for i := range b.cards {
		if b.cards[i].Column == column {
			pos++
		}
	}
	c := Card{
		ID:          newID(),
		Title:       title,
		Description: description,
		Column:      column,
		Position:    pos,
		Color:       color,
	}
	b.cards = append(b.cards, c)
	if err := b.save(); err != nil {
		// Roll back the in-memory append so disk and memory stay consistent.
		b.cards = b.cards[:len(b.cards)-1]
		return Card{}, err
	}
	return c, nil
}

// CardUpdate is a sparse update: any non-nil field is applied to the card,
// nil fields are left alone.
type CardUpdate struct {
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
	Column      *string `json:"column,omitempty"`
	Position    *int    `json:"position,omitempty"`
	Color       *string `json:"color,omitempty"`
}

// ErrCardNotFound is returned when a card ID doesn't exist on the board.
var ErrCardNotFound = errors.New("card not found")

// ErrAttachmentNotFound is returned when an attachment ID doesn't exist on a card.
var ErrAttachmentNotFound = errors.New("attachment not found")

// UpdateCard applies a sparse update and persists. Unknown IDs return ErrCardNotFound.
// When Column or Position is set, the card is moved to that (column, position) slot
// and the affected columns are renumbered 0..N-1 so positions stay contiguous.
func (b *Board) UpdateCard(id string, u CardUpdate) (Card, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	idx := -1
	for i := range b.cards {
		if b.cards[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Card{}, ErrCardNotFound
	}
	if u.Title != nil {
		b.cards[idx].Title = *u.Title
	}
	if u.Description != nil {
		b.cards[idx].Description = *u.Description
	}
	if u.Color != nil {
		b.cards[idx].Color = *u.Color
	}
	if u.Column != nil || u.Position != nil {
		targetColumn := b.cards[idx].Column
		if u.Column != nil {
			targetColumn = *u.Column
		}
		targetPosition := b.cards[idx].Position
		if u.Position != nil {
			targetPosition = *u.Position
		}
		b.moveTo(idx, targetColumn, targetPosition)
	}
	updated := b.cards[idx]
	if err := b.save(); err != nil {
		return Card{}, err
	}
	return updated, nil
}

// moveTo re-inserts the card at b.cards[idx] at (targetColumn, targetPosition)
// in that column's ordering, then renumbers the affected columns 0..N-1.
// targetPosition is clamped to [0, len(column)].
// Caller must hold b.mu.
func (b *Board) moveTo(idx int, targetColumn string, targetPosition int) {
	moved := &b.cards[idx]
	oldColumn := moved.Column
	moved.Column = targetColumn

	// Collect indices of cards in the affected columns, excluding the moved card.
	var inOld, inNew []int
	for i := range b.cards {
		if i == idx {
			continue
		}
		c := &b.cards[i]
		if c.Column == oldColumn {
			inOld = append(inOld, i)
		}
		if oldColumn != targetColumn && c.Column == targetColumn {
			inNew = append(inNew, i)
		}
	}
	sort.SliceStable(inOld, func(i, j int) bool {
		return b.cards[inOld[i]].Position < b.cards[inOld[j]].Position
	})

	if oldColumn == targetColumn {
		clamp := targetPosition
		if clamp < 0 {
			clamp = 0
		}
		if clamp > len(inOld) {
			clamp = len(inOld)
		}
		for n := 0; n < clamp; n++ {
			b.cards[inOld[n]].Position = n
		}
		moved.Position = clamp
		for n := clamp; n < len(inOld); n++ {
			b.cards[inOld[n]].Position = n + 1
		}
		return
	}

	sort.SliceStable(inNew, func(i, j int) bool {
		return b.cards[inNew[i]].Position < b.cards[inNew[j]].Position
	})
	for n, i := range inOld {
		b.cards[i].Position = n
	}
	clamp := targetPosition
	if clamp < 0 {
		clamp = 0
	}
	if clamp > len(inNew) {
		clamp = len(inNew)
	}
	for n := 0; n < clamp; n++ {
		b.cards[inNew[n]].Position = n
	}
	moved.Position = clamp
	for n := clamp; n < len(inNew); n++ {
		b.cards[inNew[n]].Position = n + 1
	}
}

// DeleteCard removes a card by ID and persists. Unknown IDs return ErrCardNotFound.
func (b *Board) DeleteCard(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.cards {
		if b.cards[i].ID != id {
			continue
		}
		b.cards = append(b.cards[:i], b.cards[i+1:]...)
		return b.save()
	}
	return ErrCardNotFound
}

// AddAttachment writes data to disk and appends attachment metadata to the card.
func (b *Board) AddAttachment(cardID, attachDir, filename, mimeType string, data []byte) (Attachment, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	idx := -1
	for i := range b.cards {
		if b.cards[i].ID == cardID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Attachment{}, ErrCardNotFound
	}
	a := Attachment{
		ID:       newID(),
		Filename: sanitizeFilename(filename),
		MimeType: mimeType,
		Size:     int64(len(data)),
	}
	dir := filepath.Join(attachDir, cardID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Attachment{}, fmt.Errorf("mkdir attachments: %w", err)
	}
	filePath := filepath.Join(dir, a.ID)
	if err := os.WriteFile(filePath, data, 0o644); err != nil {
		return Attachment{}, fmt.Errorf("write attachment: %w", err)
	}
	b.cards[idx].Attachments = append(b.cards[idx].Attachments, a)
	if err := b.save(); err != nil {
		b.cards[idx].Attachments = b.cards[idx].Attachments[:len(b.cards[idx].Attachments)-1]
		_ = os.Remove(filePath)
		return Attachment{}, err
	}
	return a, nil
}

// GetAttachmentInfo returns the Attachment metadata for (cardID, attachmentID).
func (b *Board) GetAttachmentInfo(cardID, attachmentID string) (Attachment, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.cards {
		if b.cards[i].ID != cardID {
			continue
		}
		for _, a := range b.cards[i].Attachments {
			if a.ID == attachmentID {
				return a, nil
			}
		}
		return Attachment{}, ErrAttachmentNotFound
	}
	return Attachment{}, ErrCardNotFound
}

// DeleteAttachment removes attachment metadata from the card and deletes the file.
func (b *Board) DeleteAttachment(cardID, attachmentID, attachDir string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.cards {
		if b.cards[i].ID != cardID {
			continue
		}
		atts := b.cards[i].Attachments
		for j, a := range atts {
			if a.ID != attachmentID {
				continue
			}
			b.cards[i].Attachments = append(atts[:j:j], atts[j+1:]...)
			if err := b.save(); err != nil {
				b.cards[i].Attachments = atts // restore original slice
				return err
			}
			_ = os.Remove(filepath.Join(attachDir, cardID, attachmentID))
			return nil
		}
		return ErrAttachmentNotFound
	}
	return ErrCardNotFound
}

// sanitizeFilename strips path components and control characters, truncates to 255 bytes.
func sanitizeFilename(name string) string {
	name = filepath.Base(name)
	runes := []rune(name)
	for i, r := range runes {
		if r == '/' || r == '\\' || r < 32 {
			runes[i] = '_'
		}
	}
	s := string(runes)
	if s == "" || s == "." || s == ".." {
		return "file"
	}
	if len(s) > 255 {
		s = s[:255]
	}
	return s
}

// newID returns an unguessable 16-hex-char ID.
func newID() string {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}
