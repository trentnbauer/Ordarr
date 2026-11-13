package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"github.com/trentnbauer/Ordarr/internal/portable"
	"github.com/trentnbauer/Ordarr/internal/store"
	"github.com/trentnbauer/Ordarr/internal/subscription"
	"io"
	"net/http"
	"strconv"
	"time"
)

type sourceView struct {
	Subscription      store.Subscription
	Current, Proposed string
	PendingHash       string
}

func (a *App) portablePage(w http.ResponseWriter, r *http.Request) {
	a.render(w, r, 200, page{Title: "Import or subscribe", Mode: "portable"})
}
func (a *App) importFile(w http.ResponseWriter, r *http.Request) {
	data := []byte(r.PostForm.Get("document"))
	if file, _, e := r.FormFile("file"); e == nil {
		defer file.Close()
		data, e = io.ReadAll(io.LimitReader(file, portable.MaxBytes+1))
		if e != nil {
			http.Error(w, "Could not read file", 400)
			return
		}
	} else if e != http.ErrMissingFile && r.MultipartForm != nil {
		http.Error(w, "Invalid file", 400)
		return
	}
	d, err := portable.Decode(data)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	c, err := d.Collection()
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	id, err := a.store.ImportCollection(r.Context(), c)
	if err != nil {
		a.collectionError(w, err)
		return
	}
	http.Redirect(w, r, "/collections/"+id, 303)
}
func (a *App) exportFile(w http.ResponseWriter, r *http.Request) {
	c, err := a.store.Collection(r.Context(), r.PathValue("id"))
	if err != nil {
		a.collectionError(w, err)
		return
	}
	format := r.URL.Query().Get("format")
	if format == "" {
		format = "yaml"
	}
	data, err := portable.Encode(portable.FromCollection(c), format)
	if err != nil {
		http.Error(w, "Use yaml or json", 400)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="ordarr-collection.`+format+`"`)
	w.Write(data)
}
func (a *App) subscribe(w http.ResponseWriter, r *http.Request) {
	raw := r.PostForm.Get("url")
	if err := subscription.ValidateURL(raw); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	sub, err := a.store.CreateSubscription(r.Context(), raw, r.PostForm.Get("policy"))
	if err != nil {
		a.collectionError(w, err)
		return
	}
	a.store.QueueJob(r.Context(), "source", sub.ID)
	a.wakeWorker()
	http.Redirect(w, r, "/collections/"+sub.CollectionID+"/source", 303)
}
func (a *App) sourcePage(w http.ResponseWriter, r *http.Request) {
	sub, err := a.store.CollectionSubscription(r.Context(), r.PathValue("id"))
	if err != nil {
		a.collectionError(w, err)
		return
	}
	c, err := a.store.Collection(r.Context(), sub.CollectionID)
	if err != nil {
		a.collectionError(w, err)
		return
	}
	current, _ := portable.Encode(portable.FromCollection(c), "yaml")
	if len(sub.LastValid) > 0 {
		current = sub.LastValid
	}
	a.render(w, r, 200, page{Title: "Subscription updates", Mode: "source", Source: &sourceView{Subscription: sub, Current: string(current), Proposed: string(sub.Pending), PendingHash: fmt.Sprintf("%x", sha256.Sum256(sub.Pending))}})
}
func (a *App) updateSource(w http.ResponseWriter, r *http.Request) {
	sub, err := a.store.CollectionSubscription(r.Context(), r.PathValue("id"))
	if err != nil {
		a.collectionError(w, err)
		return
	}
	switch r.PostForm.Get("action") {
	case "fetch":
		_, err = a.store.QueueJob(r.Context(), "source", sub.ID)
		a.wakeWorker()
	case "policy":
		revision, _ := strconv.Atoi(r.PostForm.Get("revision"))
		err = a.store.SubscriptionPolicy(r.Context(), sub.ID, r.PostForm.Get("policy"), revision)
		a.wakeWorker()
	case "approve":
		revision, _ := strconv.Atoi(r.PostForm.Get("revision"))
		if revision != sub.Revision || r.PostForm.Get("pending_hash") != fmt.Sprintf("%x", sha256.Sum256(sub.Pending)) {
			err = store.ErrConflict
			break
		}
		d, e := portable.Decode(sub.Pending)
		if e != nil {
			err = e
			break
		}
		c, e := d.Collection()
		if e != nil {
			err = e
			break
		}
		err = a.store.ApproveSource(r.Context(), sub, c)
	default:
		err = store.ErrInvalid
	}
	if err != nil {
		a.collectionError(w, err)
		return
	}
	http.Redirect(w, r, "/collections/"+sub.CollectionID+"/source", 303)
}
func (a *App) scheduleSources(ctx context.Context) {
	subs, err := a.store.DueSubscriptions(ctx)
	if err != nil {
		return
	}
	for _, sub := range subs {
		if _, err = a.store.QueueJob(ctx, "source", sub.ID); err == nil {
			a.store.DeferSubscription(ctx, sub.ID)
		}
	}
}
func (a *App) fetchSource(ctx context.Context, j store.Job) (ret error) {
	sub, err := a.store.Subscription(ctx, j.Target)
	if err != nil {
		return err
	}
	if sub.Policy == "paused" {
		return store.ErrConflict
	}
	defer func() {
		if ret != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			a.store.SourceFailure(cleanup, sub)
		}
	}()
	res, err := subscription.Fetch(ctx, sub.URL, sub.ETag, sub.Modified)
	if err != nil {
		return err
	}
	if res.Unchanged {
		if len(sub.LastValid) == 0 && len(sub.Pending) == 0 {
			return subscription.ErrFetch
		}
		return a.store.SourceUnchanged(ctx, sub)
	}
	d, err := portable.Decode(res.Data)
	if err != nil {
		return err
	}
	data, err := portable.Encode(d, "yaml")
	if err != nil {
		return err
	}
	c, err := d.Collection()
	if err != nil {
		return err
	}
	return a.store.ReceiveSource(ctx, sub, data, c, res.ETag, res.Modified)
}
func (a *App) optionalSource(ctx context.Context, id string) (*sourceView, error) {
	sub, err := a.store.CollectionSubscription(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &sourceView{Subscription: sub}, err
}
