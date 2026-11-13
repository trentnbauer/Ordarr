package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"html/template"
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
	"unicode/utf8"

	"github.com/trentnbauer/Ordarr/internal/plex"
	"github.com/trentnbauer/Ordarr/internal/secret"
	"github.com/trentnbauer/Ordarr/internal/store"
	"golang.org/x/crypto/bcrypt"
)

//go:embed templates/*.html static/*
var assets embed.FS

const sessionCookie = "ordarr_session"
const csrfCookie = "ordarr_csrf"

type Config struct {
	Address, DataDir string
	SecureCookies    bool
}

func LoadConfig() (Config, error) {
	c := Config{Address: "127.0.0.1:8080", DataDir: "data"}
	if v := os.Getenv("ORDARR_ADDR"); v != "" {
		c.Address = v
	}
	if v := os.Getenv("ORDARR_DATA_DIR"); v != "" {
		c.DataDir = v
	}
	if _, _, err := net.SplitHostPort(c.Address); err != nil {
		return c, err
	}
	if v := os.Getenv("ORDARR_SECURE_COOKIES"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return c, err
		}
		c.SecureCookies = b
	}
	p, err := filepath.Abs(c.DataDir)
	c.DataDir = p
	return c, err
}

type App struct {
	wake        chan struct{}
	workersDone chan struct{}
	accountMu   sync.Mutex
	accounts    *plex.AccountClient
	plex        *plex.Client
	vault       *secret.Vault
	store       *store.Store
	config      Config
	logger      *slog.Logger
	templates   *template.Template
	dummy       []byte
	mu          sync.Mutex
	attempts    map[string]attempt
}
type attempt struct {
	count int
	until time.Time
}
type page struct {
	Acquisition                                          *acquisitionView
	Source                                               *sourceView
	Destinations                                         []store.Destination
	SyncRuns                                             []store.SyncRun
	Jobs                                                 []store.Job
	MatchView                                            *matchView
	LibraryMedia                                         *libraryMediaView
	Title, Mode, CSRF, Error, Username, Address, DataDir string
	Secure                                               bool
	Collections                                          []store.CollectionSummary
	Collection                                           store.Collection
	Query, Filter, Prev, Next, EntryID                   string
	Input                                                entryInput
	PlexConnections                                      []plexView
	Notice                                               string
	Account                                              *accountView
	AuthAttempt                                          *authView
	AccountServers                                       []accountServerView
}

func New(s *store.Store, c Config, l *slog.Logger) (*App, error) {
	t, err := template.New("pages").Funcs(template.FuncMap{"inc": func(n int) int { return n + 1 }, "dec": func(n int) int { return n - 1 }}).ParseFS(assets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	dummy, err := bcrypt.GenerateFromPassword([]byte("unused-timing-padding"), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	hasCredentials, err := s.HasPlexCredentials(context.Background())
	if err != nil {
		return nil, err
	}
	vault, err := secret.Open(c.DataDir, hasCredentials)
	if err != nil {
		return nil, err
	}
	return &App{wake: make(chan struct{}, 1), workersDone: make(chan struct{}), accounts: plex.NewAccountClient(nil), plex: plex.New(), vault: vault, store: s, config: c, logger: l, templates: t, dummy: dummy, attempts: map[string]attempt{}}, nil
}
func randomToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func (a *App) cookie(w http.ResponseWriter, name, value string, age int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, Secure: a.config.SecureCookies, SameSite: http.SameSiteStrictMode, MaxAge: age})
}
func (a *App) Handler() http.Handler {
	m := http.NewServeMux()
	static, _ := fs.Sub(assets, "static")
	m.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	m.HandleFunc("GET /healthz", a.health)
	m.HandleFunc("GET /setup", a.setup)
	m.HandleFunc("POST /setup", a.setup)
	m.HandleFunc("GET /login", a.login)
	m.HandleFunc("POST /login", a.login)
	m.HandleFunc("POST /logout", a.logout)
	m.HandleFunc("GET /{$}", a.protected(a.listCollections))
	m.HandleFunc("GET /settings", a.protected(a.settings))
	m.HandleFunc("GET /collections/new", a.protected(a.newCollection))
	m.HandleFunc("POST /collections", a.protected(a.newCollection))
	m.HandleFunc("GET /collections/{id}", a.protected(a.collectionDetail))
	m.HandleFunc("POST /collections/{id}", a.protected(a.changeCollection))
	m.HandleFunc("GET /collections/{id}/entries/{entry}/edit", a.protected(a.collectionDetail))
	m.HandleFunc("GET /settings/plex", a.protected(a.plexSettings))
	m.HandleFunc("POST /settings/plex", a.protected(a.plexSave))
	m.HandleFunc("POST /settings/plex/{id}/check", a.protected(a.plexCheck))
	m.HandleFunc("POST /settings/plex/{id}/delete", a.protected(a.plexDelete))
	m.HandleFunc("POST /settings/plex/account/start", a.protected(a.accountStart))
	m.HandleFunc("GET /settings/plex/account/attempt/{attempt}", a.protected(a.accountAttempt))
	m.HandleFunc("POST /settings/plex/account/attempt/{attempt}", a.protected(a.accountPoll))
	m.HandleFunc("POST /settings/plex/account/attempt/{attempt}/cancel", a.protected(a.accountCancel))
	m.HandleFunc("POST /settings/plex/account/refresh", a.protected(a.accountRefresh))
	m.HandleFunc("POST /settings/plex/account/disconnect", a.protected(a.accountDisconnect))
	m.HandleFunc("POST /settings/plex/account/connect", a.protected(a.accountConnect))
	m.HandleFunc("GET /settings/plex/{id}/libraries/{library}/media", a.protected(a.libraryMedia))
	m.HandleFunc("POST /settings/plex/{id}/libraries/{library}/media", a.protected(a.fetchLibraryMedia))
	m.HandleFunc("GET /jobs", a.protected(a.jobsPage))
	m.HandleFunc("POST /settings/plex/{id}/scan", a.protected(a.queueScan))
	m.HandleFunc("GET /collections/{id}/matches/{connection}", a.protected(a.matchesPage))
	m.HandleFunc("POST /collections/{id}/matches/{connection}", a.protected(a.manualMatch))
	m.HandleFunc("POST /collections/{id}/destinations", a.protected(a.createDestination))
	m.HandleFunc("POST /destinations/{destination}/sync", a.protected(a.queueSync))
	m.HandleFunc("POST /destinations/{destination}/policy", a.protected(a.destinationPolicy))
	m.HandleFunc("POST /destinations/{destination}/recreate", a.protected(a.recreateDestination))
	m.HandleFunc("POST /sync-runs/{run}/approve", a.protected(a.approveSync))
	m.HandleFunc("GET /import", a.protected(a.portablePage))
	m.HandleFunc("POST /import", a.protected(a.importFile))
	m.HandleFunc("POST /subscriptions", a.protected(a.subscribe))
	m.HandleFunc("GET /collections/{id}/export", a.protected(a.exportFile))
	m.HandleFunc("GET /collections/{id}/source", a.protected(a.sourcePage))
	m.HandleFunc("POST /collections/{id}/source", a.protected(a.updateSource))
	m.HandleFunc("GET /settings/acquisition", a.protected(a.acquisitionPage))
	m.HandleFunc("POST /settings/acquisition", a.protected(a.connectArr))
	m.HandleFunc("POST /settings/acquisition/{id}/policy", a.protected(a.arrPolicy))
	m.HandleFunc("POST /settings/acquisition/{id}/refresh", a.protected(a.arrRefresh))
	m.HandleFunc("POST /settings/acquisition/{id}/requests", a.protected(a.proposeRequest))
	m.HandleFunc("POST /settings/acquisition/{id}/automation", a.protected(a.collectionAutomation))
	m.HandleFunc("POST /settings/acquisition/{id}/delete", a.protected(a.deleteArr))
	m.HandleFunc("POST /requests/{id}/approve", a.protected(a.approveRequest))
	return a.security(m)
}
func (a *App) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; script-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
		if r.Method == "POST" {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				scheme := "http"
				if a.config.SecureCookies || r.TLS != nil {
					scheme = "https"
				}
				if err != nil || u.Host != r.Host || u.Scheme != scheme {
					http.Error(w, "Invalid request origin", 403)
					return
				}
			}
			limit := int64(8192)
			if r.URL.Path == "/import" {
				limit = 4 << 20
			}
			if strings.HasPrefix(r.URL.Path, "/collections") || strings.HasPrefix(r.URL.Path, "/settings/plex") {
				limit = 128 << 10
			}
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			var formErr error
			if r.URL.Path == "/import" && strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
				formErr = r.ParseMultipartForm(2 << 20)
				if r.MultipartForm != nil {
					defer r.MultipartForm.RemoveAll()
				}
			} else {
				formErr = r.ParseForm()
			}
			if formErr != nil {
				http.Error(w, "Invalid or oversized form", 400)
				return
			}
			c, err := r.Cookie(csrfCookie)
			token := r.PostForm.Get("csrf")
			if err != nil || len(c.Value) != 64 || subtle.ConstantTimeCompare([]byte(c.Value), []byte(token)) != 1 {
				http.Error(w, "Invalid form token. Reload and try again.", 403)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
func (a *App) render(w http.ResponseWriter, r *http.Request, status int, p page) {
	// Reuse the browser's token so forms in other tabs remain valid.
	c, err := r.Cookie(csrfCookie)
	if err == nil && len(c.Value) == 64 {
		p.CSRF = c.Value
	} else {
		p.CSRF = randomToken()
		a.cookie(w, csrfCookie, p.CSRF, 86400)
	}
	var body bytes.Buffer
	if err = a.templates.ExecuteTemplate(&body, "page.html", p); err != nil {
		a.fail(w, "template rendering failed")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write(body.Bytes())
}
func (a *App) fail(w http.ResponseWriter, event string) {
	a.logger.Error(event)
	http.Error(w, "An internal error occurred", 500)
}
func (a *App) health(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DB.PingContext(r.Context()); err != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte("{\"status\":\"ok\"}\n"))
}
func (a *App) protected(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, _, err := a.store.Admin(r.Context())
		if errors.Is(err, sql.ErrNoRows) {
			http.Redirect(w, r, "/setup", 303)
			return
		}
		if err != nil {
			a.fail(w, "administrator lookup failed")
			return
		}
		c, err := r.Cookie(sessionCookie)
		if err != nil || len(c.Value) != 64 {
			http.Redirect(w, r, "/login", 303)
			return
		}
		ok, err := a.store.Authenticated(r.Context(), c.Value)
		if err != nil {
			a.fail(w, "session lookup failed")
			return
		}
		if !ok {
			a.cookie(w, sessionCookie, "", -1)
			http.Redirect(w, r, "/login", 303)
			return
		}
		next(w, r)
	}
}
func (a *App) allowed(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	for k, v := range a.attempts {
		if !now.Before(v.until) {
			delete(a.attempts, k)
		}
	}
	v, exists := a.attempts[host]
	if !exists {
		if len(a.attempts) >= 4096 {
			return false
		}
		v = attempt{until: now.Add(time.Minute)}
	}
	v.count++
	a.attempts[host] = v
	return v.count <= 5
}
func (a *App) setup(w http.ResponseWriter, r *http.Request) {
	_, _, err := a.store.Admin(r.Context())
	if err == nil {
		http.Redirect(w, r, "/login", 303)
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		a.fail(w, "administrator lookup failed")
		return
	}
	p := page{Title: "Welcome to Ordarr", Mode: "setup"}
	if r.Method == "GET" {
		a.render(w, r, 200, p)
		return
	}
	if !a.allowed(r) {
		http.Error(w, "Too many attempts. Try again in a minute.", 429)
		return
	}
	name := strings.TrimSpace(r.PostForm.Get("username"))
	password := r.PostForm.Get("password")
	if !utf8.ValidString(name) || len([]rune(name)) < 1 || len([]rune(name)) > 64 || strings.ContainsAny(name, "\r\n\t") || len(password) < 12 || len(password) > 72 {
		p.Error = "Use a username of 1–64 characters and a password of 12–72 bytes."
		a.render(w, r, 400, p)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		a.fail(w, "password hashing failed")
		return
	}
	if err = a.store.CreateAdmin(r.Context(), name, hash); err != nil {
		if _, _, lookup := a.store.Admin(r.Context()); lookup == nil {
			http.Redirect(w, r, "/login", 303)
			return
		}
		a.fail(w, "administrator creation failed")
		return
	}
	a.logger.Info("administrator created")
	a.signIn(w, r)
}
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	name, hash, err := a.store.Admin(r.Context())
	if errors.Is(err, sql.ErrNoRows) {
		http.Redirect(w, r, "/setup", 303)
		return
	}
	if err != nil {
		a.fail(w, "administrator lookup failed")
		return
	}
	p := page{Title: "Sign in", Mode: "login"}
	if r.Method == "GET" {
		a.render(w, r, 200, p)
		return
	}
	if !a.allowed(r) {
		http.Error(w, "Too many attempts. Try again in a minute.", 429)
		return
	}
	check := hash
	if r.PostForm.Get("username") != name {
		check = a.dummy
	}
	valid := bcrypt.CompareHashAndPassword(check, []byte(r.PostForm.Get("password"))) == nil
	if !valid || r.PostForm.Get("username") != name {
		p.Error = "Invalid username or password."
		a.render(w, r, 401, p)
		return
	}
	a.signIn(w, r)
}
func (a *App) signIn(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err = a.store.DeleteSession(r.Context(), c.Value); err != nil {
			a.fail(w, "session revocation failed")
			return
		}
	}
	token := randomToken()
	if err := a.store.AddSession(r.Context(), token, time.Now().Add(24*time.Hour)); err != nil {
		a.fail(w, "session creation failed")
		return
	}
	a.cookie(w, sessionCookie, token, 86400)
	http.Redirect(w, r, "/", 303)
}
func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err = a.store.DeleteSession(r.Context(), c.Value); err != nil {
			a.fail(w, "session revocation failed")
			return
		}
	}
	a.cookie(w, sessionCookie, "", -1)
	http.Redirect(w, r, "/login", 303)
}
func (a *App) settings(w http.ResponseWriter, r *http.Request) {
	name, _, err := a.store.Admin(r.Context())
	if err != nil {
		a.fail(w, "administrator lookup failed")
		return
	}
	a.render(w, r, 200, page{Title: "Settings", Mode: "settings", Username: name, Address: a.config.Address, DataDir: a.config.DataDir, Secure: a.config.SecureCookies})
}
