package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// playbackReportingPageSize must match Tracearr's requested LIMIT and its
	// decision to stop importing when a page contains fewer rows.
	playbackReportingPageSize = 5000
	historySnapshotTTL        = 30 * time.Minute
)

// Tracearr currently emits this exact Playback Reporting page query. Never
// execute supplied SQL: it is an untrusted protocol message, not a DB query.
var historyPageQuery = regexp.MustCompile(
	`(?i)^SELECT rowid, DateCreated, UserId, ItemId, ItemType, ItemName, ` +
		`PlaybackMethod, ClientName, DeviceName, PlayDuration, PauseDuration, ` +
		`RemoteAddress FROM PlaybackActivity WHERE rowid > ([0-9]+) ` +
		`ORDER BY rowid LIMIT ` + strconv.Itoa(playbackReportingPageSize) + `$`,
)

// historyColumns is the table_info result advertised to Tracearr's importer.
var historyColumns = []string{"DateCreated", "UserId", "ItemId", "ItemType", "ItemName", "PlaybackMethod", "ClientName", "DeviceName", "PlayDuration", "PauseDuration", "RemoteAddress"}

// historyEntry is one importable Silo attempt and its Playback Reporting row.
type historyEntry struct {
	id     uint64
	date   string
	values []string
}

func (b *Bridge) playbackReporting(ctx context.Context, body object) (any, error) {
	query, ok := body["CustomQueryString"].(string)
	if !ok {
		return nil, fail(400, "missing Playback Reporting query")
	}
	query = strings.TrimSpace(query)
	if strings.EqualFold(query, "pragma table_info('PlaybackActivity')") {
		rows := make([][]string, 0, len(historyColumns))
		for i, name := range historyColumns {
			rows = append(rows, []string{strconv.Itoa(i), name, "TEXT", "0", "", "0"})
		}
		return object{"results": rows}, nil
	}
	page := historyPageQuery.FindStringSubmatch(query)
	summary := strings.EqualFold(query, "SELECT COUNT(1), MIN(DateCreated), MAX(DateCreated) FROM PlaybackActivity")
	if page == nil && !summary {
		return nil, fail(400, "unsupported Playback Reporting query")
	}
	if summary {
		entries, err := b.playbackHistory(ctx)
		if err != nil {
			return nil, err
		}
		oldest, newest := "", ""
		for _, entry := range entries {
			if oldest == "" || entry.date < oldest {
				oldest = entry.date
			}
			if newest == "" || entry.date > newest {
				newest = entry.date
			}
		}
		return object{"results": [][]string{{strconv.Itoa(len(entries)), oldest, newest}}}, nil
	}
	after, err := strconv.ParseUint(page[1], 10, 53)
	if err != nil {
		return nil, fail(400, "invalid history cursor")
	}
	// Tracearr does not send an import ID. Serialize imports and keep one bounded
	// immutable snapshot. A concurrent first page fails rather than replacing a
	// snapshot that another import is still using.
	b.historyMu.Lock()
	defer b.historyMu.Unlock()
	if after == 0 {
		if b.historySnapshot != nil && time.Now().Before(b.historyExpires) {
			return nil, fail(409, "another history import is in progress")
		}
		entries, err := b.playbackHistory(ctx)
		if err != nil {
			return nil, err
		}
		b.historySnapshot = entries
		b.historyExpires = time.Now().Add(historySnapshotTTL)
	} else if b.historySnapshot == nil || time.Now().After(b.historyExpires) {
		b.historySnapshot = nil
		return nil, fail(409, "history snapshot expired; restart the import")
	}
	results := make([][]string, 0)
	for _, entry := range b.historySnapshot {
		if entry.id > after {
			results = append(results, entry.values)
			if len(results) == playbackReportingPageSize {
				break
			}
		}
	}
	// Tracearr stops paging as soon as it receives fewer than PAGE_SIZE rows.
	// Release that snapshot now so its next import can start immediately. A full
	// page is ambiguous, so Tracearr will request another page (possibly empty).
	if len(results) < playbackReportingPageSize {
		b.historySnapshot = nil
		b.historyExpires = time.Time{}
	}
	return object{"results": results}, nil
}

func (b *Bridge) playbackHistory(ctx context.Context) ([]historyEntry, error) {
	// Freeze the upper time bound before the first Silo page; sessions ending
	// during this scan belong to a later import. Silo does not guarantee a
	// transactional snapshot for updates/deletions to older records.
	cutoff := time.Now().UTC().Format(time.RFC3339Nano)
	rows, err := b.collectionQuery(ctx, "/admin/playback-history", true, url.Values{"ended_before": {cutoff}})
	if err != nil {
		return nil, err
	}
	entries := make([]historyEntry, 0, len(rows))
	stats := map[string]int{"total": len(rows), "importable": 0, "missing_identity_or_date": 0, "unsupported_type": 0, "unmeasured": 0}
	ids := make(map[uint64]string, len(rows))
	for _, row := range rows {
		session := str(row["session_id"])
		user, item := str(row["user_id"]), str(row["media_item_id"])
		started, err := time.Parse(time.RFC3339Nano, str(row["started_at"]))
		if err != nil || session == "" || user == "" || item == "" {
			stats["missing_identity_or_date"]++
			continue
		}
		kind := map[string]string{"movie": "Movie", "episode": "Episode"}[strings.ToLower(str(row["media_type"]))]
		if kind == "" {
			stats["unsupported_type"]++
			continue
		}
		digest := sha256.Sum256([]byte(session))
		// Javascript numbers are exact up to 2^53-1. Zero is the first cursor.
		id := binary.BigEndian.Uint64(digest[:8]) & ((1 << 53) - 1)
		if id == 0 || ids[id] != "" {
			return nil, fail(502, "duplicate or colliding Silo history session ID")
		}
		ids[id] = session
		date := started.UTC().Format("2006-01-02 15:04:05")
		method := map[string]string{"direct": "DirectPlay", "remux": "DirectStream", "direct_stream": "Transcode", "audio": "Transcode", "transcode": "Transcode"}[str(row["play_method"])]
		title := str(row["media_title"])
		if kind == "Episode" && str(row["series_title"]) != "" && row["season_number"] != nil && row["episode_number"] != nil {
			title = fmt.Sprintf("%s - s%02de%02d - %s", str(row["series_title"]), int(num(row["season_number"])), int(num(row["episode_number"])), title)
		}
		// A missing measurement is not a zero-length watch. Skip it rather than
		// inventing playback duration from elapsed time or seek position.
		if row["played_seconds"] == nil {
			stats["unmeasured"]++
			continue
		}
		values := []string{strconv.FormatUint(id, 10), date, rid("user", user), rid("item", item), kind, title, method, str(row["client_name"]), str(row["device_platform"]), strconv.FormatFloat(num(row["played_seconds"]), 'f', -1, 64), strconv.FormatFloat(num(row["paused_seconds"]), 'f', -1, 64), str(row["client_ip"])}
		entries = append(entries, historyEntry{id: id, date: date, values: values})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].id < entries[j].id })
	stats["importable"] = len(entries)
	b.mu.Lock()
	b.historyStats = stats
	b.mu.Unlock()
	return entries, nil
}
