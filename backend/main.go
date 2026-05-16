package main

import (
	"bufio"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func main() {
	loadDotEnv(".env")

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		log.Println("ANTHROPIC_API_KEY not set — analysis will use mock data")
	}

	uploadDir := "./uploads"
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		log.Fatalf("failed to create upload directory: %v", err)
	}

	matchThreshold := defaultMatchThreshold
	if v := os.Getenv("MATCH_SCORE_THRESHOLD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			matchThreshold = f
			log.Printf("match threshold set to %.3f from MATCH_SCORE_THRESHOLD", matchThreshold)
		} else {
			log.Printf("invalid MATCH_SCORE_THRESHOLD %q, using default %.3f", v, matchThreshold)
		}
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("DATABASE_URL must be set")
	}
	db, err := openDB(dbURL)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	if err := runMigrations(db); err != nil {
		log.Fatalf("migrations: %v", err)
	}

	store := &pgDocumentStore{db: db}
	if err := store.resetAnalyzing(); err != nil {
		log.Fatalf("reset stale analyses: %v", err)
	}

	orgName := os.Getenv("ORG_NAME")
	adminUsername := os.Getenv("AUTH_USERNAME")
	adminPassword := os.Getenv("AUTH_PASSWORD")
	if adminUsername == "" || adminPassword == "" {
		log.Fatal("AUTH_USERNAME and AUTH_PASSWORD must be set")
	}
	if err := seedAdmin(db, orgName, adminUsername, adminPassword); err != nil {
		log.Fatalf("seed admin: %v", err)
	}

	matchFeedback := &pgMatchFeedbackRepository{db: db}
	mappings      := &pgMappingRepository{db: db}
	catalog       := &pgPartCatalogRepository{db: db}
	userRepo      := &pgUserRepository{db: db}
	invites       := &pgInviteRepository{db: db}
	orgSettings   := &pgOrgSettingsRepository{db: db}
	errorLog      := &pgErrorLogRepository{db: db}
	sessions      := &pgSessionStore{db: db, ttl: 24 * time.Hour}
	priceCache    := &pgPriceCacheRepository{db: db}
	pricingRuns   := &pgPricingRunRepository{db: db}

	priceProvider := buildPricingProvider()
	pricingCacheTTL := defaultPricingCacheTTL
	if v := os.Getenv("PRICING_CACHE_TTL_HOURS"); v != "" {
		if h, err := strconv.Atoi(v); err == nil && h > 0 {
			pricingCacheTTL = time.Duration(h) * time.Hour
			log.Printf("pricing: cache TTL set to %s from PRICING_CACHE_TTL_HOURS", pricingCacheTTL)
		}
	}

	srv := &server{
		store:          store,
		mappings:       mappings,
		catalog:        catalog,
		matchFeedback:  matchFeedback,
		matchThreshold: matchThreshold,
		sessions:      sessions,
		uploadDir:     uploadDir,
		apiKey:        apiKey,
		userRepo:      userRepo,
		invites:       invites,
		orgSettings:   orgSettings,
		errorLog:      errorLog,
		adminUsername: os.Getenv("AUTH_USERNAME"),
		priceCache:      priceCache,
		priceProvider:   priceProvider,
		pricingRuns:     pricingRuns,
		pricingCacheTTL: pricingCacheTTL,
		pricingCurrency: defaultPricingCurrency,
	}

	staticDir := os.Getenv("STATIC_DIR")
	if staticDir == "" {
		staticDir = "./static"
	}

	mux := http.NewServeMux()

	// Public routes (no auth required)
	mux.HandleFunc("GET /api/documents/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /api/auth/login", srv.login)

	// Protected routes
	mux.HandleFunc("POST /api/auth/logout", srv.requireAuth(srv.logout))
	mux.HandleFunc("GET /api/auth/me", srv.requireAuth(srv.authMe))
	mux.HandleFunc("GET /api/documents", srv.requireAuth(srv.listDocuments))
	mux.HandleFunc("POST /api/documents/upload", srv.requireAuth(srv.upload))
	mux.HandleFunc("POST /api/documents/{id}/analyze", srv.requireAuth(srv.analyze))
	mux.HandleFunc("POST /api/documents/{id}/price", srv.requireAuth(srv.priceBOM))
	mux.HandleFunc("GET /api/documents/{id}", srv.requireAuth(srv.get))
	mux.HandleFunc("DELETE /api/documents/{id}", srv.requireAuth(srv.deleteDocument))
	mux.HandleFunc("GET /api/documents/{id}/bom.csv", srv.requireAuth(srv.exportCSV))
	mux.HandleFunc("GET /api/documents/{id}/export/sap", srv.requireAuth(srv.exportSAP))
	mux.HandleFunc("PUT /api/documents/{id}/bom", srv.requireAuth(srv.saveBOM))
	mux.HandleFunc("GET /api/documents/{id}/similar", srv.requireAuth(srv.similarDocs))
	mux.HandleFunc("GET /api/documents/{id}/preview", srv.requireAuth(srv.previewBOM))
	mux.HandleFunc("POST /api/documents/{id}/bom/clone-from/{sourceId}", srv.requireAuth(srv.cloneBOM))
	mux.HandleFunc("POST /api/match-feedback", srv.requireAuth(srv.recordFeedback))
	mux.HandleFunc("GET /api/mappings/suggest", srv.requireAuth(srv.suggestMappings)) // must be before /api/mappings
	mux.HandleFunc("GET /api/mappings/search", srv.requireAuth(srv.searchMappings))
	mux.HandleFunc("GET /api/mappings/clients", srv.requireAuth(srv.listMappingClients))
	mux.HandleFunc("GET /api/mappings", srv.requireAuth(srv.listMappings))
	mux.HandleFunc("POST /api/mappings/upload", srv.requireAuth(srv.uploadMappings))
	mux.HandleFunc("POST /api/mappings/import", srv.requireAuth(srv.importMappings))
	mux.HandleFunc("POST /api/mappings", srv.requireAuth(srv.saveMapping))
	mux.HandleFunc("DELETE /api/mappings/{id}", srv.requireAuth(srv.deleteMapping))
	mux.HandleFunc("PATCH /api/documents/{id}", srv.requireAuth(srv.updateDocument))
	mux.HandleFunc("GET /api/users/me", srv.requireAuth(srv.getMe))
	mux.HandleFunc("PUT /api/users/me/password", srv.requireAuth(srv.changePassword))
	mux.HandleFunc("POST /api/users", srv.requireAuth(srv.createUser))
	mux.HandleFunc("POST /api/invites", srv.requireAuth(srv.createInvite))
	mux.HandleFunc("GET /api/invites/{token}", srv.validateInvite)          // public
	mux.HandleFunc("POST /api/invites/{token}/accept", srv.acceptInvite)    // public
	mux.HandleFunc("GET /api/org/export-config", srv.requireAuth(srv.getExportConfig))
	mux.HandleFunc("PUT /api/org/export-config", srv.requireAuth(srv.saveExportConfig))

	// Admin routes (require auth + admin role)
	mux.HandleFunc("GET /api/admin/errors", srv.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		srv.requireAdmin(http.HandlerFunc(srv.listErrors)).ServeHTTP(w, r)
	}))

	if _, err := os.Stat(staticDir); err == nil {
		mux.Handle("/", spaHandler(staticDir))
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, cors(mux)))
}

func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		if key != "" && os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}
}

// buildPricingProvider wires the pricing source from the process env.
func buildPricingProvider() pricingProvider {
	p := selectPricingProvider(os.Getenv)
	if p == nil {
		log.Println("pricing: provider=csv-only (no upstream; CSV fallback only)")
	} else {
		log.Printf("pricing: provider=%s", p.name())
	}
	return p
}

// selectPricingProvider is the pure (testable) core of provider selection.
// env is an os.Getenv-shaped lookup.
//
// PRICING_PROVIDER modes:
//
//   - ""/"auto"/"multi" → compose every provider that has credentials,
//     in fixed order (Mouser, Farnell, DigiKey, TME) so the multiProvider's
//     first-wins dedupe is deterministic. One available provider is
//     returned unwrapped; none → mock.
//   - "mock"            → canned fixtures (no upstream calls).
//   - "csv-only"        → nil (handler 503s until the CSV path exists).
//   - a single provider name ("mouser"|"farnell"|"digikey"|"tme")
//     → just that one, for cost/debug control, even if other creds exist.
//     If its creds are absent, fall back to mock rather than ship a
//     provider that 401s every call.
//   - anything else     → mock (config typo shouldn't crash boot).
func selectPricingProvider(env func(string) string) pricingProvider {
	mode := strings.ToLower(strings.TrimSpace(env("PRICING_PROVIDER")))

	switch mode {
	case "mock":
		return newMockPricingProvider()
	case "csv-only":
		return nil
	}

	// Build whatever real providers have complete credentials. Order here
	// is the dedupe-precedence order: direct distributors first.
	type built struct {
		name string
		p    pricingProvider
	}
	var avail []built

	if k := env("MOUSER_API_KEY"); k != "" {
		mp := newMouserProvider(k)
		if v := env("MOUSER_SEARCH_URL"); v != "" {
			mp.searchURL = v
		}
		avail = append(avail, built{"mouser", mp})
	}
	if k := env("FARNELL_API_KEY"); k != "" {
		fp := newFarnellProvider(k)
		if v := env("FARNELL_STORE_ID"); v != "" {
			fp.storeID = v
		}
		if v := env("FARNELL_STORE_CURRENCY"); v != "" {
			fp.storeCurrency = v
		}
		if v := env("FARNELL_SEARCH_URL"); v != "" {
			fp.searchURL = v
		}
		avail = append(avail, built{"farnell", fp})
	}
	if id, sec := env("DIGIKEY_CLIENT_ID"), env("DIGIKEY_CLIENT_SECRET"); id != "" && sec != "" {
		dp := newDigikeyProvider(id, sec)
		if v := env("DIGIKEY_TOKEN_URL"); v != "" {
			dp.tokenURL = v
		}
		if v := env("DIGIKEY_SEARCH_URL"); v != "" {
			dp.searchURL = v
		}
		avail = append(avail, built{"digikey", dp})
	}
	if tok, sec := env("TME_TOKEN"), env("TME_APP_SECRET"); tok != "" && sec != "" {
		tp := newTMEProvider(tok, sec)
		if v := env("TME_BASE_URL"); v != "" {
			tp.baseURL = v
		}
		avail = append(avail, built{"tme", tp})
	}

	// Explicit single-source override.
	switch mode {
	case "mouser", "farnell", "digikey", "tme":
		for _, b := range avail {
			if b.name == mode {
				return b.p
			}
		}
		log.Printf("pricing: PRICING_PROVIDER=%s but its credentials are missing — falling back to mock", mode)
		return newMockPricingProvider()
	case "", "auto", "multi":
		// fall through to compose
	default:
		log.Printf("pricing: unknown PRICING_PROVIDER=%q — falling back to mock", mode)
		return newMockPricingProvider()
	}

	switch len(avail) {
	case 0:
		return newMockPricingProvider()
	case 1:
		return avail[0].p
	default:
		ps := make([]pricingProvider, len(avail))
		for i, b := range avail {
			ps[i] = b.p
		}
		return newMultiProvider(ps...)
	}
}

// spaHandler serves static files from dir, falling back to index.html for any
// path that doesn't correspond to a file on disk. This lets the React Router
// handle client-side routes (e.g. /settings) even on a hard refresh.
func spaHandler(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Join(dir, r.URL.Path)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		fs.ServeHTTP(w, r)
	})
}

func cors(next http.Handler) http.Handler {
	origin := os.Getenv("CORS_ORIGIN")
	if origin == "" {
		origin = "*"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
