package app

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// ComposeDeployment records the deployment settings explicitly supplied by the
// launcher. Do not export the process environment: it can contain credentials.
type ComposeDeployment struct{ BindIP, Port, Image, Timezone string }

func loadComposeDeployment() ComposeDeployment {
	value := func(key, fallback string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return fallback
	}
	return ComposeDeployment{BindIP: value("ORDARR_BIND_IP", "127.0.0.1"), Port: value("ORDARR_PORT", "8080"), Image: value("ORDARR_IMAGE", "ordarr:0.8.0-dev"), Timezone: value("TZ", "Etc/UTC")}
}
func composeEnvironment(c Config) (string, error) {
	d := c.Deployment
	if d.BindIP == "" {
		d.BindIP = "127.0.0.1"
	}
	if d.Port == "" {
		d.Port = "8080"
	}
	if d.Image == "" {
		d.Image = "ordarr:0.8.0-dev"
	}
	if d.Timezone == "" {
		d.Timezone = "Etc/UTC"
	}
	var out strings.Builder
	out.WriteString("# Exported Ordarr Docker Compose deployment settings.\n# Use: docker compose --env-file ordarr.env up -d\n# Keep the persistent volume to retain collections, connections and credentials.\n# Container listen/data paths are supplied by compose.yaml.\n# Host bind/port use Compose defaults when launcher metadata is unavailable.\n")
	for _, entry := range [][2]string{{"ORDARR_BIND_IP", d.BindIP}, {"ORDARR_PORT", d.Port}, {"ORDARR_IMAGE", d.Image}, {"ORDARR_SECURE_COOKIES", strconv.FormatBool(c.SecureCookies)}, {"TZ", d.Timezone}} {
		// Compose single-quoted values are literal (including dollar signs). Deployment
		// identifiers do not need multiline or quote/backslash escape syntax.
		if strings.ContainsAny(entry[1], "\r\n\x00'\\") {
			return "", fmt.Errorf("unsupported deployment value")
		}
		fmt.Fprintf(&out, "%s='%s'\n", entry[0], entry[1])
	}
	return out.String(), nil
}
func (a *App) exportEnvironment(w http.ResponseWriter, r *http.Request) {
	data, err := composeEnvironment(a.config)
	if err != nil {
		http.Error(w, "A deployment setting cannot be represented safely in a Compose environment file.", 400)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="ordarr.env"`)
	w.Write([]byte(data))
}
