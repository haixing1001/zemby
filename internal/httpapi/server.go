package httpapi

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/haixing1001/cinebase/internal/auth"
	"github.com/haixing1001/cinebase/internal/database"
)

const sessionCookie = "cinebase_session"

//go:embed static/*
var assets embed.FS

type Server struct {
	db         *database.DB
	auth       *auth.Manager
	mediaRoots []string
	logger     *slog.Logger
	libraryMu  sync.Mutex
	streams    chan struct{}
	loginMu    sync.Mutex
	loginFails map[string]loginFailure
}

type loginFailure struct {
	count int
	until time.Time
}

func New(db *database.DB, sessions *auth.Manager, mediaRoots []string, maxStreams int, logger *slog.Logger) *Server {
	return &Server{db: db, auth: sessions, mediaRoots: mediaRoots, logger: logger, streams: make(chan struct{}, maxStreams), loginFails: make(map[string]loginFailure)}
}

func (s *Server) Handler() http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("GET /api/session", s.session)
	api.HandleFunc("POST /api/logout", s.logout)
	api.HandleFunc("GET /api/libraries", s.listLibraries)
	api.HandleFunc("POST /api/libraries", s.createLibrary)
	api.HandleFunc("POST /api/libraries/{id}/scan", s.scanLibrary)
	api.HandleFunc("GET /api/jobs", s.listJobs)
	api.HandleFunc("GET /api/items", s.listItems)
	api.HandleFunc("GET /api/items/{id}/stream", s.streamItem)
	api.HandleFunc("HEAD /api/items/{id}/stream", s.streamItem)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if err := s.db.PingContext(ctx); err != nil {
			writeError(w, http.StatusServiceUnavailable, "database unavailable")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("POST /api/login", s.login)
	mux.Handle("/api/", s.requireSession(api))
	static, _ := fs.Sub(assets, "static")
	mux.Handle("/", http.FileServer(http.FS(static)))
	return s.securityHeaders(s.logRequests(mux))
}

func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(r, origin) {
				writeError(w, http.StatusForbidden, "cross-origin request rejected")
				return
			}
		}
		cookie, err := r.Cookie(sessionCookie)
		if err != nil || !s.auth.ValidToken(cookie.Value, time.Now()) {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(r, origin) {
		writeError(w, http.StatusForbidden, "cross-origin request rejected")
		return
	}
	remoteIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		remoteIP = r.RemoteAddr
	}
	now := time.Now()
	s.loginMu.Lock()
	if len(s.loginFails) > 4096 {
		for ip, entry := range s.loginFails {
			if !entry.until.After(now) {
				delete(s.loginFails, ip)
			}
		}
	}
	if len(s.loginFails) >= 4096 {
		if _, exists := s.loginFails[remoteIP]; !exists {
			remoteIP = "__overflow__"
		}
	}
	failure := s.loginFails[remoteIP]
	if failure.until.After(now) {
		s.loginMu.Unlock()
		writeError(w, http.StatusTooManyRequests, "too many login attempts; try again later")
		return
	}
	s.loginMu.Unlock()

	var request struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &request, 8<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if !s.auth.CheckPassword(request.Password) {
		s.loginMu.Lock()
		failure = s.loginFails[remoteIP]
		failure.count++
		if failure.count >= 5 {
			failure.until = now.Add(10 * time.Minute)
			failure.count = 0
		}
		s.loginFails[remoteIP] = failure
		s.loginMu.Unlock()
		writeError(w, http.StatusUnauthorized, "invalid password")
		return
	}
	s.loginMu.Lock()
	delete(s.loginFails, remoteIP)
	s.loginMu.Unlock()
	token := s.auth.NewToken(now)
	if token == "" {
		writeError(w, http.StatusInternalServerError, "could not create session")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", HttpOnly: true,
		Secure: requestIsSecure(r), SameSite: http.SameSiteStrictMode,
		Expires: now.Add(24 * time.Hour), MaxAge: int((24 * time.Hour).Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: requestIsSecure(r), SameSite: http.SameSiteStrictMode, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) session(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) listLibraries(w http.ResponseWriter, r *http.Request) {
	libraries, err := s.db.ListLibraries(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load libraries")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": libraries})
}

func (s *Server) createLibrary(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name string `json:"name"`
		Path string `json:"path"`
		Kind string `json:"kind"`
	}
	if err := decodeJSON(r, &request, 16<<10); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	if request.Name == "" || len([]rune(request.Name)) > 80 {
		writeError(w, http.StatusBadRequest, "name must contain 1 to 80 characters")
		return
	}
	if request.Kind != "movies" && request.Kind != "series" && request.Kind != "music" {
		writeError(w, http.StatusBadRequest, "kind must be movies, series, or music")
		return
	}
	request.Path = strings.TrimSpace(request.Path)
	if request.Path == "" || !filepath.IsAbs(request.Path) {
		writeError(w, http.StatusBadRequest, "path must be an absolute media directory")
		return
	}
	abs, err := filepath.Abs(request.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid media directory")
		return
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		writeError(w, http.StatusBadRequest, "media directory does not exist")
		return
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid media directory")
		return
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		writeError(w, http.StatusBadRequest, "path must be a directory")
		return
	}
	allowed := false
	for _, root := range s.mediaRoots {
		resolvedRoot, rootErr := filepath.EvalSymlinks(root)
		if rootErr != nil {
			resolvedRoot = root
		}
		if inside(resolvedRoot, resolved) {
			allowed = true
			break
		}
	}
	if !allowed {
		writeError(w, http.StatusForbidden, "path is outside configured media roots")
		return
	}
	s.libraryMu.Lock()
	defer s.libraryMu.Unlock()
	libraries, err := s.db.ListLibraries(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not validate library path")
		return
	}
	for _, existing := range libraries {
		if inside(existing.Path, resolved) || inside(resolved, existing.Path) {
			writeError(w, http.StatusConflict, "media library paths must not overlap")
			return
		}
	}
	library, jobID, err := s.db.CreateLibraryWithScan(r.Context(), request.Name, filepath.Clean(resolved), request.Kind)
	if err != nil {
		writeError(w, http.StatusConflict, "could not create library; check for a duplicate path")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"library": library, "jobId": jobID})
}

func (s *Server) scanLibrary(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "invalid library id")
		return
	}
	if _, err := s.db.LibraryByID(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "library not found")
		} else {
			writeError(w, http.StatusInternalServerError, "could not load library")
		}
		return
	}
	jobID, err := s.db.QueueScan(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not queue scan")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]int64{"jobId": jobID})
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.db.ListJobs(r.Context(), 30)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load scan jobs")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": jobs})
}

func (s *Server) listItems(w http.ResponseWriter, r *http.Request) {
	libraryID, err := strconv.ParseInt(r.URL.Query().Get("libraryId"), 10, 64)
	if err != nil || libraryID < 1 {
		writeError(w, http.StatusBadRequest, "libraryId is required")
		return
	}
	limit := parseBoundedInt(r.URL.Query().Get("limit"), 50, 1, 100)
	offset := parseBoundedInt(r.URL.Query().Get("offset"), 0, 0, 10_000_000)
	items, total, err := s.db.ListItems(r.Context(), libraryID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load media items")
		return
	}
	type itemResponse struct {
		database.Item
		StreamURL string `json:"streamUrl"`
	}
	response := make([]itemResponse, 0, len(items))
	for _, item := range items {
		response = append(response, itemResponse{Item: item, StreamURL: "/api/items/" + strconv.FormatInt(item.ID, 10) + "/stream"})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": response, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) streamItem(w http.ResponseWriter, r *http.Request) {
	select {
	case s.streams <- struct{}{}:
		defer func() { <-s.streams }()
	default:
		w.Header().Set("Retry-After", "5")
		writeError(w, http.StatusServiceUnavailable, "all playback slots are currently in use")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return
	}
	item, err := s.db.ItemByID(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	resolved, err := filepath.EvalSymlinks(item.Path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	allowed := false
	for _, root := range s.mediaRoots {
		resolvedRoot, rootErr := filepath.EvalSymlinks(root)
		if rootErr != nil {
			resolvedRoot = root
		}
		if inside(resolvedRoot, resolved) {
			allowed = true
			break
		}
	}
	if !allowed {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(resolved)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Cache-Control", "private, no-cache")
	http.ServeContent(w, r, item.Title+item.Extension, info.ModTime(), file)
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; media-src 'self' blob:")
		w.Header().Set("Access-Control-Expose-Headers", "Accept-Ranges, Content-Range, Content-Length")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/health" {
			s.logger.Info("http request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start))
		}
	})
}

func sameOrigin(r *http.Request, origin string) bool {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	if !strings.EqualFold(parsed.Host, r.Host) {
		return false
	}
	if requestIsSecure(r) {
		return parsed.Scheme == "https"
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

func requestIsSecure(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func inside(root, candidate string) bool {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	candidateAbs, err := filepath.Abs(candidate)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(filepath.Clean(rootAbs), filepath.Clean(candidateAbs))
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func decodeJSON(r *http.Request, target any, maxBytes int64) error {
	r.Body = http.MaxBytesReader(nil, r.Body, maxBytes)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("request must contain one JSON value")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func parseBoundedInt(raw string, fallback, min, max int) int {
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < min {
		return fallback
	}
	if value > max {
		return max
	}
	return value
}
