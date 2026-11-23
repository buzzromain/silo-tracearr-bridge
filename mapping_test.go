package main

import "testing"

func TestMappingPreservesNativeSiloIDs(t *testing.T) {
	item := mapItem(object{
		"content_id": "silo-movie-123", "type": "movie", "series_id": "silo-series-456",
		"versions": []any{object{"file_id": "silo-file-789"}},
	})
	if item["Id"] != "silo-movie-123" || item["SeriesId"] != "silo-series-456" || obj(item["MediaSources"].([]object)[0])["Id"] != "silo-file-789" {
		t.Fatalf("item identifiers were transformed: %v", item)
	}

	user := mapUser(object{"id": "silo-user-1", "username": "user", "effective_policy": object{"library_ids": []any{"silo-library-1"}}})
	folders, _ := obj(user["Policy"])["EnabledFolders"].([]string)
	if user["Id"] != "silo-user-1" || len(folders) != 1 || folders[0] != "silo-library-1" {
		t.Fatalf("user or library identifiers were transformed: %v", user)
	}
	if library := mapLibrary(object{"id": "silo-library-1", "name": "Movies", "type": "movies"}); library["ItemId"] != "silo-library-1" {
		t.Fatalf("library identifier was transformed: %v", library)
	}

	session, err := mapSession(object{
		"session_id": "silo-session-1", "user_id": "silo-user-1", "media_file_id": "silo-file-789",
		"content_id": "silo-movie-123", "position_seconds": 0, "is_paused": false,
		"updated_at": "now", "effective_play_method": "direct", "media_type": "movie",
	})
	if err != nil {
		t.Fatal(err)
	}
	if session["Id"] != "silo-session-1" || session["UserId"] != "silo-user-1" || obj(session["PlayState"])["MediaSourceId"] != "silo-file-789" || obj(session["NowPlayingItem"])["Id"] != "silo-movie-123" {
		t.Fatalf("session identifiers were transformed: %v", session)
	}
}

func TestNativeSeriesSessionIsAnEpisode(t *testing.T) {
	for _, season := range []int{0, 3} {
		row := object{"session_id": "s", "user_id": "u", "media_file_id": "f", "position_seconds": 0,
			"is_paused": false, "updated_at": "now", "effective_play_method": "direct",
			"media_type": "series", "media_title": "Grimsburg", "series_name": "Grimsburg",
			"episode_name": "CrimeShare", "season_number": season, "episode_number": 5, "year": 2024, "series_id": "series-1"}
		session, err := mapSession(row)
		if err != nil {
			t.Fatal(err)
		}
		item := obj(session["NowPlayingItem"])
		if item["Type"] != "Episode" || item["SeriesName"] != "Grimsburg" || item["Name"] != "CrimeShare" || item["ParentIndexNumber"] != season || item["IndexNumber"] != 5 || item["ProductionYear"] != 2024 || item["SeriesId"] != rid("item", "series-1") {
			t.Fatalf("incorrect episode: %v", item)
		}
	}
}

func TestSeasonItemsProvideStableJellyfinHierarchy(t *testing.T) {
	catalog := []object{
		{"content_id": "episode-1", "type": "episode", "series_id": "show-1", "series_title": "Show", "season_number": float64(1), "library_ids": []any{"tv-a"}},
		{"content_id": "episode-2", "type": "episode", "series_id": "show-1", "series_title": "Show", "season_number": float64(1), "library_ids": []any{"tv-b"}},
		{"content_id": "special", "type": "episode", "series_id": "show-1", "series_title": "Show", "season_number": float64(0), "library_ids": []any{"tv-a"}},
	}
	seasons := seasonItems(catalog)
	if len(seasons) != 2 {
		t.Fatalf("got %d seasons, want 2: %v", len(seasons), seasons)
	}
	var regular, specials object
	for _, season := range seasons {
		if season["season_number"] == 1 {
			regular = season
		} else if season["season_number"] == 0 {
			specials = season
		}
	}
	if regular == nil || regular["title"] != "Season 1" || len(anySlice(regular["library_ids"])) != 2 {
		t.Fatalf("incorrect regular season: %v", regular)
	}
	if specials == nil || specials["title"] != "Specials" {
		t.Fatalf("incorrect specials season: %v", specials)
	}
	seasonID := seasonID("show-1", 1)
	seasonSeries, seasonNumber, ok := seasonParent(mustDecodeItemID(t, seasonID))
	if !ok || seasonSeries != "show-1" || seasonNumber != 1 {
		t.Fatalf("season ID did not round trip: %q %v %d %v", seasonID, seasonSeries, seasonNumber, ok)
	}
	mappedSeason := mapItem(regular)
	if mappedSeason["Type"] != "Season" || mappedSeason["ParentId"] != rid("item", "show-1") || mappedSeason["IndexNumber"] != 1 {
		t.Fatalf("incorrect Jellyfin season: %v", mappedSeason)
	}
	mappedEpisode := mapItem(catalog[0])
	if mappedEpisode["SeasonId"] != seasonID || mappedEpisode["ParentId"] != seasonID || mappedEpisode["SeasonName"] != "Season 1" {
		t.Fatalf("incorrect Jellyfin episode hierarchy: %v", mappedEpisode)
	}
}

func TestItemMappingPassesThroughOnlyAvailablePathAndGenres(t *testing.T) {
	row := object{"content_id": "movie", "type": "movie", "title": "Film"}
	mapped := mapItem(row)
	if _, ok := mapped["Path"]; ok {
		t.Fatal("missing Silo path should stay absent")
	}
	if _, ok := mapped["Genres"]; ok {
		t.Fatal("missing Silo genres should stay absent")
	}
	row["path"] = "/media/film.mkv"
	row["genres"] = []any{"Drama"}
	mapped = mapItem(row)
	if mapped["Path"] != "/media/film.mkv" || len(anySlice(mapped["Genres"])) != 1 {
		t.Fatalf("available metadata not preserved: %v", mapped)
	}
}

func mustDecodeItemID(t *testing.T, id string) string {
	t.Helper()
	decoded, err := decodeID("item", id)
	if err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestPlaybackDetailsDoNotInventSourceOrHardware(t *testing.T) {
	row := object{"session_id": "s", "user_id": "u", "media_file_id": "f", "position_seconds": 0,
		"is_paused": false, "updated_at": "now", "effective_play_method": "transcode",
		"source_video_codec": "hevc", "source_video_resolution": "2160p", "source_video_height": 1600,
		"stream_bitrate_kbps": 4000, "target_resolution": "1920x800", "transcode_hw_accel": "none"}
	session, err := mapSession(row)
	if err != nil {
		t.Fatal(err)
	}
	source := obj(session["NowPlayingItem"])["MediaSources"].([]object)[0]
	if _, ok := source["Bitrate"]; ok {
		t.Fatal("output bitrate was presented as source bitrate")
	}
	video := source["MediaStreams"].([]object)[0]
	if _, ok := video["Width"]; ok {
		t.Fatal("invented width from resolution class")
	}
	if video["Height"] != 1600 {
		t.Fatal("lost actual cropped source height")
	}
	info := obj(session["TranscodingInfo"])
	if info["HardwareAccelerationType"] != "none" || info["Width"] != 1920 {
		t.Fatalf("incorrect output: %v", info)
	}
	row["effective_play_method"] = "remux"
	row["transcode_hw_accel"] = "qsv"
	session, err = mapSession(row)
	if err != nil {
		t.Fatal(err)
	}
	info = obj(session["TranscodingInfo"])
	if _, ok := info["HardwareAccelerationType"]; ok {
		t.Fatal("copied video reported hardware encoding")
	}
	if info["Height"] != 1600 || info["VideoCodec"] != "hevc" {
		t.Fatal("copy did not preserve source details")
	}
	for _, value := range []string{"2160p", "badx1080", "0x1080", "1920x0"} {
		if exactHeight(value) != nil || exactDimension(value, 0) != nil {
			t.Fatalf("invalid dimensions accepted: %s", value)
		}
	}
}
