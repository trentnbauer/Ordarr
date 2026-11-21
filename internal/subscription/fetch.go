// Package subscription fetches public collection files without trusting proxy
// settings or allowing a source URL to reach local services.
package subscription

import (
	"context"
	"crypto/tls"
	"errors"
	"github.com/trentnbauer/Ordarr/internal/portable"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var ErrFetch = errors.New("Source fetch failed. Use a public HTTPS raw collection file; the last valid version was retained.")

type Result struct {
	Data           []byte
	ETag, Modified string
	Unchanged      bool
}

func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.Port() != "" && u.Port() != "443" {
		return ErrFetch
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return ErrFetch
	}
	if ip, err := netip.ParseAddr(host); err == nil && !publicIP(ip) {
		return ErrFetch
	}
	return nil
}
func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return false
	}
	for _, prefix := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "2001::/32", "2002::/16", "64:ff9b::/96", "64:ff9b:1::/48"} {
		if netip.MustParsePrefix(prefix).Contains(ip) {
			return false
		}
	}
	return true
}

// publicHTTPS matches an HTTPS URL with a plain DNS host (or IPv4 address),
// the default port, and no credentials or fragment. "@" is allowed only in the
// path or query (jsDelivr-style "repo@branch" URLs), never in the authority. ValidateURL and the
// dialer still apply; this is a final syntactic gate before the request.
var publicHTTPS = regexp.MustCompile(`^https://[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)*\.?(?::443)?(?:[/?][^\s#]*)?$`)

// safeClient is an HTTP client that only ever connects to public addresses (every resolved
// address is checked at dial time), follows at most three redirects, each re-validated, and
// ignores proxy settings. The returned function releases its connections.
func safeClient() (*http.Client, func()) {
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, DisableKeepAlives: true, ResponseHeaderTimeout: 10 * time.Second, MaxResponseHeaderBytes: 32 << 10}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, ErrFetch
		}
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(ips) == 0 {
			return nil, ErrFetch
		}
		for _, ip := range ips {
			if !publicIP(ip) {
				return nil, ErrFetch
			}
		}
		dialer := net.Dialer{Timeout: 5 * time.Second}
		for _, ip := range ips {
			conn, e := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if e == nil {
				return conn, nil
			}
		}
		return nil, ErrFetch
	}
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 {
			return ErrFetch
		}
		return ValidateURL(req.URL.String())
	}}
	return client, transport.CloseIdleConnections
}

// ErrImage is returned for any cover image that cannot be fetched safely.
var ErrImage = errors.New("Cover image fetch failed. Use a public https:// link to a PNG, JPEG or WebP image of at most 2 MiB.")

// MaxImageBytes caps a cover image download.
const MaxImageBytes = 2 << 20

// FetchImage downloads a collection's cover image under the same rules as source files: https
// only, public hosts only. The bytes are sniffed, not trusted: only real PNG, JPEG or WebP data is
// returned (never SVG, which could carry script), together with the sniffed content type.
func FetchImage(ctx context.Context, raw string) ([]byte, string, error) {
	if ValidateURL(raw) != nil || !publicHTTPS.MatchString(raw) {
		return nil, "", ErrImage
	}
	client, closeClient := safeClient()
	defer closeClient()
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return nil, "", ErrImage
	}
	req.Header.Set("Accept", "image/png, image/jpeg, image/webp")
	res, err := client.Do(req)
	if err != nil {
		return nil, "", ErrImage
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, "", ErrImage
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, MaxImageBytes+1))
	if err != nil || len(data) == 0 || len(data) > MaxImageBytes {
		return nil, "", ErrImage
	}
	kind, ok := sniffImage(data)
	if !ok {
		return nil, "", ErrImage
	}
	return data, kind, nil
}

// sniffImage identifies the bytes themselves rather than trusting the server's Content-Type, and
// accepts only raster formats (no SVG, which can carry script, and no HTML served as an image).
func sniffImage(data []byte) (string, bool) {
	if len(data) == 0 || len(data) > MaxImageBytes {
		return "", false
	}
	switch kind := http.DetectContentType(data); kind {
	case "image/png", "image/jpeg", "image/webp":
		return kind, true
	}
	return "", false
}

func Fetch(ctx context.Context, raw, etag, modified string) (Result, error) {
	var out Result
	if ValidateURL(raw) != nil || !publicHTTPS.MatchString(raw) {
		return out, ErrFetch
	}
	client, closeClient := safeClient()
	defer closeClient()
	req, err := http.NewRequestWithContext(ctx, "GET", raw, nil)
	if err != nil {
		return out, ErrFetch
	}
	req.Header.Set("Accept", "application/json, application/yaml, text/yaml, text/plain")
	if len(etag) < 1024 {
		req.Header.Set("If-None-Match", etag)
	}
	if len(modified) < 1024 {
		req.Header.Set("If-Modified-Since", modified)
	}
	res, err := client.Do(req)
	if err != nil {
		return out, ErrFetch
	}
	defer res.Body.Close()
	if res.StatusCode == 304 {
		out.Unchanged = true
		return out, nil
	}
	if res.StatusCode != 200 {
		return out, ErrFetch
	}
	out.Data, err = io.ReadAll(io.LimitReader(res.Body, portable.MaxBytes+1))
	if err != nil || len(out.Data) > portable.MaxBytes {
		return Result{}, ErrFetch
	}
	out.ETag = res.Header.Get("ETag")
	out.Modified = res.Header.Get("Last-Modified")
	if len(out.ETag) > 1024 || len(out.Modified) > 1024 {
		return Result{}, ErrFetch
	}
	return out, nil
}
