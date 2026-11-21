package app

import (
	"regexp"
	"strings"
	"testing"
)

// The stylesheet has a dark palette that follows the system (and can be forced either way with the
// sidebar toggle). The forced-dark and system-dark blocks must declare exactly the same values.
func TestDarkThemeBlocksAgreeAndCoverEveryColourToken(t *testing.T) {
	cssBytes, err := assets.ReadFile("static/app.css")
	if err != nil {
		t.Fatal(err)
	}
	css := string(cssBytes)
	declarations := func(selector string) []string {
		t.Helper()
		i := strings.Index(css, selector)
		if i < 0 {
			t.Fatalf("missing %s", selector)
		}
		open := strings.Index(css[i:], "{") + i
		end := strings.Index(css[open:], "}") + open
		return regexp.MustCompile(`--[a-z0-9-]+:[^;]+;|color-scheme:[a-z]+;`).FindAllString(css[open:end], -1)
	}
	forced := declarations(`:root[data-theme="dark"]`)
	system := declarations(`:root:not([data-theme="light"])`)
	if len(forced) < 25 || strings.Join(forced, "") != strings.Join(system, "") {
		t.Fatalf("dark blocks differ or are incomplete: %d vs %d declarations", len(forced), len(system))
	}
	if !strings.Contains(css, "@media (prefers-color-scheme:dark)") {
		t.Fatal("dark theme does not follow the system")
	}
	// Every colour token the light theme defines must also be defined for dark.
	light := declarations(":root{")
	have := map[string]bool{}
	for _, d := range forced {
		have[d[:strings.Index(d, ":")]] = true
	}
	for _, d := range light {
		name := d[:strings.Index(d, ":")]
		if strings.HasPrefix(name, "--color-") && !have[name] {
			t.Errorf("dark theme does not define %s", name)
		}
	}
	// Colours outside the token blocks would not follow the theme.
	rest := css
	for _, block := range []string{":root{", `:root[data-theme="dark"]{`, `@media (prefers-color-scheme:dark){`} {
		if i := strings.Index(rest, block); i >= 0 {
			rest = rest[:i] + rest[i+len(block):]
		}
	}
	body := regexp.MustCompile(`(?s):root[^{]*\{[^}]*\}`).ReplaceAllString(css, "")
	if m := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`).FindString(body); m != "" {
		t.Errorf("hard-coded colour %s outside the theme tokens", m)
	}
}

// The saved theme is applied before first paint, and the toggle is in the sidebar footer.
func TestThemeToggleAndEarlyScript(t *testing.T) {
	_, h := fixture(t)
	session, csrf := setup(t, h)
	body := request(h, "GET", "/", nil, session, csrf).Body.String()
	script := strings.Index(body, `<script src="/static/theme.js"></script>`)
	if script < 0 || script > strings.Index(body, "<body") {
		t.Fatal("theme.js must be a blocking script in the head so the theme applies before paint")
	}
	if !strings.Contains(body, `type="button" class="theme-toggle" id="theme-toggle"`) {
		t.Fatal("theme toggle missing from the sidebar footer (it must not submit the sign-out form)")
	}
	js, err := assets.ReadFile("static/theme.js")
	if err != nil || !strings.Contains(string(js), "ordarr-theme") || !strings.Contains(string(js), "data-theme") {
		t.Fatal("theme script missing or incomplete", err)
	}
}
