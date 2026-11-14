package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

type fakeContactUC struct {
	submitted []domain.Contact
	ips       []string
	err       error
}

func (f *fakeContactUC) Submit(_ context.Context, c *domain.Contact, ip string) error {
	if f.err != nil {
		return f.err
	}
	f.submitted = append(f.submitted, *c)
	f.ips = append(f.ips, ip)
	return nil
}

func (f *fakeContactUC) List(context.Context, domain.Page) ([]domain.Contact, error) {
	return nil, f.err
}

func postContact(h *ContactHandler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/contact", strings.NewReader(body))
	req.RemoteAddr = "203.0.113.7"
	rec := httptest.NewRecorder()
	h.Submit(rec, req)
	return rec
}

const validContact = `{"name":" Ada ","email":"ada@example.com","subject":"Hello","message":"I would like to talk about a project."}`

func TestContactSubmitStoresNormalizedContact(t *testing.T) {
	uc := &fakeContactUC{}
	rec := postContact(NewContactHandler(uc), validContact)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("got %d, want 202", rec.Code)
	}
	if len(uc.submitted) != 1 || uc.submitted[0].Name != "Ada" || uc.ips[0] != "203.0.113.7" {
		t.Fatalf("submitted %+v from %v", uc.submitted, uc.ips)
	}
}

func TestContactHoneypotReturns202WithoutUsecase(t *testing.T) {
	for _, body := range []string{
		`{"website":"http://spam.example"}`, // even an otherwise invalid body is dropped quietly
		`{"name":"B","email":"b@example.com","subject":"s","message":"cheap pills for you","website":"x"}`,
	} {
		uc := &fakeContactUC{}
		rec := postContact(NewContactHandler(uc), body)
		if rec.Code != http.StatusAccepted || len(uc.submitted) != 0 {
			t.Fatalf("body %s: got %d with %d writes, want 202 and none", body, rec.Code, len(uc.submitted))
		}
	}
}

func TestContactValidation(t *testing.T) {
	tests := []struct{ name, body string }{
		{"missing name", `{"email":"a@example.com","subject":"s","message":"long enough message"}`},
		{"blank subject", `{"name":"A","email":"a@example.com","subject":"   ","message":"long enough message"}`},
		{"bad email", `{"name":"A","email":"nope","subject":"s","message":"long enough message"}`},
		{"message too short", `{"name":"A","email":"a@example.com","subject":"s","message":"too short"}`},
		{"message too long", `{"name":"A","email":"a@example.com","subject":"s","message":"` + strings.Repeat("x", 4001) + `"}`},
		{"name too long", `{"name":"` + strings.Repeat("n", 121) + `","email":"a@example.com","subject":"s","message":"long enough message"}`},
		{"unknown field", `{"name":"A","email":"a@example.com","subject":"s","message":"long enough message","extra":1}`},
		{"malformed json", `{"name":`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc := &fakeContactUC{}
			if rec := postContact(NewContactHandler(uc), tt.body); rec.Code != http.StatusBadRequest || len(uc.submitted) != 0 {
				t.Fatalf("got %d with %d writes, want 400 and none", rec.Code, len(uc.submitted))
			}
		})
	}

	// Boundaries: exactly 10 and exactly 4000 characters are accepted.
	for _, n := range []int{10, 4000} {
		body := `{"name":"A","email":"a@example.com","subject":"s","message":"` + strings.Repeat("x", n) + `"}`
		if rec := postContact(NewContactHandler(&fakeContactUC{}), body); rec.Code != http.StatusAccepted {
			t.Errorf("message of %d chars = %d, want 202", n, rec.Code)
		}
	}
}

func TestContactBodyCapIs32KB(t *testing.T) {
	big := `{"name":"A","email":"a@example.com","subject":"s","message":"long enough message","website":"` + strings.Repeat("x", 33<<10) + `"}`
	uc := &fakeContactUC{}
	if rec := postContact(NewContactHandler(uc), big); rec.Code != http.StatusRequestEntityTooLarge || len(uc.submitted) != 0 {
		t.Fatalf("got %d, want 413", rec.Code)
	}
}

func TestContactSubmitHidesInternalErrors(t *testing.T) {
	uc := &fakeContactUC{err: errors.New("pgx: connection refused to 10.0.0.5")}
	rec := postContact(NewContactHandler(uc), validContact)
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "pgx") {
		t.Fatalf("got %d %s, want a generic 500", rec.Code, rec.Body.String())
	}
}
