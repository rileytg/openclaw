// Package main implements the openclaw quarantine service — a standalone
// system that accepts pushed content, holds it for human review, and only
// promotes approved items into the bot's workspace on explicit command.
//
// This binary shares zero code, zero runtime, and zero filesystem with the
// bot.  Pushers can never write to the bot's filesystem.
package main

import "time"

// ItemStatus is the review state of a quarantined item.
type ItemStatus string

const (
	StatusPending  ItemStatus = "pending"
	StatusApproved ItemStatus = "approved"
	StatusRejected ItemStatus = "rejected"
)

// ContentKind describes what type of content an item holds.
type ContentKind string

const (
	KindMemory  ContentKind = "memory"
	KindFile    ContentKind = "file"
	KindMessage ContentKind = "message"
)

// Item is a single quarantined item as stored in the database.
type Item struct {
	ID         string     `json:"id"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	Status     ItemStatus `json:"status"`
	Kind       ContentKind `json:"kind"`
	Source     string     `json:"source"`
	Label      string     `json:"label,omitempty"`
	Content    string     `json:"content"`
	TargetPath string     `json:"targetPath,omitempty"`
	Metadata   string     `json:"metadata,omitempty"`
	ReviewedBy string     `json:"reviewedBy,omitempty"`
	ReviewNote string     `json:"reviewNote,omitempty"`
}

// PushRequest is the JSON body accepted by POST /push.
type PushRequest struct {
	Kind       ContentKind            `json:"kind"`
	Content    string                 `json:"content"`
	Label      string                 `json:"label,omitempty"`
	TargetPath string                 `json:"targetPath,omitempty"`
	Metadata   map[string]interface{} `json:"metadata,omitempty"`
}

// ReviewRequest is the JSON body accepted by POST /review/:id.
type ReviewRequest struct {
	Decision   string `json:"decision"` // "approved" or "rejected"
	ReviewedBy string `json:"reviewedBy,omitempty"`
	ReviewNote string `json:"reviewNote,omitempty"`
}

// ListFilter controls what items are returned by a list query.
type ListFilter struct {
	Status ItemStatus
	Kind   ContentKind
	Source string
	Limit  int
	Offset int
}

// ListResult is the response shape for list queries.
type ListResult struct {
	Items []Item `json:"items"`
	Total int    `json:"total"`
}
