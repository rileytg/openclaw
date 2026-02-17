package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func testAPI(t *testing.T) (*API, *Store) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "quarantine.db")
	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	cfg := &Config{
		PushToken:       "push-secret",
		AdminToken:      "admin-secret",
		MaxContentBytes: 256 * 1024,
		MaxPendingItems: 100,
		BotWorkspaceDir: filepath.Join(dir, "workspace"),
	}
	api := NewAPI(store, cfg)
	return api, store
}

func doReq(api *API, method, path, token string, body interface{}) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	api.ServeHTTP(w, req)
	return w
}

func TestPushRequiresAuth(t *testing.T) {
	api, store := testAPI(t)
	defer store.Close()

	w := doReq(api, "POST", "/push", "", PushRequest{Kind: KindMemory, Content: "x"})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	w = doReq(api, "POST", "/push", "wrong", PushRequest{Kind: KindMemory, Content: "x"})
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestPushSuccess(t *testing.T) {
	api, store := testAPI(t)
	defer store.Close()

	w := doReq(api, "POST", "/push", "push-secret", PushRequest{
		Kind:    KindMemory,
		Content: "The sky is blue.",
		Label:   "fact",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["ok"] != true {
		t.Fatalf("expected ok=true, got %v", resp)
	}
	if resp["id"] == nil || resp["id"] == "" {
		t.Fatal("expected non-empty id")
	}
}

func TestPushValidation(t *testing.T) {
	api, store := testAPI(t)
	defer store.Close()

	// Missing kind.
	w := doReq(api, "POST", "/push", "push-secret", map[string]string{
		"content": "x",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}

	// Empty content.
	w = doReq(api, "POST", "/push", "push-secret", PushRequest{
		Kind: KindMemory,
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}

	// Path traversal.
	w = doReq(api, "POST", "/push", "push-secret", PushRequest{
		Kind:       KindFile,
		Content:    "x",
		TargetPath: "../../../etc/passwd",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAdminListRequiresAuth(t *testing.T) {
	api, store := testAPI(t)
	defer store.Close()

	w := doReq(api, "GET", "/items", "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	w = doReq(api, "GET", "/items", "push-secret", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 with push token on admin endpoint, got %d", w.Code)
	}
}

func TestAdminListSuccess(t *testing.T) {
	api, store := testAPI(t)
	defer store.Close()

	store.Push(PushRequest{Kind: KindMemory, Content: "a"}, "src")
	store.Push(PushRequest{Kind: KindFile, Content: "b"}, "src")

	w := doReq(api, "GET", "/items", "admin-secret", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var result ListResult
	json.NewDecoder(w.Body).Decode(&result)
	if result.Total != 2 {
		t.Fatalf("expected 2 items, got %d", result.Total)
	}
}

func TestAdminReviewAndPromote(t *testing.T) {
	api, store := testAPI(t)
	defer store.Close()

	item, _ := store.Push(PushRequest{
		Kind:       KindMemory,
		Content:    "important fact",
		TargetPath: "facts.md",
	}, "src")

	// Approve.
	w := doReq(api, "POST", "/items/"+item.ID+"/review", "admin-secret",
		ReviewRequest{Decision: "approved", ReviewedBy: "admin"})
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Promote.
	w = doReq(api, "POST", "/items/"+item.ID+"/promote", "admin-secret", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestPendingEndpoint(t *testing.T) {
	api, store := testAPI(t)
	defer store.Close()

	store.Push(PushRequest{Kind: KindMemory, Content: "a"}, "src")

	w := doReq(api, "GET", "/pending", "push-secret", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)
	if resp["pending"] != float64(1) {
		t.Fatalf("expected 1 pending, got %v", resp["pending"])
	}
}

func TestDeleteEndpoint(t *testing.T) {
	api, store := testAPI(t)
	defer store.Close()

	item, _ := store.Push(PushRequest{Kind: KindMemory, Content: "x"}, "src")

	w := doReq(api, "DELETE", "/items/"+item.ID, "admin-secret", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Second delete should 404.
	w = doReq(api, "DELETE", "/items/"+item.ID, "admin-secret", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
