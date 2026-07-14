package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// shimProfile is the subset of a provider's profile this shim ever maps
// into an id_token — deliberately narrow (mirrors the same allow-list
// instinct as publicTankSummary in tank-api/main.go) since these claims end
// up denormalized onto the TankMaze user account via Cognito's attribute
// mapping.
type shimProfile struct {
	Sub     string `json:"sub"`
	Email   string `json:"email"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
}

// oauthProvider abstracts the one piece that differs between GitHub and
// Discord — the rest of the shim (state wrapping, code storage, JWT
// minting) is identical, which is the whole point of sharing this
// infrastructure between items 233 and 240 rather than building it twice.
type oauthProvider interface {
	name() string
	authorizeURL(redirectURI, state string) string
	exchange(ctx context.Context, code, redirectURI string) (accessToken string, err error)
	profile(ctx context.Context, accessToken string) (shimProfile, error)
}

// ---- GitHub ---------------------------------------------------------------

type githubProvider struct {
	clientID, clientSecret string
	httpClient             *http.Client
}

func (p *githubProvider) name() string { return "github" }

func (p *githubProvider) authorizeURL(redirectURI, state string) string {
	v := url.Values{}
	v.Set("client_id", p.clientID)
	v.Set("redirect_uri", redirectURI)
	// user:email is required to read email addresses at all — GitHub emails
	// are private-by-default and not included by "read:user" alone.
	v.Set("scope", "read:user user:email")
	v.Set("state", state)
	return "https://github.com/login/oauth/authorize?" + v.Encode()
}

func (p *githubProvider) exchange(ctx context.Context, code, redirectURI string) (string, error) {
	form := url.Values{}
	form.Set("client_id", p.clientID)
	form.Set("client_secret", p.clientSecret)
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://github.com/login/oauth/access_token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json") // GitHub defaults to form-encoded responses otherwise
	res, err := p.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	var out struct {
		AccessToken      string `json:"access_token"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Error != "" {
		return "", fmt.Errorf("github token exchange: %s: %s", out.Error, out.ErrorDescription)
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("github token exchange: empty access_token")
	}
	return out.AccessToken, nil
}

func (p *githubProvider) profile(ctx context.Context, accessToken string) (shimProfile, error) {
	var user struct {
		ID     int64  `json:"id"`
		Login  string `json:"login"`
		Name   string `json:"name"`
		Avatar string `json:"avatar_url"`
	}
	if err := p.getJSON(ctx, "https://api.github.com/user", accessToken, &user); err != nil {
		return shimProfile{}, err
	}

	// Primary email is a separate call — /user's own "email" field is null
	// unless the user made it public, regardless of the user:email scope.
	var emails []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if err := p.getJSON(ctx, "https://api.github.com/user/emails", accessToken, &emails); err != nil {
		return shimProfile{}, err
	}
	var email string
	for _, e := range emails {
		if e.Primary && e.Verified {
			email = e.Email
			break
		}
	}

	name := user.Name
	if name == "" {
		name = user.Login
	}
	return shimProfile{
		Sub:     strconv.FormatInt(user.ID, 10),
		Email:   email,
		Name:    name,
		Picture: user.Avatar,
	}, nil
}

func (p *githubProvider) getJSON(ctx context.Context, urlStr, accessToken string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := p.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("github API %s: %d: %s", urlStr, res.StatusCode, string(body))
	}
	return json.NewDecoder(res.Body).Decode(out)
}

// ---- Discord ----------------------------------------------------------

type discordProvider struct {
	clientID, clientSecret string
	httpClient             *http.Client
}

func (p *discordProvider) name() string { return "discord" }

func (p *discordProvider) authorizeURL(redirectURI, state string) string {
	v := url.Values{}
	v.Set("client_id", p.clientID)
	v.Set("redirect_uri", redirectURI)
	v.Set("response_type", "code")
	// identify: id/username/avatar. email: needed separately per Discord's
	// scope model (item 240's own note — identify alone doesn't grant it).
	v.Set("scope", "identify email")
	v.Set("state", state)
	return "https://discord.com/oauth2/authorize?" + v.Encode()
}

func (p *discordProvider) exchange(ctx context.Context, code, redirectURI string) (string, error) {
	form := url.Values{}
	form.Set("client_id", p.clientID)
	form.Set("client_secret", p.clientSecret)
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://discord.com/api/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := p.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		return "", fmt.Errorf("discord token exchange: %d: %s", res.StatusCode, string(body))
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("discord token exchange: empty access_token")
	}
	return out.AccessToken, nil
}

func (p *discordProvider) profile(ctx context.Context, accessToken string) (shimProfile, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://discord.com/api/users/@me", nil)
	if err != nil {
		return shimProfile{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	res, err := p.httpClient.Do(req)
	if err != nil {
		return shimProfile{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		return shimProfile{}, fmt.Errorf("discord API /users/@me: %d: %s", res.StatusCode, string(body))
	}
	var user struct {
		ID            string `json:"id"`
		Username      string `json:"username"`
		GlobalName    string `json:"global_name"`
		Discriminator string `json:"discriminator"`
		Email         string `json:"email"`
		Avatar        string `json:"avatar"`
	}
	if err := json.NewDecoder(res.Body).Decode(&user); err != nil {
		return shimProfile{}, err
	}

	name := user.GlobalName
	if name == "" {
		name = user.Username
	}

	// Discord's avatar field is a hash, not a ready URL (unlike Google/
	// Facebook/GitHub) — must be composed into a CDN path (item 240's own
	// note). Falls back to a legacy discriminator-indexed default avatar
	// when the user has none set; newer usernames use discriminator "0",
	// which has no meaningful modulus, so that case just takes index 0.
	var picture string
	if user.Avatar != "" {
		picture = fmt.Sprintf("https://cdn.discordapp.com/avatars/%s/%s.png", user.ID, user.Avatar)
	} else {
		idx := 0
		if user.Discriminator != "" && user.Discriminator != "0" {
			if d, err := strconv.Atoi(user.Discriminator); err == nil {
				idx = d % 5
			}
		}
		picture = fmt.Sprintf("https://cdn.discordapp.com/embed/avatars/%d.png", idx)
	}

	return shimProfile{
		Sub:     user.ID,
		Email:   user.Email,
		Name:    name,
		Picture: picture,
	}, nil
}
