package app

import (
	"strconv"
	"strings"
)

// statusLine is the one-line summary under a collection's title, e.g.
// "In Plex as a playlist · 67/320 playable · 241 downloading · 12 need your input".
// Counts come from the same row statuses shown on the Order tab, and "need your input"
// is picks plus items no downloader could find. Zero counts are left out.
func statusLine(v *collectionView, entries int) (line string, warn bool) {
	var parts []string
	live := false
	for _, d := range v.Destinations {
		live = live || d.RemoteID != ""
	}
	switch {
	case live:
		parts = append(parts, "In Plex as a playlist")
	case len(v.Destinations) > 0:
		parts = append(parts, "Building the Plex playlist")
	default:
		parts = append(parts, "Not in Plex yet")
		warn = true
	}
	if !v.Avail.Ready {
		if v.HasServer {
			parts = append(parts, "checking "+v.Server.Name)
		}
		return strings.Join(parts, " · "), warn
	}
	parts = append(parts, strconv.Itoa(v.Avail.Found)+"/"+strconv.Itoa(entries)+" playable")
	count := map[string]int{}
	for _, r := range v.Rows {
		if r.Status.Label != "" {
			count[r.Status.Label]++
		}
	}
	input := count[statusPick] + count[statusNotFound]
	for _, p := range []struct{ label, text string }{
		{statusDownload, "downloading"}, {statusRequested, "requested"}, {statusPlexScan, "waiting for Plex"},
		{statusAuto, "to be requested"}, {statusNotAsked, "not requested"},
	} {
		if n := count[p.label]; n > 0 {
			parts = append(parts, strconv.Itoa(n)+" "+p.text)
		}
	}
	if input > 0 {
		parts = append(parts, strconv.Itoa(input)+" need your input")
		warn = true
	}
	if v.Source != nil && v.Source.Proposed != "" {
		parts = append(parts, "source update waiting")
		warn = true
	}
	return strings.Join(parts, " · "), warn
}
