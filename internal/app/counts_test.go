package app

import "testing"

func TestAvailabilityLabelIsOneDefinition(t *testing.T) {
	for _, c := range []struct {
		v    availability
		want string
	}{
		{availability{Missing: 241, NeedPick: 12}, "241 missing · 12 need a pick"},
		{availability{Missing: 3}, "3 missing"},
		{availability{NeedPick: 1}, "1 need a pick"},
		{availability{Found: 9}, ""},
	} {
		if got := c.v.Label(); got != c.want {
			t.Errorf("%+v: got %q want %q", c.v, got, c.want)
		}
	}
}

// The collection list status must use the same wording as the header.
func TestCollectionStatusUsesSharedLabel(t *testing.T) {
	o := &overview{Destinations: nil}
	values := map[string]availability{"c": {Ready: true, Missing: 241, NeedPick: 12}}
	got, _ := collectionStatus(o, values, "c")
	if got != "Not published · 241 missing · 12 need a pick" {
		t.Fatal(got)
	}
}
