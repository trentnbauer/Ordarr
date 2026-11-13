// Package plex contains the real, read-only Plex Media Server adapter.
package plex

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var ErrAuth = errors.New("Plex rejected the token or denied library access. Check or replace the token.")
var ErrUnavailable = errors.New("Plex could not be reached securely. Check the server address, network access and TLS certificate.")
var ErrResponse = errors.New("Plex returned an invalid or incomplete response; the saved connection was not replaced.")
var ErrRateLimit = errors.New("Plex is rate limiting requests. Wait before checking again.")
var ErrRedirect = errors.New("Plex redirected the request. Use the final server URL; credentials are never forwarded through redirects.")
var ErrHTTPConsent = errors.New("HTTP sends the Plex token without transport encryption. Confirm that this is a trusted local connection, or use HTTPS.")
var ErrURL = errors.New("Use a server origin such as https://server.example:32400, without a path, query, fragment or embedded credentials.")

type Library struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Kind  string `json:"type"`
}
type Snapshot struct {
	MachineID, Name, Version string
	Libraries                []Library
}
type Client struct{ http *http.Client }

func blockedIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return !ip.IsValid() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || (ip.String() == "255.255.255.255" || ip.String() == "fd00:ec2::254") || netip.MustParsePrefix("100.100.100.200/32").Contains(ip)
}
func NormalizeURL(raw string, allowHTTP bool) (string, error) {
	if len(raw) > 2048 {
		return "", ErrURL
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Opaque != "" || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" {
		return "", ErrURL
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" || strings.ContainsAny(host, "%\\ \t\r\n") || host == "proxy" || host == "metadata.google.internal" || host == "metadata.azure.internal" {
		return "", ErrURL
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", ErrURL
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return "", ErrURL
	}
	if ip, err := netip.ParseAddr(host); err == nil && blockedIP(ip) {
		return "", ErrURL
	}
	if u.Scheme == "http" && !allowHTTP {
		return "", ErrHTTPConsent
	}
	u.Path = ""
	u.Host = strings.ToLower(u.Host)
	return u.String(), nil
}

// New preserves inherited proxy and TLS settings. Direct DNS resolutions are
// checked at dial time; proxied destination resolution is governed by the proxy.
func New() *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 2
	transport.ResponseHeaderTimeout = 4 * time.Second
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, ErrUnavailable
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(ips) == 0 {
			return nil, ErrUnavailable
		}
		for _, ip := range ips {
			if blockedIP(ip) {
				return nil, ErrUnavailable
			}
		}
		dialer := net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}
		for _, ip := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
		}
		return nil, ErrUnavailable
	}
	return &Client{http: &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func ValidToken(token string) bool {
	if len(token) < 1 || len(token) > 4096 {
		return false
	}
	for _, c := range token {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}
func (c *Client) get(ctx context.Context, base, path, token, clientID string, target any, headers *http.Header) error {
	// Only fixed application paths reach this method; response keys/URLs are never followed.
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
		if err != nil {
			return ErrURL
		}
		if start := req.URL.Query().Get("X-Plex-Container-Start"); start != "" {
			req.Header.Set("X-Plex-Container-Start", start)
			req.Header.Set("X-Plex-Container-Size", req.URL.Query().Get("X-Plex-Container-Size"))
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-Plex-Token", token)
		req.Header.Set("X-Plex-Client-Identifier", clientID)
		req.Header.Set("X-Plex-Product", "Ordarr")
		req.Header.Set("X-Plex-Version", "0.3.0-dev")
		res, err := c.http.Do(req)
		if err != nil {
			return ErrUnavailable
		}
		code := res.StatusCode
		if code == 502 || code == 503 || code == 504 {
			res.Body.Close()
			// Never retry early when a server explicitly requests a later retry.
			if attempt == 0 && res.Header.Get("Retry-After") == "" {
				select {
				case <-ctx.Done():
					return ErrUnavailable
				case <-time.After(100 * time.Millisecond):
					continue
				}
			}
			return ErrUnavailable
		}
		switch {
		case code == 401 || code == 403 || code == 498:
			res.Body.Close()
			return ErrAuth
		case code == 429:
			res.Body.Close()
			return ErrRateLimit
		case code >= 300 && code < 400:
			res.Body.Close()
			return ErrRedirect
		case code == 404:
			res.Body.Close()
			return errNotFound
		case code != 200:
			res.Body.Close()
			return ErrResponse
		}
		data, err := io.ReadAll(io.LimitReader(res.Body, 1<<20+1))
		res.Body.Close()
		if err != nil || len(data) > 1<<20 {
			return ErrResponse
		}
		if err = json.Unmarshal(data, target); err != nil {
			return ErrResponse
		}
		if headers != nil {
			*headers = res.Header.Clone()
		}
		return nil
	}
	return ErrUnavailable
}

var errNotFound = errors.New("Plex endpoint not found")

// Inspect uses authenticated endpoints rather than the unauthenticated /identity.
// It validates connectivity/read access, never playlist ownership or write permission.
func (c *Client) Inspect(ctx context.Context, base, token, clientID string, allowHTTP bool) (Snapshot, error) {
	var result Snapshot
	normalized, err := NormalizeURL(base, allowHTTP)
	if err != nil {
		return result, err
	}
	if !ValidToken(token) || len(clientID) < 1 || len(clientID) > 128 {
		return result, ErrAuth
	}
	var root struct {
		Container *struct {
			MachineID string `json:"machineIdentifier"`
			Name      string `json:"friendlyName"`
			Version   string `json:"version"`
		} `json:"MediaContainer"`
	}
	if err = c.get(ctx, normalized, "/", token, clientID, &root, nil); err != nil {
		return result, err
	}
	if root.Container == nil || !safeValue(root.Container.MachineID, 256) || !safeValue(root.Container.Name, 200) || !safeValue(root.Container.Version, 200) {
		return result, ErrResponse
	}
	// Current docs specify /library/sections/all. The legacy endpoint is also shown
	// in Plex's authentication examples; use it only for a definite 404.
	libraries, err := c.libraries(ctx, normalized, "/library/sections/all", token, clientID)
	if errors.Is(err, errNotFound) {
		libraries, err = c.libraries(ctx, normalized, "/library/sections", token, clientID)
	}
	if err != nil {
		return result, err
	}
	result = Snapshot{MachineID: root.Container.MachineID, Name: root.Container.Name, Version: root.Container.Version, Libraries: libraries}
	// Never echo a token even if a remote server reflects it into a display field.
	if strings.Contains(result.Name, token) || strings.Contains(result.Version, token) || strings.Contains(result.MachineID, token) {
		return Snapshot{}, ErrResponse
	}
	for _, lib := range libraries {
		if strings.Contains(lib.Title, token) || strings.Contains(lib.Key, token) || strings.Contains(lib.Kind, token) {
			return Snapshot{}, ErrResponse
		}
	}
	return result, nil
}
func safeValue(v string, max int) bool {
	return len(v) > 0 && len(v) <= max && !strings.ContainsAny(v, "\x00\r\n")
}
func (c *Client) libraries(ctx context.Context, base, path, token, clientID string) ([]Library, error) {
	// Library discovery itself may be paginated. Refuse inconsistent or partial
	// snapshots, duplicates, excessive data and changing totals instead of replacing
	// the last known complete result. No media items are scanned in this milestone.
	const pageSize = 100
	result := []Library{}
	seen := map[string]bool{}
	expectedTotal := -1
	for offset := 0; offset <= 1000; {
		// Query parameters are fixed non-secret pagination fields; token stays in headers.
		route := path + "?X-Plex-Container-Start=" + strconv.Itoa(offset) + "&X-Plex-Container-Size=" + strconv.Itoa(pageSize)
		var body struct {
			Container *struct {
				Size      *int      `json:"size"`
				Offset    *int      `json:"offset"`
				Total     *int      `json:"totalSize"`
				Directory []Library `json:"Directory"`
			} `json:"MediaContainer"`
		}
		var headers http.Header
		if err := c.get(ctx, base, route, token, clientID, &body, &headers); err != nil {
			return nil, err
		}
		if body.Container != nil {
			for name, field := range map[string]**int{"X-Plex-Container-Start": &body.Container.Offset, "X-Plex-Container-Total-Size": &body.Container.Total} {
				if raw := headers.Get(name); raw != "" {
					n, err := strconv.Atoi(raw)
					if err != nil || n < 0 || (*field != nil && **field != n) {
						return nil, ErrResponse
					}
					*field = &n
				}
			}
		}
		mc := body.Container
		if mc == nil || mc.Size == nil || *mc.Size != len(mc.Directory) || *mc.Size > 1000 {
			return nil, ErrResponse
		}
		if mc.Offset != nil && *mc.Offset != offset {
			return nil, ErrResponse
		}
		if mc.Total != nil {
			if *mc.Total < 0 || *mc.Total > 1000 || (expectedTotal >= 0 && expectedTotal != *mc.Total) {
				return nil, ErrResponse
			}
			expectedTotal = *mc.Total
		}
		if offset > 0 && mc.Offset == nil {
			return nil, ErrResponse
		}
		for _, lib := range mc.Directory {
			if !safeValue(lib.Key, 128) || !safeValue(lib.Title, 200) || !safeValue(lib.Kind, 64) || seen[lib.Key] {
				return nil, ErrResponse
			}
			seen[lib.Key] = true
			result = append(result, lib)
		}
		offset += len(mc.Directory)
		if len(result) > 1000 {
			return nil, ErrResponse
		}
		if mc.Total == nil {
			if mc.Offset == nil {
				return result, nil
			} // An explicitly unpaginated response.
			return nil, ErrResponse // Cannot establish a complete paginated snapshot.
		}
		if offset == expectedTotal {
			return result, nil
		}
		if offset > expectedTotal || len(mc.Directory) == 0 || mc.Offset == nil {
			return nil, ErrResponse
		}
	}
	return nil, ErrResponse
}
