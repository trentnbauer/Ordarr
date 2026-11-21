package plex

import "testing"

func TestNormalizeBaseURL(t *testing.T) {
	for in, want := range map[string]string{
		"http://latte.agg.local:8991":            "http://latte.agg.local:8991",
		"http://Latte.agg.local:8991/":           "http://latte.agg.local:8991",
		"http://latte.agg.local:8991/sonarr1080": "http://latte.agg.local:8991/sonarr1080",
		"http://latte.agg.local:8991/a/b/":       "http://latte.agg.local:8991/a/b",
	} {
		if got, err := NormalizeBaseURL(in, true); err != nil || got != want {
			t.Errorf("%q: got %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"http://h:1/a?x=1", "http://h:1/a#f", "http://u:p@h:1/a", "http://h:1/a b", "http://h:1/a%2Fb", "http://h:1//a", "ftp://h/a"} {
		if got, err := NormalizeBaseURL(in, true); err == nil {
			t.Errorf("%q accepted as %q", in, got)
		}
	}
	if _, err := NormalizeBaseURL("http://h:1/a", false); err != ErrHTTPConsent {
		t.Errorf("want ErrHTTPConsent, got %v", err)
	}
}
