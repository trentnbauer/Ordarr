package app

import (
	"strings"
	"testing"
)

func TestSidebarShowsVersionAndRepoLinkUnderTheUserBlock(t *testing.T) {
	previous := Version
	Version = "v9.9.9"
	t.Cleanup(func() { Version = previous })
	_, h := fixture(t)
	session, csrf := setup(t, h)
	body := request(h, "GET", "/", nil, session, csrf).Body.String()
	user := strings.Index(body, `class="side-foot"`)
	ver := strings.Index(body, `class="side-ver"`)
	if user < 0 || ver < user || !strings.Contains(body, "Ordarr v9.9.9") || !strings.Contains(body, `href="https://github.com/trentnbauer/Ordarr"`) {
		t.Fatal("version and repository link must sit below the signed-in block", user, ver)
	}
}
