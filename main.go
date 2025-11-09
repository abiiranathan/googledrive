package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"gdrive/gdrive"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/joho/godotenv"
	_ "github.com/mattn/go-sqlite3"
	"github.com/redis/go-redis/v9"
)

const (
	// DefaultCredentialsPath is the path to the OAuth2 credentials file.
	DefaultCredentialsPath = "credentials.json"
	// DefaultDBPath is the path to the SQLite database.
	DefaultDBPath = "gdrive.db"
	// CacheExpiration is the duration for which cached data is valid (24 hours for e-library).
	CacheExpiration = 24 * time.Hour
	// FilesListCacheKey is the Redis key for cached file list.
	FilesListCacheKey = "gdrive:files:list"
	// CacheTimestampKey is the Redis key for cache timestamp.
	CacheTimestampKey = "gdrive:files:timestamp"
)

// Server represents the web application server.
type Server struct {
	driveClient *gdrive.DriveClient
	db          *sql.DB
	redis       *redis.Client
}

// BookmarkRequest represents a bookmark creation request.
type BookmarkRequest struct {
	FileID string `json:"file_id"`
	Notes  string `json:"notes"`
}

// NewServer creates and initializes a new Server instance.
// Returns an error if database initialization or Drive client creation fails.
func NewServer(ctx context.Context, credentialsPath, dbPath string, redisAddr string) (*Server, error) {
	// Initialize Drive client
	b, err := os.ReadFile(credentialsPath)
	if err != nil {
		return nil, fmt.Errorf("unable to read credentials: %w", err)
	}

	driveClient, err := gdrive.NewDriveClientForServiceAccount(ctx, b)
	if err != nil {
		return nil, fmt.Errorf("unable to create Drive client: %w", err)
	}

	// Initialize SQLite database
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("unable to open database: %w", err)
	}

	if err := initDB(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("unable to initialize database: %w", err)
	}

	// Initialize Redis client (required for e-library caching)
	if redisAddr == "" {
		return nil, fmt.Errorf("Redis address is required for e-library operation")
	}

	redisClient := redis.NewClient(&redis.Options{
		Addr: redisAddr,
		DB:   0,
	})

	// Test connection
	if err := redisClient.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("Redis connection failed: %w", err)
	}

	log.Println("Redis connected successfully - using 24-hour cache for e-library")

	return &Server{
		driveClient: driveClient,
		db:          db,
		redis:       redisClient,
	}, nil
}

// initDB creates the necessary database tables.
func initDB(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS bookmarks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		file_id TEXT NOT NULL UNIQUE,
		file_name TEXT NOT NULL,
		notes TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS downloads (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		file_id TEXT NOT NULL,
		file_name TEXT NOT NULL,
		downloaded_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_bookmarks_file_id ON bookmarks(file_id);
	CREATE INDEX IF NOT EXISTS idx_downloads_file_id ON downloads(file_id);
	`

	_, err := db.Exec(schema)
	return err
}

// Close releases all server resources.
func (s *Server) Close() error {
	if s.redis != nil {
		s.redis.Close()
	}
	return s.db.Close()
}

// getFiles retrieves files from Redis cache or Drive API.
// Returns cached data if available and not expired, otherwise fetches fresh data.
// For e-library use case, cache is valid for 24 hours.
func (s *Server) getFiles(ctx context.Context, forceRefresh bool) ([]gdrive.FileInfo, error) {
	// Try Redis cache first (unless force refresh)
	if !forceRefresh {
		data, err := s.redis.Get(ctx, FilesListCacheKey).Bytes()
		if err == nil {
			var files []gdrive.FileInfo
			if err := json.Unmarshal(data, &files); err == nil {
				// Verify cache age
				timestamp, err := s.redis.Get(ctx, CacheTimestampKey).Int64()
				if err == nil {
					cacheAge := time.Since(time.Unix(timestamp, 0))
					if cacheAge < CacheExpiration {
						log.Printf("Serving from cache (age: %v, expires in: %v)",
							cacheAge.Round(time.Minute),
							(CacheExpiration - cacheAge).Round(time.Minute))
						return files, nil
					}
					log.Println("Cache expired, fetching fresh data from Google Drive")
				}
			}
		}
	} else {
		log.Println("Force refresh requested, fetching fresh data from Google Drive")
	}

	// Fetch from Drive API
	log.Println("Fetching files from Google Drive API...")
	files, err := s.driveClient.ListFiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to list files: %w", err)
	}

	log.Printf("Fetched %d files from Google Drive", len(files))

	// Update Redis cache with 24-hour expiration
	data, err := json.Marshal(files)
	if err != nil {
		log.Printf("Warning: Failed to marshal files for caching: %v", err)
	} else {
		// Store files list
		if err := s.redis.Set(ctx, FilesListCacheKey, data, CacheExpiration).Err(); err != nil {
			log.Printf("Warning: Failed to cache files list: %v", err)
		}
		// Store timestamp for cache age tracking
		if err := s.redis.Set(ctx, CacheTimestampKey, time.Now().Unix(), CacheExpiration).Err(); err != nil {
			log.Printf("Warning: Failed to cache timestamp: %v", err)
		}
		log.Println("Files cached in Redis for 24 hours")
	}

	return files, nil
}

// handleListFiles handles GET /api/files - returns list of all files.
func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	refresh := r.URL.Query().Get("refresh") == "true"

	files, err := s.getFiles(r.Context(), refresh)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Get cache info for response metadata
	timestamp, _ := s.redis.Get(r.Context(), CacheTimestampKey).Int64()
	cacheAge := time.Since(time.Unix(timestamp, 0))
	expiresIn := CacheExpiration - cacheAge

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"files":      files,
		"count":      len(files),
		"cache_age":  cacheAge.Round(time.Minute).String(),
		"expires_in": expiresIn.Round(time.Minute).String(),
		"cached_at":  time.Unix(timestamp, 0).Format(time.RFC3339),
	})
}

// handleDownloadFile handles GET /api/files/:id/download - streams file content.
func (s *Server) handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	fileID := chi.URLParam(r, "id")
	if fileID == "" {
		http.Error(w, "file ID required", http.StatusBadRequest)
		return
	}

	// Record download in database
	fileName := r.URL.Query().Get("name")
	if fileName == "" {
		fileName = "unknown"
	}

	_, err := s.db.Exec(
		"INSERT INTO downloads (file_id, file_name) VALUES (?, ?)",
		fileID, fileName,
	)
	if err != nil {
		log.Printf("Failed to record download: %v", err)
	}

	// Set headers for file download
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, fileName))
	w.Header().Set("Content-Type", "application/octet-stream")

	// Stream file directly to response
	_, err = s.driveClient.StreamFile(r.Context(), fileID, w)
	if err != nil {
		log.Printf("Error streaming file %s: %v", fileID, err)
		// Cannot send error response after streaming starts
	}
}

// handleAddBookmark handles POST /api/bookmarks - adds a file bookmark.
func (s *Server) handleAddBookmark(w http.ResponseWriter, r *http.Request) {
	var req BookmarkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.FileID == "" {
		http.Error(w, "file_id required", http.StatusBadRequest)
		return
	}

	// Get file info to store name
	files, err := s.getFiles(r.Context(), false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var fileName string
	for _, f := range files {
		if f.ID == req.FileID {
			fileName = f.Name
			break
		}
	}

	if fileName == "" {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	result, err := s.db.Exec(
		"INSERT OR REPLACE INTO bookmarks (file_id, file_name, notes) VALUES (?, ?, ?)",
		req.FileID, fileName, req.Notes,
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	id, _ := result.LastInsertId()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":      id,
		"message": "bookmark added",
	})
}

// handleListBookmarks handles GET /api/bookmarks - returns all bookmarks.
func (s *Server) handleListBookmarks(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(`
		SELECT id, file_id, file_name, notes, created_at 
		FROM bookmarks 
		ORDER BY created_at DESC
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type Bookmark struct {
		ID        int64     `json:"id"`
		FileID    string    `json:"file_id"`
		FileName  string    `json:"file_name"`
		Notes     string    `json:"notes"`
		CreatedAt time.Time `json:"created_at"`
	}

	bookmarks := make([]Bookmark, 0)
	for rows.Next() {
		var b Bookmark
		if err := rows.Scan(&b.ID, &b.FileID, &b.FileName, &b.Notes, &b.CreatedAt); err != nil {
			continue
		}
		bookmarks = append(bookmarks, b)
	}

	if rows.Err() != nil {
		http.Error(w, rows.Err().Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"bookmarks": bookmarks,
		"count":     len(bookmarks),
	})
}

// handleDeleteBookmark handles DELETE /api/bookmarks/:id - removes a bookmark.
func (s *Server) handleDeleteBookmark(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid bookmark ID", http.StatusBadRequest)
		return
	}

	result, err := s.db.Exec("DELETE FROM bookmarks WHERE id = ?", id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		http.Error(w, "bookmark not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "bookmark deleted"})
}

// handleGetStats handles GET /api/stats - returns download statistics.
func (s *Server) handleGetStats(w http.ResponseWriter, r *http.Request) {
	var totalDownloads int64
	err := s.db.QueryRow("SELECT COUNT(*) FROM downloads").Scan(&totalDownloads)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	rows, err := s.db.Query(`
		SELECT file_name, COUNT(*) as count 
		FROM downloads 
		GROUP BY file_name 
		ORDER BY count DESC 
		LIMIT 10
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type FileStats struct {
		FileName string `json:"file_name"`
		Count    int    `json:"count"`
	}

	topFiles := make([]FileStats, 0)
	for rows.Next() {
		var fs FileStats
		if err := rows.Scan(&fs.FileName, &fs.Count); err != nil {
			continue
		}
		topFiles = append(topFiles, fs)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"total_downloads": totalDownloads,
		"top_files":       topFiles,
	})
}

// handleClearCache handles POST /api/cache/clear - manually clears the Redis cache.
func (s *Server) handleClearCache(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Delete cache keys
	if err := s.redis.Del(ctx, FilesListCacheKey, CacheTimestampKey).Err(); err != nil {
		http.Error(w, fmt.Sprintf("failed to clear cache: %v", err), http.StatusInternalServerError)
		return
	}

	log.Println("Cache cleared manually")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"message": "cache cleared successfully",
	})
}

func main() {
	godotenv.Load()

	ctx := context.Background()

	// Get configuration from environment or use defaults
	credPath := os.Getenv("CREDENTIALS_PATH")
	if credPath == "" {
		credPath = DefaultCredentialsPath
	}

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = DefaultDBPath
	}

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		log.Fatal("REDIS_ADDR environment variable is required for e-library operation")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Initialize server
	server, err := NewServer(ctx, credPath, dbPath, redisAddr)
	if err != nil {
		log.Fatalf("Failed to initialize server: %v", err)
	}
	defer server.Close()

	// Setup router
	r := chi.NewRouter()

	// Middleware
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Content-Type"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	// API routes
	r.Route("/api", func(r chi.Router) {
		r.Get("/files", server.handleListFiles)
		r.Get("/files/{id}/download", server.handleDownloadFile)
		r.Get("/bookmarks", server.handleListBookmarks)
		r.Post("/bookmarks", server.handleAddBookmark)
		r.Delete("/bookmarks/{id}", server.handleDeleteBookmark)
		r.Get("/stats", server.handleGetStats)
		r.Post("/cache/clear", server.handleClearCache)
	})

	// Serve static files (frontend)
	r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "static/index.html")
	})

	log.Printf("E-Library server starting on http://localhost:%s", port)
	log.Printf("Cache strategy: Redis with 24-hour expiration")
	if err := http.ListenAndServe(":"+port, r); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
