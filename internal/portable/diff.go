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
