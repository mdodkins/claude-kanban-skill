package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func newServer(t *testing.T) (http.Handler, *Board) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	b, err := NewBoard(path)
	if err != nil {
		t.Fatal(err)
	}
	attachDir := filepath.Join(t.TempDir(), "attachments")
	return NewMux(b, nil, attachDir), b
}

func TestPostCardCreates201(t *testing.T) {
	mux, _ := newServer(t)

	body := `{"title":"Set up DNS","description":"A record","column":"to-do"}`
	req := httptest.NewRequest(http.MethodPost, "/api/cards", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var got Card
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body not JSON: %v\n%s", err, rec.Body.String())
	}
	if got.Title != "Set up DNS" || got.Column != "to-do" || got.ID == "" {
		t.Errorf("unexpected card: %+v", got)
	}
}

func TestPostCardMissingTitleReturns400(t *testing.T) {
	mux, _ := newServer(t)
	body := `{"description":"no title","column":"to-do"}`
	req := httptest.NewRequest(http.MethodPost, "/api/cards", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400", rec.Code)
	}
}

func TestGetCardsReturnsAll(t *testing.T) {
	mux, b := newServer(t)
	b.AddCard("a", "", "to-do", "")
	b.AddCard("b", "", "done", "")

	req := httptest.NewRequest(http.MethodGet, "/api/cards", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	var cards []Card
	if err := json.Unmarshal(rec.Body.Bytes(), &cards); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if len(cards) != 2 {
		t.Errorf("got %d cards, want 2", len(cards))
	}
}

func TestPatchCardUpdates(t *testing.T) {
	mux, b := newServer(t)
	c, _ := b.AddCard("orig", "", "to-do", "")

	body := `{"column":"done"}`
	req := httptest.NewRequest(http.MethodPatch, "/api/cards/"+c.ID, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got Card
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Column != "done" {
		t.Errorf("column not updated: %+v", got)
	}
}

func TestPatchUnknownIDReturns404(t *testing.T) {
	mux, _ := newServer(t)
	req := httptest.NewRequest(http.MethodPatch, "/api/cards/no-such-id", strings.NewReader(`{"column":"done"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", rec.Code)
	}
}

func TestDeleteCardReturns204(t *testing.T) {
	mux, b := newServer(t)
	c, _ := b.AddCard("doomed", "", "to-do", "")

	req := httptest.NewRequest(http.MethodDelete, "/api/cards/"+c.ID, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d, want 204", rec.Code)
	}
	if len(b.ListCards()) != 0 {
		t.Errorf("card not actually deleted")
	}
}

func TestDeleteUnknownIDReturns404(t *testing.T) {
	mux, _ := newServer(t)
	req := httptest.NewRequest(http.MethodDelete, "/api/cards/no-such-id", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", rec.Code)
	}
}

// multipartBody builds a multipart/form-data body with a single "file" field.
func multipartBody(t *testing.T, filename, content string) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprint(fw, content)
	w.Close()
	return &buf, w.FormDataContentType()
}

func TestAttachmentUploadAndDownload(t *testing.T) {
	mux, b := newServer(t)
	card, _ := b.AddCard("has attachment", "", "to-do", "")

	// Upload
	body, ct := multipartBody(t, "hello.txt", "hello world")
	req := httptest.NewRequest(http.MethodPost, "/api/cards/"+card.ID+"/attachments", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload: got %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var a Attachment
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		t.Fatalf("upload response not JSON: %v", err)
	}
	if a.ID == "" || a.Filename != "hello.txt" {
		t.Errorf("unexpected attachment: %+v", a)
	}

	// Download
	req2 := httptest.NewRequest(http.MethodGet, "/api/cards/"+card.ID+"/attachments/"+a.ID, nil)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("download: got %d, want 200", rec2.Code)
	}
	if got := rec2.Body.String(); got != "hello world" {
		t.Errorf("download body: got %q, want %q", got, "hello world")
	}

	// Card now carries attachment metadata
	cards := b.ListCards()
	if len(cards[0].Attachments) != 1 {
		t.Errorf("card should have 1 attachment, got %d", len(cards[0].Attachments))
	}
}

func TestAttachmentDeleteRemovesMetadata(t *testing.T) {
	mux, b := newServer(t)
	card, _ := b.AddCard("del card", "", "to-do", "")

	// Upload then delete
	body, ct := multipartBody(t, "bye.txt", "bye")
	req := httptest.NewRequest(http.MethodPost, "/api/cards/"+card.ID+"/attachments", body)
	req.Header.Set("Content-Type", ct)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var a Attachment
	json.Unmarshal(rec.Body.Bytes(), &a)

	req2 := httptest.NewRequest(http.MethodDelete, "/api/cards/"+card.ID+"/attachments/"+a.ID, nil)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNoContent {
		t.Fatalf("delete attachment: got %d, want 204", rec2.Code)
	}

	// Metadata gone
	if len(b.ListCards()[0].Attachments) != 0 {
		t.Error("attachment metadata should be removed after delete")
	}
}
