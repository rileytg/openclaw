# Push-Based Memory Model - Implementation Plan

## Problem

The current memory model is pull-based. To give the bot context about flights, hotels, etc.,
you'd need to grant it credentials to external services. That's a security risk.

**Push model**: Lightweight pushers that *you* control push structured data into the bot's
memory. The bot never gets credentials to Delta, Hilton, FedEx, etc.

**Enrichment model**: Pushed data contains private identifiers (confirmation codes, seat
assignments, room numbers). The bot can independently look up *public* information (flight
status, gate changes, delays) using identifiers from the pushed data, via `web_fetch` or
dedicated enrichment URLs. Private data stays private; public data stays fresh.

## Architecture

```
┌──────────────────┐  ┌──────────────────┐  ┌──────────────────┐
│  Flight Pusher   │  │  Hotel Pusher    │  │ Package Pusher   │
│  (Delta creds)   │  │  (Hilton creds)  │  │  (FedEx creds)   │
└────────┬─────────┘  └────────┬─────────┘  └────────┬─────────┘
         │                     │                      │
         └─────────┬───────────┘──────────────────────┘
                   │
           POST /v1/memory/push
           (Bearer: hooks-token)
                   │
                   ▼
    ┌──────────────────────────────┐
    │     Push Memory Handler      │
    │                              │
    │  1. Auth (hooks token)       │
    │  2. Validate schema (Zod)    │
    │  3. Store structured JSON    │
    │  4. Generate searchable .md  │
    │  5. Trigger memory sync      │
    │  6. Fire webhooks            │
    └──────────────┬───────────────┘
                   │
          ┌────────┴─────────┐
          ▼                  ▼
   ┌──────────────┐   ┌──────────────────┐
   │  SQLite DB   │   │ Memory Files     │
   │  push_entries│   │ ~/.openclaw/     │
   │              │   │  memory/push/    │
   │ Structured   │   │  {source}/       │
   │ JSON fields  │   │  {key}.md        │
   │ + metadata   │   │                  │
   └──────────────┘   └──────────────────┘
          │                  │
          └────────┬─────────┘
                   ▼
          ┌────────────────────┐
          │    Agent Tools     │
          │                    │
          │ memory_search      │ ← semantic queries (existing)
          │ memory_push_lookup │ ← structured lookups (new)
          │ web_fetch          │ ← public enrichment (existing)
          └────────────────────┘
```

## Key Design Decision: Public Enrichment

Pushed entries can include `enrichment` metadata — public URLs or API patterns the bot
can use to refresh live data without needing credentials:

```json
{
  "source": "flights",
  "key": "DL1234-2026-02-19",
  "schema_type": "flight",
  "data": {
    "airline": "Delta",
    "flightNumber": "DL1234",
    "departure": { "airport": "SFO", "time": "2026-02-19T08:00:00-08:00" },
    "arrival": { "airport": "JFK" },
    "confirmationCode": "ABC123",
    "seat": "12A"
  },
  "enrichment": {
    "urls": [
      {
        "label": "Live flight status",
        "url": "https://flightaware.com/live/flight/DAL1234",
        "hint": "Check this for current gate, delays, and arrival time"
      }
    ],
    "refresh_hint": "Check status within 4 hours of departure"
  }
}
```

When the bot needs current flight status, it uses its existing `web_fetch` tool with the
enrichment URL. The pusher provides the URL; the bot does the fetching. No credentials
needed — FlightAware/FlightStats pages are public.

**What stays private** (only in pushed data): confirmation code, seat, booking reference
**What the bot can refresh** (via public URLs): flight status, gate, delays, terminal

## Storage

### SQLite Table (`push_entries`)

Stored in the agent's existing memory database (alongside embeddings).

```sql
CREATE TABLE push_entries (
  id TEXT PRIMARY KEY,
  source TEXT NOT NULL,
  key TEXT NOT NULL,
  category TEXT,
  schema_type TEXT NOT NULL,
  data TEXT NOT NULL,             -- structured JSON
  enrichment TEXT,                -- enrichment config JSON
  tags TEXT,                      -- JSON array of strings
  priority TEXT DEFAULT 'normal',
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  expires_at INTEGER,             -- unix ms, NULL = never
  UNIQUE(source, key)
);
CREATE INDEX idx_push_source ON push_entries(source);
CREATE INDEX idx_push_category ON push_entries(category);
CREATE INDEX idx_push_schema ON push_entries(schema_type);
CREATE INDEX idx_push_expires ON push_entries(expires_at);
```

### Memory Files

Each push entry also produces a managed markdown file at:
`~/.openclaw/memory/push/{source}/{key}.md`

This is what gets indexed by the existing memory sync pipeline. The file is auto-generated
from structured data — never hand-edited.

Example (`~/.openclaw/memory/push/flights/DL1234-2026-02-19.md`):

```markdown
---
source: flights
key: DL1234-2026-02-19
schema_type: flight
category: travel
expires_at: 2026-02-22T08:00:00Z
---
# Flight: Delta DL1234

- **Route**: SFO → JFK
- **Departure**: Feb 19, 2026 8:00 AM PST
- **Arrival**: Feb 19, 2026 4:30 PM EST
- **Confirmation**: ABC123
- **Seat**: 12A
- **Status**: Scheduled

> To check live status: https://flightaware.com/live/flight/DAL1234
```

## Schema Types

### `flight`

```typescript
type FlightData = {
  airline: string;
  flightNumber: string;
  departure: {
    airport: string;
    terminal?: string;
    gate?: string;
    time: string; // ISO 8601
  };
  arrival: {
    airport: string;
    terminal?: string;
    gate?: string;
    time?: string;
  };
  confirmationCode?: string;
  seat?: string;
  status?: "scheduled" | "delayed" | "cancelled" | "boarding" | "departed" | "arrived";
  passengers?: string[];
  bookingReference?: string;
};
```

### `hotel`

```typescript
type HotelData = {
  name: string;
  brand?: string; // "Hilton", "Marriott", etc.
  address?: string;
  city?: string;
  checkIn: string;  // ISO date or datetime
  checkOut: string;
  confirmationCode?: string;
  roomType?: string;
  roomNumber?: string;
  loyaltyNumber?: string;
  guests?: string[];
  notes?: string;
};
```

### `package_tracking`

```typescript
type PackageTrackingData = {
  carrier: string;        // "FedEx", "UPS", "USPS", "DHL"
  trackingNumber: string;
  description?: string;   // "MacBook Pro 16-inch"
  origin?: string;
  destination?: string;
  status?: "pre_transit" | "in_transit" | "out_for_delivery" | "delivered" | "exception";
  estimatedDelivery?: string;
  lastUpdate?: string;
  lastLocation?: string;
};
```

### `calendar_event`

```typescript
type CalendarEventData = {
  title: string;
  startTime: string;
  endTime?: string;
  location?: string;
  description?: string;
  attendees?: string[];
  conferenceUrl?: string;
  recurring?: boolean;
};
```

### `generic`

```typescript
type GenericData = {
  title: string;
  body?: string;
  fields?: Record<string, unknown>;
};
```

Unknown schema types are accepted and treated like `generic` — the system is open, not
closed. New types can be added without breaking anything.

## Enrichment Config

Every push entry can optionally include enrichment metadata:

```typescript
type PushEnrichment = {
  urls?: Array<{
    label: string;         // "Live flight status"
    url: string;           // Public URL the bot can fetch
    hint?: string;         // When/how to use this URL
  }>;
  refresh_hint?: string;   // Human-readable guidance for the bot
};
```

The bot's existing `web_fetch` tool handles the actual fetching. The push system just
stores the URLs alongside the data so the bot knows *where* to look for updates.

## Webhooks (Two-Way)

Push entries can register outbound webhook notifications:

```typescript
type PushWebhook = {
  url: string;             // URL to POST to
  events: Array<
    "expiring" |           // Entry expires within `before` period
    "expired" |            // Entry has expired and been reaped
    "updated" |            // Entry was updated by a subsequent push
    "deleted"              // Entry was explicitly deleted
  >;
  before?: number;         // Seconds before expiry to fire "expiring" (default: 3600)
  headers?: Record<string, string>;  // Custom headers for the webhook
};
```

Example: A flight pusher registers a webhook so it knows to refresh the push when data
is about to expire:

```json
{
  "source": "flights",
  "key": "DL1234-2026-02-19",
  "schema_type": "flight",
  "data": { "..." : "..." },
  "webhooks": [{
    "url": "https://my-flight-pusher.example.com/refresh",
    "events": ["expiring"],
    "before": 7200
  }]
}
```

Webhook payloads:

```json
{
  "event": "expiring",
  "source": "flights",
  "key": "DL1234-2026-02-19",
  "schema_type": "flight",
  "expires_at": "2026-02-22T08:00:00.000Z",
  "expires_in_seconds": 7200
}
```

## API Endpoints

All under the existing hooks HTTP infrastructure, using the same auth token.

### `POST /hooks/push`

Push one or more entries. Upserts on `(source, key)`.

```json
{
  "entries": [{
    "source": "flights",
    "key": "DL1234-2026-02-19",
    "schema_type": "flight",
    "category": "travel",
    "data": { ... },
    "enrichment": { ... },
    "webhooks": [{ ... }],
    "tags": ["travel", "delta"],
    "ttl": 259200,
    "priority": "normal"
  }]
}
```

Response:
```json
{
  "ok": true,
  "accepted": 1,
  "results": [{
    "id": "uuid",
    "source": "flights",
    "key": "DL1234-2026-02-19",
    "status": "created"
  }]
}
```

### `DELETE /hooks/push/{source}/{key}`

Remove a specific entry. Deletes the DB row, removes the memory file, fires webhooks.

### `GET /hooks/push`

List entries. Query params: `?source=flights&category=travel&active=true`

### `GET /hooks/push/{source}/{key}`

Get a specific entry with full structured data.

## Agent Tool: `memory_push_lookup`

A new tool registered alongside `memory_search` and `memory_get`:

```typescript
memory_push_lookup({
  source?: string,       // filter by source
  category?: string,     // filter by category
  schema_type?: string,  // filter by schema type
  key?: string,          // exact key lookup
  active?: boolean,      // only non-expired (default true)
})
```

Returns structured JSON — not text snippets. The bot gets real fields it can reason about:

```json
{
  "entries": [{
    "source": "flights",
    "key": "DL1234-2026-02-19",
    "schema_type": "flight",
    "data": {
      "airline": "Delta",
      "flightNumber": "DL1234",
      "departure": { "airport": "SFO", "time": "2026-02-19T08:00:00-08:00" },
      "arrival": { "airport": "JFK" },
      "confirmationCode": "ABC123",
      "seat": "12A"
    },
    "enrichment": {
      "urls": [{ "label": "Live status", "url": "https://flightaware.com/..." }]
    },
    "expires_at": "2026-02-22T08:00:00.000Z"
  }],
  "total": 1
}
```

Tool description emphasizes: "For live/current status, use the enrichment URLs with
web_fetch after looking up the entry."

## TTL Reaper

A periodic job running on a configurable interval (default: every 15 minutes):

1. Find entries where `expires_at < now()`
2. Fire "expired" webhooks for entries that haven't already been notified
3. Find entries where `expires_at < now() + before` (for "expiring" webhooks)
4. Fire "expiring" webhooks
5. Delete expired entries from DB
6. Delete corresponding memory files from `~/.openclaw/memory/push/`
7. Next memory sync cycle cleans up stale index entries

## Configuration

Added to `memory` in `openclaw.json`:

```jsonc
{
  "memory": {
    // existing fields...
    "push": {
      "enabled": true,
      "allowedSources": [],           // empty = allow all
      "maxEntries": 1000,
      "maxEntryBytes": 102400,        // 100KB per entry
      "defaultTtlSeconds": 2592000,   // 30 days
      "reapIntervalMinutes": 15,
      "webhooks": {
        "enabled": true,
        "timeoutMs": 10000,
        "maxRetries": 2
      }
    }
  }
}
```

## Security Model

- Bot never gets external credentials — pushers run independently
- Hooks token required for all push API calls (reuses existing hooks auth)
- Source allowlisting — optionally restrict which source names are accepted
- Size limits — per-entry and global caps
- TTL enforcement — data doesn't accumulate indefinitely
- No code execution — pushed data is passive, never evaluated
- Enrichment URLs are fetched by the bot's `web_fetch` tool, which already has SSRF
  protections and domain filtering

## Implementation Order

### Phase 1: Core Storage + API

| # | File | What |
|---|------|------|
| 1 | `src/config/types.memory.ts` | Add `MemoryPushConfig` type |
| 2 | `src/config/types.openclaw.ts` | No change needed — `MemoryConfig` already imported |
| 3 | `src/memory/push/schema.ts` | Zod schemas for push API, known schema types, enrichment |
| 4 | `src/memory/push/store.ts` | SQLite CRUD for `push_entries` |
| 5 | `src/memory/push/content-gen.ts` | Structured data → markdown file content |
| 6 | `src/memory/push/file-sync.ts` | Write/delete managed markdown files |
| 7 | `src/memory/push/reaper.ts` | TTL expiry + "expiring"/"expired" webhook dispatch |
| 8 | `src/memory/push/webhooks.ts` | Outbound webhook delivery |

### Phase 2: HTTP + Agent Integration

| # | File | What |
|---|------|------|
| 9 | `src/gateway/push-memory-http.ts` | HTTP handlers for `/hooks/push` endpoints |
| 10 | `src/gateway/server-http.ts` | Register push handler in dispatch chain |
| 11 | `src/agents/tools/push-lookup-tool.ts` | `memory_push_lookup` agent tool |
| 12 | `src/memory/internal.ts` | Add push dir to `listMemoryFiles()` discovery |

### Phase 3: Tool Registration + Wiring

| # | File | What |
|---|------|------|
| 13 | `extensions/memory-core/index.ts` | Register push lookup tool alongside memory tools |
| 14 | `src/plugins/runtime/index.ts` | Expose push store to plugin runtime |

## User Flow Example

```
1. You deploy a flight pusher daemon:
   $ openclaw-push flights \
       --gateway http://localhost:18789 \
       --token $HOOKS_TOKEN \
       --delta-email you@gmail.com

2. Pusher detects a new flight email, pushes structured data:
   POST /hooks/push
   { entries: [{ source: "flights", key: "DL1234-2026-02-19", ... }] }

3. Bot now has the flight in memory. Later, in conversation:

   You: "When does my flight leave tomorrow?"
   Bot: [uses memory_push_lookup, reads data.departure.time]
   Bot: "Your Delta DL1234 departs SFO at 8:00 AM PST."

   You: "Is it on time?"
   Bot: [reads enrichment.urls, uses web_fetch on FlightAware URL]
   Bot: "Checking live status... Yes, DL1234 is on time. Gate B34."

   You: "What's my confirmation code?"
   Bot: [reads data.confirmationCode from push entry]
   Bot: "Your confirmation code is ABC123."
```

The private data (confirmation code) came from the push.
The live data (gate, on-time status) came from a public URL.
The bot never touched your Delta account.
