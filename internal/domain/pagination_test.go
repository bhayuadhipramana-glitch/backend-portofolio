package domain

import "testing"

func TestPageNormalize(t *testing.T) {
	tests := []struct {
		name string
		in   Page
		want Page
	}{
		{"zero value gets default limit", Page{}, Page{Limit: DefaultPageLimit}},
		{"negative limit gets default", Page{Limit: -5}, Page{Limit: DefaultPageLimit}},
		{"limit above max is capped", Page{Limit: 1000}, Page{Limit: MaxPageLimit}},
		{"negative offset is clamped", Page{Limit: 10, Offset: -3}, Page{Limit: 10}},
		{"valid page is unchanged", Page{Limit: 50, Offset: 100}, Page{Limit: 50, Offset: 100}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.in.Normalize(); got != tt.want {
				t.Fatalf("Normalize(%+v) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

func TestVisibilityZeroValueIsPublic(t *testing.T) {
	var v Visibility
	if v != VisibilityPublic {
		t.Fatal("zero Visibility must be VisibilityPublic so a forgotten argument fails closed")
	}
}
