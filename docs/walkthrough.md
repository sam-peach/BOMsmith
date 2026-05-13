# Praxis — Architecture & Application Walkthrough

This document explains how Praxis works end-to-end: from a user uploading a PDF drawing to a completed, exportable Bill of Materials. It is written for anyone who needs to understand, maintain, or extend the codebase.

---

## Table of Contents

1. [High-level overview](#1-high-level-overview)
2. [Repository layout](#2-repository-layout)
3. [Authentication](#3-authentication)
4. [Request lifecycle — upload & analyse](#4-request-lifecycle--upload--analyse)
5. [Analysis pipeline](#5-analysis-pipeline)
6. [Data models](#6-data-models)
7. [Mapping system](#7-mapping-system)
8. [HTTP API reference](#8-http-api-reference)
9. [Frontend architecture](#9-frontend-architecture)
10. [Storage](#10-storage)
11. [Deployment architecture](#11-deployment-architecture)
12. [Development patterns](#12-development-patterns)

---

## 1. High-level overview

```
┌────────────────────���────────────────────────────────────────────┐
│  Browser                                                        │
│  React SPA (Vite / TypeScript)                                  │
│  • Login gate                                                   │
│  • Upload → Analyse → Review → Export flow                      │
└───────────────────────┬─────────────────────────────────────────┘
                        │  HTTPS  (cookie: sme_session)
                        ▼
┌─────────────────────────────────────────────────────────────────┐
│  Go HTTP server  (net/http, no framework)                       │
│                                                                 │
│  Public routes:   GET /healthz   POST /api/auth/login           │
│  Protected:       all other /api/* routes                       │
│                                                                 │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │  PostgreSQL: documents · mappings · part_catalog · …   │   │
│  └─────────────────────────────────────────────────────────┘   │
└───────────────────────┬─────────────────────────────────────────┘
                        │  HTTPS POST /v1/messages
                        ▼
              ┌─────────────────────┐
              │  Anthropic API      │
              │  claude-sonnet-4-6  │
              └─────────────────────┘
```

The entire application is a single binary. In production the Go server serves the compiled React bundle as static files from `./static`, so there is no separate frontend origin — everything is same-origin.

---

## 2. Repository layout

```
BOMsmith/
├── backend/
│   ├── main.go          Server wiring: env, stores, routes, CORS
│   ├── auth.go          Session store, login/logout handlers, requireAuth middleware
│   ├── handler.go       HTTP handlers (upload, analyse, get, exportCSV, saveBOM, mappings)
│   ├── analysis.go      Full analysis pipeline: PDF → text → LLM → BOMRows
│   ├── fingerprint.go   Part attribute extraction (type, material, standard, diameter, color)
│   ├── catalog.go       Part catalog: fingerprint scoring, suggestion pipeline, pg repository
│   ├── mock.go          Realistic mock BOM for development (no API key needed)
│   ├── mappings.go      Mapping repository: pg-backed CPN → IPN cross-references
│   ├── store.go         Document repository interface + pg implementation
│   ├── extract.go       PDF text extraction via ledongthuc/pdf
│   ├── types.go         Core structs: Document, BOMRow, Quantity, Mapping, CatalogPart, PartFingerprint
│   ├── *_test.go        TDD test files
│   ├── .env.example     Template for local environment variables
│   └── go.mod / go.sum
├── frontend/
│   └── src/
│       ├── App.tsx               Root: auth gate + main BOM workflow
│       ├── api/client.ts         Typed fetch wrappers for every API endpoint
│       ├── types/api.ts          TypeScript types mirroring Go structs
│       └── components/
│           ├── LoginPage.tsx     Sign-in form
│           ├── BomTable.tsx      Editable BOM table with flags, confidence, mapping save
│           ├── UploadArea.tsx    Drag-and-drop / click-to-upload PDF area
│           └── WarningsPanel.tsx Dismissible banner for analysis warnings
├── infra/
│   ├── main.tf                   ECR repository + App Runner service
│   ├── variables.tf              Input variables (region, app_name, secrets)
│   ├── outputs.tf                ECR URL + App Runner public URL
│   ├── deploy.sh                 Build → ECR push → App Runner redeploy
│   └── terraform.tfvars.example  Template for secret variables
├── Dockerfile                    Multi-stage: Node build → Go build → alpine runtime
└── CLAUDE.md                     Developer guidelines (TDD rules, stack, invariants)
```

---

## 3. Authentication

BOMsmith uses **server-side session tokens** stored in the `sessions` PostgreSQL table. There are no JWTs or third-party auth providers.

### Session store (`auth.go`)

```
sessionStore
  sessions  map[string]time.Time   token → expiry
  ttl       time.Duration          24 hours (set in main.go)
```

- `create()` — generates a 32-byte cryptographically random hex token, stores it with an expiry timestamp, returns the token
- `valid(token)` — looks up the token, deletes it if expired, returns `true` only if found and not expired
- `delete(token)` — removes the token immediately (used on logout)

### Login flow

```
POST /api/auth/login  { "username": "...", "password": "..." }
  │
  ├─ compare against AUTH_USERNAME / AUTH_PASSWORD env vars
  │  (wrong credentials → 401)
  │
  └─ sessions.create() → token
     Set-Cookie: sme_session=<token>; HttpOnly; SameSite=Lax; MaxAge=86400
     → 200 { "ok": true }
```

### requireAuth middleware

Every protected route is wrapped with `requireAuth`:

```go
func (s *server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        cookie, err := r.Cookie("sme_session")
        if err != nil || !s.sessions.valid(cookie.Value) {
            writeError(w, http.StatusUnauthorized, "unauthorized")
            return
        }
        next(w, r)
    }
}
```

The cookie is `HttpOnly` (inaccessible to JavaScript) and `SameSite=Lax` (sent on top-level navigations but not cross-site sub-requests).

### Frontend auth gate

On mount, `App.tsx` calls `GET /api/auth/me`. If the response is 200, the user is already authenticated (cookie was sent automatically). If it's 401, `LoginPage` is rendered instead of the main UI. After a successful login the cookie is set by the server and subsequent requests carry it automatically.

---

## 4. Request lifecycle — upload & analyse

The core user workflow involves two sequential HTTP calls:

### Step 1 — Upload (`POST /api/documents/upload`)

```
Browser
  │  multipart/form-data  field: "file"
  ▼
handler.upload()
  │
  ├─ validate: .pdf extension + "%PDF" magic bytes
  ├─ write file to ./uploads/<uuid>.pdf
  ├─ create Document{status: "uploaded"} in documentStore
  └─ return 201 Document JSON
```

The file is stored on the local filesystem (in `./uploads/`) and the document metadata is persisted to PostgreSQL via `pgDocumentStore`. The server generates a UUID for each document using `crypto/rand`.

### Step 2 — Analyse (`POST /api/documents/{id}/analyze`)

```
Browser
  ▼
handler.analyze()
  │
  ├─ set doc.Status = "analyzing"
  ├─ call analyzeDocument(doc, apiKey, mappingStore)
  │    └─ see Section 5: Analysis pipeline
  │
  ├─ on success: set doc.Status = "done", store BOMRows + Warnings
  ├─ on failure: set doc.Status = "error"
  └─ return 200/422 Document JSON
```

The server blocks on `analyzeDocument` — the Anthropic API call has a 5-minute timeout. The client shows a spinner and polls the UI state from the response.

---

## 5. Analysis pipeline

`analyzeDocument()` in `analysis.go` is the full pipeline. It runs in three stages:

```
PDF file
    │
    ▼  extractText()  [extract.go]
Text string
    │
    ▼  callAnthropic()
Raw JSON string from LLM
    │
    ▼  parseBOMRows()
[]BOMRow  +  []string warnings
```

### Stage 1 — PDF text extraction (`extract.go`)

Uses the `github.com/ledongthuc/pdf` library to read the text layer from the PDF content stream. Returns `("", nil)` for image-only/scanned PDFs (no text layer). A scanned drawing will produce an explicit error message instructing the user to provide a text-based PDF.

> **Future work:** An OCR fallback using Tesseract is stubbed out as a TODO comment.

### Stage 2 — LLM call (`callAnthropic`)

The full extracted text is sent to `claude-sonnet-4-6` via the Anthropic Messages API. The system prompt is carefully engineered for the drawing format used by this manufacturer:

- **Sheet 1** — schematic (wire routing, connectors, terminals)
- **Sheet 2** — physical layout (wire lengths in mm)
- **Sheet 3** — Part Reference, Cable Type, Heatshrink/Sleeve reference tables

The prompt instructs the model to output a **single JSON array** with no markdown fences. Each element has these fields:

| Field | Description |
|-------|-------------|
| `rawLabel` | Label as it appears on the drawing (e.g. `"HS2"`, `"1"`) |
| `description` | Engineering description |
| `rawQuantity` | Quantity **exactly** as written — never transformed |
| `unit` | Canonical unit: `"EA"` or `"M"` |
| `customerPartNumber` | Customer's part number (usually `""` for wiring harnesses) |
| `manufacturerPartNumber` | From the Part Reference table |
| `supplierReference` | RS or Farnell distributor code |
| `notes` | Anything worth flagging |
| `confidence` | 0.0–1.0 |
| `flags` | Array: `needs-review`, `low-confidence`, `ambiguous-spec`, `dimension-estimated`, `missing-manufacturer-pn` |

### Stage 3 — Post-processing (`parseBOMRows`)

After JSON parsing, every row goes through a five-step pipeline:

#### a) `parseQuantity(rawStr, declaredUnit)`

Parses the raw quantity string using a regex (`(\d+(?:\.\d+)?)([a-z]+)?`):

- If the inline unit (e.g. `mm` in `"150mm"`) conflicts with the LLM's declared canonical unit (e.g. `M`), the flag `unit_ambiguous` is set and **neither value is silently changed** — `Quantity.Raw` is always preserved verbatim.
- `Quantity.Normalized` is set equal to `Quantity.Value` (no unit conversion is performed — this is intentional; SAP handles normalisation).

#### b) `detectSupplier(row)`

Classifies the `SupplierReference` field using regex patterns:
- RS Components: `NNN-NNNN` or 7-digit plain
- Farnell: 7-digit optionally followed by one letter
- Anything else → `"Unknown"`

Sets `row.Supplier` and adds the `supplier_reference_detected` flag.

#### c) `enrichFromSupplierRef(row)`

If a supplier reference exists but no manufacturer part number was found, a placeholder MPN is derived: `"MPN-" + supplierReference`. This is marked `low_confidence` and noted for verification.

> **Future work:** Replace with a real RS/Farnell API lookup.

#### d) `applyMapping(row, mappingReader)`

Checks the mappings table for a known cross-reference keyed on `customerPartNumber` (or `manufacturerPartNumber` when CPN is absent), case-insensitive. If found, fills in `InternalPartNumber` and/or `ManufacturerPartNumber` from the stored mapping. Any field filled by a mapping is added to `BOMRow.ConfirmedFields` — a stored mapping is a prior human declaration, so fields it supplies are **Confirmed**, not Suggested. `LastUsedAt` is updated asynchronously (fire-and-forget goroutine).

#### e) `suggestFromCatalog(row, catalog)` — only runs when `InternalPartNumber` is still empty

Queries the part catalog for a match in two stages:

1. **Exact MPN** — if `ManufacturerPartNumber` is set, looks for a catalog entry with that MPN. Score 1.0.
2. **Fingerprint match** — `buildFingerprint(description)` extracts structured attributes (type, material, standard, diameter, color) using rule-based regexps. Candidates of the same part type are fetched and scored with `scoreFingerprint`. Type and diameter mismatches are fatal (return 0); standard, color, material mismatches reduce the score but do not eliminate the candidate. Only attributes present on **both** sides are scored.

Match treatment depends on the source:

- **Exact-MPN match** (`s.Source == "exact_mpn"`) is an identity match — the MPN is a globally unique part identifier and every catalog entry traces back to a stored mapping, which itself traces to a human declaration. Treated the same as an `applyMapping` hit: `InternalPartNumber` is filled and `"internalPartNumber"` is added to `ConfirmedFields`. The MPN cell is also marked Confirmed whenever the row has a value (whether pre-populated by the LLM from the Part Reference Table or filled now from the catalog) — the catalog hit by-definition validates the MPN value, so leaving it as a Suggested cell would force a redundant operator click.
- **Fingerprint match** (`s.Source == "fingerprint"`) is a similarity match — the system is inferring identity, not confirming it. Attached to `BOMRow.Suggestion` and surfaced for operator review; never fills `InternalPartNumber` silently.

The fail-safe principle is preserved: only humans confirm. The exact-MPN promotion isn't the system confirming itself — it's the system recognising that the value already traces to a prior human declaration via the catalog → mapping chain.

The catalog is populated whenever a mapping is saved (`POST /api/mappings` or auto-learn in `PUT /api/documents/{id}/bom`).

#### Final flag promotion

Any flags set on the `Quantity` struct (e.g. `unit_ambiguous`) are copied up to the `BOMRow.Flags` slice so the frontend can tint the entire row.

### Mock mode

When `ANTHROPIC_API_KEY` is empty, `mockAnalysis()` in `mock.go` is called instead. It builds a realistic six-row cable assembly BOM covering all flag types:

| Row | Exercises |
|-----|-----------|
| Row 1 | Clean row, high confidence |
| Row 2 | RS supplier reference, no MPN → `enrichFromSupplierRef` |
| Row 3 | Unit conflict (`150mm` vs `M`) → `unit_ambiguous` |
| Row 4 | Dimension estimated from layout → `dimension-estimated` |
| Row 5 | Customer part number → `applyMapping` |
| Row 6 | No MPN, low confidence → `missing_part_number`, `needs-review` |

Critically, `mockAnalysis` serialises the rows to JSON and calls `parseBOMRows` on them — so all post-processing logic runs identically to the real pipeline.

---

## 6. Data models

### `Document` (types.go)

```
Document
  ID          string           — UUID (crypto/rand)
  Filename    string           — original filename from upload
  FilePath    string           — server-side only (not serialised to JSON)
  Status      DocumentStatus   — "uploaded" | "analyzing" | "done" | "error"
  UploadedAt  time.Time
  BOMRows     []BOMRow
  Warnings    []string
  ClientLabel string           — optional client tag; scopes mapping lookups during analysis
```

### `BOMRow` (types.go)

```
BOMRow
  ID                      string          — "row-N" (sequential, reset on each analysis)
  LineNumber              int
  RawLabel                string          — verbatim from drawing
  Description             string
  Quantity                Quantity
  CustomerPartNumber      string
  InternalPartNumber      string          — filled by mapping or user edit (never by catalog match)
  ManufacturerPartNumber  string
  SupplierReference       string          — RS/Farnell order code
  Supplier                string          — "RS" | "Farnell" | "Unknown" | ""
  Notes                   string
  Confidence              float64         — 0.0–1.0
  Flags                   []string
  Suggestion              *PartSuggestion — non-nil when the catalog has a match awaiting operator review
  ConfirmedFields         []string        — JSON field names ("customerPartNumber" etc.) that
                                            a human has declared. Cells not in this list with a
                                            value are system Suggestions awaiting confirmation.
```

### `Quantity` (types.go)

```
Quantity
  Raw         string     — NEVER modified after extraction; source of truth
  Value       *float64   — parsed numeric value
  Unit        *string    — resolved unit string
  Normalized  *float64   — currently equals Value (no conversion)
  Flags       []string   — e.g. ["unit_ambiguous"]
```

**Key invariant:** `Quantity.Raw` is set once during `parseQuantity` and never overwritten. All downstream logic operates on `Value`/`Unit`. If the user edits `Raw` in the UI, `parseQuantity` would need to be re-run (currently a manual operation — editing `Value`/`Unit` directly is the intended correction path).

### `ClientMappingSummary` (mappings.go)

```
ClientMappingSummary
  Label  string  — client label ("" = generic / pooled bucket)
  Count  int     — number of mappings stored under this label in the org
```

Returned by `GET /api/mappings/clients` and used by the UI to populate the
client-tag datalist and the Settings page client-mappings list.

### `Mapping` (types.go)

```
Mapping
  ID                      string
  ClientLabel             string    — "" = generic / pooled bucket; otherwise scopes to a specific client
  CustomerPartNumber      string    — lookup key (stored upper-cased)
  InternalPartNumber      string
  ManufacturerPartNumber  string
  Description             string
  Source                  string    — "manual" | "inferred" | "csv-upload" | "excel-import"
  Confidence              float64
  LastUsedAt              time.Time
  CreatedAt               time.Time
  UpdatedAt               time.Time
```

Mappings are stored with a unique key of `(org_id, client_label, customer_part_number)`.
Two clients of the same org can use the same CPN for unrelated parts without
collision — each lives in its own bucket. An empty `ClientLabel` means the
mapping is in the generic / pooled bucket and is visible as a fallback to
every drawing.

### `CatalogPart` (types.go)

Canonical part entry used by the fingerprint-based suggestion engine. Distinct from `Mapping`: mappings are keyed by customer part number; catalog parts are matched by structured attributes derived from the description.

### Pricing (types.go, pricing.go)

```
RowPricing
  Offers             []SupplierOffer
  BestUnitPrice      *Money          — picked from the cheapest break ≤ row qty
  BestStockSupplier  string          — supplier with the highest in-stock count
  FetchedAt          time.Time       — newest FetchedAt across the offer set

SupplierOffer
  Supplier      string       — "DigiKey" | "Mouser" | "Farnell" | "RS" | …
  SKU           string       — supplier order code
  PriceBreaks   []PriceBreak — ascending by quantity
  Stock         *int
  LeadTimeDays  *int
  SupplierURL   string       — click-through to product page
  Source        string       — "nexar" | "csv" | "manual" | "mock"
  Currency      string       — ISO 4217 (always equals the run's currency)
  FetchedAt     time.Time

PricingRun
  ID                string
  DocumentID        string
  StartedAt         time.Time
  CompletedAt       *time.Time
  RowsTotal         int    — total rows on the BOM at run time
  RowsPriced        int    — rows that got at least one offer
  RowsUnavailable   int    — rows with an MPN but no offers anywhere
  RowsSkipped       int    — rows with no MPN
  NexarCallsMade    int    — cache misses that hit the provider
  CacheHits         int    — RowsPriced - NexarCallsMade
  Currency          string
  ErrorMessage      string — populated on 502/transport failure
```

**Key invariants:**

- `Pricing` is never persisted on a `BOMRow` — `documents.bom_rows` JSON stays pure operator-confirmed content. It is joined at read time from `part_prices` keyed on `(mpn, currency)`.
- `part_prices` is **not** org-scoped. Two orgs querying the same MPN share the cache; pricing is commodity data, mapping is the org-scoped layer.
- Cache TTL is 24h by default (`PRICING_CACHE_TTL_HOURS`). Stale rows are treated as misses, not silently returned.
- `pricing_unavailable` is set on a row only after a run finds no offers from any source — never on rows skipped for missing MPN.

```
CatalogPart
  ID                      string
  InternalPartNumber      string          — canonical IPN for this part
  ManufacturerPartNumber  string
  Description             string
  Fingerprint             PartFingerprint — structured attributes
  UsageCount              int             — incremented on each successful match
  LastUsedAt              time.Time
  CreatedAt / UpdatedAt   time.Time
```

### `PartFingerprint` (types.go)

Extracted by `buildFingerprint(description string)` in `fingerprint.go`. All fields lowercase. Empty string = attribute not detected.

```
PartFingerprint
  Type      string   — "wire" | "connector" | "heatshrink" | "ferrule" | "fuse" | ...
  Material  string   — "pvc" | "ptfe" | "xlpe" | "silicone" | "lszh" | ...
  Standard  string   — "bs4808" | "ul1015" | "iec60228" | ...
  Diameter  string   — "0.20mm" | "0.50mm²" | "16awg"
  Color     string   — "blue" | "red" | "black" | ...
```

### `PartSuggestion` (types.go)

Attached to `BOMRow.Suggestion` when the catalog matched at medium confidence (0.50–0.89). Auto-accepted matches (≥0.90) fill `InternalPartNumber` directly and do not set this field.

```
PartSuggestion
  CatalogPartID          string
  InternalPartNumber     string
  ManufacturerPartNumber string
  Score                  float64   — 0.0–1.0
  Source                 string    — "exact_mpn" | "fingerprint"
  MatchReasons           []string  — human-readable attribute matches
```

---

## 7. Mapping system

The mapping system cross-references a **customer part number** (as it appears on the drawing) with the **internal part number** used in-house and the **manufacturer part number** for procurement.

### Storage

Mappings are persisted in the `mappings` PostgreSQL table, keyed by `(organization_id, client_label, customer_part_number)` where `customer_part_number` is stored upper-cased and `client_label` is normalised to lower-case for the unique key. All lookups normalise the key with `normKey()` (CPN) and `normClientLabel()` (client) before querying.

An empty `client_label` is the **generic / pooled bucket**: mappings here are visible as a fallback to drawings tagged with any client.

### Creating a mapping

There are four paths:

1. **Manual** — user clicks the `↗` button on a BOM row in the UI. The row's `customerPartNumber`, `internalPartNumber`, and `manufacturerPartNumber` are POSTed to `POST /api/mappings`. The mapping inherits the drawing's `clientLabel`.

2. **CSV bulk import** — `POST /api/mappings/upload` accepts a CSV with headers `CustomerPartNumber`, `InternalPartNumber`, `ManufacturerPartNumber`, `Description`. Column matching is case-insensitive. Optional `clientLabel` form field scopes the import.

3. **Excel import (per-client)** — `POST /api/mappings/import` accepts a JSON body `{ clientLabel, rows: [...] }`. The frontend parses `.xlsx` client-side via SheetJS (header-synonym matching: "Customer P/N", "Customer Part Number", "Cust PN" all map to `customerPartNumber`) and POSTs the canonical rows here. Existing entries in the same `(org, client, cpn)` bucket are overwritten — the client just supplied the file, so it's the authoritative state. The response returns `{ saved, overwritten, skipped }` so operators can see what changed.

4. **Auto-learn from confirmed rows** — `PUT /api/documents/{id}/bom` inspects each saved row and persists an `inferred` mapping only when `"internalPartNumber"` is present in `ConfirmedFields`. The mapping inherits the drawing's `clientLabel`. Operator-curated mappings (`manual`, `csv-upload`, `excel-import`) in the same client bucket are never overwritten by an auto-learn.

### Applying a mapping

During `parseBOMRows`, `applyMapping` queries via `orgScopedMappings` which is constructed per-document with both `orgID` and the drawing's `clientLabel`. Lookup is two-tier:

1. If the drawing has a `clientLabel`, look up `(org, client_label, cpn)` first.
2. If no hit (or no client tag), fall back to `(org, "", cpn)` — the generic bucket.

If a match exists at either tier:
- `InternalPartNumber` is filled (if currently empty) and `"internalPartNumber"` is appended to `ConfirmedFields`
- `ManufacturerPartNumber` is filled (if currently empty) and `"manufacturerPartNumber"` is appended to `ConfirmedFields`
- The `mapping_applied` flag is added
- `LastUsedAt` is updated in a background goroutine

A stored mapping represents a prior human declaration (manual save, CSV/Excel import, or auto-learn from a confirmed row), so values it supplies are treated as Confirmed.

CPN lookup is case-insensitive (`normKey` uppercases the input); client-label matching is case-insensitive + whitespace-trimmed (`normClientLabel`).

`touchLastUsed` is scoped by `(org, client_label, cpn)` — touching one bucket must not bump another's `last_used_at`, otherwise cross-client CPN collisions wreck the search ranking. Search and suggest queries escape LIKE metacharacters (`%`, `_`, `\`) before building the pattern and use `ESCAPE '\'`, so customer P/Ns with underscores (e.g. `CBL_RED_035`) are matched literally.

### On-demand mapping search

Operators can interrogate the mapping store directly via the search bar in the top nav (component: `MappingSearch.tsx`). The frontend calls `GET /api/mappings/search?q=...&limit=50` which substring-matches across `customer_part_number`, `internal_part_number`, `manufacturer_part_number`, and `description` and returns up to 50 mappings ordered by `last_used_at DESC`. The same MPN under multiple client buckets returns as multiple rows so the operator sees each client's distinct interpretation.

Results are grouped client-side by `(clientLabel, internalPartNumber)`: when many customer P/Ns share an IPN (color variants of a generic wire, say), they collapse into a single result row with the shared IPN as the headline and the differentiating CPN/MPN values listed as nested variants under a left rail. Single-variant groups render flat (no nesting). IPN-less mappings each form their own singleton group — pooling them by empty key would mash unrelated parts together. The header tally shows `N mappings across M internal P/Ns` when grouping collapses the count.

Result rows lead with the **internal P/N** as the headline answer — that's what Andrew uses next (paste into SAP). Customer P/N and Mfr P/N are demoted to the footer. Each P/N value is independently click-to-copy.

Keyboard navigation: ↑/↓ moves the highlight between groups; Enter copies the highlighted group's internal P/N; Esc closes the dropdown. The debounce + a ref-counter guard prevent stale responses from clobbering newer queries.

### Mappings management page

The full maintenance surface lives at `/mappings` (component: `MappingsPage.tsx`). It lists every mapping in the org with free-text search, client filter, and source filter. Inline edit is allowed on `internalPartNumber`, `manufacturerPartNumber`, and `description`; the primary key (`clientLabel` + `customerPartNumber`) is read-only and must be changed via delete-and-recreate. Each row has a Delete button (with a confirmation prompt) that calls `DELETE /api/mappings/{id}`. Edits use the existing `POST /api/mappings` upsert path.

Addresses Andrew's 2026-04-15 request for "some view... to be able to upload current x-ref sheet and make corrections if errors are introduced" — the upload half is the Settings page client-mappings import; this page is the corrections half.

### Part catalog (`catalog.go`)

For rows where no exact mapping is found, a second lookup runs against the `part_catalog` table using `suggestFromCatalog`. See §5e for the full scoring logic.

The catalog is populated automatically whenever a mapping is saved (manual or auto-learn). `upsertCatalogFromMapping` builds a `PartFingerprint` from the mapping's `Description` and upserts a `CatalogPart` record keyed on `internal_part_number`. Over time, the catalog accumulates fingerprints for every known IPN, enabling cross-drawing part matching even for parts with no customer part number.

---

## 8. HTTP API reference

All routes except `/healthz` and `/api/auth/login` require a valid `sme_session` cookie.

### Auth

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/auth/login` | Authenticate. Body: `{"username":"...","password":"..."}`. Sets `sme_session` cookie. |
| `POST` | `/api/auth/logout` | Invalidate session. Clears cookie. |
| `GET` | `/api/auth/me` | Returns `{"ok":true}` if session is valid; 401 otherwise. Used by frontend on load. |

### Documents

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/documents/healthz` | Health check (public). Returns 200. |
| `POST` | `/api/documents/upload` | Upload a PDF. Multipart `file` field, optional `clientLabel` field. Returns `Document`. |
| `POST` | `/api/documents/{id}/analyze` | Trigger analysis. Returns updated `Document`. |
| `GET` | `/api/documents/{id}` | Fetch document by ID. Decorates each row with cached pricing (when present) and `lastPricingRun` from `pricing_runs`. |
| `POST` | `/api/documents/{id}/price` | Run pricing for every BOM row with a non-empty MPN. Cache-first, falls through to the configured `pricingProvider`. Returns the decorated `Document` plus `lastPricingRun`. 503 when no provider is configured, 502 on upstream transport failure. |
| `PATCH` | `/api/documents/{id}` | Update mutable fields. Body: `{"clientLabel":"..."}` to retag a drawing. |
| `PUT` | `/api/documents/{id}/bom` | Save edited BOM rows. Body: `[]BOMRow`. |
| `GET` | `/api/documents/{id}/bom.csv` | Download BOM as SAP-compatible CSV. |

**CSV column order:** Line, Description, Quantity (raw), Unit, Customer P/N, Internal P/N, Manufacturer P/N, Notes.

### Mappings

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/mappings` | List all stored mappings. |
| `GET` | `/api/mappings/search` | On-demand operator-facing mapping search. Params: `q` (required substring across CPN/IPN/MPN/description, case-insensitive), `client` (optional exact-match bucket filter), `limit` (default 20, max 100). Returns `Mapping[]` ordered by `last_used_at DESC`. Empty/whitespace `q` returns `[]`. |
| `GET` | `/api/mappings/suggest` | Tight typeahead for the BomTable in-cell `?` popover. Matches description + CPN only. Limit 5. Result rows include `clientLabel` so the popover can disambiguate same-CPN-different-client hits. |
| `GET` | `/api/mappings/clients` | List distinct client labels in the org with mapping counts. Returns `ClientMappingSummary[]`. |
| `POST` | `/api/mappings` | Create or update a single mapping. Body: `Mapping`. |
| `DELETE` | `/api/mappings/{id}` | Remove a stored mapping. Org-scoped. 204 on success, 404 if no match for this org. |
| `POST` | `/api/mappings/upload` | Bulk import from CSV. Multipart `file` field, optional `clientLabel`. Returns `{"saved":N,"skipped":N}`. |
| `POST` | `/api/mappings/import` | Structured per-client import (frontend parses Excel client-side). Body: `{"clientLabel":"...","rows":[...]}`. Returns `{"saved":N,"overwritten":N,"skipped":N}`. |

### Error format

All errors return JSON: `{"error": "message"}` with an appropriate HTTP status code.

---

## 9. Frontend architecture

The frontend is a single-page React app (no routing library). State is held entirely in `App.tsx` — there is no global state manager.

### Auth state gate

```
App mounts
    │
    ▼ checkAuth() → GET /api/auth/me
    │
    ├─ 200 → authed = true  → render main BOM UI
    ├─ 401 → authed = false → render <LoginPage>
    └─ (pending) → authed = null → render nothing (brief flash prevention)
```

### Main workflow state

```
App.tsx state
  doc       Document | null     — current document
  rows      BOMRow[]            — live-editable copy of doc.bomRows
  uploading bool
  analyzing bool
  saved     bool
  error     string | null
```

`rows` is kept as a separate array from `doc.bomRows` so the user can make edits without immediately triggering a save. The `Save Changes` button calls `PUT /api/documents/{id}/bom` to persist the current `rows` state.

### Component breakdown

| Component | Responsibility |
|-----------|---------------|
| `LoginPage` | Sign-in form; calls `onLogin(username, password)` prop |
| `UploadArea` | Drag-and-drop or click-to-select PDF; validates `.pdf` extension client-side |
| `BomTable` | Editable table; each cell is an `<input>`; cell-level visual state for confirmed vs system-suggested cross-reference fields. The "Best Price" column renders `PricingCell` — click-through to the best-stock supplier's URL. |
| `PriceBOMButton` (inline in `App.tsx`) | Workflow-page toolbar button that POSTs to `/api/documents/{id}/price`. Disabled when no row carries a non-empty MPN; flips label to "Re-price BOM" once a `lastPricingRun` exists; shows a compact `N/M priced · K unavailable` summary inline. |
| `MappingSearch` | Top-nav search bar; debounced on-demand mapping lookup with click-to-copy per P/N field |
| `MappingsPage` | Full mappings management page at `/mappings`; browse / filter / inline-edit / delete |
| `WarningsPanel` | Dismissible warning banners surfaced from `doc.warnings` |

### BomTable internals

Each `BomRow` renders a row of `<input>` elements. Changes call back to `BomTable` via:
- `onUpdate(index, field, value)` — for top-level `BOMRow` fields
- `onUpdateQty(index, field, value)` — for nested `Quantity` fields
- `onConfirmCell(index, field)` — confirm a single cross-reference cell as-is
- `onConfirmRow(index)` — confirm every system suggestion on a row

The `↗` (save mapping) button is only shown when `customerPartNumber` is non-empty. It fires `onSaveMapping` which calls `POST /api/mappings`.

#### Cell state (cross-reference fields)

The cross-reference fields — `customerPartNumber`, `internalPartNumber`, `manufacturerPartNumber` — each have one of three visual states, derived from `BOMRow.confirmedFields`:

| State | Means | Rendering |
|-------|-------|-----------|
| Empty | Cell has no value | Empty `<input>` |
| Suggested | Cell has a value the system filled in but no human has confirmed | Italic, amber background, click-to-confirm tick `✓` next to the input |
| Confirmed | Operator typed/clicked the value, OR a stored mapping supplied it | Plain `<input>` |

Editing a cross-reference cell auto-confirms it on blur (typing the value is itself an explicit human declaration). Clearing a cell drops it back to Empty. The table toolbar shows a `Confirm all suggestions (n)` button when any suggestions exist, and an export-area counter (`⚠ n cells need review`) keeps the count visible at point of export.

Row background tinting is now reserved for quantity ambiguity (`unit_ambiguous`) — confidence and missing-PN signals are expressed per-cell instead.

### API client (`api/client.ts`)

All API calls are centralised in `client.ts` as typed async functions. Every function calls `parseError(res)` on non-OK responses to extract the `{"error":"..."}` message from the server before throwing. Auth functions: `checkAuth`, `login`, `logout`.

---

## 10. Storage

### PostgreSQL (`store.go`, `mappings.go`, `catalog.go`)

All application state is persisted in PostgreSQL. `DATABASE_URL` is required at startup; the server will not start without it. On startup, `runMigrations` applies any pending SQL migration files from `backend/migrations/` in order.

| Table | Purpose |
|-------|---------|
| `documents` | Document metadata + BOM rows (stored as JSONB) |
| `mappings` | CPN → IPN/MPN cross-references |
| `part_catalog` | Canonical parts with structured fingerprints for fuzzy matching |
| `sessions` | Server-side session tokens |
| `organizations` / `users` | Multi-tenancy |

Uploaded PDF files are stored on the local filesystem in `./uploads/`. On App Runner these are ephemeral (lost on restart); for production use, mount an EFS volume or push files to S3.

---

## 11. Deployment architecture

```
┌──────────────────────────────────────────────────────────────┐
│  AWS                                                         │
│                                                              │
│  ECR Repository                                              │
│    └─ bomsmith:latest  (linux/amd64 Docker image)            │
│                                                              │
│  App Runner Service                                          │
│    ├─ Pulls from ECR on deployment                           │
│    ├─ 0.25 vCPU / 0.5 GB RAM                                 │
│    ├─ Port 8080                                              │
│    ├─ Auto-TLS, public HTTPS URL                             │
│    └─ Env vars: ANTHROPIC_API_KEY, AUTH_USERNAME, AUTH_PASSWORD │
└──────────────────────────────────────────────────────────────┘
```

### Docker image (multi-stage)

1. **Stage 1** (`node:20-alpine`) — `npm ci && npm run build` → `frontend/dist/`
2. **Stage 2** (`golang:1.24-alpine`) — `go build -o bomsmith` → single binary
3. **Stage 3** (`alpine:3.20`) — copies binary + `frontend/dist/` as `./static`

At runtime, Go serves the React bundle as static files. The API and frontend share the same origin — no CORS issues in production.

### Deploy script (`infra/deploy.sh`)

```bash
aws ecr get-login-password | docker login ...           # authenticate to ECR
docker buildx build --platform linux/amd64 ...          # build for x86_64
docker tag ... && docker push ...                        # push to ECR
aws apprunner start-deployment --service-arn ...         # trigger redeploy
```

App Runner uses `GET /api/documents/healthz` as its health check (1-second timeout, 10-second interval).

### Infrastructure as code

All AWS resources are defined in `infra/main.tf`:
- `aws_ecr_repository` — stores Docker images; lifecycle policy keeps the last 5
- `aws_iam_role` + `aws_iam_role_policy_attachment` — grants App Runner permission to pull from ECR
- `aws_apprunner_service` — the running service

Sensitive values (`anthropic_api_key`, `auth_username`, `auth_password`) live in `terraform.tfvars` (gitignored) and are passed as `runtime_environment_variables` to the container.

---

## 12. Development patterns

### Test-driven development

All backend features are written test-first. The mandatory order is:

1. Write a `_test.go` file with tests that describe the desired behaviour
2. Run `go test ./...` — confirm they fail (compile errors count as failure)
3. Write implementation until the tests pass

Tests use `testify` for assertions and `net/http/httptest` for handler tests.

### Adding a new API endpoint

1. Write the handler test in `handler_test.go` (or a new `*_test.go`)
2. Add the handler method to `handler.go`
3. Register the route in `main.go`, wrapped in `srv.requireAuth(...)` if it should be protected
4. Update `frontend/src/api/client.ts` with a typed wrapper function
5. Update `frontend/src/types/api.ts` if the response shape changed
6. Update this walkthrough

### Adding a new flag type

1. Add the flag string as a constant or inline in `analysis.go`
2. Add a `FLAG_CONFIG` entry in `BomTable.tsx` with label and colours
3. Add a test in `analysis_test.go` verifying the flag is set correctly

### Extending the analysis prompt

The system prompt lives in the `callAnthropic` function in `analysis.go`. Changes to extraction logic should be reflected in `mockAnalysis` in `mock.go` — the mock is the integration test for the full post-processing pipeline.

### Environment variable reference

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `DATABASE_URL` | Yes | — | PostgreSQL connection string |
| `AUTH_USERNAME` | Yes | — | Seed admin login username |
| `AUTH_PASSWORD` | Yes | — | Seed admin login password |
| `ANTHROPIC_API_KEY` | No | — | Omit to use mock data |
| `PORT` | No | `8080` | HTTP listen port |
| `STATIC_DIR` | No | `./static` | Directory for compiled frontend |
| `CORS_ORIGIN` | No | `*` | Value for `Access-Control-Allow-Origin` |
| `MATCH_SCORE_THRESHOLD` | No | `0.15` | Minimum score for similar-document suggestions |
