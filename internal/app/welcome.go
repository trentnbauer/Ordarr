package app

import (
	"net/http"
)

// welcomeView drives the first-run wizard. Its state is derived from what is connected plus
// two "skipped" flags, so it can be left and resumed at any point.
type welcomeView struct {
	Step                        int // 1 Plex, 2 Sonarr, 3 Radarr, 4 automatic downloads, 5 finished
	Plex, Sonarr, Radarr        bool
	SkippedSonarr, SkippedRadar bool
	Auto                        bool
}

func (a *App) welcomePage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	o, err := a.loadOverview(ctx)
	if err != nil {
		a.fail(w, "overview failed")
		return
	}
	v := &welcomeView{Plex: len(o.Plex) > 0, Auto: o.Defaults.AutoDownload,
		SkippedSonarr: a.store.Pref(ctx, "wizard_skip_sonarr") != "", SkippedRadar: a.store.Pref(ctx, "wizard_skip_radarr") != ""}
	for _, c := range o.Arr {
		v.Sonarr = v.Sonarr || c.Kind == "sonarr"
		v.Radarr = v.Radarr || c.Kind == "radarr"
	}
	switch {
	case !v.Plex:
		v.Step = 1
	case !v.Sonarr && !v.SkippedSonarr:
		v.Step = 2
	case !v.Radarr && !v.SkippedRadar:
		v.Step = 3
	case a.store.Pref(ctx, "wizard_done") == "":
		v.Step = 4
	default:
		v.Step = 5
	}
	a.render(w, r, 200, page{Title: "Welcome to Ordarr", Mode: "welcome", Welcome: v})
}

// welcomeSkip skips an optional downloader.
func (a *App) welcomeSkip(w http.ResponseWriter, r *http.Request) {
	kind := r.PostForm.Get("kind")
	if kind != "sonarr" && kind != "radarr" {
		http.Error(w, "Unknown service", 400)
		return
	}
	if err := a.store.SetPref(r.Context(), "wizard_skip_"+kind, "1"); err != nil {
		a.fail(w, "wizard failed")
		return
	}
	http.Redirect(w, r, "/welcome", 303)
}

// welcomeFinish saves the one global choice the wizard asks for and ends the wizard.
func (a *App) welcomeFinish(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d, err := a.store.Defaults(ctx)
	if err != nil {
		a.fail(w, "defaults failed")
		return
	}
	d.AutoDownload = r.PostForm.Get("auto") != "no"
	if err = a.store.SaveDefaults(ctx, d); err != nil {
		a.fail(w, "defaults failed")
		return
	}
	if err = a.store.SetPref(ctx, "wizard_done", "1"); err != nil {
		a.fail(w, "wizard failed")
		return
	}
	http.Redirect(w, r, "/add", 303)
}
