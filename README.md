# Praxis — Drawing to BOM

Praxis converts wiring harness engineering drawings (PDF) into draft Bills of Materials. Upload a drawing, run analysis, review and correct the generated BOM, then export to CSV for SAP import.

> For a full walkthrough of how the application works, see [docs/walkthrough.md](docs/walkthrough.md).

---

## How it works

1. **Upload** a PDF drawing through the web UI
2. **Analyse** — text is extracted from the PDF and sent to Claude (Anthropic API) with a domain-specific prompt tuned for wiring harness drawings
3. **Review** the generated BOM — every row is editable; rows are colour-coded by confidence and flagged for issues (ambiguous units, missing part numbers, etc.)
4. **Save mappings** — link a customer part number to an internal/manufacturer part number; the mapping is stored and applied automatically to future drawings
5. **Export** the finished BOM as a CSV formatted for SAP import

---

## Prerequisites

| Tool | Version |
|------|---------|
| Go | 1.24+ |
| Node.js | 20+ |
| Docker + Buildx | for deployment |
| AWS CLI | for deployment |
| Terraform | 1.5+ for infrastructure |

---

## Local development

### 1. Backend

```bash
cd backend
cp .env.example .env          # then fill in real values
go run .
# → listening on :8080
```

**Required environment variables** (set in `backend/.env`):

| Variable | Description |
|----------|-------------|
| `AUTH_USERNAME` | Login username |
| `AUTH_PASSWORD` | Login password |
| `ANTHROPIC_API_KEY` | Anthropic API key — omit to use mock data |
| `PRICING_PROVIDER` | Empty / `auto` / `multi` composes every provider that has credentials. A single name (`mouser`/`farnell`/`digikey`/`tme`) pins one source. `mock` = canned fixtures (kill switch); `csv-only` = no upstream. |
| `MOUSER_API_KEY` | Mouser Search API key (free tier). |
| `FARNELL_API_KEY` | Farnell / element14 API key (free tier). |
| `FARNELL_STORE_ID` | Farnell store that fixes price currency (default `uk.farnell.com` → GBP). |
| `DIGIKEY_CLIENT_ID` / `DIGIKEY_CLIENT_SECRET` | Digi-Key Product Information API OAuth2 (free tier). |
| `TME_TOKEN` / `TME_APP_SECRET` | TME API token + HMAC signing secret (free tier). |
| `*_SEARCH_URL` / `*_TOKEN_URL` / `TME_BASE_URL` | Endpoint overrides (testing only). |
| `PRICING_CACHE_TTL_HOURS` | `part_prices` freshness window (default `24`). |

Provider selection: with **no** PRICING_PROVIDER set, the backend composes a `multiProvider` from every source whose credentials are present, in the fixed order Mouser → Farnell → Digi-Key → TME so the cross-provider dedupe (first-wins on supplier+SKU) is deterministic. One available provider is used unwrapped; none → mock.

When `ANTHROPIC_API_KEY` is not set the server falls back to `mockAnalysis()`. The same pattern applies to pricing: `PRICING_PROVIDER=mock` returns canned offers for `MOCK-MULTI` / `MOCK-SINGLE` MPNs so the full BOM-pricing UX works with no upstream credentials.

### 2. Frontend

```bash
cd frontend
npm install
npm run dev
# → http://localhost:5173
```

The Vite dev server proxies all `/api` requests to `localhost:8080`, so cookies and auth work seamlessly.

### 3. Running tests

```bash
cd backend && go test ./...          # all tests
cd backend && go test -run TestName  # single test
cd frontend && npx tsc --noEmit      # TypeScript check
```

---

## Deployment (AWS App Runner)

Infrastructure is managed with Terraform. A single Docker image serves both the API and the compiled React frontend.

### First-time setup

```bash
cd infra
cp terraform.tfvars.example terraform.tfvars
# edit terraform.tfvars — fill in anthropic_api_key, auth_username, auth_password

terraform init
terraform apply
```

### Deploy an update

```bash
./infra/deploy.sh
```

This script builds the `linux/amd64` image, pushes it to ECR, and triggers an App Runner redeployment. It requires `terraform output` to be available (i.e. `terraform apply` has been run at least once).

---

## Project layout

```
backend/
  auth.go         — session store, login/logout handlers, requireAuth middleware
  analysis.go     — LLM call + post-processing pipeline
  fingerprint.go  — part attribute extraction (type, material, standard, diameter, color)
  catalog.go      — part catalog: fingerprint scoring, suggestion pipeline, pg repository
  mock.go         — mock analysis for dev/test (no API key required)
  mappings.go     — mapping repository (pg-backed CPN → IPN cross-references)
  handler.go      — HTTP handlers, server struct
  store.go        — document repository interface + pg implementation
  extract.go      — PDF text extraction
  types.go        — shared structs (Document, BOMRow, Quantity, Mapping, CatalogPart, PartFingerprint)
  main.go         — server wiring

frontend/src/
  types/api.ts    — TypeScript types (mirror Go structs)
  api/client.ts   — fetch wrappers (including auth)
  components/     — React components
    LoginPage.tsx
    BomTable.tsx
    UploadArea.tsx
    WarningsPanel.tsx
  App.tsx         — root component, auth state gate

infra/
  main.tf         — ECR + App Runner resources
  variables.tf    — input variables
  outputs.tf      — ECR URL, App Runner URL
  deploy.sh       — build → push → redeploy script
```

---

## Architecture overview

```
Browser
  │  (HTTPS, cookie-auth)
  ▼
App Runner  ──→  Go HTTP server  ──→  Anthropic API
                     │
                     ▼
                 PostgreSQL
            ┌────────┬─────────────┐
            │        │             │
        documents  mappings   part_catalog
```

See [docs/walkthrough.md](docs/walkthrough.md) for a full architectural walkthrough.
