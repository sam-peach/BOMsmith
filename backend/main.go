package main

import (
	"bufio"
	"log"
	"net/http"
	"os"
	"path/filepath"
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

	var (
		mappings    mappingRepository
		userRepo    userRepository
		invites     inviteRepository
		orgSettings orgSettingsRepository
		errorLog    errorLogRepository
	)

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL != "" {
		db, err := openDB(dbURL)
		if err != nil {
			log.Fatalf("database: %v", err)
		}
		if err := runMigrations(db); err != nil {
			log.Fatalf("migrations: %v", err)
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

		mappings    = &pgMappingRepository{db: db}
		userRepo    = &pgUserRepository{db: db}
		invites     = &pgInviteRepository{db: db}
		orgSettings = &pgOrgSettingsRepository{db: db}
		errorLog    = &pgErrorLogRepository{db: db}
		log.Println("using Postgres storage")
	} else {
		// Dev/test mode: in-memory stores backed by optional JSON file.
		authUsername := os.Getenv("AUTH_USERNAME")
		authPassword := os.Getenv("AUTH_PASSWORD")
		if authUsername == "" || authPassword == "" {
			log.Fatal("AUTH_USERNAME and AUTH_PASSWORD must be set")
		}

		dataDir := os.Getenv("DATA_DIR")
		if dataDir == "" {
			dataDir = "./data"
		}
		if err := os.MkdirAll(dataDir, 0755); err != nil {
			log.Fatalf("failed to create data directory: %v", err)
		}

		ms, err := newMappingStore(filepath.Join(dataDir, "mappings.json"))
		if err != nil {
			log.Fatalf("mapping store: %v", err)
		}
		mappings    = &inMemoryMappingRepository{store: ms}
		invites     = newMemInviteRepo()
		orgSettings = &memOrgSettingsRepository{}
		errorLog    = &memErrorLogRepository{}

		ur, err := newEnvUserRepository(authUsername, authPassword)
		if err != nil {
			log.Fatalf("user repository: %v", err)
		}
		userRepo = ur
		log.Println("DATABASE_URL not set — using in-memory storage")
	}

	srv := &server{
		store:         newStore(),
		mappings:      mappings,
		sessions:      newSessionStore(24 * time.Hour),
		uploadDir:     uploadDir,
		apiKey:        apiKey,
		userRepo:      userRepo,
		invites:       invites,
		orgSettings:   orgSettings,
		errorLog:      errorLog,
		adminUsername: os.Getenv("AUTH_USERNAME"),
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
	mux.HandleFunc("POST /api/documents/upload", srv.requireAuth(srv.upload))
	mux.HandleFunc("POST /api/documents/{id}/analyze", srv.requireAuth(srv.analyze))
	mux.HandleFunc("GET /api/documents/{id}", srv.requireAuth(srv.get))
	mux.HandleFunc("GET /api/documents/{id}/bom.csv", srv.requireAuth(srv.exportCSV))
	mux.HandleFunc("GET /api/documents/{id}/export/sap", srv.requireAuth(srv.exportSAP))
	mux.HandleFunc("PUT /api/documents/{id}/bom", srv.requireAuth(srv.saveBOM))
	mux.HandleFunc("GET /api/mappings/suggest", srv.requireAuth(srv.suggestMappings)) // must be before /api/mappings
	mux.HandleFunc("GET /api/mappings", srv.requireAuth(srv.listMappings))
	mux.HandleFunc("POST /api/mappings/upload", srv.requireAuth(srv.uploadMappings)) // must be before /api/mappings
	mux.HandleFunc("POST /api/mappings", srv.requireAuth(srv.saveMapping))
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
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
