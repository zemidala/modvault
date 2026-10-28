package nexus

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zemidala/modvault/i18n"
)

// Sign-in through OAuth 2.0 with PKCE: Modvault is a public application and
// has no client secret. Endpoints, request fields and the public key follow
// the Nexus guide (modding.wiki/en/api/oauth2-guide) and
// https://users.nexusmods.com/.well-known/openid-configuration.

// DefaultAuth is the Nexus Mods sign-in endpoint.
const DefaultAuth = "https://users.nexusmods.com/oauth"

// ClientID is the Modvault application id on Nexus.
const ClientID = "modvault"

// DefaultCallback is the redirect URI registered for Modvault. Nexus accepts
// no other port.
const DefaultCallback = "http://127.0.0.1:8089/callback"

// nexusKey is the public key Nexus signs access tokens with.
const nexusKey = `-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAs/57oX8HW8xC+W/etH7J
PgoSTiGPKZa6Gq3/K/7GgrpJcZhPdr9MTGocb2uLzQBJW+u1XpSgyeKH4JCxxeHF
3zcUtb7SUg3KnxlR5QUmOnqBvbUuL4opUpfgWUGltASduYqZBJD2WTK8Hvwh9X1v
ACeqp1zgorZm3f0J2H15TDbzIp9ihCFuthJUFumdzvrt/WvimW2fiyqndTNQwe5h
XM8hj8cemdWQXCd99qnj7UQkpu+yNisVMHQCsAqXITe6Ehp6IY9eCd4DJKjDvyLc
3vbY8UL+bcVK5tYAKemZ56uw3q1YdcyqGlItyLi4j4EISdBQaCCqT7YZUhMYzhUd
1QIDAQAB
-----END PUBLIC KEY-----`

var (
	// ErrSignedOut means the sign-in no longer works: access was revoked on
	// the site or the refresh token expired.
	ErrSignedOut = i18n.NewError("вход в Nexus больше не действует: войдите снова")
	// ErrDenied means access was not allowed on the Nexus page.
	ErrDenied = i18n.NewError("вход отменён на странице Nexus")
)

// Tokens are the sign-in tokens. Access lives a few hours, Refresh until the
// user revokes access.
type Tokens struct {
	Access  string
	Refresh string
	Expires time.Time
}

// Auth signs in to Nexus.
type Auth struct {
	Base     string         // empty: DefaultAuth
	Callback string         // empty: DefaultCallback
	Key      *rsa.PublicKey // empty: the Nexus key
	Version  string         // program version for request headers
	HTTP     *http.Client
}

// Login opens the Nexus sign-in page through open and waits until the user
// allows access; cancel through ctx.
func (a *Auth) Login(ctx context.Context, open func(string) error) (Tokens, error) {
	back, err := url.Parse(a.callback())
	if err != nil {
		return Tokens{}, err
	}
	ln, err := net.Listen("tcp", back.Host)
	if err != nil {
		return Tokens{}, i18n.Errorf("Modvault не может принять ответ Nexus на %s — адрес занят другой программой: %w", back.Host, err)
	}
	back.Host = ln.Addr().String() // tests listen on :0, any free port
	verifier, state := random(32), random(16)
	sum := sha256.Sum256([]byte(verifier))

	type answer struct {
		code string
		err  error
	}
	done := make(chan answer, 1)
	mux := http.NewServeMux()
	mux.HandleFunc(back.Path, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		// A foreign or stale answer (a tab from an earlier sign-in) does not
		// end the sign-in: keep waiting for the real one.
		if q.Get("state") != state {
			page(w, http.StatusBadRequest, i18n.T("Эта ссылка от прошлого входа. Вернитесь в Modvault и войдите снова."))
			return
		}
		var a answer
		switch {
		case q.Get("error") == "access_denied":
			a.err = ErrDenied
		case q.Get("error") != "":
			a.err = i18n.Errorf("Nexus не пустил: %s", firstNonEmpty(q.Get("error_description"), q.Get("error")))
		case q.Get("code") == "":
			a.err = i18n.NewError("Nexus не прислал код входа")
		default:
			a.code = q.Get("code")
		}
		if a.err != nil {
			page(w, http.StatusOK, a.err.Error())
		} else {
			page(w, http.StatusOK, i18n.T("Готово: Modvault вошёл в Nexus. Эту вкладку можно закрыть."))
		}
		select {
		case done <- a:
		default:
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln)
	defer srv.Close()

	q := url.Values{
		"client_id":             {ClientID},
		"response_type":         {"code"},
		"scope":                 {""},
		"redirect_uri":          {back.String()},
		"state":                 {state},
		"code_challenge_method": {"S256"},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
	}
	if err := open(a.base() + "/authorize?" + q.Encode()); err != nil {
		return Tokens{}, err
	}

	var got answer
	select {
	case <-ctx.Done():
		return Tokens{}, ctx.Err()
	case got = <-done:
	}
	if got.err != nil {
		return Tokens{}, got.err
	}
	return a.token(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {ClientID},
		"redirect_uri":  {back.String()},
		"scope":         {""},
		"code":          {got.code},
		"code_verifier": {verifier},
	}, "")
}

// Refresh gets a new access token for the refresh token. A 4xx answer means
// access was revoked: ErrSignedOut.
func (a *Auth) Refresh(ctx context.Context, refresh string) (Tokens, error) {
	return a.token(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {ClientID},
		"refresh_token": {refresh},
	}, refresh)
}

// Revoke revokes the token on Nexus, so it is useless there after sign-out.
func (a *Auth) Revoke(ctx context.Context, token string) error {
	_, err := a.post(ctx, "/revoke", url.Values{"client_id": {ClientID}, "token": {token}})
	return err
}

// token requests tokens; old is the previous refresh token, kept when Nexus
// does not send a new one.
func (a *Auth) token(ctx context.Context, form url.Values, old string) (Tokens, error) {
	answer, err := a.post(ctx, "/token", form)
	if err != nil {
		return Tokens{}, err
	}
	var reply struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Expires int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(answer, &reply); err != nil {
		return Tokens{}, i18n.Errorf("непонятный ответ Nexus: %w", err)
	}
	if reply.Access == "" {
		return Tokens{}, i18n.NewError("Nexus не прислал токен входа")
	}
	if _, err := a.User(reply.Access); err != nil {
		return Tokens{}, err
	}
	t := Tokens{Access: reply.Access, Refresh: firstNonEmpty(reply.Refresh, old)}
	// A minute of margin: the token must not expire on its way to Nexus.
	t.Expires = time.Now().Add(time.Duration(reply.Expires)*time.Second - time.Minute)
	return t, nil
}

func (a *Auth) post(ctx context.Context, path string, form url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.base()+path, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Modvault/"+a.Version)
	req.Header.Set("Application-Name", "Modvault")
	req.Header.Set("Application-Version", a.Version)
	hc := a.HTTP
	if hc == nil {
		hc = defaultHTTP
	}
	resp, err := hc.Do(req)
	if err != nil {
		// The request URL holds no secrets, but the bare cause reads better.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return nil, i18n.Errorf("Nexus недоступен: %w", err)
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, i18n.Errorf("Nexus недоступен: %w", err)
	}
	switch {
	case resp.StatusCode == http.StatusOK:
		return answer, nil
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, ErrRateLimited
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		var e struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		json.Unmarshal(answer, &e)
		if why := firstNonEmpty(e.Description, e.Error); why != "" {
			return nil, fmt.Errorf("%w (%s)", ErrSignedOut, why)
		}
		return nil, ErrSignedOut
	}
	return nil, i18n.Errorf("Nexus ответил %s", resp.Status)
}

// User checks the access token signature and reads whose token it is.
func (a *Auth) User(access string) (User, error) {
	bad := func(why string) (User, error) {
		return User{}, i18n.Errorf("Nexus прислал негодный токен: %s", why)
	}
	parts := strings.Split(access, ".")
	if len(parts) != 3 {
		return bad("not a JWT")
	}
	var head struct {
		Alg string `json:"alg"`
	}
	if raw, err := base64.RawURLEncoding.DecodeString(parts[0]); err != nil || json.Unmarshal(raw, &head) != nil {
		return bad("unreadable header")
	}
	if head.Alg != "RS256" {
		return bad("signed with " + head.Alg)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return bad("unreadable signature")
	}
	key, err := a.key()
	if err != nil {
		return User{}, err
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig) != nil {
		return bad("signature mismatch")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return bad("unreadable payload")
	}
	var claims struct {
		User struct {
			ID    int      `json:"id"`
			Name  string   `json:"username"`
			Roles []string `json:"membership_roles"`
		} `json:"user"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil || claims.User.Name == "" {
		return bad("no user name")
	}
	u := claims.User
	return User{ID: u.ID, Name: u.Name, Premium: slices.Contains(u.Roles, "premium") || slices.Contains(u.Roles, "lifetimepremium")}, nil
}

var (
	keyOnce   sync.Once
	parsedKey *rsa.PublicKey
	keyErr    error
)

func (a *Auth) key() (*rsa.PublicKey, error) {
	if a.Key != nil {
		return a.Key, nil
	}
	keyOnce.Do(func() {
		block, _ := pem.Decode([]byte(nexusKey))
		k, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			keyErr = err
			return
		}
		parsedKey, _ = k.(*rsa.PublicKey)
		if parsedKey == nil {
			keyErr = errors.New("nexus key is not RSA")
		}
	})
	return parsedKey, keyErr
}

func (a *Auth) base() string {
	if a.Base == "" {
		return DefaultAuth
	}
	return a.Base
}

func (a *Auth) callback() string {
	if a.Callback == "" {
		return DefaultCallback
	}
	return a.Callback
}

// random returns n random bytes as base64url.
func random(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// page is the short browser page shown after sign-in.
func page(w http.ResponseWriter, status int, text string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Modvault</title>`+
		`<body style="background:#07080b;color:#e8eaf0;font:16px system-ui,sans-serif;display:grid;place-items:center;height:90vh">`+
		`<p style="max-width:32em;text-align:center">%s</p>`, html.EscapeString(text))
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// Session is a live sign-in: it hands out an access token, refreshes it when
// it expires and saves the new refresh token in Store.
type Session struct {
	Auth  *Auth
	Store Keys // the refresh token
	// Changed, if set, gets the user after every token refresh (Premium may
	// have started or ended) and an empty user once the sign-in stops
	// working. It is called without the session lock held.
	Changed func(User)

	mu     sync.Mutex
	tokens Tokens
}

// NewSession makes a session with tokens already received; with empty Tokens
// the refresh token is read from Store on the first request.
func NewSession(auth *Auth, store Keys, t Tokens) *Session {
	return &Session{Auth: auth, Store: store, tokens: t}
}

// Token returns a valid access token; stale means Nexus has just rejected
// the previous one and a new one is needed.
func (s *Session) Token(ctx context.Context, stale bool) (string, error) {
	s.mu.Lock()
	if !stale && s.tokens.Access != "" && time.Now().Before(s.tokens.Expires) {
		defer s.mu.Unlock()
		return s.tokens.Access, nil
	}
	user, err := s.refresh(ctx)
	access := s.tokens.Access
	s.mu.Unlock()
	if s.Changed != nil && (err == nil || errors.Is(err, ErrSignedOut)) {
		s.Changed(user)
	}
	return access, err
}

func (s *Session) refresh(ctx context.Context) (User, error) {
	refresh := s.tokens.Refresh
	if refresh == "" {
		var err error
		if refresh, err = s.Store.Load(); err != nil {
			return User{}, err
		}
		if refresh == "" {
			s.tokens = Tokens{}
			return User{}, ErrSignedOut
		}
	}
	t, err := s.Auth.Refresh(ctx, refresh)
	if errors.Is(err, ErrSignedOut) {
		s.tokens = Tokens{}
		s.Store.Delete()
		return User{}, err
	}
	if err != nil {
		return User{}, err
	}
	if t.Refresh != refresh {
		if err := s.Store.Save(t.Refresh); err != nil {
			return User{}, err
		}
	}
	s.tokens = t
	return s.Auth.User(t.Access)
}

// Logout revokes the sign-in on Nexus and erases it on the PC. If Nexus is
// unreachable the sign-in is erased anyway: access can be revoked on the site.
func (s *Session) Logout(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	refresh := s.tokens.Refresh
	if refresh == "" {
		refresh, _ = s.Store.Load()
	}
	if refresh != "" {
		s.Auth.Revoke(ctx, refresh)
	}
	s.tokens = Tokens{}
	return s.Store.Delete()
}
