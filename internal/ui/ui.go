// Package ui implements a server-rendered administration and user web UI for
// Meiling, styled after https://lain.rocks/ (dark background, green accents,
// monospace typography, CRT scanlines).
//
// The UI is split into two sections:
//   /ui        - user dashboard: browse buckets and objects, upload/download
//   /ui/admin  - administrator: IPFS nodes, IPFS pins
//
// Authentication is cookie-based (separate from the SigV4 S3 API). The UI
// reuses the S3 seed credentials: the account email and the S3 secret access
// key act as the login.
package ui

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"meiling/internal/db"
	"meiling/internal/metrics"
	"meiling/internal/store"
	"meiling/internal/validation"
)

// Session is an authenticated UI session.
type Session struct {
	ID        string
	AccountID uuid.UUID
	Email     string
	Role      string
	CreatedAt time.Time
}

// IsAdmin returns true if the session belongs to an admin account.
func (s *Session) IsAdmin() bool {
	return s.Role == db.RoleAdmin
}

// IsUser returns true if the session belongs to a user account.
func (s *Session) IsUser() bool {
	return s.Role == db.RoleUser || s.Role == ""
}

// memoryStore holds sessions in memory. Good enough for a single-process
// gateway; each session expires after uiSessionTTL.
type memoryStore struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

func newMemoryStore() *memoryStore {
	return &memoryStore{sessions: make(map[string]*Session)}
}

func (s *memoryStore) set(id string, sess *Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[id] = sess
}

func (s *memoryStore) get(id string) (*Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[id]
	return sess, ok
}

func (s *memoryStore) delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}

// Server hosts the web UI.
type Server struct {
	Pool     *pgxpool.Pool
	Store    store.ObjectStore
	Reg      *metrics.Registry
	Sessions *memoryStore
	Tmpl     map[string]*template.Template
	BaseURL  string // public host used for generating object URLs
	UISecret []byte // used to sign session cookies
}

// New creates a UI server and parses all HTML templates.
func New(cfgUISecret string, pool *pgxpool.Pool, st store.ObjectStore, reg *metrics.Registry, baseURL string) *Server {
	secret := []byte(cfgUISecret)
	if len(secret) == 0 {
		secret = []byte("meiling-ui-default-secret-change-me")
	}
	tmpl := make(map[string]*template.Template)
	templates := []string{
		"login.html",
		"dashboard.html",
		"bucket.html",
		"upload.html",
		"admin_dashboard.html",
		"admin_nodes.html",
		"admin_pins.html",
		"admin_accounts.html",
	}
	for _, name := range templates {
		data, err := os.ReadFile(path.Join("internal/ui/templates", name))
		if err != nil {
			slog.Error("ui: read template", "template", name, "error", err)
			os.Exit(1)
		}
		t, err := template.New(name).Parse(string(data))
		if err != nil {
			slog.Error("ui: parse template", "template", name, "error", err)
			os.Exit(1)
		}
		tmpl[name] = t
	}

	return &Server{
		Pool:     pool,
		Store:    st,
		Reg:      reg,
		Sessions: newMemoryStore(),
		Tmpl:     tmpl,
		BaseURL:  baseURL,
		UISecret: secret,
	}
}

// Route registers the UI handlers on the given mux. The UI is served under /ui.
func (s *Server) Route(mux *http.ServeMux) {
	mux.HandleFunc("/ui/login", s.handleLogin)
	mux.HandleFunc("/ui/logout", s.handleLogout)
	mux.Handle("/ui/static/", http.StripPrefix("/ui/static/", http.FileServer(http.Dir("internal/ui/static"))))
	mux.HandleFunc("/ui/", s.requireAuth(s.handleUI))
	mux.HandleFunc("/ui/bucket/create", s.requireAuth(s.handleBucketCreate))
	mux.HandleFunc("/ui/upload", s.requireAuth(s.handleUpload))
	mux.HandleFunc("/ui/admin/", s.requireAuth(s.handleAdmin))
}

// requireAuth wraps a session-handling handler so only authenticated sessions
// reach it; unauthenticated requests are redirected to the login page.
func (s *Server) requireAuth(next sessionHandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, ok := s.sessionFromRequest(r)
		if !ok {
			http.Redirect(w, r, "/ui/login", http.StatusFound)
			return
		}
		next(w, r, sess)
	}
}

// sessionHandlerFunc is an http.Handler that also receives the authenticated
// session.
type sessionHandlerFunc func(http.ResponseWriter, *http.Request, *Session)

// sessionFromRequest validates the session cookie and returns the session.
func (s *Server) sessionFromRequest(r *http.Request) (*Session, bool) {
	cookie, err := r.Cookie("meiling_session")
	if err != nil {
		return nil, false
	}
	id, ok := s.verifySessionCookie(cookie.Value)
	if !ok {
		return nil, false
	}
	sess, ok := s.Sessions.get(id)
	if !ok {
		return nil, false
	}
	if time.Since(sess.CreatedAt) > uiSessionTTL {
		s.Sessions.delete(id)
		return nil, false
	}
	return sess, true
}

const uiSessionTTL = 24 * time.Hour

// issueSession creates a session, signs it, and sets the cookie.
func (s *Server) issueSession(w http.ResponseWriter, sess *Session) {
	s.Sessions.set(sess.ID, sess)
	cookie := &http.Cookie{
		Name:     "meiling_session",
		Value:    s.signSession(sess),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   false, // set true when served over TLS
		MaxAge:   int(uiSessionTTL.Seconds()),
	}
	http.SetCookie(w, cookie)
}

// signSession returns a base64-encoded "id:signature" cookie value.
func (s *Server) signSession(sess *Session) string {
	mac := hmac.New(sha256.New, s.UISecret)
	mac.Write([]byte(sess.ID))
	sig := hex.EncodeToString(mac.Sum(nil))
	return base64.RawURLEncoding.EncodeToString([]byte(sess.ID + ":" + sig))
}

// verifySessionCookie parses and verifies a signed session cookie.
func (s *Server) verifySessionCookie(value string) (string, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		slog.Warn("ui: base64 decode failed", "error", err)
		return "", false
	}
	parts := strings.SplitN(string(raw), ":", 2)
	if len(parts) != 2 {
		slog.Warn("ui: no colon separator", "raw", string(raw))
		return "", false
	}
	id, sig := parts[0], parts[1]
	mac := hmac.New(sha256.New, s.UISecret)
	mac.Write([]byte(id))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		slog.Warn("ui: hmac mismatch", "id", id[:8])
		return "", false
	}
	return id, true
}

// clearSession removes the session cookie.
func (s *Server) clearSession(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("meiling_session")
	if err == nil {
		s.Sessions.delete(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "meiling_session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// handleLogin renders/login to the UI.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		s.render(w, r, "login.html", nil)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(r.Form.Get("email"))
	password := r.Form.Get("password")

	// The UI reuses the S3 seed credentials: account email matches the seed
	// account and the "password" is the S3 secret access key.
	acc, err := db.GetAccountByEmail(r.Context(), s.Pool, email)
	if err != nil {
		s.render(w, r, "login.html", map[string]interface{}{"error": "invalid credentials"})
		return
	}
	key, err := db.GetAccessKeyByAccount(r.Context(), s.Pool, acc.ID)
	if err != nil {
		slog.Error("ui: get access key", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if key.SecretAccessKey != password {
		s.render(w, r, "login.html", map[string]interface{}{"error": "invalid credentials"})
		return
	}

	sess := &Session{
		ID:        newSessionID(),
		AccountID: acc.ID,
		Email:     acc.Email,
		Role:      acc.Role,
		CreatedAt: time.Now(),
	}
	s.issueSession(w, sess)
	http.Redirect(w, r, "/ui/", http.StatusFound)
}

func newSessionID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString([]byte(time.Now().String()))
	}
	return hex.EncodeToString(b)
}

// handleLogout clears the session.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.clearSession(w, r)
	http.Redirect(w, r, "/ui/login", http.StatusFound)
}

// handleUI serves the user dashboard and dispatches to bucket/object routes.
func (s *Server) handleUI(w http.ResponseWriter, r *http.Request, sess *Session) {
	switch {
	case r.URL.Path == "/ui/":
		s.handleDashboard(w, r, sess)
	case strings.HasPrefix(r.URL.Path, "/ui/bucket/"):
		s.handleBucket(w, r, sess)
	case strings.HasPrefix(r.URL.Path, "/ui/object/"):
		s.handleObject(w, r, sess)
	default:
		http.NotFound(w, r)
	}
}

// bucketAndKeyFromUIPath splits /ui/bucket/<bucket>[/<key>] into bucket, key.
func bucketAndKeyFromUIPath(r *http.Request, prefix string) (bucket, key string, ok bool) {
	rel := strings.TrimPrefix(r.URL.Path, prefix)
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		return "", "", false
	}
	parts := strings.SplitN(rel, "/", 2)
	bucket = parts[0]
	if len(parts) == 1 {
		return bucket, "", true
	}
	return bucket, parts[1], true
}

// handleDashboard lists buckets for the account.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request, sess *Session) {
	buckets, err := db.ListBuckets(r.Context(), s.Pool, sess.AccountID)
	if err != nil {
		slog.Error("ui: list buckets", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, r, "dashboard.html", map[string]interface{}{
		"Account":  sess.Email,
		"Buckets":  buckets,
		"BaseURL":  s.BaseURL,
	})
}

// handleBucket lists objects in a bucket.
func (s *Server) handleBucket(w http.ResponseWriter, r *http.Request, sess *Session) {
	bucket, _, ok := bucketAndKeyFromUIPath(r, "/ui/bucket/")
	if !ok || bucket == "" {
		http.Redirect(w, r, "/ui/", http.StatusFound)
		return
	}
	bucketID, err := db.GetBucketIDByName(r.Context(), s.Pool, bucket)
	if err != nil {
		if err == db.ErrNotFound {
			http.Error(w, "bucket not found", http.StatusNotFound)
		} else {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	objs, err := db.ListObjects(r.Context(), s.Pool, bucketID, "", "", 1000)
	if err != nil {
		slog.Error("ui: list objects", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, r, "bucket.html", map[string]interface{}{
		"Account":  sess.Email,
		"Bucket":   bucket,
		"BucketID": bucketID,
		"Objects":  objs,
		"BaseURL":  s.BaseURL,
	})
}

// handleBucketCreate creates a new bucket.
func (s *Server) handleBucketCreate(w http.ResponseWriter, r *http.Request, sess *Session) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	bucket := strings.TrimSpace(r.Form.Get("bucket"))
	if err := validation.ValidateBucketName(bucket); err != nil {
		s.render(w, r, "dashboard.html", map[string]interface{}{
			"Account": sess.Email,
			"Error":   err.Error(),
		})
		return
	}
	if _, err := db.GetBucketIDByName(r.Context(), s.Pool, bucket); err == nil {
		s.render(w, r, "dashboard.html", map[string]interface{}{
			"Account": sess.Email,
			"Error":   "bucket already exists",
		})
		return
	}
	if _, err := db.CreateBucket(r.Context(), s.Pool, sess.AccountID, bucket); err != nil {
		slog.Error("ui: create bucket", "error", err)
		s.render(w, r, "dashboard.html", map[string]interface{}{
			"Account": sess.Email,
			"Error":   "could not create bucket",
		})
		return
	}
	http.Redirect(w, r, "/ui/bucket/"+bucket, http.StatusFound)
}

// handleObject serves an object's content for download or inline viewing.
func (s *Server) handleObject(w http.ResponseWriter, r *http.Request, sess *Session) {
	bucket, key, ok := bucketAndKeyFromUIPath(r, "/ui/object/")
	if !ok || bucket == "" || key == "" {
		http.NotFound(w, r)
		return
	}
	bucketID, err := db.GetBucketIDByName(r.Context(), s.Pool, bucket)
	if err != nil {
		if err == db.ErrNotFound {
			http.NotFound(w, r)
		} else {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	obj, err := db.GetObjectByKey(r.Context(), s.Pool, bucketID, key)
	if err != nil {
		if err == db.ErrNotFound {
			http.NotFound(w, r)
		} else {
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
		return
	}
	rc, err := s.Store.Open(r.Context(), bucket, key)
	if err != nil {
		http.Error(w, "could not read object", http.StatusInternalServerError)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", obj.ContentType.String)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", obj.Size))
	w.Header().Set("ETag", "\""+obj.ETag+"\"")
	w.Header().Set("Accept-Ranges", "bytes")
	_, _ = io.Copy(w, rc)
}

// handleUpload renders the upload form and processes POST uploads.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request, sess *Session) {
	if r.Method == http.MethodGet {
		s.render(w, r, "upload.html", map[string]interface{}{
			"Account": sess.Email,
			"BaseURL": s.BaseURL,
		})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(1024 * 1024 * 1024); err != nil {
		s.render(w, r, "upload.html", map[string]interface{}{
			"Account": sess.Email,
			"BaseURL": s.BaseURL,
			"Error":   "upload too large",
		})
		return
	}
	bucket := strings.TrimSpace(r.FormValue("bucket"))
	key := strings.TrimSpace(r.FormValue("key"))
	if bucket == "" || key == "" {
		s.render(w, r, "upload.html", map[string]interface{}{
			"Account": sess.Email,
			"BaseURL": s.BaseURL,
			"Error":   "bucket and key are required",
		})
		return
	}
	f, fh, err := r.FormFile("file")
	if err != nil {
		s.render(w, r, "upload.html", map[string]interface{}{
			"Account": sess.Email,
			"BaseURL": s.BaseURL,
			"Error":   "no file provided",
		})
		return
	}
	defer f.Close()
	contentType := fh.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	bucketID, err := db.GetBucketIDByName(r.Context(), s.Pool, bucket)
	if err != nil {
		s.render(w, r, "upload.html", map[string]interface{}{
			"Account": sess.Email,
			"BaseURL": s.BaseURL,
			"Error":   "bucket not found",
		})
		return
	}
	userMetadata := map[string]string{}
	if v := strings.TrimSpace(r.FormValue("x-amz-meta-title")); v != "" {
		userMetadata["title"] = v
	}
	info, err := s.Store.Put(r.Context(), bucket, key, f, contentType)
	if err != nil {
		slog.Error("ui: put object", "error", err)
		s.render(w, r, "upload.html", map[string]interface{}{
			"Account": sess.Email,
			"BaseURL": s.BaseURL,
			"Error":   "could not store object",
		})
		return
	}
	if _, err := db.CreateObject(r.Context(), s.Pool, bucketID, key, info.Key, info.Size, contentType, info.ETag, "", "none", userMetadata); err != nil {
		slog.Error("ui: record object", "error", err)
		s.render(w, r, "upload.html", map[string]interface{}{
			"Account": sess.Email,
			"BaseURL": s.BaseURL,
			"Error":   "could not record object",
		})
		return
	}
	http.Redirect(w, r, "/ui/bucket/"+bucket, http.StatusFound)
}

// handleAdmin serves the admin section.
func (s *Server) handleAdmin(w http.ResponseWriter, r *http.Request, sess *Session) {
	if !sess.IsAdmin() {
		http.Error(w, "admin access required", http.StatusForbidden)
		return
	}
	switch {
	case r.URL.Path == "/ui/admin/":
		s.handleAdminDashboard(w, r, sess)
	case r.URL.Path == "/ui/admin/nodes":
		s.handleAdminNodes(w, r, sess)
	case strings.HasPrefix(r.URL.Path, "/ui/admin/nodes/"):
		s.handleAdminNodeAction(w, r, sess)
	case r.URL.Path == "/ui/admin/pins":
		s.handleAdminPins(w, r, sess)
	case strings.HasPrefix(r.URL.Path, "/ui/admin/pins/"):
		s.handleAdminPinDelete(w, r, sess)
	case r.URL.Path == "/ui/admin/accounts":
		s.handleAdminAccounts(w, r, sess)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleAdminDashboard(w http.ResponseWriter, r *http.Request, sess *Session) {
	var buckets []db.Bucket
	var err error
	if sess.IsAdmin() {
		buckets, err = db.ListAllBuckets(r.Context(), s.Pool)
	} else {
		buckets, err = db.ListBuckets(r.Context(), s.Pool, sess.AccountID)
	}
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	nodes, err := db.ListNodes(r.Context(), s.Pool)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, r, "admin_dashboard.html", map[string]interface{}{
		"Account": sess.Email,
		"Buckets": buckets,
		"Nodes":   nodes,
		"BaseURL": s.BaseURL,
		"IsAdmin": sess.IsAdmin(),
	})
}

func (s *Server) handleAdminNodes(w http.ResponseWriter, r *http.Request, sess *Session) {
	nodes, err := db.ListNodes(r.Context(), s.Pool)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, r, "admin_nodes.html", map[string]interface{}{
		"Account": sess.Email,
		"Nodes":   nodes,
		"BaseURL": s.BaseURL,
	})
}

func (s *Server) handleAdminNodeAction(w http.ResponseWriter, r *http.Request, sess *Session) {
	rel := strings.TrimPrefix(r.URL.Path, "/ui/admin/nodes/")
	rel = strings.TrimSuffix(rel, "/")
	parts := strings.SplitN(rel, "/", 2)
	nodeIDStr := parts[0]
	action := ""
	if len(parts) == 2 {
		action = parts[1]
	}
	nodeID, err := uuid.Parse(nodeIDStr)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case r.Method == http.MethodPost && action == "delete":
		if err := db.DeleteNode(r.Context(), s.Pool, nodeID); err != nil {
			slog.Error("ui: delete node", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/ui/admin/nodes", http.StatusFound)
	case r.Method == http.MethodPost && action == "add":
		endpoint := strings.TrimSpace(r.FormValue("endpoint"))
		peerID := strings.TrimSpace(r.FormValue("peerid"))
		if endpoint == "" {
			s.render(w, r, "admin_nodes.html", map[string]interface{}{
				"Account": sess.Email,
				"Nodes":   nil,
				"Error":   "endpoint is required",
				"BaseURL": s.BaseURL,
			})
			return
		}
		if _, err := db.CreateNode(r.Context(), s.Pool, endpoint, peerID); err != nil {
			slog.Error("ui: create node", "error", err)
			s.render(w, r, "admin_nodes.html", map[string]interface{}{
				"Account": sess.Email,
				"Nodes":   nil,
				"Error":   "could not register node",
				"BaseURL": s.BaseURL,
			})
			return
		}
		http.Redirect(w, r, "/ui/admin/nodes", http.StatusFound)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleAdminPins(w http.ResponseWriter, r *http.Request, sess *Session) {
	var pins []db.IPFSPin
	var err error
	if sess.IsAdmin() {
		pins, _, err = db.ListPins(r.Context(), s.Pool, uuid.Nil, nil, time.Time{}, time.Time{}, 100)
	} else {
		pins, _, err = db.ListPins(r.Context(), s.Pool, sess.AccountID, nil, time.Time{}, time.Time{}, 100)
	}
	if err != nil {
		slog.Error("ui: list pins", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, r, "admin_pins.html", map[string]interface{}{
		"Account": sess.Email,
		"Pins":    pins,
		"BaseURL": s.BaseURL,
		"IsAdmin": sess.IsAdmin(),
	})
}

func (s *Server) handleAdminPinDelete(w http.ResponseWriter, r *http.Request, sess *Session) {
	rel := strings.TrimPrefix(r.URL.Path, "/ui/admin/pins/")
	rel = strings.TrimSuffix(rel, "/")
	if rel == "" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if err := db.DeletePin(r.Context(), s.Pool, sess.AccountID, rel); err != nil {
		slog.Error("ui: delete pin", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/ui/admin/pins", http.StatusFound)
}

func (s *Server) handleAdminAccounts(w http.ResponseWriter, r *http.Request, sess *Session) {
	if !sess.IsAdmin() {
		http.Error(w, "admin access required", http.StatusForbidden)
		return
	}
	if r.Method == http.MethodPost {
		s.handleAdminAccountCreate(w, r, sess)
		return
	}
	accounts, err := db.ListAccounts(r.Context(), s.Pool)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.render(w, r, "admin_accounts.html", map[string]interface{}{
		"Account":  sess.Email,
		"Accounts": accounts,
		"BaseURL":  s.BaseURL,
		"IsAdmin":  sess.IsAdmin(),
	})
}

func (s *Server) handleAdminAccountCreate(w http.ResponseWriter, r *http.Request, sess *Session) {
	if !sess.IsAdmin() {
		http.Error(w, "admin access required", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(r.Form.Get("email"))
	role := strings.TrimSpace(r.Form.Get("role"))
	if email == "" || role == "" {
		s.render(w, r, "admin_accounts.html", map[string]interface{}{
			"Account": sess.Email,
			"Error":   "email and role are required",
			"BaseURL": s.BaseURL,
			"IsAdmin": sess.IsAdmin(),
		})
		return
	}
	if role != db.RoleAdmin && role != db.RoleUser {
		s.render(w, r, "admin_accounts.html", map[string]interface{}{
			"Account": sess.Email,
			"Error":   "role must be 'admin' or 'user'",
			"BaseURL": s.BaseURL,
			"IsAdmin": sess.IsAdmin(),
		})
		return
	}
	_, err := db.CreateAccount(r.Context(), s.Pool, email, role)
	if err != nil {
		slog.Error("ui: create account", "error", err)
		s.render(w, r, "admin_accounts.html", map[string]interface{}{
			"Account": sess.Email,
			"Error":   "could not create account",
			"BaseURL": s.BaseURL,
			"IsAdmin": sess.IsAdmin(),
		})
		return
	}
	http.Redirect(w, r, "/ui/admin/accounts", http.StatusFound)
}

// render executes a named template with the given data.
func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, data map[string]interface{}) {
	if data == nil {
		data = make(map[string]interface{})
	}
	if _, ok := data["Account"]; !ok {
		if sess, ok := s.sessionFromRequest(r); ok {
			data["Account"] = sess.Email
		}
	}
	if _, ok := data["BaseURL"]; !ok {
		data["BaseURL"] = s.BaseURL
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	t, ok := s.Tmpl[name]
	if !ok {
		http.Error(w, "template not found", http.StatusInternalServerError)
		return
	}
	if err := t.ExecuteTemplate(w, name, data); err != nil {
		slog.Error("ui: render template", "template", name, "error", err)
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}
