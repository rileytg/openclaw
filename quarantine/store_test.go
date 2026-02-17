package main

import (
	"os"
	"path/filepath"
	"testing"
)

func tempDB(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "quarantine.db")
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return store, dbPath
}

func TestOpenAndCreate(t *testing.T) {
	store, _ := tempDB(t)
	defer store.Close()
	n, err := store.Count("")
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("expected 0, got %d", n)
	}
}

func TestPushAndGet(t *testing.T) {
	store, _ := tempDB(t)
	defer store.Close()

	item, err := store.Push(PushRequest{
		Kind:    KindMemory,
		Content: "The sky is blue.",
		Label:   "fact",
	}, "test-source")
	if err != nil {
		t.Fatal(err)
	}
	if item.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if item.Status != StatusPending {
		t.Fatalf("expected pending, got %s", item.Status)
	}

	got, err := store.Get(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected item, got nil")
	}
	if got.Kind != KindMemory {
		t.Fatalf("expected memory, got %s", got.Kind)
	}
	if got.Content != "The sky is blue." {
		t.Fatalf("content mismatch: %s", got.Content)
	}
	if got.Label != "fact" {
		t.Fatalf("label mismatch: %s", got.Label)
	}
	if got.Source != "test-source" {
		t.Fatalf("source mismatch: %s", got.Source)
	}
}

func TestListWithFilters(t *testing.T) {
	store, _ := tempDB(t)
	defer store.Close()

	store.Push(PushRequest{Kind: KindMemory, Content: "a"}, "src-a")
	store.Push(PushRequest{Kind: KindFile, Content: "b"}, "src-b")
	store.Push(PushRequest{Kind: KindMessage, Content: "c"}, "src-a")

	all, err := store.List(ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if all.Total != 3 {
		t.Fatalf("expected 3, got %d", all.Total)
	}

	memOnly, err := store.List(ListFilter{Kind: KindMemory})
	if err != nil {
		t.Fatal(err)
	}
	if memOnly.Total != 1 {
		t.Fatalf("expected 1 memory, got %d", memOnly.Total)
	}

	srcA, err := store.List(ListFilter{Source: "src-a"})
	if err != nil {
		t.Fatal(err)
	}
	if srcA.Total != 2 {
		t.Fatalf("expected 2 from src-a, got %d", srcA.Total)
	}
}

func TestApprove(t *testing.T) {
	store, _ := tempDB(t)
	defer store.Close()

	item, _ := store.Push(PushRequest{Kind: KindMemory, Content: "fact"}, "src")
	updated, err := store.Review(item.ID, ReviewRequest{
		Decision:   "approved",
		ReviewedBy: "admin",
		ReviewNote: "looks good",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != StatusApproved {
		t.Fatalf("expected approved, got %s", updated.Status)
	}
	if updated.ReviewedBy != "admin" {
		t.Fatalf("expected admin, got %s", updated.ReviewedBy)
	}
}

func TestReject(t *testing.T) {
	store, _ := tempDB(t)
	defer store.Close()

	item, _ := store.Push(PushRequest{Kind: KindMemory, Content: "spam"}, "src")
	updated, err := store.Review(item.ID, ReviewRequest{Decision: "rejected"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != StatusRejected {
		t.Fatalf("expected rejected, got %s", updated.Status)
	}
}

func TestDoubleReviewFails(t *testing.T) {
	store, _ := tempDB(t)
	defer store.Close()

	item, _ := store.Push(PushRequest{Kind: KindMemory, Content: "fact"}, "src")
	store.Review(item.ID, ReviewRequest{Decision: "approved"})
	_, err := store.Review(item.ID, ReviewRequest{Decision: "rejected"})
	if err == nil {
		t.Fatal("expected error on double review")
	}
}

func TestGetNonexistent(t *testing.T) {
	store, _ := tempDB(t)
	defer store.Close()

	got, err := store.Get("nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatal("expected nil")
	}
}

func TestDelete(t *testing.T) {
	store, _ := tempDB(t)
	defer store.Close()

	item, _ := store.Push(PushRequest{Kind: KindMemory, Content: "x"}, "src")
	ok, err := store.Delete(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected true")
	}

	got, _ := store.Get(item.ID)
	if got != nil {
		t.Fatal("expected nil after delete")
	}

	ok, _ = store.Delete(item.ID)
	if ok {
		t.Fatal("expected false for second delete")
	}
}

func TestPurgeByStatus(t *testing.T) {
	store, _ := tempDB(t)
	defer store.Close()

	store.Push(PushRequest{Kind: KindMemory, Content: "a"}, "src")
	item2, _ := store.Push(PushRequest{Kind: KindMemory, Content: "b"}, "src")
	store.Review(item2.ID, ReviewRequest{Decision: "approved"})
	store.Push(PushRequest{Kind: KindMemory, Content: "c"}, "src")

	n, err := store.Purge(StatusApproved)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 purged, got %d", n)
	}

	total, _ := store.Count("")
	if total != 2 {
		t.Fatalf("expected 2 remaining, got %d", total)
	}
}

func TestCount(t *testing.T) {
	store, _ := tempDB(t)
	defer store.Close()

	store.Push(PushRequest{Kind: KindMemory, Content: "a"}, "src")
	store.Push(PushRequest{Kind: KindMemory, Content: "b"}, "src")
	item3, _ := store.Push(PushRequest{Kind: KindMemory, Content: "c"}, "src")
	store.Review(item3.ID, ReviewRequest{Decision: "rejected"})

	total, _ := store.Count("")
	if total != 3 {
		t.Fatalf("expected 3, got %d", total)
	}
	pending, _ := store.Count(StatusPending)
	if pending != 2 {
		t.Fatalf("expected 2 pending, got %d", pending)
	}
	rejected, _ := store.Count(StatusRejected)
	if rejected != 1 {
		t.Fatalf("expected 1 rejected, got %d", rejected)
	}
}

func TestMetadata(t *testing.T) {
	store, _ := tempDB(t)
	defer store.Close()

	item, err := store.Push(PushRequest{
		Kind:       KindFile,
		Content:    "data",
		TargetPath: "notes/pr42.md",
		Metadata:   map[string]interface{}{"origin": "github", "pr": 42},
	}, "src")
	if err != nil {
		t.Fatal(err)
	}

	got, _ := store.Get(item.ID)
	if got.TargetPath != "notes/pr42.md" {
		t.Fatalf("targetPath mismatch: %s", got.TargetPath)
	}
	if got.Metadata == "" {
		t.Fatal("expected metadata")
	}
}

func TestPromoteApprovedItem(t *testing.T) {
	store, _ := tempDB(t)
	defer store.Close()

	workspace := t.TempDir()

	item, _ := store.Push(PushRequest{
		Kind:       KindMemory,
		Content:    "The sky is blue.",
		TargetPath: "facts.md",
	}, "src")
	store.Review(item.ID, ReviewRequest{Decision: "approved"})
	got, _ := store.Get(item.ID)

	result, err := Promote(got, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Written {
		t.Fatal("expected written=true")
	}

	content, err := os.ReadFile(filepath.Join(workspace, "facts.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "The sky is blue." {
		t.Fatalf("content mismatch: %s", string(content))
	}
}

func TestPromoteAppendsToExisting(t *testing.T) {
	store, _ := tempDB(t)
	defer store.Close()

	workspace := t.TempDir()
	os.WriteFile(filepath.Join(workspace, "facts.md"), []byte("existing"), 0o644)

	item, _ := store.Push(PushRequest{
		Kind:       KindMemory,
		Content:    "new fact",
		TargetPath: "facts.md",
	}, "src")
	store.Review(item.ID, ReviewRequest{Decision: "approved"})
	got, _ := store.Get(item.ID)

	Promote(got, workspace)

	content, _ := os.ReadFile(filepath.Join(workspace, "facts.md"))
	if string(content) != "existing\n\n---\n\nnew fact" {
		t.Fatalf("content mismatch: %q", string(content))
	}
}

func TestPromoteRejectsPending(t *testing.T) {
	store, _ := tempDB(t)
	defer store.Close()

	item, _ := store.Push(PushRequest{
		Kind:       KindMemory,
		Content:    "x",
		TargetPath: "x.md",
	}, "src")
	got, _ := store.Get(item.ID)

	_, err := Promote(got, t.TempDir())
	if err == nil {
		t.Fatal("expected error promoting pending item")
	}
}

func TestPromoteRejectsPathTraversal(t *testing.T) {
	store, _ := tempDB(t)
	defer store.Close()

	// We bypass the push validation here to test promote's own guard.
	item := &Item{
		ID:         "test",
		Status:     StatusApproved,
		Kind:       KindFile,
		Content:    "evil",
		TargetPath: "../../../etc/passwd",
	}
	_, err := Promote(item, t.TempDir())
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

func TestPromoteMessageNoFile(t *testing.T) {
	item := &Item{
		ID:     "msg-1",
		Status: StatusApproved,
		Kind:   KindMessage,
	}
	result, err := Promote(item, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if result.Written {
		t.Fatal("expected written=false for message")
	}
}
