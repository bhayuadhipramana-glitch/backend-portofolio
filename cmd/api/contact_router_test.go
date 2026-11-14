package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/handler"
	authmw "github.com/bhayuadhipramana-glicth/backend-portofolio/internal/middleware"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/usecase"
)

type memContacts struct {
	mu   sync.Mutex
	rows []domain.Contact
	ips  []string
}

func (m *memContacts) CreateWithOutbox(_ context.Context, c *domain.Contact) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c.ID = "00000000-0000-4000-8000-000000000001"
	m.rows = append(m.rows, *c)
	m.ips = append(m.ips, c.IPHash)
	return nil
}

func (m *memContacts) List(context.Context, domain.Page) ([]domain.Contact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]domain.Contact(nil), m.rows...), nil
}

func (m *memContacts) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.rows)
}

// cookieAuth accepts the session token "admin-token" and nothing else.
type cookieAuth struct{}

func (cookieAuth) Authenticate(_ context.Context, token string) (*domain.Session, error) {
	if token == "admin-token" {
		return &domain.Session{GithubID: 1, Login: "admin"}, nil
	}
	return nil, domain.ErrUnauthorized
}

// routerEnv wraps a router so tests can issue requests tersely.
type routerEnv struct {
	router http.Handler
}

type f9Env struct {
	routerEnv
	contacts *memContacts
}

func newF9Router(t *testing.T) f9Env {
	t.Helper()
	contacts := &memContacts{}

	cuc, err := usecase.NewContactUsecase(contacts)
	if err != nil {
		t.Fatal(err)
	}
	router := newRouter(routerDeps{
		db:             fakePinger{},
		appEnv:         "local",
		contactHandler: handler.NewContactHandler(cuc),
		contactLimiter: authmw.NewWindowLimiter(contactPerIP, contactWindow).Middleware,
		authenticator:  cookieAuth{},
		allowedOrigins: []string{"http://localhost:3000"},
	})
	return f9Env{routerEnv: routerEnv{router: router}, contacts: contacts}
}

func (e routerEnv) do(method, path, body, remoteAddr string, mutate func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func asAdmin(r *http.Request) { r.AddCookie(&http.Cookie{Name: "session", Value: "admin-token"}) }

const contactBody = `{"name":"Ada","email":"ada@example.com","subject":"Hello","message":"I would like to talk about a project."}`

func TestContactHoneypotIsDroppedSilently(t *testing.T) {
	e := newF9Router(t)
	body := `{"name":"Bot","email":"bot@example.com","subject":"Buy","message":"cheap pills for you","website":"http://spam.example"}`

	rec := e.do(http.MethodPost, "/api/v1/contact", body, "198.51.100.1:1", nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("honeypot answered %d, want 202", rec.Code)
	}
	if e.contacts.count() != 0 {
		t.Fatal("honeypot submission was written to the database")
	}
}

func TestContactRateLimitedPerIP(t *testing.T) {
	e := newF9Router(t)

	for i := 0; i < contactPerIP; i++ {
		if rec := e.do(http.MethodPost, "/api/v1/contact", contactBody, "198.51.100.2:1", nil); rec.Code != http.StatusAccepted {
			t.Fatalf("submission %d answered %d, want 202", i+1, rec.Code)
		}
	}
	rec := e.do(http.MethodPost, "/api/v1/contact", contactBody, "198.51.100.2:2", nil)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("4th submission = %d Retry-After=%q, want 429 with Retry-After", rec.Code, rec.Header().Get("Retry-After"))
	}
	if e.contacts.count() != contactPerIP {
		t.Fatalf("stored %d contacts, want %d (throttled request must not write)", e.contacts.count(), contactPerIP)
	}

	// Another visitor is unaffected.
	if rec := e.do(http.MethodPost, "/api/v1/contact", contactBody, "198.51.100.3:1", nil); rec.Code != http.StatusAccepted {
		t.Fatalf("other IP answered %d, want 202", rec.Code)
	}
	if contactWindow != 10*time.Minute || contactPerIP != 3 {
		t.Fatalf("contact limit is %d per %s, want 3 per 10m", contactPerIP, contactWindow)
	}
}

func TestContactStoresOnlyHashedIP(t *testing.T) {
	e := newF9Router(t)
	e.do(http.MethodPost, "/api/v1/contact", contactBody, "198.51.100.4:1", nil)

	if e.contacts.count() != 1 {
		t.Fatalf("stored %d contacts, want 1", e.contacts.count())
	}
	if got := e.contacts.ips[0]; got != usecase.HashIP("198.51.100.4:1") || len(got) != 64 {
		t.Fatalf("ip_hash = %q, want the 64-char sha256 of the client address", got)
	}
}

func TestAdminContactsRequiresSession(t *testing.T) {
	e := newF9Router(t)
	e.do(http.MethodPost, "/api/v1/contact", contactBody, "198.51.100.5:1", nil)

	if rec := e.do(http.MethodGet, "/api/v1/admin/contacts", "", "", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d, want 401", rec.Code)
	}
	rec := e.do(http.MethodGet, "/api/v1/admin/contacts", "", "", asAdmin)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "ada@example.com") {
		t.Fatalf("admin = %d %s, want 200 with the contact", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "ip_hash") {
		t.Fatal("admin list leaks ip_hash")
	}
}
