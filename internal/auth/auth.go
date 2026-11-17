// Package auth gestiona contraseñas, sesiones por cookie, roles y protección CSRF.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"sboe/internal/store"
)

const (
	CookieName     = "sboe_session"
	SessionTTL     = 30 * 24 * time.Hour
	MinPasswordLen = 8
	CSRFHeader     = "X-CSRF-Token"
	CSRFField      = "csrf_token"
)

var (
	ErrBadCredentials = errors.New("email o contraseña incorrectos")
	ErrTooManyTries   = errors.New("demasiados intentos; espera unos minutos")
	ErrWeakPassword   = errors.New("la contraseña debe tener al menos 8 caracteres")
)

func HashPassword(pw string) (string, error) {
	if len(pw) < MinPasswordLen {
		return "", ErrWeakPassword
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(h), err
}

var dummyHash = sync.OnceValue(func() []byte {
	h, _ := bcrypt.GenerateFromPassword([]byte("dummy-password"), bcrypt.DefaultCost)
	return h
})

func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// HashToken: en BD solo se guarda el hash del token de sesión.
func HashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

type ctxKey int

const (
	userKey ctxKey = iota
	sessKey
)

// UserFrom devuelve el usuario autenticado (ok=false si no hay sesión).
func UserFrom(ctx context.Context) (store.User, bool) {
	u, ok := ctx.Value(userKey).(store.User)
	return u, ok
}

// SessionFrom devuelve la sesión actual.
func SessionFrom(ctx context.Context) (store.Session, bool) {
	s, ok := ctx.Value(sessKey).(store.Session)
	return s, ok
}

// Auth gestiona las sesiones.
type Auth struct {
	st     *store.Store
	secure bool
	lim    *limiter
}

func New(st *store.Store, secureCookie bool) *Auth {
	return &Auth{st: st, secure: secureCookie, lim: &limiter{hits: map[string][]time.Time{}}}
}

// Login valida credenciales, crea la sesión y pone la cookie.
func (a *Auth) Login(w http.ResponseWriter, r *http.Request, email, password string) (store.User, error) {
	key := strings.ToLower(strings.TrimSpace(email))
	if !a.lim.allow(key) {
		return store.User{}, ErrTooManyTries
	}
	u, err := a.st.UserByEmail(r.Context(), email)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// mismo coste que con usuario existente, para no revelar qué emails existen
			bcrypt.CompareHashAndPassword(dummyHash(), []byte(password))
			a.lim.fail(key)
			return store.User{}, ErrBadCredentials
		}
		return store.User{}, err
	}
	if !u.Active || !CheckPassword(u.PasswordHash, password) {
		a.lim.fail(key)
		return store.User{}, ErrBadCredentials
	}
	a.lim.reset(key)
	token := randomToken()
	sess := store.Session{TokenHash: HashToken(token), UserID: u.ID, CSRFToken: randomToken(), ExpiresAt: time.Now().Add(SessionTTL)}
	if err := a.st.CreateSession(r.Context(), sess); err != nil {
		return store.User{}, err
	}
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: token, Path: "/", Expires: sess.ExpiresAt,
		HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteLaxMode,
	})
	return u, nil
}

// Logout borra la sesión actual.
func (a *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	if s, ok := SessionFrom(r.Context()); ok {
		a.st.DeleteSession(r.Context(), s.TokenHash)
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteLaxMode})
}

// Load añade usuario y sesión al contexto si la cookie es válida, y aplica CSRF a las peticiones con sesión.
func (a *Auth) Load(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(CookieName)
		if err == nil && c.Value != "" {
			sess, u, err := a.st.SessionUser(r.Context(), HashToken(c.Value))
			if err == nil {
				ctx := context.WithValue(r.Context(), userKey, u)
				ctx = context.WithValue(ctx, sessKey, sess)
				r = r.WithContext(ctx)
			}
		}
		if !safeMethod(r.Method) {
			if !a.checkCSRF(r) {
				http.Error(w, "token CSRF inválido; recarga la página", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func (a *Auth) checkCSRF(r *http.Request) bool {
	sess, ok := SessionFrom(r.Context())
	if !ok {
		// Sin sesión (p. ej. POST /login): exigimos mismo origen.
		return sameOrigin(r)
	}
	tok := r.Header.Get(CSRFHeader)
	if tok == "" {
		tok = r.PostFormValue(CSRFField)
	}
	return tok != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(sess.CSRFToken)) == 1
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		ref := r.Header.Get("Referer")
		if ref == "" {
			return true // clientes sin Origin/Referer (curl); la cookie SameSite=Lax cubre el resto
		}
		origin = ref
	}
	o := strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://")
	if i := strings.IndexByte(o, '/'); i >= 0 {
		o = o[:i]
	}
	return o == r.Host
}

// RequireUser redirige al login si no hay sesión.
func RequireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := UserFrom(r.Context()); !ok {
			if r.Header.Get("HX-Request") != "" {
				w.Header().Set("HX-Redirect", "/login")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login?next="+urlEscape(r.URL.RequestURI()), http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAdmin exige rol admin.
func RequireAdmin(next http.Handler) http.Handler {
	return RequireUser(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, _ := UserFrom(r.Context())
		if !u.IsAdmin() {
			http.Error(w, "solo administradores", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func urlEscape(s string) string {
	return strings.NewReplacer("%", "%25", "&", "%26", "?", "%3F", "#", "%23", " ", "%20", "=", "%3D").Replace(s)
}

// limiter: máx. 10 intentos fallidos por email cada 15 minutos.
type limiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

const (
	limWindow = 15 * time.Minute
	limMax    = 10
)

func (l *limiter) recent(key string) []time.Time {
	cut := time.Now().Add(-limWindow)
	var keep []time.Time
	for _, t := range l.hits[key] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	l.hits[key] = keep
	return keep
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(key)) < limMax
}

func (l *limiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.hits[key] = append(l.recent(key), time.Now())
}

func (l *limiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, key)
}
