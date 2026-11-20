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
