package app

import (
	"context"
	"strings"
	"testing"

	"github.com/trentnbauer/Ordarr/internal/store"
)

// An editable collection keeps its editing help; the status column says what it is; the copy action
// has one name.
func TestCollectionPageLabels(t *testing.T) {
	a, h := fixture(t)
	session, csrf := setup(t, h)
	id, err := a.store.ImportCollection(context.Background(), store.Collection{Title: "Own", Entries: []store.Entry{{ID: "a", Kind: "movie", Title: "M", Provider: "tmdb", ExternalID: "1"}}})
	if err != nil {
		t.Fatal(err)
	}
	body := request(h, "GET", "/collections/"+id, nil, session, csrf).Body.String()
	if !strings.Contains(body, "Select entries and use Move") || !strings.Contains(body, "Status on ") {
		t.Error("editing help or status header missing")
	}
	if strings.Contains(body, "Duplicate as an editable copy") {
		t.Error("one name for the copy action")
	}
}
