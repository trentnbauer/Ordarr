// Package community lists the community watch orders published in a public
// GitHub repository of Ordarr version 1 collection files.
package community

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/trentnbauer/Ordarr/internal/portable"
	"github.com/trentnbauer/Ordarr/internal/subscription"
)

// DefaultRepository is the community collections repository.
const DefaultRepository = "trentnbauer/OrdarrCollections"

// MaxOrders bounds how many files one catalogue refresh reads.
const MaxOrders = 300

var ErrUnavailable = errors.New("The community repository could not be read right now. Try again later, or add a collection by URL or paste.")
var validRepository = regexp.MustCompile(`^[A-Za-z0-9-]{1,39}/[A-Za-z0-9._-]{1,100}$`)
var validBranch = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,100}$`)

// Order summarises one community collection file.
type Order struct {
	Path, URL, ID, Title, Description string
	Entries, Movies, Episodes, Series int
}

// Fetcher returns the body of a public HTTPS URL.
type Fetcher func(ctx context.Context, url string) ([]byte, error)

// Catalogue caches a repository's orders. Nothing is fetched until it is used,
// so an idle instance makes no requests.
type Catalogue struct {
	Repository, Branch string
	API, Raw           string // base URLs; overridden in tests
	Fetch              Fetcher
	TTL                time.Duration

	mu      sync.Mutex
	orders  []Order
	fetched time.Time
	failed  time.Time
}

func ValidRepository(repo string) bool { return validRepository.MatchString(repo) }
func ValidBranch(branch string) bool {
	return validBranch.MatchString(branch) && !strings.Contains(branch, "..")
}

// New returns a catalogue of repo at branch using the hardened public fetcher.
func New(repo, branch string) *Catalogue {
	return &Catalogue{Repository: repo, Branch: branch, API: "https://api.github.com", Raw: "https://raw.githubusercontent.com", TTL: time.Hour, Fetch: func(ctx context.Context, u string) ([]byte, error) {
		res, err := subscription.Fetch(ctx, u, "", "")
		return res.Data, err
	}}
}

// FileURL is the raw URL Ordarr subscribes to for a repository path.
func (c *Catalogue) FileURL(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return c.Raw + "/" + c.Repository + "/" + url.PathEscape(c.Branch) + "/" + strings.Join(parts, "/")
}

// RepositoryURL is the human-facing repository page.
func (c *Catalogue) RepositoryURL() string { return "https://github.com/" + c.Repository }

// Orders returns the cached orders, refreshing them when stale. A failed
// refresh keeps serving the last good list and is not retried for a minute.
func (c *Catalogue) Orders(ctx context.Context) ([]Order, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if !c.fetched.IsZero() && now.Sub(c.fetched) < c.TTL {
		return c.orders, nil
	}
	if now.Sub(c.failed) < time.Minute {
		if c.orders != nil {
			return c.orders, nil
		}
		return nil, ErrUnavailable
	}
	orders, err := c.load(ctx)
	if err != nil {
		c.failed = now
		if c.orders != nil {
			return c.orders, nil
		}
		return nil, ErrUnavailable
	}
	c.orders, c.fetched = orders, now
	return orders, nil
}

// Order finds one order by repository path.
func (c *Catalogue) Order(ctx context.Context, path string) (Order, error) {
	orders, err := c.Orders(ctx)
	if err != nil {
		return Order{}, err
	}
	for _, o := range orders {
		if o.Path == path {
			return o, nil
		}
	}
	return Order{}, ErrUnavailable
}

func collectionFile(path string) bool {
	if strings.HasPrefix(path, ".") || strings.Contains(path, "/.") {
		return false
	}
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".json")
}

func (c *Catalogue) load(ctx context.Context) ([]Order, error) {
	data, err := c.Fetch(ctx, c.API+"/repos/"+c.Repository+"/git/trees/"+url.PathEscape(c.Branch)+"?recursive=1")
	if err != nil {
		return nil, err
	}
	var tree struct {
		Tree []struct{ Path, Type string } `json:"tree"`
	}
	if err = json.Unmarshal(data, &tree); err != nil {
		return nil, err
	}
	var paths []string
	for _, t := range tree.Tree {
		if t.Type == "blob" && collectionFile(t.Path) && len(paths) < MaxOrders {
			paths = append(paths, t.Path)
		}
	}
	orders := make([]*Order, len(paths))
	var wg sync.WaitGroup
	limit := make(chan struct{}, 4)
	for i, path := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			limit <- struct{}{}
			defer func() { <-limit }()
			body, err := c.Fetch(ctx, c.FileURL(path))
			if err != nil {
				return
			}
			o, err := Summarise(body)
			if err != nil {
				return // invalid files are not offered
			}
			o.Path, o.URL = path, c.FileURL(path)
			orders[i] = &o
		}()
	}
	wg.Wait()
	result := []Order{}
	for _, o := range orders {
		if o != nil {
			result = append(result, *o)
		}
	}
	if len(paths) > 0 && len(result) == 0 {
		return nil, ErrUnavailable
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i].Title) < strings.ToLower(result[j].Title) })
	return result, nil
}

// Summarise validates a collection file exactly as an import would and counts it.
func Summarise(body []byte) (Order, error) {
	d, err := portable.Decode(body)
	if err != nil {
		return Order{}, err
	}
	collection, err := d.Collection()
	if err != nil {
		return Order{}, err
	}
	o := Order{ID: d.ID, Title: collection.Title, Description: collection.Description, Entries: len(collection.Entries)}
	series := map[string]bool{}
	for _, e := range collection.Entries {
		if e.Kind == "movie" {
			o.Movies++
		} else {
			o.Episodes++
			series[e.Provider+":"+e.ExternalID] = true
		}
	}
	o.Series = len(series)
	return o, nil
}
