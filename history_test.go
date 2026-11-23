package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPlaybackReportingImport(t *testing.T) {
	cutoff := ""
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("ended_before") == "" {
			t.Error("missing history cutoff")
		}
		if r.URL.Query().Get("cursor") == "" {
			cutoff = r.URL.Query().Get("ended_before")
		} else if r.URL.Query().Get("ended_before") != cutoff {
			t.Error("history cutoff changed between Silo pages")
		}
		if r.URL.Path != "/api/v2/admin/playback-history" || r.Header.Get("Authorization") != "Bearer silo-secret" {
			t.Errorf("unexpected upstream request: %s", r.URL.Path)
			w.WriteHeader(500)
			return
		}
		if r.URL.Query().Get("cursor") == "" {
			respond(w, 200, object{"items": []any{object{"session_id": "session-a", "user_id": float64(3), "media_item_id": "m1", "media_type": "movie", "media_title": "Film", "started_at": "2026-01-01T12:00:00Z", "played_seconds": 123.0, "paused_seconds": 4.0, "client_ip": "192.0.2.1", "play_method": "direct"}, object{"session_id": "unmeasured", "user_id": float64(3), "media_item_id": "m2", "media_type": "movie", "started_at": "2026-01-01T12:00:00Z"}}, "page": object{"next_cursor": "next"}})
		} else {
			respond(w, 200, object{"items": []any{object{"session_id": "session-b", "user_id": float64(4), "media_item_id": "e1", "media_type": "episode", "media_title": "Episode", "series_title": "Series", "season_number": float64(1), "episode_number": float64(2), "started_at": "2026-01-02T12:00:00Z", "played_seconds": 50.0}}, "page": object{}})
		}
	}))
	defer upstream.Close()
	b := newBridge(Config{SiloURL: upstream.URL, SiloKey: "silo-secret", BridgeKey: "bridge-secret"})
	call := func(query, key string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(object{"CustomQueryString": query})
		r := httptest.NewRequest("POST", "/user_usage_stats/submit_custom_query", bytes.NewReader(body))
		r.Header.Set("X-Emby-Token", key)
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		return w
	}
	if w := call("pragma table_info('PlaybackActivity')", "bad"); w.Code != 401 {
		t.Fatalf("unauthorized: %d", w.Code)
	}
	if w := call("DELETE FROM PlaybackActivity", "bridge-secret"); w.Code != 400 {
		t.Fatalf("unsupported query: %d", w.Code)
	}
	if w := call("pragma table_info('PlaybackActivity')", "bridge-secret"); w.Code != 200 || !strings.Contains(w.Body.String(), "RemoteAddress") {
		t.Fatalf("schema: %d %s", w.Code, w.Body.String())
	}
	w := call("SELECT COUNT(1), MIN(DateCreated), MAX(DateCreated) FROM PlaybackActivity", "bridge-secret")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"2"`) {
		t.Fatalf("summary: %d %s", w.Code, w.Body.String())
	}
	b.mu.Lock()
	if b.historyStats["total"] != 3 || b.historyStats["importable"] != 2 || b.historyStats["unmeasured"] != 1 {
		t.Errorf("history stats: %v", b.historyStats)
	}
	b.mu.Unlock()
	query := "SELECT rowid, DateCreated, UserId, ItemId, ItemType, ItemName, PlaybackMethod, ClientName, DeviceName, PlayDuration, PauseDuration, RemoteAddress FROM PlaybackActivity WHERE rowid > %d ORDER BY rowid LIMIT 5000"
	w = call(fmt.Sprintf(query, 0), "bridge-secret")
	var result struct {
		Results [][]string `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 || len(result.Results) != 2 {
		t.Fatalf("page: %d %s %v", w.Code, w.Body.String(), err)
	}
	first := result.Results[0]
	if len(first) != 12 {
		t.Fatalf("row: %v", first)
	}
	// Tracearr stops after a short page, so it must release the snapshot now.
	// A fresh import must start immediately and preserve row identity.
	w = call(fmt.Sprintf(query, 0), "bridge-secret")
	var repeated struct {
		Results [][]string `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &repeated); err != nil || w.Code != 200 || len(repeated.Results) != len(result.Results) {
		t.Fatalf("repeat import: %d %s", w.Code, w.Body.String())
	}
	for i, row := range repeated.Results {
		if row[0] != result.Results[i][0] || row[2] != result.Results[i][2] || row[3] != result.Results[i][3] {
			t.Fatalf("unstable identity: %v != %v", row, result.Results[i])
		}
		if row[2] != rid("user", "3") && row[2] != rid("user", "4") {
			t.Fatalf("unmapped user: %v", row)
		}
	}
	w = call(fmt.Sprintf(query, mustHistoryID(t, first[0])), "bridge-secret")
	if w.Code != 409 {
		t.Fatalf("completed snapshot accepted cursor: %d %s", w.Code, w.Body.String())
	}
}

func TestHistorySnapshotPagination(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		items := make([]any, 5001)
		for i := range items {
			items[i] = object{"session_id": fmt.Sprintf("session-%d", i), "user_id": "1", "media_item_id": "film", "media_type": "movie", "started_at": "2026-01-01T00:00:00Z", "played_seconds": 20.0}
		}
		if calls > 1 {
			t.Error("unexpected history rescan")
		}
		respond(w, 200, object{"items": items, "page": object{}})
	}))
	defer upstream.Close()
	b := newBridge(Config{SiloURL: upstream.URL, SiloKey: "s", BridgeKey: "b"})
	query := "SELECT rowid, DateCreated, UserId, ItemId, ItemType, ItemName, PlaybackMethod, ClientName, DeviceName, PlayDuration, PauseDuration, RemoteAddress FROM PlaybackActivity WHERE rowid > %d ORDER BY rowid LIMIT 5000"
	call := func(cursor uint64) *httptest.ResponseRecorder {
		body, _ := json.Marshal(object{"CustomQueryString": fmt.Sprintf(query, cursor)})
		r := httptest.NewRequest("POST", "/user_usage_stats/submit_custom_query", bytes.NewReader(body))
		r.Header.Set("X-Emby-Token", "b")
		w := httptest.NewRecorder()
		b.ServeHTTP(w, r)
		return w
	}
	w := call(0)
	var first struct {
		Results [][]string `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &first); err != nil || w.Code != 200 || len(first.Results) != 5000 {
		t.Fatalf("first page: %d %s", w.Code, w.Body.String())
	}
	if w = call(0); w.Code != 409 {
		t.Fatalf("concurrent import: %d", w.Code)
	}
	w = call(mustHistoryID(t, first.Results[4999][0]))
	var second struct {
		Results [][]string `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &second); err != nil || w.Code != 200 || len(second.Results) != 1 {
		t.Fatalf("second page: %d %s", w.Code, w.Body.String())
	}
	w = call(mustHistoryID(t, second.Results[0][0]))
	if w.Code != 409 {
		t.Fatalf("short final page snapshot was not released: %d", w.Code)
	}
	if calls != 1 {
		t.Fatalf("upstream requests: %d", calls)
	}
}

func TestHistoryUpstreamFailureNeverReturnsPartialSuccess(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "" {
			respond(w, 200, object{"items": []any{object{"session_id": "1", "user_id": "1", "media_item_id": "1", "media_type": "movie", "started_at": "2026-01-01T00:00:00Z", "played_seconds": 1.0}}, "page": object{"next_cursor": "next"}})
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer upstream.Close()
	b := newBridge(Config{SiloURL: upstream.URL, SiloKey: "s", BridgeKey: "b"})
	body := `{"CustomQueryString":"SELECT COUNT(1), MIN(DateCreated), MAX(DateCreated) FROM PlaybackActivity"}`
	r := httptest.NewRequest("POST", "/user_usage_stats/submit_custom_query", strings.NewReader(body))
	r.Header.Set("X-Emby-Token", "b")
	w := httptest.NewRecorder()
	b.ServeHTTP(w, r)
	if w.Code != 502 || strings.Contains(w.Body.String(), `"results"`) {
		t.Fatalf("expected failure, got %d %s", w.Code, w.Body.String())
	}
}

func mustHistoryID(t *testing.T, s string) uint64 {
	t.Helper()
	var n uint64
	if _, err := fmt.Sscan(s, &n); err != nil {
		t.Fatal(err)
	}
	return n
}
