# Push-Based Memory Model - Implementation Plan

## Problem

The bot needs context about your flights, hotels, packages — but granting it credentials
to Delta, Hilton, FedEx is a safety risk. Even if the bot is trustworthy today, credential
scope creep is how breaches happen.

## Safety Model

The system is built around a single invariant: **the bot never holds credentials to any
external service**. Everything flows from this.

Three access tiers enforce the boundary:

### Tier 0 — Cached (instant, no network, always safe)

The bot reads structured data that was previously pushed into its local store.

- Confirmation codes, seat assignments, room numbers, tracking numbers
- These don't change often — reading from cache is correct
- No network call, no approval needed

*"What's my confirmation code?" → reads `data.confirmationCode` from local SQLite*

### Tier 1 — Public Enrichment (bot fetches freely, no approval)

The bot uses its existing `web_fetch` tool on public URLs. The push entry tells it
*where* to look; the bot does the fetching. No credentials needed.

- Flight status on FlightAware (public)
- Package tracking on carrier websites (public with tracking number)
- Gate changes, delays, delivery updates

*"Is my flight on time?" → `web_fetch` on FlightAware URL from push entry*

### Tier 2 — Private Refresh (user approves on the push service)

When pushed data is stale and the bot needs fresh private info, it **surfaces a refresh
URL** to the user. The user clicks the link, which opens the push service's UI. The user
approves the refresh there. The push service fetches from Delta, pushes fresh data back.

**The bot never calls the push service directly.** It just hands the user a link.

```
Bot: "Your flight data was last updated 3 days ago."
Bot: "Refresh → https://my-flights.example.com/refresh/DL1234-2026-02-19"

User clicks → push service UI → user approves →
push service talks to Delta → pushes fresh data to bot →
bot sees updated entry

Bot: "Got the update — seat still 12A, gate changed to B42."
```

The approval happens entirely on the push service side, in the user's browser/app.
The bot is just a messenger that knows how to construct the refresh URL.

## Why This Is Safe

| Action | Who holds credentials? | Who approves? |
|--------|----------------------|---------------|
| Read cached data | Nobody (local read) | Nobody needed |
| Fetch public status | Nobody (public URL) | Nobody needed |
| Refresh private data | Push service | User (on push service UI) |
| Push new data to bot | Push service | Push service (automated) |

The bot never touches Delta, Hilton, FedEx, or the push service's refresh endpoint.
At worst, a compromised bot leaks data it already has cached. It can't escalate to
fetching new private data or acting on your accounts.

## Architecture

```
  Push Service (you deploy, you control, has your credentials)
       │
       │  Periodic push: POST /hooks/push
       │  (hooks token auth)
       │
       ▼
  ┌──────────────────────────────────────────────────────┐
  │                    OpenClaw Gateway                   │
  │                                                      │
  │  push_entries (SQLite)     memory/push/*.md (files)  │
  │  ┌─────────────────┐      ┌──────────────────────┐  │
  │  │ structured JSON  │      │ searchable text      │  │
  │  │ + refresh URLs   │      │ (auto-generated)     │  │
  │  │ + enrichment     │      │ indexed by memory    │  │
  │  │ + staleness      │      │ search pipeline      │  │
  │  └────────┬────────┘      └──────────┬───────────┘  │
  │           │                          │               │
  │           └──────────┬───────────────┘               │
  │                      ▼                               │
  │           ┌─────────────────────┐                    │
  │           │    Agent Runtime     │                    │
  │           │                     │                    │
  │           │ Tier 0: push_lookup │ → read local data  │
  │           │ Tier 1: web_fetch   │ → public URLs      │
  │           │ Tier 2: surface URL │ → user clicks      │
  │           └─────────────────────┘                    │
  └──────────────────────────────────────────────────────┘
```

## Push Entry Structure

Every push entry has three sections: **data** (structured fields), **access** (how the
bot and user can get fresh info), and **metadata** (TTL, staleness, tags).

```json
{
  "source": "flights",
  "key": "DL1234-2026-02-19",
  "schema_type": "flight",
  "category": "travel",

  "data": {
    "airline": "Delta",
    "flightNumber": "DL1234",
    "departure": {
      "airport": "SFO",
      "terminal": "2",
      "gate": "B34",
      "time": "2026-02-19T08:00:00-08:00"
    },
    "arrival": {
      "airport": "JFK",
      "terminal": "4",
      "time": "2026-02-19T16:30:00-05:00"
    },
    "confirmationCode": "ABC123",
    "seat": "12A",
    "status": "scheduled"
  },

  "access": {
    "public": [
      {
        "label": "Live flight status",
        "url": "https://flightaware.com/live/flight/DAL1234",
        "hint": "Check for current gate, delays, and arrival time"
      }
    ],
    "refresh": {
      "url_template": "https://my-flights.example.com/refresh/{key}",
      "label": "Refresh from Delta",
      "hint": "Updates seat, gate, confirmation, and booking details"
    }
  },

  "staleness": {
    "stale_after_seconds": 86400,
    "refresh_hint": "Your flight data is {age} old. Refresh from Delta?"
  },

  "tags": ["travel", "delta"],
  "ttl": 259200,
  "priority": "normal"
}
```

### `access.public` — Tier 1

URLs the bot can freely `web_fetch` with no approval. These are public pages that
don't require authentication.

### `access.refresh` — Tier 2

A URL template the bot surfaces to the user when data is stale. The `{key}` and
`{source}` placeholders get filled in. The user clicks the link, approves on the push
service, and the push service sends a fresh push.

The `url_template` can also be a full URL without placeholders — it's just a string
the bot presents to the user.

### `staleness` — When to offer refresh

- `stale_after_seconds`: After this many seconds since `updated_at`, the bot considers
  the data potentially outdated for volatile fields (seat, gate, status)
- `refresh_hint`: Template for what the bot says when offering a refresh link.
  `{age}` is replaced with human-readable age ("3 days", "2 hours")
- Factual data that doesn't change (confirmation code, flight number) is always
  served from Tier 0 regardless of staleness

## Storage

### SQLite Table (`push_entries`)

In the agent's memory database.

```sql
CREATE TABLE push_entries (
  id TEXT PRIMARY KEY,
  source TEXT NOT NULL,
  key TEXT NOT NULL,
  category TEXT,
  schema_type TEXT NOT NULL,
  data TEXT NOT NULL,               -- structured JSON (the real payload)
  access TEXT,                      -- JSON: { public: [...], refresh: {...} }
  staleness TEXT,                   -- JSON: { stale_after_seconds, refresh_hint }
  tags TEXT,                        -- JSON array of strings
  priority TEXT DEFAULT 'normal',
  created_at INTEGER NOT NULL,      -- unix ms
  updated_at INTEGER NOT NULL,      -- unix ms
  expires_at INTEGER,               -- unix ms, NULL = never
  UNIQUE(source, key)
);

CREATE INDEX idx_push_source ON push_entries(source);
CREATE INDEX idx_push_category ON push_entries(category);
CREATE INDEX idx_push_schema ON push_entries(schema_type);
CREATE INDEX idx_push_expires ON push_entries(expires_at);
```

### Memory Files

Auto-generated at `~/.openclaw/memory/push/{source}/{key}.md` for the existing
memory search pipeline to index.

Example:

```markdown
# Flight: Delta DL1234

- **Route**: SFO → JFK
- **Departure**: Feb 19, 2026 8:00 AM PST (Terminal 2, Gate B34)
- **Arrival**: Feb 19, 2026 4:30 PM EST (Terminal 4)
- **Confirmation**: ABC123
- **Seat**: 12A
- **Status**: Scheduled

Live status: https://flightaware.com/live/flight/DAL1234
```

No frontmatter needed — the structured data lives in SQLite. The markdown file exists
purely to make push data discoverable via `memory_search`.

## Schema Types

Built-in types with known fields. Unknown types accepted as `generic`.

### `flight`

```typescript
type FlightData = {
  airline: string;
  flightNumber: string;
  departure: {
    airport: string;
    terminal?: string;
    gate?: string;
    time: string;           // ISO 8601
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
  brand?: string;           // "Hilton", "Marriott"
  address?: string;
  city?: string;
  checkIn: string;          // ISO date or datetime
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
  carrier: string;           // "FedEx", "UPS", "USPS", "DHL"
  trackingNumber: string;
  description?: string;      // "MacBook Pro 16-inch"
  sender?: string;
  origin?: string;
  destination?: string;
  status?: "pre_transit" | "in_transit" | "out_for_delivery" | "delivered" | "exception";
  estimatedDelivery?: string;
  lastUpdate?: string;
  lastLocation?: string;
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

## Agent Tool: `memory_push_lookup`

Returns structured JSON with staleness and access info so the bot can make the right
decision about which tier to use.

```typescript
memory_push_lookup({
  source?: string,
  category?: string,
  schema_type?: string,
  key?: string,
  active?: boolean,       // only non-expired (default true)
})
```

Response includes staleness metadata so the bot reasons correctly:

```json
{
  "entries": [{
    "source": "flights",
    "key": "DL1234-2026-02-19",
    "schema_type": "flight",
    "data": { ... },
    "access": {
      "public": [{ "label": "Live status", "url": "https://flightaware.com/..." }],
      "refresh": {
        "url": "https://my-flights.example.com/refresh/DL1234-2026-02-19",
        "label": "Refresh from Delta"
      }
    },
    "age_seconds": 259200,
    "is_stale": true,
    "refresh_hint": "Your flight data is 3 days old. Refresh from Delta?",
    "expires_at": "2026-02-22T08:00:00.000Z"
  }],
  "total": 1
}
```

Tool description for the agent:

> Look up structured data pushed by external services (flights, hotels, packages).
> Returns typed fields you can read directly (e.g. `data.departure.gate`).
>
> **Tier 0** (always safe): Read any field from the returned data.
> **Tier 1** (public, free): Use `access.public` URLs with `web_fetch` for live status.
> **Tier 2** (user approval): If `is_stale` is true and `access.refresh` exists,
> present the refresh URL to the user so they can approve a data refresh.
> Never call the refresh URL yourself.

## API Endpoints

All under `/hooks/push`, using the existing hooks auth token.

### `POST /hooks/push` — Push entries

Upserts on `(source, key)`. The push service calls this periodically.

```json
{
  "entries": [{
    "source": "flights",
    "key": "DL1234-2026-02-19",
    "schema_type": "flight",
    "category": "travel",
    "data": { ... },
    "access": { ... },
    "staleness": { ... },
    "tags": ["travel"],
    "ttl": 259200
  }]
}
```

Response:
```json
{
  "ok": true,
  "accepted": 1,
  "results": [{
    "id": "...",
    "source": "flights",
    "key": "DL1234-2026-02-19",
    "status": "created" | "updated"
  }]
}
```

### `DELETE /hooks/push/{source}/{key}` — Remove entry

### `GET /hooks/push` — List entries

Query: `?source=flights&category=travel&active=true`

### `GET /hooks/push/{source}/{key}` — Get specific entry

## TTL Reaper

Runs on a configurable interval (default: 15 minutes):

1. Query entries where `expires_at IS NOT NULL AND expires_at < now()`
2. Delete expired rows from SQLite
3. Delete corresponding memory files
4. Memory sync pipeline cleans up stale index entries on next cycle

Simple. No webhook complexity — the push service knows its own TTLs.

## Configuration

In `openclaw.json` under `memory`:

```jsonc
{
  "memory": {
    "push": {
      "enabled": true,
      "allowedSources": [],           // empty = allow all
      "maxEntries": 1000,
      "maxEntryBytes": 102400,        // 100KB per entry
      "defaultTtlSeconds": 2592000,   // 30 days
      "reapIntervalMinutes": 15
    }
  }
}
```

## Implementation Files

### Phase 1: Core

| # | File | What |
|---|------|------|
| 1 | `src/config/types.memory.ts` | Add `MemoryPushConfig` type |
| 2 | `src/memory/push/schema.ts` | Zod schemas: push API, schema types, access, staleness |
| 3 | `src/memory/push/store.ts` | SQLite CRUD for `push_entries` |
| 4 | `src/memory/push/content-gen.ts` | Structured data → markdown text |
| 5 | `src/memory/push/file-sync.ts` | Write/delete managed memory files |
| 6 | `src/memory/push/reaper.ts` | TTL expiry cleanup |

### Phase 2: HTTP + Agent

| # | File | What |
|---|------|------|
| 7 | `src/gateway/push-memory-http.ts` | HTTP handlers for `/hooks/push` |
| 8 | `src/gateway/server-http.ts` | Register push handler in dispatch chain |
| 9 | `src/agents/tools/push-lookup-tool.ts` | `memory_push_lookup` agent tool |
| 10 | `src/memory/internal.ts` | Add push dir to memory file discovery |

### Phase 3: Registration

| # | File | What |
|---|------|------|
| 11 | `extensions/memory-core/index.ts` | Register push lookup tool |
| 12 | `src/plugins/runtime/index.ts` | Expose to plugin runtime |

## Example Flow

```
1. You deploy your flight push service:
   - It has your Delta credentials
   - It runs as a cron, pushes flight data every 6 hours
   - It has a web UI at my-flights.example.com with a "refresh now" button

2. Push service pushes structured data:
   POST /hooks/push { entries: [{ source: "flights", key: "DL1234-...", ... }] }

3. Conversation — Tier 0 (cached):
   You: "What's my confirmation code?"
   Bot: [memory_push_lookup → data.confirmationCode]
   Bot: "Your confirmation is ABC123."

4. Conversation — Tier 1 (public):
   You: "Is my flight on time?"
   Bot: [memory_push_lookup → access.public[0].url]
   Bot: [web_fetch on FlightAware]
   Bot: "DL1234 is on time, departing Gate B34."

5. Conversation — Tier 2 (refresh):
   You: "Did my seat change?"
   Bot: [memory_push_lookup → is_stale: true, 3 days old]
   Bot: "Your last pushed data shows seat 12A, but it's 3 days old."
   Bot: "Refresh from Delta → https://my-flights.example.com/refresh/DL1234-2026-02-19"
   User: *clicks link, approves on push service*
   Push service: *fetches from Delta, pushes fresh data*
   Bot: "Got the update — still seat 12A, no changes."
```

**The bot never touched Delta. The bot never called your push service.
The bot just knew what to read locally, what to fetch publicly, and when to hand you a link.**
