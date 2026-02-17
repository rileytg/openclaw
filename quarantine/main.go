package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"text/tabwriter"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "serve":
		cmdServe()
	case "list":
		cmdList()
	case "inspect":
		cmdInspect()
	case "approve":
		cmdApproveOrReject(StatusApproved)
	case "reject":
		cmdApproveOrReject(StatusRejected)
	case "promote":
		cmdPromote()
	case "purge":
		cmdPurge()
	case "count":
		cmdCount()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `openclaw-quarantine — standalone quarantine system

Commands:
  serve                 Start the HTTP server
  list [--status=X]     List quarantined items
  inspect <id>          Show a single item
  approve <id>          Approve an item
  reject <id>           Reject an item
  promote <id>          Promote an approved item to bot workspace
  purge [--status=X]    Delete reviewed items
  count [--status=X]    Count items

Environment:
  QUARANTINE_DB_PATH          Path to SQLite database (required)
  QUARANTINE_PUSH_TOKEN       Bearer token for pushers (required for serve)
  QUARANTINE_ADMIN_TOKEN      Bearer token for admin API (optional)
  QUARANTINE_LISTEN           Listen address (default :8033)
  QUARANTINE_MAX_CONTENT_BYTES  Max content per item (default 256KB)
  QUARANTINE_MAX_PENDING_ITEMS  Max pending items (default 1000)
  QUARANTINE_BOT_WORKSPACE    Bot workspace dir (for promote)
`)
}

// --- serve ---

func cmdServe() {
	cfg, err := LoadConfig()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	store, err := OpenStore(cfg.DBPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer store.Close()

	api := NewAPI(store, cfg)

	log.Printf("quarantine listening on %s (db: %s)", cfg.ListenAddr, cfg.DBPath)
	srv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      api,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

// --- CLI commands (direct DB access, no HTTP) ---

func openStoreFromEnv() *Store {
	dbPath := os.Getenv("QUARANTINE_DB_PATH")
	if dbPath == "" {
		fmt.Fprintln(os.Stderr, "QUARANTINE_DB_PATH is required")
		os.Exit(1)
	}
	store, err := OpenStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open store: %v\n", err)
		os.Exit(1)
	}
	return store
}

func cmdList() {
	store := openStoreFromEnv()
	defer store.Close()

	f := ListFilter{Limit: 100}
	for _, arg := range os.Args[2:] {
		if v, ok := flagVal(arg, "--status"); ok {
			f.Status = ItemStatus(v)
		}
		if v, ok := flagVal(arg, "--kind"); ok {
			f.Kind = ContentKind(v)
		}
		if v, ok := flagVal(arg, "--source"); ok {
			f.Source = v
		}
	}

	result, err := store.List(f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "list: %v\n", err)
		os.Exit(1)
	}

	if len(result.Items) == 0 {
		fmt.Println("No items.")
		return
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tSTATUS\tKIND\tSOURCE\tLABEL\tCREATED")
	for _, item := range result.Items {
		label := item.Label
		if len(label) > 30 {
			label = label[:27] + "..."
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			item.ID, item.Status, item.Kind, item.Source, label,
			item.CreatedAt.Format(time.RFC3339),
		)
	}
	tw.Flush()
	fmt.Printf("\nTotal: %d\n", result.Total)
}

func cmdInspect() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: quarantine inspect <id>")
		os.Exit(1)
	}
	store := openStoreFromEnv()
	defer store.Close()

	item, err := store.Get(os.Args[2])
	if err != nil {
		fmt.Fprintf(os.Stderr, "get: %v\n", err)
		os.Exit(1)
	}
	if item == nil {
		fmt.Fprintln(os.Stderr, "not found")
		os.Exit(1)
	}

	b, _ := json.MarshalIndent(item, "", "  ")
	fmt.Println(string(b))
}

func cmdApproveOrReject(decision ItemStatus) {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "usage: quarantine %s <id>\n", os.Args[1])
		os.Exit(1)
	}
	store := openStoreFromEnv()
	defer store.Close()

	reviewedBy := os.Getenv("USER")
	if reviewedBy == "" {
		reviewedBy = "cli"
	}

	item, err := store.Review(os.Args[2], ReviewRequest{
		Decision:   string(decision),
		ReviewedBy: reviewedBy,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "review: %v\n", err)
		os.Exit(1)
	}
	if item == nil {
		fmt.Fprintln(os.Stderr, "not found")
		os.Exit(1)
	}

	fmt.Printf("Item %s → %s\n", item.ID, item.Status)
}

func cmdPromote() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: quarantine promote <id>")
		os.Exit(1)
	}
	store := openStoreFromEnv()
	defer store.Close()

	workspaceDir := os.Getenv("QUARANTINE_BOT_WORKSPACE")

	item, err := store.Get(os.Args[2])
	if err != nil {
		fmt.Fprintf(os.Stderr, "get: %v\n", err)
		os.Exit(1)
	}
	if item == nil {
		fmt.Fprintln(os.Stderr, "not found")
		os.Exit(1)
	}

	result, err := Promote(item, workspaceDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "promote: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Promoted %s → %s (written: %v)\n", result.ID, result.TargetPath, result.Written)
}

func cmdPurge() {
	store := openStoreFromEnv()
	defer store.Close()

	var status ItemStatus
	for _, arg := range os.Args[2:] {
		if v, ok := flagVal(arg, "--status"); ok {
			status = ItemStatus(v)
		}
	}

	n, err := store.Purge(status)
	if err != nil {
		fmt.Fprintf(os.Stderr, "purge: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Purged %d items\n", n)
}

func cmdCount() {
	store := openStoreFromEnv()
	defer store.Close()

	var status ItemStatus
	for _, arg := range os.Args[2:] {
		if v, ok := flagVal(arg, "--status"); ok {
			status = ItemStatus(v)
		}
	}

	n, err := store.Count(status)
	if err != nil {
		fmt.Fprintf(os.Stderr, "count: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(n)
}

// flagVal extracts "--key=value" from an arg.
func flagVal(arg, key string) (string, bool) {
	prefix := key + "="
	if len(arg) > len(prefix) && arg[:len(prefix)] == prefix {
		return arg[len(prefix):], true
	}
	return "", false
}
