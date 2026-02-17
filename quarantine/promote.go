package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PromotionResult describes what happened when an item was promoted.
type PromotionResult struct {
	ID         string `json:"id"`
	TargetPath string `json:"targetPath"`
	Written    bool   `json:"written"`
}

// Promote writes an approved item's content into the bot's workspace.
// This is the ONLY code path that bridges quarantine → bot filesystem.
//
// For "memory" and "file" items, targetPath is required and resolved
// relative to workspaceDir. The resolved path must stay inside workspaceDir.
//
// For "message" items, no file is written.
func Promote(item *Item, workspaceDir string) (*PromotionResult, error) {
	if item.Status != StatusApproved {
		return nil, fmt.Errorf("cannot promote item %s: status is %q, expected \"approved\"", item.ID, item.Status)
	}

	if item.Kind == KindMessage {
		return &PromotionResult{
			ID:         item.ID,
			TargetPath: "(message — no file)",
			Written:    false,
		}, nil
	}

	if item.TargetPath == "" {
		return nil, fmt.Errorf("cannot promote %s item %s: targetPath is required", item.Kind, item.ID)
	}

	if workspaceDir == "" {
		return nil, fmt.Errorf("QUARANTINE_BOT_WORKSPACE is not configured — cannot promote files")
	}

	resolved := filepath.Join(workspaceDir, item.TargetPath)
	resolved, err := filepath.Abs(resolved)
	if err != nil {
		return nil, fmt.Errorf("resolve path: %w", err)
	}

	absWorkspace, err := filepath.Abs(workspaceDir)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace: %w", err)
	}

	// Safety: resolved path must stay inside the workspace.
	if !strings.HasPrefix(resolved, absWorkspace+string(filepath.Separator)) && resolved != absWorkspace {
		return nil, fmt.Errorf("targetPath %q escapes workspace %q", item.TargetPath, workspaceDir)
	}

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir: %w", err)
	}

	// If file exists, append with separator; otherwise create.
	existing, err := os.ReadFile(resolved)
	if err == nil && len(existing) > 0 {
		content := string(existing) + "\n\n---\n\n" + item.Content
		if err := os.WriteFile(resolved, []byte(content), 0o644); err != nil {
			return nil, fmt.Errorf("write (append): %w", err)
		}
	} else {
		if err := os.WriteFile(resolved, []byte(item.Content), 0o644); err != nil {
			return nil, fmt.Errorf("write: %w", err)
		}
	}

	return &PromotionResult{
		ID:         item.ID,
		TargetPath: resolved,
		Written:    true,
	}, nil
}
