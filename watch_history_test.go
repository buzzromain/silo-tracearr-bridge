package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestUserItemsReturnsPlayedHistoryInJellyfinShape(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/admin/users":
			respond(w, http.StatusOK, object{"items": []any{object{"id": "42", "effective_policy": object{"library_ids": nil}}}, "page": object{}})
			return
		case "/api/v2/admin/catalog/items":
			respond(w, http.StatusOK, object{"items": []any{
				object{"content_id": "movie-1", "type": "movie"},
				object{"content_id": "movie-2", "type": "movie"},
				object{"content_id": "episode-1", "type": "episode"},
			}, "page": object{}})
			return
		}
		if r.URL.Path != "/api/v2/admin/playback-history" || r.Header.Get("Authorization") != "Bearer silo-secret" {
			t.Errorf("unexpected Silo request: %s %s", r.URL.Path, r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if r.URL.Query().Get("user_id") != "42" || r.URL.Query().Get("completed") != "true" || r.URL.Query().Get("limit") != "200" {
			t.Errorf("history filters not applied: %v", r.URL.Query())
		}
		if r.URL.Query().Get("cursor") == "" {
			respond(w, http.StatusOK, object{
				"items": []any{
					object{"media_item_id": "movie-1", "media_type": "movie", "media_title": "Film", "ended_at": "2026-01-03T00:00:00Z"},
					object{"media_item_id": "movie-1", "media_type": "movie", "media_title": "Film", "ended_at": "2026-01-01T00:00:00Z"},
				},
				"page": object{"next_cursor": "next"},
			})
			return
		}
		if r.URL.Query().Get("cursor") != "next" {
			t.Errorf("second page cursor not supplied: %v", r.URL.Query())
		}
		respond(w, http.StatusOK, object{
			"items": []any{
				object{"media_item_id": "movie-2", "media_type": "movie", "media_title": "Other", "ended_at": "2026-01-02T00:00:00Z"},
				object{"media_item_id": "episode-1", "media_type": "episode", "media_title": "Episode", "series_title": "Show", "season_number": float64(0), "episode_number": float64(3), "ended_at": "2026-01-04T00:00:00Z"},
			},
			"page": object{},
		})
	}))
	defer upstream.Close()
	b := newBridge(Config{SiloURL: upstream.URL, SiloKey: "silo-secret", BridgeKey: "bridge-secret"})
	query := url.Values{
		"Filters":          {"IsPlayed"},
		"IncludeItemTypes": {"Movie,Episode"},
		"Recursive":        {"true"},
		"IsMissing":        {"false"},
		"SortBy":           {"DatePlayed"},
		"SortOrder":        {"Descending"},
	}
	result, err := b.watchHistory(httptest.NewRequest(http.MethodGet, "/", nil).Context(), rid("user", 42), query)
	if err != nil {
		t.Fatal(err)
	}
	payload := result.(object)
	items := payload["Items"].([]object)
	if len(items) != 3 || payload["TotalRecordCount"] != 3 {
		t.Fatalf("unexpected watch history: %v", payload)
	}
	if items[0]["Id"] != rid("item", "episode-1") || items[1]["Id"] != rid("item", "movie-1") || items[2]["Id"] != rid("item", "movie-2") {
		t.Fatalf("incorrect DatePlayed ordering: %v", items)
	}
	userData := items[1]["UserData"].(object)
	if userData["Played"] != true || userData["PlayCount"] != 2 || userData["LastPlayedDate"] != "2026-01-03T00:00:00Z" {
		t.Fatalf("incorrect aggregated playback data: %v", userData)
	}
	if items[0]["SeriesName"] != "Show" || items[0]["ParentIndexNumber"] != float64(0) || items[0]["IndexNumber"] != float64(3) {
		t.Fatalf("episode metadata missing: %v", items[0])
	}

	query.Set("StartIndex", "1")
	query.Set("Limit", "1")
	result, err = b.watchHistory(httptest.NewRequest(http.MethodGet, "/", nil).Context(), rid("user", 42), query)
	if err != nil {
		t.Fatal(err)
	}
	paged := result.(object)
	if got := paged["Items"].([]object); len(got) != 1 || got[0]["Id"] != rid("item", "movie-1") || paged["StartIndex"] != 1 {
		t.Fatalf("incorrect watch history page: %v", paged)
	}
}

func TestUserItemsRequiresPlayedFilterAndDoesNotReturnPartialHistory(t *testing.T) {
	requests := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Query().Get("cursor") == "" {
			respond(w, http.StatusOK, object{
				"items": []any{object{"media_item_id": "movie-1", "media_type": "movie", "media_title": "Film", "ended_at": "2026-01-03T00:00:00Z"}},
				"page":  object{"next_cursor": "next"},
			})
			return
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer upstream.Close()
	b := newBridge(Config{SiloURL: upstream.URL, SiloKey: "silo-secret"})
	ctx := httptest.NewRequest(http.MethodGet, "/", nil).Context()
	if _, err := b.watchHistory(ctx, rid("user", 42), url.Values{}); err == nil {
		t.Fatal("unfiltered item query should not be treated as complete watch history")
	}
	if requests != 0 {
		t.Fatal("unsupported query reached Silo")
	}
	result, err := b.watchHistory(ctx, rid("user", 42), url.Values{"Filters": {"IsPlayed"}})
	if err == nil || result != nil {
		t.Fatalf("partial history returned as success: %v %v", result, err)
	}
}

func TestUserItemsHonorsLibraryPolicyAndMissingFilter(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/admin/playback-history":
			respond(w, http.StatusOK, object{"items": []any{
				object{"media_item_id": "allowed", "media_type": "movie", "media_title": "Allowed", "ended_at": "2026-01-03T00:00:00Z"},
				object{"media_item_id": "forbidden", "media_type": "movie", "media_title": "Forbidden", "ended_at": "2026-01-02T00:00:00Z"},
				object{"media_item_id": "missing", "media_type": "movie", "media_title": "Missing", "ended_at": "2026-01-01T00:00:00Z"},
			}, "page": object{}})
		case "/api/v2/admin/users":
			respond(w, http.StatusOK, object{"items": []any{object{"id": "42", "effective_policy": object{"library_ids": []any{"library-a"}}}}, "page": object{}})
		case "/api/v2/admin/catalog/items":
			respond(w, http.StatusOK, object{"items": []any{
				object{"content_id": "allowed", "type": "movie", "library_ids": []any{"library-a"}},
				object{"content_id": "forbidden", "type": "movie", "library_ids": []any{"library-b"}},
			}, "page": object{}})
		default:
			t.Errorf("unexpected Silo path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	b := newBridge(Config{SiloURL: upstream.URL, SiloKey: "silo-secret"})
	result, err := b.watchHistory(httptest.NewRequest(http.MethodGet, "/", nil).Context(), rid("user", 42), url.Values{
		"Filters": {"IsPlayed"}, "IsMissing": {"false"},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := result.(object)
	items := payload["Items"].([]object)
	if len(items) != 1 || items[0]["Id"] != rid("item", "allowed") || payload["TotalRecordCount"] != 1 {
		t.Fatalf("library policy or missing filter was ignored: %v", payload)
	}
}

func TestUserItemsRouteRequiresBridgeAuthentication(t *testing.T) {
	b := newBridge(Config{BridgeKey: "bridge-secret"})
	request := httptest.NewRequest(http.MethodGet, "/Users/"+rid("user", "42")+"/Items?Filters=IsPlayed", nil)
	response := httptest.NewRecorder()
	b.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated history status: %d", response.Code)
	}
	request = httptest.NewRequest(http.MethodGet, "/Users/"+rid("user", "42")+"/Items?Filters=IsPlayed", nil)
	request.Header.Set("Authorization", `MediaBrowser Token="bridge-secret"`)
	response = httptest.NewRecorder()
	b.ServeHTTP(response, request)
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusBadGateway {
		t.Fatalf("authenticated route should reach upstream request: %d %s %v", response.Code, response.Body.String(), err)
	}
}

func TestUserItemsRejectsInvalidHistoryDate(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/admin/users" {
			respond(w, http.StatusOK, object{"items": []any{object{"id": "42", "effective_policy": object{"library_ids": nil}}}, "page": object{}})
			return
		}
		respond(w, http.StatusOK, object{"items": []any{object{"media_item_id": "movie-1", "media_type": "movie", "ended_at": "not-a-date"}}, "page": object{}})
	}))
	defer upstream.Close()
	b := newBridge(Config{SiloURL: upstream.URL, SiloKey: "s"})
	query := url.Values{"Filters": {"IsPlayed"}}
	result, err := b.watchHistory(httptest.NewRequest(http.MethodGet, "/", nil).Context(), rid("user", 42), query)
	if err == nil || result != nil {
		t.Fatalf("invalid date produced partial result: %v %v", result, err)
	}
}
