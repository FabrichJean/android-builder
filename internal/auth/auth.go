// Package auth gère la connexion Google (OAuth2) optionnelle : si aucun
// GOOGLE_CLIENT_ID n'est configuré, Manager reste désactivé et l'app
// fonctionne sans compte (comportement historique, historique local au
// navigateur). Connecté, l'utilisateur obtient un historique de builds privé
// (voir internal/buildstore), identifié par cookie de session signé — pas de
// stockage de session côté serveur.
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	cookieName     = "session"
	cookieMaxAge   = 30 * 24 * time.Hour
	userInfoURL    = "https://www.googleapis.com/oauth2/v3/userinfo"
	stateCookie    = "oauth_state"
	stateCookieTTL = 10 * time.Minute
)

// User représente un compte Google connecté.
type User struct {
	ID      string `json:"id"` // "sub" Google : identifiant stable du compte
	Email   string `json:"email"`
	Name    string `json:"name"`
	Picture string `json:"picture"`
}

// Manager gère le flux OAuth2 Google et les cookies de session signés.
type Manager struct {
	oauth  *oauth2.Config
	secret []byte
	secure bool // cookies "Secure" si l'app tourne en https
}

// New construit un Manager. clientID/clientSecret vides -> connexion désactivée.
func New(clientID, clientSecret, baseURL, secret string) *Manager {
	m := &Manager{secret: []byte(secret), secure: strings.HasPrefix(baseURL, "https://")}
	if clientID == "" || clientSecret == "" {
		return m
	}
	m.oauth = &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  baseURL + "/auth/google/callback",
		Endpoint:     google.Endpoint,
		Scopes:       []string{"openid", "email", "profile"},
	}
	return m
}

// Enabled indique si la connexion Google est configurée.
func (m *Manager) Enabled() bool { return m.oauth != nil }

// BeginLogin pose un cookie d'état anti-CSRF et renvoie l'URL de consentement Google.
func (m *Manager) BeginLogin(w http.ResponseWriter) (string, error) {
	if !m.Enabled() {
		return "", errors.New("connexion Google non configurée")
	}
	state, err := randomToken(16)
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name: stateCookie, Value: state, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: int(stateCookieTTL.Seconds()), Secure: m.secure,
	})
	return m.oauth.AuthCodeURL(state), nil
}

// CheckState vérifie le paramètre "state" du callback contre le cookie posé par BeginLogin.
func (m *Manager) CheckState(r *http.Request) bool {
	c, err := r.Cookie(stateCookie)
	if err != nil || c.Value == "" {
		return false
	}
	return c.Value == r.URL.Query().Get("state")
}

// Exchange échange le code d'autorisation contre les infos du compte Google.
func (m *Manager) Exchange(ctx context.Context, code string) (*User, error) {
	if !m.Enabled() {
		return nil, errors.New("connexion Google non configurée")
	}
	tok, err := m.oauth.Exchange(ctx, code)
	if err != nil {
		return nil, err
	}
	client := m.oauth.Client(ctx, tok)
	resp, err := client.Get(userInfoURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("userinfo Google: " + resp.Status)
	}
	var raw struct {
		Sub     string `json:"sub"`
		Email   string `json:"email"`
		Name    string `json:"name"`
		Picture string `json:"picture"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	if raw.Sub == "" {
		return nil, errors.New("réponse Google incomplète (sub manquant)")
	}
	return &User{ID: raw.Sub, Email: raw.Email, Name: raw.Name, Picture: raw.Picture}, nil
}

// SetSession pose le cookie de session signé pour l'utilisateur donné.
func (m *Manager) SetSession(w http.ResponseWriter, u *User) error {
	payload, err := json.Marshal(u)
	if err != nil {
		return err
	}
	val := base64.RawURLEncoding.EncodeToString(payload)
	sig := m.sign(val)
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: val + "." + sig, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: int(cookieMaxAge.Seconds()), Secure: m.secure,
	})
	return nil
}

// ClearSession déconnecte l'utilisateur courant.
func (m *Manager) ClearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: "", Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: -1, Secure: m.secure,
	})
}

// UserFromRequest lit et vérifie le cookie de session. ok=false si absent/invalide.
func (m *Manager) UserFromRequest(r *http.Request) (*User, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return nil, false
	}
	dot := strings.LastIndexByte(c.Value, '.')
	if dot < 0 {
		return nil, false
	}
	val, sig := c.Value[:dot], c.Value[dot+1:]
	if !hmac.Equal([]byte(m.sign(val)), []byte(sig)) {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(val)
	if err != nil {
		return nil, false
	}
	var u User
	if err := json.Unmarshal(raw, &u); err != nil || u.ID == "" {
		return nil, false
	}
	return &u, true
}

func (m *Manager) sign(data string) string {
	h := hmac.New(sha256.New, m.secret)
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
