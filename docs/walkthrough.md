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
│  ┌──────────────────┐  ┌───────────────────┐                   │
│  │  documentStore   │  │   mappingStore    │                   │
│  │  (in-memory map) │  │  (memory + JSON)  │                   │
│  └──────────────────┘  └───────────────────┘                   │
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
praxis/
├── backend/
│   ├── main.go          Server wiring: env, stores, routes, CORS
│   ├── auth.go          Session store, login/logout handlers, requireAuth middleware
│   ├── handler.go       HTTP handlers (upload, analyse, get, exportCSV, saveBOM, mappings)
│   ├── analysis.go      Full analysis pipeline: PDF → text → LLM → BOMRows
│   ├── mock.go          Realistic mock BOM for development (no API key needed)
│   ├── mappings.go      mappingStore: in-memory + JSON-backed part number cross-references
│   ├── store.go         documentStore: in-memory map of documents
│   ├── extract.go       PDF text extraction via ledongthuc/pdf
│   ├── types.go         Core structs: Document, BOMRow, Quantity, Mapping, AnalysisResult
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

Praxis uses **server-side session tokens** stored in an in-memory map. There are no JWTs or third-party auth providers.

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

The file is stored on the local filesystem (in `./uploads/`) and the document metadata lives in the in-memory `documentStore`. The server generates a UUID for each document using `crypto/rand`.

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

After JSON parsing, every row goes through a four-step pipeline:

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

#### d) `applyMapping(row, mappingStore)`

Checks `mappingStore` for a known cross-reference keyed on `customerPartNumber` (case-insensitive). If found, fills in `InternalPartNumber` and/or `ManufacturerPartNumber` from the stored mapping. `LastUsedAt` is updated asynchronously (fire-and-forget goroutine).

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
```

### `BOMRow` (types.go)

```
BOMRow
  ID                      string     — "row-N" (sequential, reset on each analysis)
  LineNumber              int
  RawLabel                string     — verbatim from drawing
  Description             string
  Quantity                Quantity
  CustomerPartNumber      string
  InternalPartNumber      string     — filled by mappingStore or user edit
  ManufacturerPartNumber  string
  SupplierReference       string     — RS/Farnell order code
  Supplier                string     — "RS" | "Farnell" | "Unknown" | ""
  Notes                   string
  Confidence              float64    — 0.0–1.0
  Flags                   []string
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

### `Mapping` (types.go)

```
Mapping
  ID                      string
  CustomerPartNumber      string    — lookup key (stored upper-cased)
  InternalPartNumber      string
  ManufacturerPartNumber  string
  Description             string
  Source                  string    — "manual" | "inferred" | "csv-upload"
  Confidence              float64
  LastUsedAt              time.Time
  CreatedAt               time.Time
  UpdatedAt               time.Time
```

---

## 7. Mapping system

The mapping system cross-references a **customer part number** (as it appears on the drawing) with the **internal part number** used in-house and the **manufacturer part number** for procurement.

### Storage (`mappings.go`)

```
mappingStore
  data      map[string]*Mapping   keyed by normKey(customerPartNumber) = strings.ToUpper(trimmed)
  filePath  string                path to mappings.json
```

All reads use a `sync.RWMutex`. Writes call `persist()` which writes to a `.tmp` file and renames it atomically — protecting against corruption from a mid-write crash.

### Creating a mapping

There are two paths:

1. **Manual** — user clicks the `↗` button on a BOM row in the UI. The row's `customerPartNumber`, `internalPartNumber`, and `manufacturerPartNumber` are POSTed to `POST /api/mappings`.

2. **CSV bulk import** — `POST /api/mappings/upload` accepts a CSV with headers `CustomerPartNumber`, `InternalPartNumber`, `ManufacturerPartNumber`, `Description`. Column matching is case-insensitive.

### Applying a mapping

During `parseBOMRows`, `applyMapping` looks up each row's `customerPartNumber`. If a match exists:
- `InternalPartNumber` is filled (if currently empty)
- `ManufacturerPartNumber` is filled (if currently empty)
- The `mapping_applied` flag is added
- `LastUsedAt` is updated in a background goroutine

Lookup is case-insensitive (`normKey` uppercases the input before lookup).

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
| `POST` | `/api/documents/upload` | Upload a PDF. Multipart `file` field. Returns `Document`. |
| `POST` | `/api/documents/{id}/analyze` | Trigger analysis. Returns updated `Document`. |
| `GET` | `/api/documents/{id}` | Fetch document by ID. |
| `PUT` | `/api/documents/{id}/bom` | Save edited BOM rows. Body: `[]BOMRow`. |
| `GET` | `/api/documents/{id}/bom.csv` | Download BOM as SAP-compatible CSV. |

**CSV column order:** Line, Description, Quantity (raw), Unit, Customer P/N, Internal P/N, Manufacturer P/N, Notes.

### Mappings

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/mappings` | List all stored mappings. |
| `POST` | `/api/mappings` | Create or update a single mapping. Body: `Mapping`. |
| `POST` | `/api/mappings/upload` | Bulk import from CSV. Multipart `file` field. Returns `{"saved":N,"skipped":N}`. |

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
| `BomTable` | Editable table; each cell is an `<input>`; row-level tinting by confidence/flags |
| `WarningsPanel` | Dismissible warning banners surfaced from `doc.warnings` |

### BomTable internals

Each `BomRow` renders a row of `<input>` elements. Changes call back to `BomTable` via:
- `onUpdate(index, field, value)` — for top-level `BOMRow` fields
- `onUpdateQty(index, field, value)` — for nested `Quantity` fields

The `↗` (save mapping) button is only shown when `customerPartNumber` is non-empty. It fires `onSaveMapping` which calls `POST /api/mappings`.

Row background tinting logic (`rowTint`):

| Condition | Background |
|-----------|------------|
| `confidence < 0.65` | Light red (`#fff5f5`) |
| `unit_ambiguous` or `missing_part_number` flag | Light yellow (`#fffdf0`) |
| Any other flags | Off-white (`#fafafa`) |
| No issues | White |

### API client (`api/client.ts`)

All API calls are centralised in `client.ts` as typed async functions. Every function calls `parseError(res)` on non-OK responses to extract the `{"error":"..."}` message from the server before throwing. Auth functions: `checkAuth`, `login`, `logout`.

---

## 10. Storage

### Documents — in-memory (`store.go`)

```
documentStore
  docs  map[string]*Document   ID → Document pointer
  mu    sync.RWMutex
```

Documents are stored in memory only. **Restarting the server clears all documents.** Uploaded PDF files persist on disk in `./uploads/` and survive restarts, but the metadata does not. This is intentional for the prototype — documents are transient working artefacts.

### Mappings — persistent (`mappings.go`)

Mappings are loaded from `data/mappings.json` on startup and written back atomically on every change. This file is not committed (it lives in `backend/data/` which is gitignored) and accumulates across server restarts.

In the Docker container the data directory is at the path set by `DATA_DIR` (defaults to `./data`). Because App Runner does not have persistent storage, **mappings are reset on each deployment**. To persist them across deployments, mount an EFS volume or migrate to a database.

---

## 11. Deployment architecture

```
┌──────────────────────────────────────────────────────────────┐
│  AWS                                                         │
│                                                              │
│  ECR Repository                                              │
│    └─ sme-prototype:latest  (linux/amd64 Docker image)       │
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
2. **Stage 2** (`golang:1.24-alpine`) — `go build -o sme-prototype` → single binary
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
| `AUTH_USERNAME` | Yes | — | Login username |
| `AUTH_PASSWORD` | Yes | — | Login password |
| `ANTHROPIC_API_KEY` | No | — | Omit to use mock data |
| `PORT` | No | `8080` | HTTP listen port |
| `DATA_DIR` | No | `./data` | Directory for `mappings.json` |
| `STATIC_DIR` | No | `./static` | Directory for compiled frontend |
| `CORS_ORIGIN` | No | `*` | Value for `Access-Control-Allow-Origin` |
