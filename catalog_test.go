package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestItemsTracearrBatchWithoutLimit(t *testing.T) {
	b := newBridge(Config{})
	rows := make([]object, 200)
	ids := make([]string, 200)
	for i := range rows {
		id := fmt.Sprintf("item-%03d", i)
		rows[i] = object{"content_id": id, "type": "movie", "title": id}
		ids[i] = rid("item", id)
	}
	b.rows["/admin/catalog/items"] = cachedRows{until: time.Now().Add(time.Minute), rows: rows}
	q := url.Values{"Ids": {strings.Join(ids, ",")}, "Fields": {"ProductionYear,RunTimeTicks"}}
	out, err := b.items(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(out.(object)["Items"].([]object)); got != 200 {
		t.Fatalf("got %d items, want 200", got)
	}
	q.Set("Limit", "10")
	out, err = b.items(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(out.(object)["Items"].([]object)); got != 10 {
		t.Fatalf("explicit limit: got %d", got)
	}
}

func TestItemsBuildSeasonsAndHonorParentHierarchy(t *testing.T) {
	b := newBridge(Config{})
	rows := []object{
		{"content_id": "e1", "type": "episode", "title": "One", "series_id": "show", "series_title": "Show", "season_number": float64(1), "episode_number": float64(1), "library_ids": []any{"tv"}},
		{"content_id": "e2", "type": "episode", "title": "Two", "series_id": "show", "series_title": "Show", "season_number": float64(1), "episode_number": float64(2), "library_ids": []any{"tv"}},
		{"content_id": "e3", "type": "episode", "title": "Special", "series_id": "show", "series_title": "Show", "season_number": float64(0), "episode_number": float64(1), "library_ids": []any{"tv"}},
	}
	b.rows["/admin/catalog/items"] = cachedRows{until: time.Now().Add(time.Minute), rows: rows}
	query := url.Values{
		"ParentId":         {rid("library", "tv")},
		"Recursive":        {"true"},
		"IncludeItemTypes": {"Episode,Season"},
	}
	result, err := b.items(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	items := result.(object)["Items"].([]object)
	if len(items) != 5 {
		t.Fatalf("library results: got %d items, want 3 episodes and 2 seasons: %v", len(items), items)
	}
	seasonID := seasonID("show", 1)
	query.Set("ParentId", seasonID)
	query.Set("IncludeItemTypes", "Episode")
	result, err = b.items(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	items = result.(object)["Items"].([]object)
	if len(items) != 2 || items[0]["ParentId"] != seasonID || items[1]["ParentId"] != seasonID {
		t.Fatalf("season children: %v", items)
	}
	query.Set("ParentId", rid("item", "show"))
	query.Set("Recursive", "false")
	query.Set("IncludeItemTypes", "Season")
	result, err = b.items(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	items = result.(object)["Items"].([]object)
	if len(items) != 2 {
		t.Fatalf("direct series children: %v", items)
	}
}

func TestCatalogSharesScanAcrossCancelledAndConcurrentCallers(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Query().Get("cursor") == "" {
			close(started)
			<-release
			respond(w, 200, object{"items": []object{{"content_id": "first"}}, "page": object{"next_cursor": "next"}})
		} else {
			respond(w, 200, object{"items": []object{{"content_id": "second"}}})
		}
	}))
	defer upstream.Close()
	b := newBridge(Config{SiloURL: upstream.URL})
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := b.catalog(ctx); first <- err }()
	<-started
	cancel()
	if err := <-first; err == nil {
		t.Fatal("cancelled caller succeeded")
	}
	var callers sync.WaitGroup
	for i := 0; i < 8; i++ {
		callers.Add(1)
		go func() {
			defer callers.Done()
			rows, err := b.catalog(context.Background())
			if err != nil || len(rows) != 2 {
				t.Errorf("incomplete snapshot: %v %v", rows, err)
			}
		}()
	}
	close(release)
	callers.Wait()
	if requests.Load() != 2 {
		t.Fatalf("duplicate pagination: %d requests", requests.Load())
	}
}

func TestCatalogRateLimitRetryDoesNotPublishPartialRows(t *testing.T) {
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requests.Add(1)
		if n == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(429)
			return
		}
		if r.URL.Query().Get("cursor") == "" {
			respond(w, 200, object{"items": []object{{"content_id": "first"}}, "page": object{"next_cursor": "next"}})
		} else {
			w.WriteHeader(500)
		}
	}))
	defer upstream.Close()
	b := newBridge(Config{SiloURL: upstream.URL})
	rows, err := b.catalog(context.Background())
	if err == nil || rows != nil {
		t.Fatal("failed scan returned partial rows")
	}
	if len(b.rows) != 0 || requests.Load() != 3 {
		t.Fatalf("retry/publication failure: %d", requests.Load())
	}
	_, _ = b.catalog(context.Background())
	if requests.Load() != 3 {
		t.Fatal("failure cooldown did not prevent rescan")
	}
}

func TestCatalogRetryAfterAndCancellation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, test := range []struct {
		value string
		want  time.Duration
	}{
		{"2", 2 * time.Second}, {now.Add(5 * time.Second).Format(http.TimeFormat), 5 * time.Second},
		{"bad", time.Second}, {"999999999999", 11 * time.Minute},
	} {
		if got := retryDelay(test.value, now, 0); got != test.want {
			t.Fatalf("Retry-After %q: %v", test.value, got)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if waitContext(ctx, time.Hour) == nil {
		t.Fatal("retry wait ignored cancellation")
	}
}
