package plex

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var ErrAccount = errors.New("Plex account authorisation could not be verified. Try again or authorise the device again.")
var ErrPending = errors.New("Plex authorisation is still pending. Complete sign-in at Plex, then check again.")
var ErrExpired = errors.New("This Plex sign-in attempt has expired. Start a new attempt.")
var ErrAccountResponse = errors.New("Plex returned an incomplete account response. Existing account credentials were retained.")

type Device struct {
	ClientID, Kid string
	PrivateKey    []byte
}
type PIN struct {
	ID        int64
	Code      string
	ExpiresAt int64
}
type AccountUser struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	FriendlyName string `json:"friendlyName"`
	Restricted   bool   `json:"restricted"`
	Anonymous    bool   `json:"anonymous"`
}
type ResourceConnection struct {
	URI   string `json:"uri"`
	Local bool   `json:"local"`
	Relay bool   `json:"relay"`
}
type Resource struct {
	MachineID   string               `json:"clientIdentifier"`
	Name        string               `json:"name"`
	Provides    string               `json:"provides"`
	AccessToken string               `json:"accessToken"`
	Owned       bool                 `json:"owned"`
	Connections []ResourceConnection `json:"connections"`
}

// AccountCredentials must only be persisted through the encrypted vault.
type AccountCredentials struct {
	Device    Device
	Token     string
	Resources []Resource
}
type AccountResult struct {
	User        AccountUser
	Credentials AccountCredentials
	ExpiresAt   int64
}
type AccountClient struct{ http *http.Client }

// A transport can be supplied by tests; production uses the same verified TLS and
// inherited proxy configuration as the PMS adapter. API origins remain fixed.
func NewAccountClient(transport http.RoundTripper) *AccountClient {
	client := New().http
	if transport != nil {
		client.Transport = transport
	}
	return &AccountClient{http: client}
}
func NewDevice() Device {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	bytes := make([]byte, 32)
	if _, err = rand.Read(bytes); err != nil {
		panic(err)
	}
	return Device{ClientID: base64.RawURLEncoding.EncodeToString(bytes[:16]), Kid: base64.RawURLEncoding.EncodeToString(bytes[16:]), PrivateKey: private}
}
func (d Device) assertion(nonce string) (string, error) {
	if len(d.PrivateKey) != ed25519.PrivateKeySize || !safeValue(d.ClientID, 128) || !safeValue(d.Kid, 128) {
		return "", ErrAccountResponse
	}
	header, _ := json.Marshal(map[string]string{"alg": "EdDSA", "typ": "JWT", "kid": d.Kid})
	claims := map[string]any{"aud": "plex.tv", "iss": d.ClientID, "iat": time.Now().Unix(), "exp": time.Now().Add(5 * time.Minute).Unix(), "scope": "username,friendly_name,restricted,anonymous"}
	if nonce != "" {
		claims["nonce"] = nonce
	}
	payload, _ := json.Marshal(claims)
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	signature := ed25519.Sign(ed25519.PrivateKey(d.PrivateKey), []byte(unsigned))
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}
func (c *AccountClient) request(ctx context.Context, method, origin, path string, d Device, token string, body any, result any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return ErrAccountResponse
		}
		reader = strings.NewReader(string(data))
	}
	req, err := http.NewRequestWithContext(ctx, method, origin+path, reader)
	if err != nil {
		return ErrAccountResponse
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Client-Identifier", d.ClientID)
	req.Header.Set("X-Plex-Product", "Ordarr")
	req.Header.Set("X-Plex-Version", clientVersion)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("X-Plex-Token", token)
	}
	// Account POSTs are never automatically retried; duplicate PIN/device operations
	// and nonce reuse must require an explicit user retry.
	res, err := c.http.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == 401 || res.StatusCode == 403 || res.StatusCode == 498:
		return ErrAccount
	case res.StatusCode == 404 || res.StatusCode == 410:
		return ErrExpired
	case res.StatusCode == 429:
		return ErrRateLimit
	case res.StatusCode >= 300 && res.StatusCode < 400:
		return ErrRedirect
	case res.StatusCode < 200 || res.StatusCode >= 300:
		return ErrAccountResponse
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return ErrAccountResponse
	}
	if err = json.Unmarshal(data, result); err != nil {
		return ErrAccountResponse
	}
	return nil
}
func (c *AccountClient) Start(ctx context.Context, d Device) (PIN, error) {
	if len(d.PrivateKey) != ed25519.PrivateKeySize {
		return PIN{}, ErrAccountResponse
	}
	pub := ed25519.PrivateKey(d.PrivateKey).Public().(ed25519.PublicKey)
	body := map[string]any{"strong": true, "jwk": map[string]string{"kty": "OKP", "crv": "Ed25519", "x": base64.RawURLEncoding.EncodeToString(pub), "kid": d.Kid, "alg": "EdDSA"}}
	var response struct {
		ID        int64  `json:"id"`
		Code      string `json:"code"`
		ExpiresIn int64  `json:"expiresIn"`
		ExpiresAt string `json:"expiresAt"`
	}
	if err := c.request(ctx, "POST", "https://clients.plex.tv", "/api/v2/pins", d, "", body, &response); err != nil {
		return PIN{}, err
	}
	if response.ID <= 0 || len(response.Code) < 8 || len(response.Code) > 128 {
		return PIN{}, ErrAccountResponse
	}
	for _, ch := range response.Code {
		if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_') {
			return PIN{}, ErrAccountResponse
		}
	}
	expires := time.Now().Add(10 * time.Minute).Unix()
	if response.ExpiresIn > 0 {
		expires = min(expires, time.Now().Unix()+response.ExpiresIn)
	}
	if response.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, response.ExpiresAt)
		if err != nil {
			return PIN{}, ErrAccountResponse
		}
		expires = min(expires, t.Unix())
	}
	if expires <= time.Now().Unix() {
		return PIN{}, ErrExpired
	}
	return PIN{ID: response.ID, Code: response.Code, ExpiresAt: expires}, nil
}
func AuthURL(d Device, p PIN) string {
	query := url.Values{"clientID": {d.ClientID}, "code": {p.Code}, "context[device][product]": {"Ordarr"}}
	// No forward URL supplied: return/poll is a local, authenticated CSRF-protected
	// operation. No callback tokens or external redirect destinations are accepted.
	return "https://app.plex.tv/auth#?" + query.Encode()
}
func (c *AccountClient) Poll(ctx context.Context, d Device, p PIN) (AccountResult, error) {
	if time.Now().Unix() >= p.ExpiresAt {
		return AccountResult{}, ErrExpired
	}
	assertion, err := d.assertion("")
	if err != nil {
		return AccountResult{}, err
	}
	var response struct {
		ID    int64  `json:"id"`
		Code  string `json:"code"`
		Token string `json:"authToken"`
	}
	path := "/api/v2/pins/" + strconv.FormatInt(p.ID, 10) + "?" + url.Values{"deviceJWT": {assertion}}.Encode()
	if err = c.request(ctx, "GET", "https://clients.plex.tv", path, d, "", nil, &response); err != nil {
		return AccountResult{}, err
	}
	if response.ID != p.ID || response.Code != p.Code {
		return AccountResult{}, ErrAccountResponse
	}
	if response.Token == "" {
		return AccountResult{}, ErrPending
	}
	return c.Verify(ctx, AccountCredentials{Device: d, Token: response.Token})
}

// tokenExpiry reads a scheduling hint, never account identity or permissions.
// Identity is independently verified by the authenticated Plex user endpoint.
func tokenExpiry(token string) (int64, error) {
	if !ValidToken(token) {
		return 0, ErrAccountResponse
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return 0, ErrAccountResponse
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, ErrAccountResponse
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(data, &claims) != nil || claims.Exp <= time.Now().Unix()+30 || claims.Exp > time.Now().Add(8*24*time.Hour).Unix() {
		return 0, ErrAccountResponse
	}
	return claims.Exp, nil
}
func (c *AccountClient) Verify(ctx context.Context, credentials AccountCredentials) (AccountResult, error) {
	expiry, err := tokenExpiry(credentials.Token)
	if err != nil {
		return AccountResult{}, err
	}
	var user AccountUser
	if err = c.request(ctx, "GET", "https://plex.tv", "/api/v2/user", credentials.Device, credentials.Token, nil, &user); err != nil {
		return AccountResult{}, err
	}
	if user.ID <= 0 || user.Anonymous || !safeValue(user.Username, 200) || strings.Contains(user.Username, credentials.Token) || strings.Contains(user.FriendlyName, credentials.Token) || len(user.FriendlyName) > 200 {
		return AccountResult{}, ErrAccountResponse
	}
	var resources []Resource
	if err = c.request(ctx, "GET", "https://clients.plex.tv", "/api/v2/resources?includeHttps=1&includeRelay=1&includeIPv6=1", credentials.Device, credentials.Token, nil, &resources); err != nil {
		return AccountResult{}, err
	}
	filtered := []Resource{}
	seen := map[string]bool{}
	for _, resource := range resources {
		if !containsServer(resource.Provides) {
			continue
		}
		if len(filtered) >= 50 || !safeValue(resource.MachineID, 256) || !safeValue(resource.Name, 200) || !ValidToken(resource.AccessToken) || seen[resource.MachineID] || len(resource.Connections) > 20 || strings.Contains(resource.Name, credentials.Token) || strings.Contains(resource.Name, resource.AccessToken) || strings.Contains(resource.MachineID, credentials.Token) || strings.Contains(resource.MachineID, resource.AccessToken) {
			return AccountResult{}, ErrAccountResponse
		}
		seen[resource.MachineID] = true
		valid := []ResourceConnection{}
		uris := map[string]bool{}
		for _, connection := range resource.Connections {
			uri, err := NormalizeURL(connection.URI, true)
			if err != nil {
				continue
			}
			if strings.Contains(uri, credentials.Token) || strings.Contains(uri, resource.AccessToken) {
				return AccountResult{}, ErrAccountResponse
			}
			if !uris[uri] {
				connection.URI = uri
				valid = append(valid, connection)
				uris[uri] = true
			}
		}
		resource.Connections = valid
		filtered = append(filtered, resource)
	}
	credentials.Resources = filtered
	return AccountResult{User: user, Credentials: credentials, ExpiresAt: expiry}, nil
}
func containsServer(provides string) bool {
	for _, part := range strings.Split(provides, ",") {
		if strings.TrimSpace(part) == "server" {
			return true
		}
	}
	return false
}
func (c *AccountClient) Refresh(ctx context.Context, credentials AccountCredentials) (AccountResult, error) {
	var nonce struct {
		Nonce string `json:"nonce"`
	}
	if err := c.request(ctx, "GET", "https://clients.plex.tv", "/api/v2/auth/nonce", credentials.Device, "", nil, &nonce); err != nil {
		return AccountResult{}, err
	}
	if !safeValue(nonce.Nonce, 512) {
		return AccountResult{}, ErrAccountResponse
	}
	assertion, err := credentials.Device.assertion(nonce.Nonce)
	if err != nil {
		return AccountResult{}, err
	}
	var token struct {
		Token string `json:"auth_token"`
	}
	if err = c.request(ctx, "POST", "https://clients.plex.tv", "/api/v2/auth/token", credentials.Device, "", map[string]string{"jwt": assertion}, &token); err != nil {
		return AccountResult{}, err
	}
	credentials.Token = token.Token
	return c.Verify(ctx, credentials)
}
