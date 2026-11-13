package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/trentnbauer/Ordarr/internal/plex"
	"github.com/trentnbauer/Ordarr/internal/store"
)

type accountView struct {
	UserID                                       int64
	Username, FriendlyName, ExpiresAt, CheckedAt string
	Revision                                     int
	Restricted, Expired                          bool
}
type authView struct{ ID, URL, ExpiresAt string }
type accountServerView struct {
	MachineID, Name string
	Owned           bool
	Connections     []plex.ResourceConnection
}
type attemptSecret struct {
	PrivateKey []byte
	Code       string
}

func sessionValue(r *http.Request) string {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return cookie.Value
}
func (a *App) accountCredentials(account store.PlexAccount) (plex.AccountCredentials, error) {
	raw, err := a.vault.Open(account.CredentialsCipher, "plex:account:"+strconv.FormatInt(account.UserID, 10))
	if err != nil {
		return plex.AccountCredentials{}, err
	}
	var credentials plex.AccountCredentials
	if json.Unmarshal([]byte(raw), &credentials) != nil || credentials.Device.ClientID != account.ClientID {
		return credentials, plex.ErrAccountResponse
	}
	return credentials, nil
}
func (a *App) accountProjection(ctx context.Context) (*accountView, []accountServerView, error) {
	account, err := a.store.PlexAccount(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	credentials, err := a.accountCredentials(account)
	if err != nil {
		return nil, nil, err
	}
	view := &accountView{UserID: account.UserID, Username: account.Username, FriendlyName: account.FriendlyName, Restricted: account.Restricted, Revision: account.Revision, ExpiresAt: time.Unix(account.ExpiresAt, 0).UTC().Format(time.RFC3339), CheckedAt: time.Unix(account.CheckedAt, 0).UTC().Format(time.RFC3339), Expired: account.ExpiresAt <= time.Now().Unix()}
	servers := []accountServerView{}
	for _, resource := range credentials.Resources {
		servers = append(servers, accountServerView{MachineID: resource.MachineID, Name: resource.Name, Owned: resource.Owned, Connections: resource.Connections})
	}
	return view, servers, nil
}
func (a *App) accountError(w http.ResponseWriter, r *http.Request, err error) {
	status := 502
	message := "Plex account operation failed. Saved credentials and connections were retained."
	switch {
	case errors.Is(err, plex.ErrPending):
		status = 202
		message = plex.ErrPending.Error()
	case errors.Is(err, plex.ErrExpired) || errors.Is(err, store.ErrAuthAttempt):
		status = 410
		message = store.ErrAuthAttempt.Error()
	case errors.Is(err, store.ErrPollSoon):
		status = 429
		message = err.Error()
	case errors.Is(err, store.ErrConflict):
		status = 409
		message = "The saved Plex account changed. Reload before trying again."
	case errors.Is(err, store.ErrInvalid):
		status = 400
		message = err.Error()
	case errors.Is(err, plex.ErrAccount) || errors.Is(err, plex.ErrAccountResponse) || errors.Is(err, plex.ErrUnavailable) || errors.Is(err, plex.ErrRedirect) || errors.Is(err, plex.ErrRateLimit) || errors.Is(err, plex.ErrHTTPConsent):
		message = err.Error()
	default:
		a.logger.Error("Plex account operation failed")
	}
	a.renderPlex(w, r, status, message, "")
}
func (a *App) lockAccount(w http.ResponseWriter, r *http.Request) bool {
	if a.accountMu.TryLock() {
		return true
	}
	a.renderPlex(w, r, 409, "Another Plex account operation is running. Wait for it to finish before retrying.", "")
	return false
}
func (a *App) accountStart(w http.ResponseWriter, r *http.Request) {
	if !a.allowed(r) {
		a.renderPlex(w, r, 429, "Too many sign-in attempts. Wait a minute before retrying.", "")
		return
	}
	if !a.lockAccount(w, r) {
		return
	}
	defer a.accountMu.Unlock()
	revision := 0
	existing, err := a.store.PlexAccount(r.Context())
	if err == nil {
		revision = existing.Revision
	} else if !errors.Is(err, sql.ErrNoRows) {
		a.accountError(w, r, err)
		return
	}
	device := plex.NewDevice()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	pin, err := a.accounts.Start(ctx, device)
	if err != nil {
		a.accountError(w, r, err)
		return
	}
	id := randomToken()[:32]
	raw, _ := json.Marshal(attemptSecret{PrivateKey: device.PrivateKey, Code: pin.Code})
	attempt := store.PlexAuthAttempt{ID: id, ClientID: device.ClientID, Kid: device.Kid, PINID: pin.ID, ExpiresAt: pin.ExpiresAt, AccountRevision: revision, PrivateCipher: a.vault.Seal(string(raw), "plex:attempt:"+id)}
	if err = a.store.BeginPlexAuth(r.Context(), sessionValue(r), attempt); err != nil {
		a.accountError(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings/plex/account/attempt/"+id, 303)
}
func (a *App) attemptDevice(attempt store.PlexAuthAttempt) (plex.Device, plex.PIN, error) {
	raw, err := a.vault.Open(attempt.PrivateCipher, "plex:attempt:"+attempt.ID)
	if err != nil {
		return plex.Device{}, plex.PIN{}, err
	}
	var secret attemptSecret
	if json.Unmarshal([]byte(raw), &secret) != nil {
		return plex.Device{}, plex.PIN{}, plex.ErrAccountResponse
	}
	return plex.Device{ClientID: attempt.ClientID, Kid: attempt.Kid, PrivateKey: secret.PrivateKey}, plex.PIN{ID: attempt.PINID, Code: secret.Code, ExpiresAt: attempt.ExpiresAt}, nil
}
func (a *App) renderAttempt(w http.ResponseWriter, r *http.Request, attempt store.PlexAuthAttempt, status int, message string) {
	device, pin, err := a.attemptDevice(attempt)
	if err != nil {
		a.accountError(w, r, err)
		return
	}
	p, err := a.plexPage(r.Context())
	if err != nil {
		a.fail(w, "Plex account page failed")
		return
	}
	p.Error = message
	p.AuthAttempt = &authView{ID: attempt.ID, URL: plex.AuthURL(device, pin), ExpiresAt: time.Unix(attempt.ExpiresAt, 0).UTC().Format(time.RFC3339)}
	a.render(w, r, status, p)
}
func (a *App) accountAttempt(w http.ResponseWriter, r *http.Request) {
	attempt, err := a.store.PlexAuthAttempt(r.Context(), sessionValue(r), r.PathValue("attempt"), false)
	if err != nil {
		a.accountError(w, r, err)
		return
	}
	a.renderAttempt(w, r, attempt, 200, "")
}
func (a *App) saveAccountResult(ctx context.Context, result plex.AccountResult, revision int, attemptID, session string) error {
	raw, err := json.Marshal(result.Credentials)
	if err != nil {
		return err
	}
	account := store.PlexAccount{UserID: result.User.ID, Username: result.User.Username, FriendlyName: result.User.FriendlyName, Restricted: result.User.Restricted, ClientID: result.Credentials.Device.ClientID, ExpiresAt: result.ExpiresAt, CredentialsCipher: a.vault.Seal(string(raw), "plex:account:"+strconv.FormatInt(result.User.ID, 10))}
	return a.store.SavePlexAccount(ctx, account, revision, attemptID, session)
}
func (a *App) accountPoll(w http.ResponseWriter, r *http.Request) {
	if !a.lockAccount(w, r) {
		return
	}
	defer a.accountMu.Unlock()
	attempt, err := a.store.PlexAuthAttempt(r.Context(), sessionValue(r), r.PathValue("attempt"), true)
	if err != nil {
		a.accountError(w, r, err)
		return
	}
	device, pin, err := a.attemptDevice(attempt)
	if err != nil {
		a.accountError(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	result, err := a.accounts.Poll(ctx, device, pin)
	if errors.Is(err, plex.ErrPending) {
		a.renderAttempt(w, r, attempt, 202, err.Error())
		return
	}
	if err != nil {
		a.accountError(w, r, err)
		return
	}
	old, err := a.store.PlexAccount(r.Context())
	if err == nil && old.UserID != result.User.ID {
		a.renderPlex(w, r, 409, "A different Plex account is already connected. Disconnect it explicitly before choosing another user.", "")
		return
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		a.accountError(w, r, err)
		return
	}
	if err = a.saveAccountResult(r.Context(), result, attempt.AccountRevision, attempt.ID, sessionValue(r)); err != nil {
		a.accountError(w, r, err)
		return
	}
	a.logger.Info("Plex account authorised")
	http.Redirect(w, r, "/settings/plex?result=account", 303)
}
func (a *App) accountCancel(w http.ResponseWriter, r *http.Request) {
	if err := a.store.CancelPlexAuth(r.Context(), sessionValue(r), r.PathValue("attempt")); err != nil {
		a.accountError(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings/plex", 303)
}
func (a *App) verifiedAccount(ctx context.Context, forceRefresh bool) (store.PlexAccount, plex.AccountResult, error) {
	account, err := a.store.PlexAccount(ctx)
	if err != nil {
		return account, plex.AccountResult{}, err
	}
	credentials, err := a.accountCredentials(account)
	if err != nil {
		return account, plex.AccountResult{}, err
	}
	var result plex.AccountResult
	if forceRefresh || account.ExpiresAt <= time.Now().Add(15*time.Minute).Unix() {
		result, err = a.accounts.Refresh(ctx, credentials)
	} else {
		result, err = a.accounts.Verify(ctx, credentials)
	}
	if err != nil {
		return account, result, err
	}
	if result.User.ID != account.UserID {
		return account, result, plex.ErrAccount
	}
	if err = a.saveAccountResult(ctx, result, account.Revision, "", ""); err != nil {
		return account, result, err
	}
	return account, result, nil
}
func (a *App) accountRefresh(w http.ResponseWriter, r *http.Request) {
	if !a.lockAccount(w, r) {
		return
	}
	defer a.accountMu.Unlock()
	account, err := a.store.PlexAccount(r.Context())
	if err != nil {
		a.accountError(w, r, err)
		return
	}
	revision, err := strconv.Atoi(r.PostForm.Get("revision"))
	if err != nil || revision != account.Revision {
		a.accountError(w, r, store.ErrConflict)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if _, _, err = a.verifiedAccount(ctx, true); err != nil {
		a.accountError(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings/plex?result=refreshed", 303)
}
func (a *App) accountDisconnect(w http.ResponseWriter, r *http.Request) {
	if !a.lockAccount(w, r) {
		return
	}
	defer a.accountMu.Unlock()
	account, err := a.store.PlexAccount(r.Context())
	if err != nil {
		a.accountError(w, r, err)
		return
	}
	revision, err := strconv.Atoi(r.PostForm.Get("revision"))
	if err != nil {
		a.accountError(w, r, store.ErrConflict)
		return
	}
	if r.PostForm.Get("confirm_username") != account.Username {
		a.renderPlex(w, r, 400, "Type the connected Plex username exactly to disconnect it and its linked servers locally.", "")
		return
	}
	if err = a.store.DisconnectPlexAccount(r.Context(), revision); err != nil {
		a.accountError(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings/plex?result=disconnected", 303)
}
func findResource(result plex.AccountResult, id, uri string) (plex.Resource, string, error) {
	for _, resource := range result.Credentials.Resources {
		if resource.MachineID == id {
			for _, connection := range resource.Connections {
				if connection.URI == uri {
					return resource, resource.AccessToken, nil
				}
			}
		}
	}
	return plex.Resource{}, "", fmt.Errorf("%w: That server connection is no longer in the authorised account resources. Refresh and choose again.", store.ErrInvalid)
}
func (a *App) accountConnect(w http.ResponseWriter, r *http.Request) {
	if !a.lockAccount(w, r) {
		return
	}
	defer a.accountMu.Unlock()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	account, err := a.store.PlexAccount(ctx)
	if err != nil {
		a.accountError(w, r, err)
		return
	}
	revision, err := strconv.Atoi(r.PostForm.Get("account_revision"))
	if err != nil || revision != account.Revision {
		a.accountError(w, r, store.ErrConflict)
		return
	}
	_, result, err := a.verifiedAccount(ctx, false)
	if err != nil {
		a.accountError(w, r, err)
		return
	}
	resource, token, err := findResource(result, r.PostForm.Get("resource_id"), r.PostForm.Get("uri"))
	if err != nil {
		a.accountError(w, r, err)
		return
	}
	base, err := plex.NormalizeURL(r.PostForm.Get("uri"), r.PostForm.Get("allow_http") == "yes")
	if err != nil {
		a.plexFailure(w, r, err)
		return
	}
	snapshot, err := a.plex.Inspect(ctx, base, token, result.Credentials.Device.ClientID, r.PostForm.Get("allow_http") == "yes")
	if err != nil {
		a.plexFailure(w, r, err)
		return
	}
	if snapshot.MachineID != resource.MachineID {
		a.renderPlex(w, r, 409, "That address returned a different Plex server. No destination was created.", "")
		return
	}
	id := randomToken()[:32]
	c := store.PlexConnection{ID: id, Name: resource.Name, BaseURL: base, ClientID: result.Credentials.Device.ClientID, MachineID: snapshot.MachineID, ServerName: snapshot.Name, ServerVersion: snapshot.Version, AllowHTTP: r.PostForm.Get("allow_http") == "yes", AccountID: 1, ResourceID: resource.MachineID, Libraries: snapshot.Libraries, TokenCipher: a.vault.Seal(token, "plex:"+id+":token")}
	if err = a.store.SavePlexConnection(ctx, c, 0); err != nil {
		a.plexFailure(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings/plex?result=saved", 303)
}

// refreshLinked obtains the server-scoped resource token, never sends the Plex
// account token to a discovered PMS and never changes a saved destination origin.
func (a *App) refreshLinked(ctx context.Context, c store.PlexConnection) (store.PlexConnection, error) {
	_, result, err := a.verifiedAccount(ctx, false)
	if err != nil {
		return c, err
	}
	resource, token, err := findResource(result, c.ResourceID, c.BaseURL)
	if err != nil {
		return c, err
	}
	if resource.MachineID != c.MachineID || result.Credentials.Device.ClientID != c.ClientID {
		return c, store.ErrConflict
	}
	c.TokenCipher = a.vault.Seal(token, "plex:"+c.ID+":token")
	return c, nil
}
