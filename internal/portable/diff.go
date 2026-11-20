package portable

import "reflect"

type Changes struct {
	Added, Removed, Changed int
	Reordered               bool
}

func Diff(before, after Document) Changes {
	var diff Changes
	a, _ := before.Collection()
	b, _ := after.Collection()
	old, new := map[string]Entry{}, map[string]Entry{}
	for _, e := range FromCollection(a).Entries {
		old[e.ID] = e
	}
	for _, e := range FromCollection(b).Entries {
		new[e.ID] = e
	}
	for id, e := range old {
		if n, ok := new[id]; !ok {
			diff.Removed++
		} else if !reflect.DeepEqual(e, n) {
			diff.Changed++
		}
	}
	for id := range new {
		if _, ok := old[id]; !ok {
			diff.Added++
		}
	}
	left, right := []string{}, []string{}
	for _, e := range a.Entries {
		if _, ok := new[e.ID]; ok {
			left = append(left, e.ID)
		}
	}
	for _, e := range b.Entries {
		if _, ok := old[e.ID]; ok {
			right = append(right, e.ID)
		}
	}
	diff.Reordered = !reflect.DeepEqual(left, right)
	return diff
}

// Change describes one entry-level difference between two source versions.
// Positions are 1-based; From is 0 for additions and To is 0 for removals.
type Change struct {
	Kind     string // added, removed, moved or changed
	Title    string
	From, To int
}

// DiffDetail lists the changes between two versions: additions, removals,
// entries whose content changed, and retained entries that moved relative to
// the others (those outside the longest common subsequence of retained IDs).
func DiffDetail(before, after Document) []Change {
	a, _ := before.Collection()
	b, _ := after.Collection()
	oldPos, newPos := map[string]int{}, map[string]int{}
	for i, e := range a.Entries {
		oldPos[e.ID] = i
	}
	for i, e := range b.Entries {
		newPos[e.ID] = i
	}
	var left, right []string
	for _, e := range a.Entries {
		if _, ok := newPos[e.ID]; ok {
			left = append(left, e.ID)
		}
	}
	for _, e := range b.Entries {
		if _, ok := oldPos[e.ID]; ok {
			right = append(right, e.ID)
		}
	}
	stable := lcs(left, right)
	var changes []Change
	for i, e := range b.Entries {
		j, existed := oldPos[e.ID]
		switch {
		case !existed:
			changes = append(changes, Change{Kind: "added", Title: e.Title, To: i + 1})
		case !stable[e.ID]:
			changes = append(changes, Change{Kind: "moved", Title: e.Title, From: j + 1, To: i + 1})
		case !reflect.DeepEqual(a.Entries[j], e):
			changes = append(changes, Change{Kind: "changed", Title: e.Title, From: j + 1, To: i + 1})
		}
	}
	for i, e := range a.Entries {
		if _, ok := newPos[e.ID]; !ok {
			changes = append(changes, Change{Kind: "removed", Title: e.Title, From: i + 1})
		}
	}
	return changes
}

// lcs returns the members of a longest common subsequence of x and y.
func lcs(x, y []string) map[string]bool {
	n, m := len(x), len(y)
	t := make([][]int32, n+1)
	for i := range t {
		t[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if x[i] == y[j] {
				t[i][j] = t[i+1][j+1] + 1
			} else {
				t[i][j] = max(t[i+1][j], t[i][j+1])
			}
		}
	}
	keep := map[string]bool{}
	for i, j := 0, 0; i < n && j < m; {
		switch {
		case x[i] == y[j]:
			keep[x[i]] = true
			i++
			j++
		case t[i+1][j] >= t[i][j+1]:
			i++
		default:
			j++
		}
	}
	return keep
}
