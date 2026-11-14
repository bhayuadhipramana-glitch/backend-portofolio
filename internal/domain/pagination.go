package domain

const (
	// DefaultPageLimit is used when a caller does not ask for a page size.
	DefaultPageLimit = 20
	// MaxPageLimit caps the page size of every list query.
	MaxPageLimit = 100
)

// Page is a LIMIT/OFFSET window for list queries.
type Page struct {
	Limit  int
	Offset int
}

// Normalize returns a page with a limit in [1, MaxPageLimit] and a
// non-negative offset, so a repository never runs an unbounded query.
func (p Page) Normalize() Page {
	switch {
	case p.Limit <= 0:
		p.Limit = DefaultPageLimit
	case p.Limit > MaxPageLimit:
		p.Limit = MaxPageLimit
	}
	if p.Offset < 0 {
		p.Offset = 0
	}
	return p
}

// Visibility selects which records a read may return.
// The zero value is VisibilityPublic so a forgotten argument fails closed.
type Visibility int

const (
	// VisibilityPublic returns only records meant for visitors: published
	// articles and the active profile/resume.
	VisibilityPublic Visibility = iota
	// VisibilityAll returns every record. It is reserved for admin routes.
	VisibilityAll
)
