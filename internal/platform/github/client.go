// Package github implements domain.IdentityProvider on top of GitHub OAuth2.
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/oauth2"
	oauthgithub "golang.org/x/oauth2/github"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
)

const (
	userEndpoint   = "https://api.github.com/user"
	requestTimeout = 10 * time.Second
)

// Client performs the OAuth2 authorization-code flow against GitHub.
type Client struct {
	cfg *oauth2.Config
}

var _ domain.IdentityProvider = (*Client)(nil)

// New builds a Client. No scopes are requested: the public profile (id, login)
// is all that is needed to identify the admin.
func New(clientID, clientSecret, redirectURL string) *Client {
	return &Client{cfg: &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Endpoint:     oauthgithub.Endpoint,
	}}
}

// AuthCodeURL returns the GitHub consent URL carrying the CSRF state.
func (c *Client) AuthCodeURL(state string) string {
	return c.cfg.AuthCodeURL(state)
}

// Exchange trades the authorization code for a token and resolves the user.
func (c *Client) Exchange(ctx context.Context, code string) (*domain.GitHubUser, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	tok, err := c.cfg.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("github exchange code: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userEndpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("github build user request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.cfg.Client(ctx, tok).Do(req)
	if err != nil {
		return nil, fmt.Errorf("github fetch user: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github fetch user: unexpected status %d", resp.StatusCode)
	}

	var body struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("github decode user: %w", err)
	}
	if body.ID == 0 {
		return nil, fmt.Errorf("github fetch user: empty id in response")
	}
	return &domain.GitHubUser{ID: body.ID, Login: body.Login}, nil
}
