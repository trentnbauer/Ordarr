package app

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/trentnbauer/Ordarr/internal/plex"
	"github.com/trentnbauer/Ordarr/internal/store"
)

// Only this public projection reaches templates. No ciphertext or raw token is
// included, even for the local administrator or while reporting validation errors.
type plexView struct {
	AccountLinked                                           bool
	ID, Name, BaseURL, MachineID, ServerName, ServerVersion string
	Revision                                                int
	AllowHTTP                                               bool
	CheckedAt, AttemptedAt, LastError                       string
	Libraries                                               []plex.Library
}

func plexPublic(c store.PlexConnection) plexView {
	return plexView{AccountLinked: c.AccountID == 1, ID: c.ID, Name: c.Name, BaseURL: c.BaseURL, MachineID: c.MachineID, ServerName: c.ServerName, ServerVersion: c.ServerVersion, Revision: c.Revision, AllowHTTP: c.AllowHTTP, CheckedAt: time.Unix(c.CheckedAt, 0).UTC().Format(time.RFC3339), AttemptedAt: time.Unix(c.AttemptedAt, 0).UTC().Format(time.RFC3339), LastError: c.LastError, Libraries: c.Libraries}
}
func (a *App) plexPage(ctx context.Context) (page, error) {
	p := page{Title: "Plex connections", Mode: "plex"}
	ids, err := a.store.PlexConnectionIDs(ctx)
	if err != nil {
		return p, err
	}
	for _, id := range ids {
		c, err := a.store.PlexConnection(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return p, err
		}
		p.PlexConnections = append(p.PlexConnections, plexPublic(c))
	}
	p.Account, p.AccountServers, err = a.accountProjection(ctx)
	return p, err
}
func (a *App) renderPlex(w http.ResponseWriter, r *http.Request, status int, message, notice string) {
	p, err := a.plexPage(r.Context())
	if err != nil {
		a.fail(w, "Plex account page failed")
		return
	}
	p.Error = message
	p.Notice = notice
	a.render(w, r, status, p)
}
func (a *App) plexSettings(w http.ResponseWriter, r *http.Request) {
	notice := ""
	switch r.URL.Query().Get("result") {
	case "account":
		notice = "Plex account authorised. Choose a server connection explicitly below."
	case "refreshed":
		notice = "Plex account token and available server resources refreshed."
	case "disconnected":
		notice = "Plex account and its linked connections removed locally. Manual connections and collections were retained."
	case "saved":
		notice = "Connection validated and saved. Accessible libraries were refreshed."
	case "checked":
		notice = "Connection rechecked. Accessible libraries were refreshed."
	case "deleted":
		notice = "Connection and its saved token were removed locally. No Plex media or playlists were changed."
	}
	a.renderPlex(w, r, 200, "", notice)
}
func (a *App) plexFailure(w http.ResponseWriter, r *http.Request, err error) {
	status := 502
	message := "The connection operation failed. The existing saved connection was retained."
	switch {
	case errors.Is(err, store.ErrConflict):
		status = 409
		message = "This Plex connection changed while the operation was running. Reload before trying again."
	case errors.Is(err, store.ErrInvalid):
		status = 400
		message = err.Error()
	case errors.Is(err, plex.ErrURL) || errors.Is(err, plex.ErrHTTPConsent):
		status = 400
		message = err.Error()
	case errors.Is(err, plex.ErrAuth):
		status = 400
		message = plex.ErrAuth.Error()
	case errors.Is(err, plex.ErrResponse) || errors.Is(err, plex.ErrUnavailable) || errors.Is(err, plex.ErrRedirect) || errors.Is(err, plex.ErrRateLimit):
		message = err.Error()
	case errors.Is(err, sql.ErrNoRows):
		http.NotFound(w, r)
		return
	default:
		a.logger.Error("Plex operation failed")
	}
	a.renderPlex(w, r, status, message, "")
}
func (a *App) plexSave(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.PostForm.Get("name"))
	base, err := plex.NormalizeURL(r.PostForm.Get("url"), r.PostForm.Get("allow_http") == "yes")
	if err != nil {
		a.plexFailure(w, r, err)
		return
	}
	// Validate labels before making an external request. Never echo submitted fields.
	if len([]rune(name)) < 1 || len([]rune(name)) > 200 || strings.ContainsAny(name, "\r\n\t\x00") {
		a.renderPlex(w, r, 400, "Enter a connection name of 1–200 characters.", "")
		return
	}
	id := r.PostForm.Get("id")
	expected := 0
	c := store.PlexConnection{ID: randomToken()[:32], ClientID: randomToken()[:32]}
	if id != "" {
		expected, err = strconv.Atoi(r.PostForm.Get("revision"))
		if err != nil || expected < 1 {
			a.renderPlex(w, r, 400, "A valid connection revision is required. Reload the page.", "")
			return
		}
		c, err = a.store.PlexConnection(r.Context(), id)
		if err != nil {
			a.plexFailure(w, r, err)
			return
		}
		if c.AccountID != 0 {
			a.renderPlex(w, r, 400, "This connection uses a Plex account. Recheck it to refresh its server token, or remove it and add a manual connection.", "")
			return
		}
		if c.Revision != expected {
			a.plexFailure(w, r, store.ErrConflict)
			return
		}
	}
	token := r.PostForm.Get("token")
	if token == "" && id != "" {
		if base != c.BaseURL {
			a.renderPlex(w, r, 400, "Supply a new token when changing the server URL; the saved token will not be sent to a different address.", "")
			return
		}
		token, err = a.vault.Open(c.TokenCipher, "plex:"+c.ID+":token")
		if err != nil {
			a.plexFailure(w, r, err)
			return
		}
	}
	if !plex.ValidToken(token) {
		a.plexFailure(w, r, plex.ErrAuth)
		return
	}
	if strings.Contains(name, token) || strings.Contains(base, token) {
		a.renderPlex(w, r, 400, "The connection name and URL must not contain the supplied token.", "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	snapshot, err := a.plex.Inspect(ctx, base, token, c.ClientID, r.PostForm.Get("allow_http") == "yes")
	if err != nil {
		a.plexFailure(w, r, err)
		return
	}
	if id != "" && c.MachineID != snapshot.MachineID {
		a.renderPlex(w, r, 409, "This address now identifies a different Plex server. Add it as a separate connection; the saved destination was retained.", "")
		return
	}
	c.Name = name
	c.BaseURL = base
	c.MachineID = snapshot.MachineID
	c.ServerName = snapshot.Name
	c.ServerVersion = snapshot.Version
	c.Libraries = snapshot.Libraries
	c.AllowHTTP = r.PostForm.Get("allow_http") == "yes"
	c.TokenCipher = a.vault.Seal(token, "plex:"+c.ID+":token")
	if err = a.store.SavePlexConnection(r.Context(), c, expected); err != nil {
		a.plexFailure(w, r, err)
		return
	}
	a.logger.Info("Plex connection saved")
	http.Redirect(w, r, "/settings/plex?result=saved", 303)
}
func (a *App) plexCheck(w http.ResponseWriter, r *http.Request) {
	c, err := a.store.PlexConnection(r.Context(), r.PathValue("id"))
	if err != nil {
		a.plexFailure(w, r, err)
		return
	}
	revision, err := strconv.Atoi(r.PostForm.Get("revision"))
	if err != nil || revision != c.Revision {
		a.plexFailure(w, r, store.ErrConflict)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if c.AccountID == 1 {
		if !a.lockAccount(w, r) {
			return
		}
		defer a.accountMu.Unlock()
		c, err = a.refreshLinked(ctx, c)
		if err != nil {
			// A cloud/account failure also makes this recheck unsuccessful. Keep the
			// old credential and snapshot, but never present it as a fresh success.
			if saveErr := a.store.PlexCheckFailed(r.Context(), c.ID, c.Revision, "Plex account verification or resource refresh failed. Cached libraries may be stale."); saveErr != nil {
				a.plexFailure(w, r, saveErr)
				return
			}
			a.accountError(w, r, err)
			return
		}
	}
	token, err := a.vault.Open(c.TokenCipher, "plex:"+c.ID+":token")
	if err != nil {
		a.plexFailure(w, r, err)
		return
	}
	snapshot, err := a.plex.Inspect(ctx, c.BaseURL, token, c.ClientID, c.AllowHTTP)
	if err == nil && snapshot.MachineID != c.MachineID {
		err = errors.New("The address identifies a different Plex server. The saved destination and libraries were retained.")
	}
	if err != nil {
		// All adapter errors are fixed messages; no server body or request URL is saved.
		if saveErr := a.store.PlexCheckFailed(r.Context(), c.ID, c.Revision, err.Error()); saveErr != nil {
			a.plexFailure(w, r, saveErr)
			return
		}
		a.renderPlex(w, r, 502, err.Error(), "")
		return
	}
	c.ServerName = snapshot.Name
	c.ServerVersion = snapshot.Version
	c.Libraries = snapshot.Libraries
	if err = a.store.SavePlexConnection(r.Context(), c, c.Revision); err != nil {
		a.plexFailure(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings/plex?result=checked", 303)
}
func (a *App) plexDelete(w http.ResponseWriter, r *http.Request) {
	c, err := a.store.PlexConnection(r.Context(), r.PathValue("id"))
	if err != nil {
		a.plexFailure(w, r, err)
		return
	}
	revision, err := strconv.Atoi(r.PostForm.Get("revision"))
	if err != nil {
		a.plexFailure(w, r, store.ErrConflict)
		return
	}
	if r.PostForm.Get("confirm_name") != c.Name {
		a.renderPlex(w, r, 400, "Type the connection name exactly to remove it.", "")
		return
	}
	if err = a.store.DeletePlexConnection(r.Context(), c.ID, revision); err != nil {
		a.plexFailure(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings/plex?result=deleted", 303)
}
