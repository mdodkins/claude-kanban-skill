package main

import (
	"os"
	"path/filepath"
	"testing"
)

func freshBoard(t *testing.T) *Board {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	b, err := NewBoard(path)
	if err != nil {
		t.Fatalf("NewBoard: %v", err)
	}
	return b
}

func TestNewBoardOnMissingFileStartsEmpty(t *testing.T) {
	b := freshBoard(t)
	if got := len(b.ListCards()); got != 0 {
		t.Fatalf("expected empty board, got %d cards", got)
	}
}

func TestAddCardAppearsInListCards(t *testing.T) {
	b := freshBoard(t)
	c, _ := b.AddCard("first", "", "to-do", "")
	cards := b.ListCards()
	if len(cards) != 1 {
		t.Fatalf("expected 1 card, got %d", len(cards))
	}
	if cards[0].ID != c.ID {
		t.Errorf("ListCards returned different card: got %+v, want %+v", cards[0], c)
	}
}

func TestAddCardPersistsAcrossReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	// Session 1: create + add.
	b1, _ := NewBoard(path)
	c, _ := b1.AddCard("survives restart", "", "in-progress", "")

	// Session 2: fresh Board on the same path, should see the card.
	b2, err := NewBoard(path)
	if err != nil {
		t.Fatalf("NewBoard reload: %v", err)
	}
	cards := b2.ListCards()
	if len(cards) != 1 {
		t.Fatalf("expected 1 card after reload, got %d", len(cards))
	}
	if cards[0].ID != c.ID || cards[0].Title != "survives restart" || cards[0].Column != "in-progress" {
		t.Errorf("reloaded card mismatch: got %+v", cards[0])
	}
}

func TestUpdateCardChangesFields(t *testing.T) {
	b := freshBoard(t)
	orig, _ := b.AddCard("title", "desc", "to-do", "")

	newTitle := "renamed"
	newCol := "in-progress"
	updated, err := b.UpdateCard(orig.ID, CardUpdate{
		Title:  &newTitle,
		Column: &newCol,
	})
	if err != nil {
		t.Fatalf("UpdateCard: %v", err)
	}
	if updated.Title != "renamed" {
		t.Errorf("Title: got %q, want %q", updated.Title, "renamed")
	}
	if updated.Column != "in-progress" {
		t.Errorf("Column: got %q, want %q", updated.Column, "in-progress")
	}
	if updated.Description != "desc" {
		t.Errorf("Description should be unchanged, got %q", updated.Description)
	}

	// Reload to verify persistence.
	b2, _ := NewBoard(b.path)
	got := b2.ListCards()[0]
	if got.Title != "renamed" || got.Column != "in-progress" {
		t.Errorf("update did not persist: %+v", got)
	}
}

func TestUpdateCardUnknownIDReturnsError(t *testing.T) {
	b := freshBoard(t)
	_, err := b.UpdateCard("does-not-exist", CardUpdate{})
	if err == nil {
		t.Fatalf("expected error for unknown id, got nil")
	}
}

func TestDeleteCardRemoves(t *testing.T) {
	b := freshBoard(t)
	a, _ := b.AddCard("a", "", "to-do", "")
	bcard, _ := b.AddCard("b", "", "done", "")

	if err := b.DeleteCard(a.ID); err != nil {
		t.Fatalf("DeleteCard: %v", err)
	}
	cards := b.ListCards()
	if len(cards) != 1 {
		t.Fatalf("expected 1 card left, got %d", len(cards))
	}
	if cards[0].ID != bcard.ID {
		t.Errorf("wrong card survived")
	}

	// Persistence.
	b2, _ := NewBoard(b.path)
	if len(b2.ListCards()) != 1 {
		t.Errorf("delete did not persist")
	}
}

func TestDeleteCardUnknownIDReturnsError(t *testing.T) {
	b := freshBoard(t)
	if err := b.DeleteCard("nope"); err == nil {
		t.Fatalf("expected error for unknown id")
	}
}

func TestAddCardAssignsAppendingPositionPerColumn(t *testing.T) {
	b := freshBoard(t)
	a, _ := b.AddCard("a", "", "to-do", "")
	bcard, _ := b.AddCard("b", "", "to-do", "")
	c, _ := b.AddCard("c", "", "done", "")
	d, _ := b.AddCard("d", "", "to-do", "")

	if a.Position != 0 {
		t.Errorf("a: want pos 0, got %d", a.Position)
	}
	if bcard.Position != 1 {
		t.Errorf("b: want pos 1, got %d", bcard.Position)
	}
	if c.Position != 0 {
		t.Errorf("c (different column): want pos 0, got %d", c.Position)
	}
	if d.Position != 2 {
		t.Errorf("d: want pos 2, got %d", d.Position)
	}
}

func TestUpdateCardMoveWithinColumnRenumbers(t *testing.T) {
	b := freshBoard(t)
	a, _ := b.AddCard("a", "", "to-do", "") // pos 0
	bcard, _ := b.AddCard("b", "", "to-do", "") // pos 1
	c, _ := b.AddCard("c", "", "to-do", "") // pos 2

	col := "to-do"
	pos := 0
	if _, err := b.UpdateCard(c.ID, CardUpdate{Column: &col, Position: &pos}); err != nil {
		t.Fatalf("UpdateCard: %v", err)
	}

	got := map[string]int{}
	for _, card := range b.ListCards() {
		got[card.ID] = card.Position
	}
	if got[c.ID] != 0 {
		t.Errorf("c: want pos 0, got %d", got[c.ID])
	}
	if got[a.ID] != 1 {
		t.Errorf("a: want pos 1, got %d", got[a.ID])
	}
	if got[bcard.ID] != 2 {
		t.Errorf("b: want pos 2, got %d", got[bcard.ID])
	}
}

func TestUpdateCardMoveAcrossColumnsRenumbersBoth(t *testing.T) {
	b := freshBoard(t)
	a, _ := b.AddCard("a", "", "to-do", "")     // to-do pos 0
	bcard, _ := b.AddCard("b", "", "to-do", "") // to-do pos 1
	c, _ := b.AddCard("c", "", "to-do", "")     // to-do pos 2
	d, _ := b.AddCard("d", "", "done", "")      // done pos 0

	// Move b from to-do (pos 1) to done at position 0.
	col := "done"
	pos := 0
	if _, err := b.UpdateCard(bcard.ID, CardUpdate{Column: &col, Position: &pos}); err != nil {
		t.Fatalf("UpdateCard: %v", err)
	}

	byID := map[string]Card{}
	for _, card := range b.ListCards() {
		byID[card.ID] = card
	}
	if byID[a.ID].Column != "to-do" || byID[a.ID].Position != 0 {
		t.Errorf("a: want to-do pos 0, got %s pos %d", byID[a.ID].Column, byID[a.ID].Position)
	}
	if byID[c.ID].Column != "to-do" || byID[c.ID].Position != 1 {
		t.Errorf("c: want to-do pos 1 (compacted), got %s pos %d", byID[c.ID].Column, byID[c.ID].Position)
	}
	if byID[bcard.ID].Column != "done" || byID[bcard.ID].Position != 0 {
		t.Errorf("b: want done pos 0, got %s pos %d", byID[bcard.ID].Column, byID[bcard.ID].Position)
	}
	if byID[d.ID].Column != "done" || byID[d.ID].Position != 1 {
		t.Errorf("d: want done pos 1 (shifted), got %s pos %d", byID[d.ID].Column, byID[d.ID].Position)
	}
}

func TestUpdateCardClampsPositionPastEnd(t *testing.T) {
	b := freshBoard(t)
	a, _ := b.AddCard("a", "", "to-do", "")
	bcard, _ := b.AddCard("b", "", "to-do", "")

	// Position 99 is past the end; should clamp to end (after a, last slot for b).
	col := "to-do"
	pos := 99
	if _, err := b.UpdateCard(a.ID, CardUpdate{Column: &col, Position: &pos}); err != nil {
		t.Fatalf("UpdateCard: %v", err)
	}
	byID := map[string]int{}
	for _, card := range b.ListCards() {
		byID[card.ID] = card.Position
	}
	if byID[bcard.ID] != 0 {
		t.Errorf("b: want pos 0, got %d", byID[bcard.ID])
	}
	if byID[a.ID] != 1 {
		t.Errorf("a: want pos 1 (clamped end), got %d", byID[a.ID])
	}
}

func TestUpdateCardTitleOnlyDoesNotReorder(t *testing.T) {
	b := freshBoard(t)
	a, _ := b.AddCard("a", "", "to-do", "") // pos 0
	bcard, _ := b.AddCard("b", "", "to-do", "") // pos 1

	newTitle := "renamed"
	if _, err := b.UpdateCard(a.ID, CardUpdate{Title: &newTitle}); err != nil {
		t.Fatalf("UpdateCard: %v", err)
	}
	byID := map[string]Card{}
	for _, card := range b.ListCards() {
		byID[card.ID] = card
	}
	if byID[a.ID].Position != 0 || byID[bcard.ID].Position != 1 {
		t.Errorf("title edit shouldn't renumber: a=%d b=%d", byID[a.ID].Position, byID[bcard.ID].Position)
	}
}

func TestLoadNormalisesLegacyZeroPositions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	// Simulate a pre-renumbering state file: three cards, all position 0.
	legacy := `[
	  {"id":"aaa","title":"a","description":"","column":"to-do","position":0},
	  {"id":"bbb","title":"b","description":"","column":"to-do","position":0},
	  {"id":"ccc","title":"c","description":"","column":"to-do","position":0}
	]`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	b, err := NewBoard(path)
	if err != nil {
		t.Fatalf("NewBoard: %v", err)
	}
	got := map[string]int{}
	for _, card := range b.ListCards() {
		got[card.ID] = card.Position
	}
	if got["aaa"] != 0 || got["bbb"] != 1 || got["ccc"] != 2 {
		t.Errorf("legacy renumber: got aaa=%d bbb=%d ccc=%d", got["aaa"], got["bbb"], got["ccc"])
	}
}

func TestAddCardReturnsCardWithGivenFields(t *testing.T) {
	b := freshBoard(t)
	c, err := b.AddCard("Set up DNS", "A record for kanban.pitchforks.net", "to-do", "")
	if err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if c.ID == "" {
		t.Errorf("Card ID should be non-empty")
	}
	if c.Title != "Set up DNS" {
		t.Errorf("Title: got %q, want %q", c.Title, "Set up DNS")
	}
	if c.Description != "A record for kanban.pitchforks.net" {
		t.Errorf("Description: got %q", c.Description)
	}
	if c.Column != "to-do" {
		t.Errorf("Column: got %q, want %q", c.Column, "to-do")
	}
}

func TestColorRoundTripsThroughAddAndUpdate(t *testing.T) {
	b := freshBoard(t)
	c, err := b.AddCard("with colour", "", "to-do", "blue")
	if err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if c.Color != "blue" {
		t.Errorf("Color on create: got %q, want blue", c.Color)
	}

	// Sparse update changes the colour but leaves other fields alone.
	newColor := "red"
	updated, err := b.UpdateCard(c.ID, CardUpdate{Color: &newColor})
	if err != nil {
		t.Fatalf("UpdateCard: %v", err)
	}
	if updated.Color != "red" {
		t.Errorf("Color after update: got %q, want red", updated.Color)
	}
	if updated.Title != "with colour" {
		t.Errorf("Title should be untouched by colour-only update: got %q", updated.Title)
	}

	// Clearing the colour via empty-string update.
	empty := ""
	cleared, err := b.UpdateCard(c.ID, CardUpdate{Color: &empty})
	if err != nil {
		t.Fatalf("UpdateCard clear: %v", err)
	}
	if cleared.Color != "" {
		t.Errorf("Color after clear: got %q, want empty", cleared.Color)
	}

	// Reload from disk to confirm the colour persists.
	b2, err := NewBoard(b.path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	roundtripped, _ := b2.AddCard("round-trip", "", "to-do", "purple")
	all := b2.ListCards()
	if len(all) != 2 {
		t.Fatalf("expected 2 cards after reload, got %d", len(all))
	}
	for _, x := range all {
		if x.ID == roundtripped.ID && x.Color != "purple" {
			t.Errorf("purple lost across reload: got %q", x.Color)
		}
	}
}

func TestNewBoardSeedsDefaultColumns(t *testing.T) {
	b := freshBoard(t)
	cols := b.Columns()
	if len(cols) != len(defaultColumns()) {
		t.Fatalf("expected %d default columns, got %d", len(defaultColumns()), len(cols))
	}
	if cols[0].ID != "to-do" {
		t.Errorf("first default column = %q, want to-do", cols[0].ID)
	}
}

func TestAddColumnPersistsAcrossReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	b, err := NewBoard(path)
	if err != nil {
		t.Fatalf("NewBoard: %v", err)
	}
	c, err := b.AddColumn("Icebox")
	if err != nil {
		t.Fatalf("AddColumn: %v", err)
	}
	if c.ID == "" || c.Label != "Icebox" {
		t.Fatalf("unexpected new column: %+v", c)
	}
	b2, err := NewBoard(path)
	if err != nil {
		t.Fatalf("reload NewBoard: %v", err)
	}
	cols := b2.Columns()
	last := cols[len(cols)-1]
	if last.ID != c.ID || last.Label != "Icebox" {
		t.Errorf("added column not persisted/appended: got %+v", last)
	}
}

func TestRenameColumn(t *testing.T) {
	b := freshBoard(t)
	if err := b.RenameColumn("to-do", "Backlog"); err != nil {
		t.Fatalf("RenameColumn: %v", err)
	}
	if got := b.Columns()[0].Label; got != "Backlog" {
		t.Errorf("rename not applied: got %q", got)
	}
	if err := b.RenameColumn("nope", "x"); err != ErrColumnNotFound {
		t.Errorf("expected ErrColumnNotFound, got %v", err)
	}
}

func TestDeleteColumnCascadesCards(t *testing.T) {
	b := freshBoard(t)
	b.AddCard("keep", "", "to-do", "")
	b.AddCard("gone", "", "done", "")
	if err := b.DeleteColumn("done"); err != nil {
		t.Fatalf("DeleteColumn: %v", err)
	}
	for _, c := range b.Columns() {
		if c.ID == "done" {
			t.Fatalf("column 'done' still present after delete")
		}
	}
	for _, c := range b.ListCards() {
		if c.Column == "done" {
			t.Fatalf("card in deleted column survived: %+v", c)
		}
	}
	if got := len(b.ListCards()); got != 1 {
		t.Errorf("expected 1 card left, got %d", got)
	}
	if err := b.DeleteColumn("nope"); err != ErrColumnNotFound {
		t.Errorf("expected ErrColumnNotFound, got %v", err)
	}
}

func TestLegacyLabelMapMigratesToColumns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	// Old format: a JSON object of id -> label overrides.
	if err := os.WriteFile(filepath.Join(dir, "columns.json"), []byte(`{"to-do":"Backlog"}`), 0o644); err != nil {
		t.Fatalf("seed legacy file: %v", err)
	}
	b, err := NewBoard(path)
	if err != nil {
		t.Fatalf("NewBoard: %v", err)
	}
	cols := b.Columns()
	if len(cols) != len(defaultColumns()) {
		t.Fatalf("expected default set after migration, got %d", len(cols))
	}
	if cols[0].Label != "Backlog" {
		t.Errorf("legacy override not folded in: got %q", cols[0].Label)
	}
}
