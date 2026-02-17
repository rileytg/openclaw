package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
)

// API is the HTTP handler for the quarantine service.
type API struct {
	store  *Store
	config *Config
	mux    *http.ServeMux
}

// NewAPI creates a new quarantine API handler.
func NewAPI(store *Store, config *Config) *API {
	a := &API{store: store, config: config, mux: http.NewServeMux()}
	a.mux.HandleFunc("POST /push", a.requirePushAuth(a.handlePush))
	a.mux.HandleFunc("GET /pending", a.requirePushAuth(a.handlePending))
	a.mux.HandleFunc("GET /items", a.requireAdminAuth(a.handleList))
	a.mux.HandleFunc("GET /items/{id}", a.requireAdminAuth(a.handleGet))
	a.mux.HandleFunc("POST /items/{id}/review", a.requireAdminAuth(a.handleReview))
	a.mux.HandleFunc("POST /items/{id}/promote", a.requireAdminAuth(a.handlePromote))
	a.mux.HandleFunc("DELETE /items/{id}", a.requireAdminAuth(a.handleDelete))
	a.mux.HandleFunc("POST /purge", a.requireAdminAuth(a.handlePurge))
	return a
}

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mux.ServeHTTP(w, r)
}

// --- auth middleware ---

func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	if t := r.Header.Get("X-Quarantine-Token"); t != "" {
		return strings.TrimSpace(t)
	}
	return ""
}

func safeEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func tokenSourceID(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:6])
}

func (a *API) requirePushAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := extractBearerToken(r)
		if !safeEqual(token, a.config.PushToken) {
			writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
				"ok": false, "error": "unauthorized",
			})
			return
		}
		next(w, r)
	}
}

func (a *API) requireAdminAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.config.AdminToken == "" {
			writeJSON(w, http.StatusForbidden, map[string]interface{}{
				"ok": false, "error": "admin API disabled (QUARANTINE_ADMIN_TOKEN not set)",
			})
			return
		}
		token := extractBearerToken(r)
		if !safeEqual(token, a.config.AdminToken) {
			writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
				"ok": false, "error": "unauthorized",
			})
			return
		}
		next(w, r)
	}
}

// --- handlers ---

func (a *API) handlePush(w http.ResponseWriter, r *http.Request) {
	// Enforce pending-item cap.
	pending, err := a.store.Count(StatusPending)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"ok": false, "error": "internal error",
		})
		return
	}
	if pending >= a.config.MaxPendingItems {
		writeJSON(w, http.StatusTooManyRequests, map[string]interface{}{
			"ok":    false,
			"error": fmt.Sprintf("quarantine full (%d pending, max %d)", pending, a.config.MaxPendingItems),
		})
		return
	}

	var req PushRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, int64(a.config.MaxContentBytes*2))).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok": false, "error": "invalid JSON: " + err.Error(),
		})
		return
	}

	// Validate.
	if err := validatePushRequest(&req, a.config.MaxContentBytes); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok": false, "error": err.Error(),
		})
		return
	}

	source := tokenSourceID(extractBearerToken(r))
	item, err := a.store.Push(req, source)
	if err != nil {
		log.Printf("push failed: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"ok": false, "error": "internal error",
		})
		return
	}

	log.Printf("push: id=%s kind=%s source=%s", item.ID, item.Kind, source)
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"ok": true, "id": item.ID, "status": item.Status,
	})
}

func (a *API) handlePending(w http.ResponseWriter, _ *http.Request) {
	count, err := a.store.Count(StatusPending)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"ok": false, "error": "internal error",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true, "pending": count,
	})
}

func (a *API) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := ListFilter{
		Status: ItemStatus(q.Get("status")),
		Kind:   ContentKind(q.Get("kind")),
		Source: q.Get("source"),
		Limit:  queryInt(q.Get("limit"), 50),
		Offset: queryInt(q.Get("offset"), 0),
	}
	result, err := a.store.List(f)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"ok": false, "error": "internal error",
		})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *API) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	item, err := a.store.Get(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"ok": false, "error": "internal error",
		})
		return
	}
	if item == nil {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"ok": false, "error": "not found",
		})
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (a *API) handleReview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var req ReviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok": false, "error": "invalid JSON",
		})
		return
	}

	item, err := a.store.Review(id, req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok": false, "error": err.Error(),
		})
		return
	}
	if item == nil {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"ok": false, "error": "not found",
		})
		return
	}

	log.Printf("review: id=%s decision=%s by=%s", id, req.Decision, req.ReviewedBy)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true, "item": item,
	})
}

func (a *API) handlePromote(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	item, err := a.store.Get(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"ok": false, "error": "internal error",
		})
		return
	}
	if item == nil {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"ok": false, "error": "not found",
		})
		return
	}

	result, err := Promote(item, a.config.BotWorkspaceDir)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"ok": false, "error": err.Error(),
		})
		return
	}

	log.Printf("promote: id=%s target=%s written=%v", id, result.TargetPath, result.Written)
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true, "result": result,
	})
}

func (a *API) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ok, err := a.store.Delete(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"ok": false, "error": "internal error",
		})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"ok": false, "error": "not found",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (a *API) handlePurge(w http.ResponseWriter, r *http.Request) {
	status := ItemStatus(r.URL.Query().Get("status"))
	n, err := a.store.Purge(status)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"ok": false, "error": "internal error",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true, "purged": n,
	})
}

// --- validation ---

var validKinds = map[ContentKind]bool{
	KindMemory: true, KindFile: true, KindMessage: true,
}

func validatePushRequest(req *PushRequest, maxContentBytes int) error {
	if !validKinds[req.Kind] {
		return fmt.Errorf("kind must be one of: memory, file, message")
	}
	req.Content = strings.TrimSpace(req.Content)
	if req.Content == "" {
		return fmt.Errorf("content is required")
	}
	if len(req.Content) > maxContentBytes {
		return fmt.Errorf("content exceeds maximum size of %d bytes", maxContentBytes)
	}
	req.Label = strings.TrimSpace(req.Label)
	req.TargetPath = strings.TrimSpace(req.TargetPath)
	if req.TargetPath != "" {
		if strings.Contains(req.TargetPath, "..") ||
			strings.HasPrefix(req.TargetPath, "/") ||
			strings.HasPrefix(req.TargetPath, "\\") {
			return fmt.Errorf("targetPath must be a relative path without '..'")
		}
	}
	return nil
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func queryInt(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}
