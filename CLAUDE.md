# sme-prototype — Claude Code Guidelines

## 1. Test-Driven Development (TDD)

**Write failing tests before writing any implementation code.**

- Create the `_test.go` file and write tests that specify the behaviour.
- Run them (`go test ./...`). They must fail. Only then write implementation until the tests pass.
- This order is mandatory: **test → fail → implement → pass**.
- "It's a small change" is not an exception. "It's a refactor" is not an exception.

## 2. Stack

- **Backend:** Go 1.24, standard library only (no frameworks), `testify` for assertions
- **Frontend:** React + TypeScript, Vite, no UI framework
- **Storage:** in-process memory (`documentStore`) + JSON file (`mappings.json`)
- **Analysis:** Anthropic Claude API; falls back to `mockAnalysis()` when no API key

## 3. Project layout

```
backend/
  types.go        — shared structs (Document, BOMRow, Quantity, Mapping)
  analysis.go     — LLM call + post-processing pipeline
  mock.go         — mock analysis for dev/test (no API key required)
  mappings.go     — mapping store with JSON persistence
  handler.go      — HTTP handlers
  store.go        — in-memory document store
  extract.go      — PDF text extraction
  main.go         — server wiring

frontend/src/
  types/api.ts    — TypeScript types (mirror Go structs)
  api/client.ts   — fetch wrappers
  components/     — React components
  App.tsx         — root component
```

## 4. Running tests

```bash
cd backend && go test ./...          # all tests
cd backend && go test -run TestName  # single test
cd frontend && npx tsc --noEmit      # TypeScript check
```

## 5. Key invariants

- `Quantity.Raw` is **never** modified after extraction — it is the source of truth from the drawing.
- `Quantity.Value`/`Unit` are derived by `parseQuantity()` — never set directly on BOM rows.
- A unit mismatch between `rawQuantity` and the declared unit always sets `unit_ambiguous` and is **never** silently normalised.
- Mapping lookups are case-insensitive (keyed by `strings.ToUpper(customerPartNumber)`).
- `mappings.json` is written atomically via a temp-file rename.

## 6. Adding features

1. Write the test first in `*_test.go`.
2. Run `go test ./...` — confirm it fails.
3. Implement until the test passes.
4. Update TypeScript types in `frontend/src/types/api.ts` if the API shape changed.
5. Update `README.md` and `docs/walkthrough.md` if the change affects architecture, the API, data models, auth, storage, deployment, or the analysis pipeline.

## 7. Keeping docs up to date

`README.md` and `docs/walkthrough.md` are the canonical references for this project. After completing any task, check whether the work affects any of the following areas and update the relevant section if so:

| Changed area | Sections to update |
|---|---|
| New or removed API endpoint | README layout table · walkthrough §8 HTTP API reference |
| New data field or struct | walkthrough §6 Data models |
| Auth changes | walkthrough §3 Authentication |
| Analysis pipeline changes | walkthrough §5 Analysis pipeline |
| New flag type | walkthrough §5 · §12 Adding a new flag type |
| Mapping system changes | walkthrough §7 Mapping system |
| New environment variable | README env table · walkthrough §12 env var reference |
| Infra / deployment changes | README Deployment · walkthrough §11 Deployment architecture |
| New frontend component | walkthrough §9 Frontend architecture |
| Project layout changes | README layout · CLAUDE.md §3 · walkthrough §2 |

If a change is purely internal (refactor, test, bug fix with no visible behaviour change) and no public-facing contract changed, doc updates are not required.
