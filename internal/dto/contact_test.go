package dto

import "testing"

func TestContactRequestIsSpam(t *testing.T) {
	tests := []struct {
		website string
		want    bool
	}{{"", false}, {"   ", false}, {"http://spam.example", true}, {"x", true}}
	for _, tt := range tests {
		if got := (ContactRequest{Website: tt.website}).IsSpam(); got != tt.want {
			t.Errorf("IsSpam(%q) = %v, want %v", tt.website, got, tt.want)
		}
	}
}

func TestContactRequestNormalizeTrims(t *testing.T) {
	r := ContactRequest{Name: " A ", Email: " a@example.com ", Subject: "\ts\n", Message: "  hello there friend  "}
	r.Normalize()
	if r.Name != "A" || r.Email != "a@example.com" || r.Subject != "s" || r.Message != "hello there friend" {
		t.Fatalf("not trimmed: %+v", r)
	}
	// Padding must not satisfy the minimum length.
	short := ContactRequest{Name: "A", Email: "a@example.com", Subject: "s", Message: "   short   "}
	short.Normalize()
	if Validate(&short) == nil {
		t.Fatal("whitespace padding counted toward the 10-character minimum")
	}
}
