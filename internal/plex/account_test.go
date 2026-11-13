package plex

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// FixtureTransport redirects only the official account API origins into a test
// server. Production AccountClient has no configurable API origins.
type fixtureAccountTransport struct {
	base *url.URL
	rt   http.RoundTripper
}

func (f fixtureAccountTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "https" || (r.URL.Host != "clients.plex.tv" && r.URL.Host != "plex.tv") {
		return nil, errors.New("unexpected account origin")
	}
	copy := r.Clone(r.Context())
	u := *r.URL
	u.Scheme = f.base.Scheme
	u.Host = f.base.Host
	copy.URL = &u
	return f.rt.RoundTrip(copy)
}
func fixtureJWT(expiry int64) string {
	payload, _ := json.Marshal(map[string]any{"exp": expiry})
	return base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"EdDSA"}`)) + "." + base64.RawURLEncoding.EncodeToString(payload) + ".fixture-signature"
}
func verifyAssertion(t *testing.T, token string, pub ed25519.PublicKey, kid, clientID, nonce string) {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatal("invalid device assertion")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !ed25519.Verify(pub, []byte(parts[0]+"."+parts[1]), sig) {
		t.Fatal("invalid device signature")
	}
	headerBytes, _ := base64.RawURLEncoding.DecodeString(parts[0])
	claimsBytes, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var header map[string]any
	var claims map[string]any
	json.Unmarshal(headerBytes, &header)
	json.Unmarshal(claimsBytes, &claims)
	if header["kid"] != kid || header["alg"] != "EdDSA" || claims["iss"] != clientID || claims["aud"] != "plex.tv" {
		t.Fatal("incorrect device claims")
	}
	if nonce != "" && claims["nonce"] != nonce {
		t.Fatal("refresh nonce missing")
	}
	if claims["exp"].(float64) <= float64(time.Now().Unix()) || claims["exp"].(float64) > float64(time.Now().Add(6*time.Minute).Unix()) {
		t.Fatal("unbounded assertion expiry")
	}
}
func TestPlexJWTAccountProtocol(t *testing.T) {
	device := NewDevice()
	var mu sync.Mutex
	var pub ed25519.PublicKey
	var kid string
	var granted atomic.Bool
	token := fixtureJWT(time.Now().Add(7 * 24 * time.Hour).Unix())
	refreshed := fixtureJWT(time.Now().Add(6 * 24 * time.Hour).Unix())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Plex-Client-Identifier") != device.ClientID || r.Header.Get("Accept") != "application/json" {
			t.Error("missing device/JSON headers")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v2/pins":
			if r.Method != "POST" || r.Header.Get("X-Plex-Token") != "" {
				t.Error("bad PIN creation")
			}
			var body struct {
				Strong bool                                   `json:"strong"`
				JWK    struct{ Kty, Crv, X, Kid, Alg string } `json:"jwk"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || !body.Strong || body.JWK.Kty != "OKP" || body.JWK.Crv != "Ed25519" || body.JWK.Alg != "EdDSA" {
				t.Error("invalid public JWK")
			}
			key, _ := base64.RawURLEncoding.DecodeString(body.JWK.X)
			mu.Lock()
			pub = key
			kid = body.JWK.Kid
			mu.Unlock()
			io.WriteString(w, `{"id":42,"code":"strong-pin-code-42","expiresIn":900}`)
		case "/api/v2/pins/42":
			mu.Lock()
			verifyAssertion(t, r.URL.Query().Get("deviceJWT"), pub, kid, device.ClientID, "")
			mu.Unlock()
			body := map[string]any{"id": 42, "code": "strong-pin-code-42"}
			if granted.Load() {
				body["authToken"] = token
			}
			json.NewEncoder(w).Encode(body)
		case "/api/v2/user":
			if r.Header.Get("X-Plex-Token") != token && r.Header.Get("X-Plex-Token") != refreshed {
				t.Error("account token missing")
			}
			io.WriteString(w, `{"id":123,"username":"connected-user","friendlyName":"Friendly","restricted":false}`)
		case "/api/v2/resources":
			if r.URL.Query().Get("includeHttps") != "1" {
				t.Error("resource discovery flags missing")
			}
			io.WriteString(w, `[{"clientIdentifier":"server-one","name":"Home server","provides":"server","accessToken":"scoped-resource-token","owned":true,"connections":[{"uri":"https://server.example:32400","local":true},{"uri":"https://server.example:32400","local":true},{"uri":"http://169.254.169.254"}]}]`)
		case "/api/v2/auth/nonce":
			if r.Header.Get("X-Plex-Token") != "" {
				t.Error("refresh should use device credentials")
			}
			io.WriteString(w, `{"nonce":"one-use-fixture-nonce"}`)
		case "/api/v2/auth/token":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			verifyAssertion(t, body["jwt"], pub, kid, device.ClientID, "one-use-fixture-nonce")
			mu.Unlock()
			json.NewEncoder(w).Encode(map[string]string{"auth_token": refreshed})
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client := NewAccountClient(fixtureAccountTransport{base, http.DefaultTransport})
	pin, err := client.Start(context.Background(), device)
	if err != nil {
		t.Fatal(err)
	}
	if pin.ExpiresAt > time.Now().Add(10*time.Minute).Unix() || pin.ID != 42 {
		t.Fatal("unbounded PIN")
	}
	auth := AuthURL(device, pin)
	u, err := url.Parse(auth)
	if err != nil || u.Host != "app.plex.tv" || u.RawQuery != "" || !strings.Contains(u.Fragment, "clientID=") {
		t.Fatal("unsafe auth URL")
	}
	if _, err = client.Poll(context.Background(), device, pin); !errors.Is(err, ErrPending) {
		t.Fatal("pending token accepted", err)
	}
	granted.Store(true)
	result, err := client.Poll(context.Background(), device, pin)
	if err != nil {
		t.Fatal(err)
	}
	if result.User.ID != 123 || len(result.Credentials.Resources) != 1 || len(result.Credentials.Resources[0].Connections) != 1 || result.Credentials.Resources[0].AccessToken == result.Credentials.Token {
		t.Fatal("bad identity/resource scoping")
	}
	result, err = client.Refresh(context.Background(), result.Credentials)
	if err != nil || result.Credentials.Token != refreshed {
		t.Fatal("refresh failed", err)
	}
}
func TestPlexAccountFailureHandling(t *testing.T) {
	for _, status := range []int{401, 403, 404, 410, 429, 498, 500, 302} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", "https://untrusted.example")
				w.WriteHeader(status)
				io.WriteString(w, "do-not-log-this-token")
			}))
			defer s.Close()
			base, _ := url.Parse(s.URL)
			client := NewAccountClient(fixtureAccountTransport{base, http.DefaultTransport})
			_, err := client.Start(context.Background(), NewDevice())
			if err == nil || calls.Load() != 1 || strings.Contains(err.Error(), "do-not-log") {
				t.Fatal("unsafe retry/error", err)
			}
		})
	}
	device := NewDevice()
	if _, err := device.assertion("nonce"); err != nil {
		t.Fatal(err)
	}
	if _, err := tokenExpiry("opaque-legacy-token"); err == nil {
		t.Fatal("silently accepted token without JWT refresh contract")
	}
	if _, err := tokenExpiry(fixtureJWT(time.Now().Add(-time.Hour).Unix())); err == nil {
		t.Fatal("expired JWT accepted")
	}
}
func TestAccountRejectsAnonymousMalformedAndReflectedIdentity(t *testing.T) {
	token := fixtureJWT(time.Now().Add(time.Hour).Unix())
	for _, body := range []string{`{}`, `{"id":1,"username":"anonymous","anonymous":true}`, `{"id":1,"username":"` + token + `"}`} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
		base, _ := url.Parse(s.URL)
		_, err := NewAccountClient(fixtureAccountTransport{base, http.DefaultTransport}).Verify(context.Background(), AccountCredentials{Device: NewDevice(), Token: token})
		s.Close()
		if !errors.Is(err, ErrAccountResponse) || strings.Contains(err.Error(), token) {
			t.Fatal("bad identity accepted", err)
		}
	}
}
