package app

import (
	"strconv"
	"strings"
	"time"

	"github.com/trentnbauer/Ordarr/internal/store"
)

// routineSource is a successful source check that changed nothing. Jobs recorded before source
// checks stored their outcome say just "Completed"; those are treated the same.
func routineSource(j store.Job) bool {
	return j.Kind == "source" && j.State == "succeeded" && (j.Message == "" || j.Message == "Completed" || j.Message == "Source unchanged.")
}

// collapseRoutine folds runs of routine rows for the same thing into one row
// ("Checked 24× today, no changes"). rows are newest first. Anything that is not routine —
// a change, a request, a publish, a failure — stays a row of its own and ends the run it
// belongs to, so "no changes" is never claimed across a change.
func collapseRoutine(rows []jobRow, now time.Time) []jobRow {
	var out []jobRow
	open := map[string]int{} // kind|what -> index in out of the run still being extended
	for _, r := range rows {
		key := r.Kind + "|" + r.What
		if !r.Routine {
			delete(open, key)
			out = append(out, r)
			continue
		}
		if i, ok := open[key]; ok {
			out[i].Count++
			out[i].Oldest = r.When
			continue
		}
		r.Count, r.Oldest = 1, r.When
		open[key] = len(out)
		out = append(out, r)
	}
	for i := range out {
		if out[i].Routine && out[i].Count > 1 {
			out[i].Result = "Checked " + strconv.Itoa(out[i].Count) + "× " + span(out[i].Oldest, out[i].When, now) + ", no changes"
		}
	}
	return out
}

// span words the period a run of checks covers.
func span(oldest, newest int64, now time.Time) string {
	o, n := time.Unix(oldest, 0), time.Unix(newest, 0)
	y1, m1, d1 := o.Date()
	y2, m2, d2 := n.Date()
	y3, m3, d3 := now.Date()
	switch {
	case y1 == y3 && m1 == m3 && d1 == d3 && y2 == y3 && m2 == m3 && d2 == d3:
		return "today"
	case y1 == y2 && m1 == m2 && d1 == d2:
		return "on " + o.Format("2 Jan")
	}
	return "since " + o.Format("2 Jan")
}

// historyRows turns one collection's jobs (newest first) into display rows.
func historyRows(jobs []store.Job, now time.Time) []jobRow {
	var rows []jobRow
	for _, j := range jobs {
		r := jobRow{When: j.CreatedAt, Kind: "Playlist sync", Routine: routineSource(j)}
		if j.Kind == "source" {
			r.Kind = "Source check"
		}
		switch j.State {
		case "failed":
			r.Result, r.Class = "Failed · "+j.Message, "warn"
		case "queued", "running":
			r.Result, r.Class = strings.ToUpper(j.State[:1])+j.State[1:], "muted"
		default:
			r.Result = j.Message
			if r.Routine {
				r.Result = "Checked, no changes"
			}
		}
		rows = append(rows, r)
	}
	rows = collapseRoutine(rows, now)
	if len(rows) > 50 {
		rows = rows[:50]
	}
	return rows
}
