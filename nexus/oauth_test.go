package nexus

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAuth is a fake Nexus sign-in server.
type fakeAuth struct {
	t   *testing.T
	srv *httptest.Server
	key *rsa.PrivateKey

	mu        sync.Mutex
	challenge string
	premium   bool
	expires   int64  // token lifetime in seconds
	issued    int    // access tokens issued so far
	refresh   string // the valid refresh token
	revoked   []string
	forms     []url.Values
}

func newFakeAuth(t *testing.T) *fakeAuth {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeAuth{t: t, key: key, expires: 21600}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAuth) auth() *Auth {
	return &Auth{Base: f.srv.URL, Callback: "http://127.0.0.1:0/callback", Key: &f.key.PublicKey, Version: "test"}
}

// jwt signs an access token with key.
func jwt(t *testing.T, key *rsa.PrivateKey, claims any) string {
	enc := func(v any) string {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return base64.RawURLEncoding.EncodeToString(data)
	}
	head := enc(map[string]string{"alg": "RS256", "typ": "JWT"}) + "." + enc(claims)
	sum := sha256.Sum256([]byte(head))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return head + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (f *fakeAuth) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r.ParseForm()
	f.forms = append(f.forms, r.PostForm)
	if r.Header.Get("Application-Name") != "Modvault" || r.Header.Get("Application-Version") != "test" {
		f.t.Errorf("%s без заголовков программы", r.URL.Path)
	}
	if r.PostForm.Get("client_id") != ClientID || r.PostForm.Get("client_secret") != "" {
		f.t.Errorf("%s: client_id %q, секрет %q", r.URL.Path, r.PostForm.Get("client_id"), r.PostForm.Get("client_secret"))
	}
	switch r.URL.Path {
	case "/revoke":
		f.revoked = append(f.revoked, r.PostForm.Get("token"))
		return
	case "/token":
	default:
		http.NotFound(w, r)
		return
	}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if r.PostForm.Get("code") != "the-code" || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"invalid_grant"}`)
			return
		}
	case "refresh_token":
		if r.PostForm.Get("refresh_token") != f.refresh {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"invalid_grant","error_description":"revoked"}`)
			return
		}
	default:
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	f.issued++
	f.refresh = fmt.Sprintf("refresh-%d", f.issued)
	roles := []string{"member"}
	if f.premium {
		roles = append(roles, "premium")
	}
	access := jwt(f.t, f.key, map[string]any{"sub": "7", "jti": f.issued, "user": map[string]any{"id": 7, "username": "zd", "membership_roles": roles}})
	json.NewEncoder(w).Encode(map[string]any{"access_token": access, "refresh_token": f.refresh, "expires_in": f.expires, "token_type": "Bearer"})
}

// browser plays the browser: it checks the sign-in URL and sends the given
// answers back to Modvault (by default the right code).
func (f *fakeAuth) browser(t *testing.T, answers ...url.Values) func(string) error {
	return func(link string) error {
		u, err := url.Parse(link)
		if err != nil {
			return err
		}
		q := u.Query()
		if u.Path != "/authorize" || q.Get("client_id") != ClientID || q.Get("response_type") != "code" ||
			q.Get("code_challenge_method") != "S256" || q.Get("state") == "" {
			t.Errorf("адрес входа: %s", link)
		}
		f.mu.Lock()
		f.challenge = q.Get("code_challenge")
		f.mu.Unlock()
		if len(answers) == 0 {
			answers = []url.Values{{"code": {"the-code"}}}
		}
		go func() {
			for _, a := range answers {
				if a.Get("state") == "" {
					a.Set("state", q.Get("state"))
				}
				resp, err := http.Get(q.Get("redirect_uri") + "?" + a.Encode())
				if err != nil {
					t.Error(err)
					return
				}
				resp.Body.Close()
			}
		}()
		return nil
	}
}

func TestLogin(t *testing.T) {
	f := newFakeAuth(t)
	a := f.auth()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// An answer from a foreign tab (other state) is skipped; sign-in waits for its own.
	tokens, err := a.Login(ctx, f.browser(t, url.Values{"state": {"old"}, "code": {"stolen"}}, url.Values{"code": {"the-code"}}))
	if err != nil {
		t.Fatal(err)
	}
	if tokens.Refresh != "refresh-1" || time.Until(tokens.Expires) < 5*time.Hour {
		t.Errorf("токены: %+v", tokens)
	}
	user, err := a.User(tokens.Access)
	if err != nil || user != (User{ID: 7, Name: "zd"}) {
		t.Errorf("пользователь %+v, %v", user, err)
	}

	if _, err := a.Login(ctx, f.browser(t, url.Values{"error": {"access_denied"}})); !errors.Is(err, ErrDenied) {
		t.Errorf("отказ на сайте: %v", err)
	}
	// A swapped code fails the PKCE check.
	if _, err := a.Login(ctx, f.browser(t, url.Values{"code": {"other"}})); !errors.Is(err, ErrSignedOut) {
		t.Errorf("чужой код: %v", err)
	}

	short, stop := context.WithCancel(ctx)
	stop()
	if _, err := a.Login(short, func(string) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Errorf("отмена: %v", err)
	}
}

func TestTokenSignature(t *testing.T) {
	f := newFakeAuth(t)
	a := f.auth()
	claims := map[string]any{"user": map[string]any{"id": 7, "username": "zd", "membership_roles": []string{"lifetimepremium"}}}
	if u, err := a.User(jwt(t, f.key, claims)); err != nil || !u.Premium {
		t.Errorf("пожизненный Premium: %+v, %v", u, err)
	}
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	for _, bad := range []string{jwt(t, other, claims), "a.b", "", jwt(t, f.key, map[string]any{"sub": "7"})} {
		if _, err := a.User(bad); err == nil {
			t.Errorf("негодный токен принят: %.30s", bad)
		}
	}
	// The real Nexus key parses.
	if _, err := (&Auth{}).key(); err != nil {
		t.Fatal(err)
	}
}

func TestSession(t *testing.T) {
	f := newFakeAuth(t)
	a := f.auth()
	ctx := context.Background()
	f.refresh = "saved"
	store := &MemoryKeys{Key: "saved"}
	var changes []User
	s := NewSession(a, store, Tokens{})
	s.Changed = func(u User) { changes = append(changes, u) }

	// No access token yet: it comes from the saved refresh token, and the
	// new refresh token is saved.
	first, err := s.Token(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if store.Key != "refresh-1" || len(changes) != 1 || changes[0].Name != "zd" {
		t.Errorf("после обновления: хранилище %q, изменения %+v", store.Key, changes)
	}
	if again, _ := s.Token(ctx, false); again != first || f.issued != 1 {
		t.Errorf("действующий токен запрошен заново: выдано %d", f.issued)
	}
	// Nexus rejected the token: a new one is taken.
	f.premium = true
	if again, _ := s.Token(ctx, true); again == first || !changes[1].Premium {
		t.Errorf("устаревший токен не обновлён: %+v", changes)
	}

	// Access revoked on the site: the sign-in is erased and the owner is told.
	f.refresh = "revoked-on-site"
	if _, err := s.Token(ctx, true); !errors.Is(err, ErrSignedOut) {
		t.Errorf("отозванный вход: %v", err)
	}
	if store.Key != "" || changes[len(changes)-1].Name != "" {
		t.Errorf("после отзыва: хранилище %q, изменения %+v", store.Key, changes)
	}
	if _, err := s.Token(ctx, false); !errors.Is(err, ErrSignedOut) {
		t.Errorf("без входа: %v", err)
	}
}

func TestLogout(t *testing.T) {
	f := newFakeAuth(t)
	store := &MemoryKeys{Key: "saved"}
	s := NewSession(f.auth(), store, Tokens{})
	if err := s.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.Key != "" || len(f.revoked) != 1 || f.revoked[0] != "saved" {
		t.Errorf("выход: хранилище %q, отозваны %v", store.Key, f.revoked)
	}
}

func TestClientBearer(t *testing.T) {
	f := newFakeAuth(t)
	f.refresh = "saved"
	s := NewSession(f.auth(), &MemoryKeys{Key: "saved"}, Tokens{})
	var seen []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		if r.Header.Get("apikey") != "" {
			t.Error("при входе через OAuth ушёл ключ")
		}
		// Nexus "forgot" the first token before it expired.
		if len(seen) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"user_id":7,"name":"zd"}`)
	}))
	defer api.Close()
	c := &Client{Base: api.URL, Session: s, Key: "ignored", Version: "test"}
	if _, err := c.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || !strings.HasPrefix(seen[0], "Bearer ") || seen[0] == seen[1] {
		t.Errorf("заголовки: %v", seen)
	}
}
