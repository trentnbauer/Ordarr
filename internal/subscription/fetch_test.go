package subscription

import (
	"context"
	"net/netip"
	"testing"
)

func TestSourceURLAndAddressBoundaries(t *testing.T) {
	for _, raw := range []string{"http://example.com/a", "https://127.0.0.1/a", "https://[::ffff:127.0.0.1]/a", "https://user:pass@example.com/a", "https://example.com:8080/a", "https://localhost/a", "file:///tmp/a", "https://100.100.100.100/a", "https://169.254.169.254/a"} {
		if ValidateURL(raw) == nil {
			t.Fatal("accepted", raw)
		}
	}
	for _, ip := range []string{"10.1.2.3", "192.168.1.1", "::1", "fe80::1", "2001:db8::1", "64:ff9b::7f00:1"} {
		if publicIP(netip.MustParseAddr(ip)) {
			t.Fatal(ip)
		}
	}
	if err := ValidateURL("https://raw.githubusercontent.com/user/repo/main/order.yaml"); err != nil {
		t.Fatal(err)
	}
	if _, err := Fetch(context.Background(), "https://127.0.0.1/secret", "", ""); err == nil {
		t.Fatal("local fetch accepted")
	}
}

func TestPublicHTTPSPattern(t *testing.T) {
	for _, ok := range []string{"https://raw.githubusercontent.com/trentnbauer/OrdarrCollections/main/mcu-main-timeline.yaml", "https://example.com:443/a%20b.yaml?ref=main", "https://example.com", "https://cdn.jsdelivr.net/gh/fans/orders@main/order.yaml"} {
		if !publicHTTPS.MatchString(ok) {
			t.Error("rejected", ok)
		}
	}
	for _, bad := range []string{"http://example.com/a.yaml", "https://user@example.com/a", "https://user:pw@example.com/a", "https://example.com@evil.test/a", "https://example.com:8443/a", "https://example.com/a#frag", "https://exa mple.com/", "https://[::1]/a", "https://example.com/a\nb"} {
		if publicHTTPS.MatchString(bad) {
			t.Error("accepted", bad)
		}
	}
}

func TestCoverImagesMustBeRealRasterImagesFromPublicHTTPS(t *testing.T) {
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 32)...)
	jpeg := append([]byte("\xff\xd8\xff\xe0"), make([]byte, 32)...)
	webp := append([]byte("RIFF\x24\x00\x00\x00WEBPVP8 "), make([]byte, 32)...)
	for name, data := range map[string][]byte{"png": png, "jpeg": jpeg, "webp": webp} {
		if _, ok := sniffImage(data); !ok {
			t.Errorf("%s rejected", name)
		}
	}
	for name, data := range map[string][]byte{
		"svg":   []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"html":  []byte("<!doctype html><html></html>"),
		"gif":   []byte("GIF89a\x01\x00\x01\x00"),
		"empty": nil,
		"huge":  append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, MaxImageBytes)...),
	} {
		if _, ok := sniffImage(data); ok {
			t.Errorf("%s accepted as an image", name)
		}
	}
	// Only public https URLs are ever fetched: no network call is made for these.
	for _, raw := range []string{"http://example.com/a.png", "https://localhost/a.png", "https://127.0.0.1/a.png", "https://10.0.0.5/a.png", "https://user:pw@example.com/a.png", "https://example.com:8443/a.png", "file:///etc/passwd", ""} {
		if _, _, err := FetchImage(context.Background(), raw); err != ErrImage {
			t.Errorf("%q: got %v, want ErrImage", raw, err)
		}
	}
}
