package main

import (
	"context"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type watchedItem struct {
	item      object
	lastPlay  time.Time
	playCount int
}

// watchHistory implements the Jellyfin per-user Items query used by Tracearr
// to read played media. Silo's finalized attempt log is the source of truth.
func (b *Bridge) watchHistory(ctx context.Context, userID string, input url.Values) (any, error) {
	nativeUserID, err := decodeID("user", userID)
	if err != nil {
		return nil, err
	}
	query := url.Values{}
	for key, values := range input {
		query[strings.ToLower(key)] = values
	}
	allowed := map[string]bool{
		"recursive": true, "includeitemtypes": true, "ismissing": true,
		"filters": true, "sortby": true, "sortorder": true, "startindex": true,
		"limit": true, "fields": true, "api_key": true, "apikey": true,
	}
	for key := range query {
		if !allowed[key] {
			return nil, fail(400, "unsupported user item query")
		}
	}
	if !strings.EqualFold(query.Get("filters"), "IsPlayed") {
		return nil, fail(501, "user item queries require the IsPlayed filter")
	}
	if sortBy := query.Get("sortby"); sortBy != "" && !strings.EqualFold(sortBy, "DatePlayed") {
		return nil, fail(400, "unsupported watch history sort field")
	}
	if sortOrder := query.Get("sortorder"); sortOrder != "" && !strings.EqualFold(sortOrder, "Descending") && !strings.EqualFold(sortOrder, "Ascending") {
		return nil, fail(400, "unsupported watch history sort order")
	}
	startIndex, limit := 0, 100
	if query.Has("startindex") {
		startIndex, err = strconv.Atoi(query.Get("startindex"))
		if err != nil || startIndex < 0 {
			return nil, fail(400, "invalid start index")
		}
	}
	if query.Has("limit") {
		limit, err = strconv.Atoi(query.Get("limit"))
		if err != nil || limit < 0 || limit > 10000 {
			return nil, fail(400, "invalid limit")
		}
	}
	if query.Get("ismissing") != "" && !strings.EqualFold(query.Get("ismissing"), "true") && !strings.EqualFold(query.Get("ismissing"), "false") {
		return nil, fail(400, "invalid IsMissing value")
	}
	if strings.EqualFold(query.Get("ismissing"), "true") {
		return object{"Items": []object{}, "TotalRecordCount": 0, "StartIndex": startIndex}, nil
	}
	if recursive := query.Get("recursive"); recursive != "" && !strings.EqualFold(recursive, "true") && !strings.EqualFold(recursive, "false") {
		return nil, fail(400, "invalid Recursive value")
	}
	includeTypes := map[string]bool{"movie": true, "episode": true}
	if query.Get("includeitemtypes") != "" {
		includeTypes = map[string]bool{}
		for _, itemType := range strings.Split(strings.ToLower(query.Get("includeitemtypes")), ",") {
			includeTypes[strings.TrimSpace(itemType)] = true
		}
	}

	rows, err := b.collectionQuery(ctx, "/admin/playback-history", true, url.Values{
		"user_id":   {nativeUserID},
		"completed": {"true"},
	})
	if err != nil {
		return nil, err
	}
	allowedLibraries, restrictLibraries, err := b.userLibraryAccess(ctx, nativeUserID)
	if err != nil {
		return nil, err
	}
	var presentItems map[string]object
	if strings.EqualFold(query.Get("ismissing"), "false") || restrictLibraries {
		catalog, err := b.catalog(ctx)
		if err != nil {
			return nil, err
		}
		presentItems = make(map[string]object, len(catalog))
		for _, item := range catalog {
			presentItems[str(item["content_id"])] = item
		}
	}
	byItem := make(map[string]*watchedItem)
	for _, row := range rows {
		mediaID, mediaType := str(row["media_item_id"]), strings.ToLower(str(row["media_type"]))
		if mediaID == "" || !includeTypes[mediaType] {
			continue
		}
		if presentItems != nil {
			catalogItem, exists := presentItems[mediaID]
			if !exists && (strings.EqualFold(query.Get("ismissing"), "false") || restrictLibraries) {
				continue
			}
			if exists && restrictLibraries && !hasAllowedLibrary(catalogItem, allowedLibraries) {
				continue
			}
		}
		endedAt, err := time.Parse(time.RFC3339Nano, str(row["ended_at"]))
		if err != nil {
			return nil, fail(502, "invalid Silo playback history date")
		}
		key := mediaType + "\x00" + mediaID
		watched := byItem[key]
		if watched == nil {
			kind := map[string]string{"movie": "Movie", "episode": "Episode"}[mediaType]
			watched = &watchedItem{item: object{
				"Id": rid("item", mediaID), "Name": row["media_title"], "Type": kind,
				"SeriesName": row["series_title"], "ParentIndexNumber": row["season_number"],
				"IndexNumber": row["episode_number"],
				"UserData":    object{"Played": true, "PlayCount": 0},
			}}
			byItem[key] = watched
		}
		watched.playCount++
		if endedAt.After(watched.lastPlay) {
			watched.lastPlay = endedAt
		}
	}
	items := make([]*watchedItem, 0, len(byItem))
	for _, item := range byItem {
		item.item["UserData"].(object)["PlayCount"] = item.playCount
		item.item["UserData"].(object)["LastPlayedDate"] = item.lastPlay.UTC().Format(time.RFC3339Nano)
		items = append(items, item)
	}
	descending := !strings.EqualFold(query.Get("sortorder"), "Ascending")
	sort.Slice(items, func(i, j int) bool {
		if items[i].lastPlay.Equal(items[j].lastPlay) {
			return str(items[i].item["Id"]) < str(items[j].item["Id"])
		}
		if descending {
			return items[i].lastPlay.After(items[j].lastPlay)
		}
		return items[i].lastPlay.Before(items[j].lastPlay)
	})
	out := make([]object, 0, min(limit, len(items)))
	for _, item := range items[min(startIndex, len(items)):min(startIndex+limit, len(items))] {
		out = append(out, item.item)
	}
	return object{"Items": out, "TotalRecordCount": len(items), "StartIndex": startIndex}, nil
}

func (b *Bridge) userLibraryAccess(ctx context.Context, userID string) (map[string]bool, bool, error) {
	users, err := b.cached(ctx, "/admin/users", true)
	if err != nil {
		return nil, false, err
	}
	for _, user := range users {
		if str(user["id"]) != userID {
			continue
		}
		libraries, exists := obj(user["effective_policy"])["library_ids"]
		if !exists || libraries == nil {
			return nil, false, nil
		}
		allowed := make(map[string]bool)
		for _, id := range anySlice(libraries) {
			allowed[str(id)] = true
		}
		return allowed, true, nil
	}
	return nil, false, fail(404, "unknown Jellyfin user")
}

func hasAllowedLibrary(item object, allowed map[string]bool) bool {
	for _, id := range anySlice(item["library_ids"]) {
		if allowed[str(id)] {
			return true
		}
	}
	return false
}
