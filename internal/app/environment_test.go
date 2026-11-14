package app

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestComposeEnvironmentExportAuthenticatedAndSecretFree(t *testing.T) {
	a, h := fixture(t)
	a.config.SecureCookies = true
	a.config.Deployment = ComposeDeployment{BindIP: "127.0.0.2", Port: "9123", Image: "ghcr.io/trentnbauer/ordarr:0.7.0-rc.1", Timezone: "Australia/Sydney"}
	t.Setenv("ORDARR_PLEX_TOKEN", "do-not-export-token")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "do-not-export-secret")
	w := request(h, "GET", "/settings/export.env", nil)
	if w.Code != http.StatusSeeOther {
		t.Fatal("unauthenticated export", w.Code)
	}
	session, csrf := setup(t, h)
	w = request(h, "GET", "/settings/export.env", nil, session, csrf)
	if w.Code != 200 || w.Header().Get("Content-Disposition") != `attachment; filename="ordarr.env"` {
		t.Fatal(w.Code, w.Header())
	}
	body := w.Body.String()
	for _, want := range []string{"ORDARR_BIND_IP='127.0.0.2'", "ORDARR_PORT='9123'", "ORDARR_SECURE_COOKIES='true'", "TZ='Australia/Sydney'"} {
		if !strings.Contains(body, want) {
			t.Fatal("missing", want, body)
		}
	}
	for _, forbidden := range []string{"do-not-export", "AWS_SECRET", "ORDARR_PLEX_TOKEN", a.config.DataDir} {
		if strings.Contains(body, forbidden) {
			t.Fatal("secret or host path exported", forbidden)
		}
	}
	// Exercise Compose itself, rather than assuming dotenv/shell quoting are identical.
	if _, err := exec.LookPath("docker"); err == nil {
		envFile := filepath.Join(t.TempDir(), "ordarr.env")
		if err = os.WriteFile(envFile, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("docker", "compose", "--env-file", envFile, "-f", "../../compose.yaml", "config", "--format", "json")
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
		data, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatal("Compose rejected export", err, string(data))
		}
		var config struct {
			Services map[string]struct {
				Image       string
				Environment map[string]string
				Ports       []struct {
					HostIP    string `json:"host_ip"`
					Published string
				}
			}
		}
		if err = json.Unmarshal(data, &config); err != nil {
			t.Fatal(err)
		}
		service := config.Services["ordarr"]
		if service.Image != a.config.Deployment.Image || service.Environment["TZ"] != "Australia/Sydney" || service.Environment["ORDARR_SECURE_COOKIES"] != "true" || len(service.Ports) != 1 || service.Ports[0].HostIP != "127.0.0.2" || service.Ports[0].Published != "9123" {
			t.Fatal(string(data))
		}
	}
}
func TestComposeExportLiteralValuesAndRejectsLineInjection(t *testing.T) {
	c := Config{Deployment: ComposeDeployment{Image: "literal-${UNRELATED_SECRET}"}}
	out, err := composeEnvironment(c)
	if err != nil || !strings.Contains(out, "ORDARR_IMAGE='literal-${UNRELATED_SECRET}'") {
		t.Fatal(out, err)
	}
	for _, value := range []string{"UTC\nAWS_SECRET=bad", "bad'quote", "bad\\escape"} {
		c.Deployment.Timezone = value
		if _, err = composeEnvironment(c); err == nil {
			t.Fatal("invalid value accepted")
		}
	}
}
func TestComposeDeploymentCapturesLauncherSettings(t *testing.T) {
	t.Setenv("ORDARR_BIND_IP", "127.0.0.2")
	t.Setenv("ORDARR_PORT", "9000")
	t.Setenv("ORDARR_IMAGE", "ordarr:custom")
	t.Setenv("TZ", "Australia/Sydney")
	d := loadComposeDeployment()
	if d.BindIP != "127.0.0.2" || d.Port != "9000" || d.Image != "ordarr:custom" || d.Timezone != "Australia/Sydney" {
		t.Fatal(d)
	}
}
