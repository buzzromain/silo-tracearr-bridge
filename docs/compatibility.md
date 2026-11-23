# Tracearr compatibility

Tracearr Bridge implements the Jellyfin routes Tracearr uses. It is a targeted
compatibility layer, not a complete Jellyfin server.

The implementation was checked against:

- Silo `e3af98b06af2ee0ced92da518bf372f4b9fd5df6` native API v2 endpoints.
- Tracearr `6c9da518cbd21454aaebd9b3c652aaf59cfb891a` Jellyfin client,
  parsers, and Playback Reporting importer.

## Jellyfin routes

| Route | Behavior |
| --- | --- |
| `/System/Info` | Server identity and bridge version |
| `/System/Info/Public` | Public connection metadata |
| `/Users` | Silo accounts mapped to Jellyfin users |
| `/Users/{id}` | One Silo account |
| `/Users/{id}/Items?Filters=IsPlayed` | Aggregated completed watch history |
| `/Library/VirtualFolders` | Movie and TV libraries |
| `/Items` | Movie, series, derived season, and episode queries |
| `/Sessions` | Active Silo playback sessions |
| `/Items/{id}/Images/Primary` | On-demand Silo artwork |
| `/Sessions/{id}/Playing/Stop` | Authority-revoking stream termination |

Unsupported query filters return an explicit error instead of silently changing
the meaning of a request.

## Identifiers

Users, libraries, items, media files, and sessions use Silo's native identifiers
as opaque strings. The bridge does not add type prefixes or Base64 encoding.

Seasons are the only derived identity because Silo's catalog has no season
records. A season ID combines the Silo series ID and season number. Season names
follow Jellyfin convention: `Season N`, with season zero named `Specials`.

Playback Reporting uses synthetic row IDs derived from Silo session IDs. These
IDs are stable for re-import detection, but they are separate from live session
IDs.

## Live sessions

Tracearr controls polling frequency by calling `/Sessions`. The plugin does not
start a permanent session polling loop. Each request reads Silo's current admin
session collection, enriches active content IDs with targeted catalog metadata,
and maps the result into Jellyfin session objects.

Silo may report TV playback as the parent `series` type. When season and episode
numbers are present, the plugin maps the session as an episode.

## Watch history

`/Users/{id}/Items?Filters=IsPlayed` groups completed Silo attempts by media
item:

- `UserData.PlayCount` is the number of completed Silo attempts.
- `UserData.LastPlayedDate` is the latest Silo `ended_at`.
- Results can be sorted and paginated by `LastPlayedDate`.

History is account-level because Silo does not distinguish household profiles
inside one login account.

## Playback Reporting import

Tracearr's historical import normally queries the Jellyfin Playback Reporting
plugin through `/user_usage_stats/submit_custom_query`. The bridge emulates the
narrow protocol Tracearr uses:

1. Tracearr requests the `PlaybackActivity` schema.
2. The bridge advertises only the columns Tracearr requires.
3. Tracearr requests summary statistics and rowid-ordered pages of 5,000 rows.
4. The bridge maps completed, measured Silo attempts into those rows.

No incoming SQL is executed. Unsupported queries are rejected.

Only finalized movie and episode attempts with a known user, item, valid start
time, and measured watch time are importable. Select UTC as the import timezone
in Tracearr.

Tracearr computes the imported stop time as `DateCreated + PlayDuration`.
Silo's true start and stop timestamps cannot both be represented by this
protocol. Pauses, buffering, and seeks can therefore make the imported stop time
differ from Silo's actual end time.

Tracearr applies its own import cutoff and overlap handling. Complete
cross-source deduplication is not guaranteed. Test imports against a backup or
an isolated Tracearr instance.

## Caching and resource limits

- User, library, and profile caches expire after 60 seconds.
- Catalog snapshots are shared across concurrent requests and cached for five
  minutes after a complete scan.
- Session metadata is cached for five minutes, with short negative caching.
- Artwork lookup URLs are limited to 16 entries. Images are limited to 8 MiB
  each.
- Upstream JSON responses are limited to 8 MiB.
- One paginated Silo collection is limited to 200,000 rows.
- One Playback Reporting snapshot can contain up to 200,000 rows.
- Incoming POST bodies are limited to 16 KiB.

The independent limits are defensive upper bounds, not a global memory budget.
Large libraries and concurrent historical imports can produce significant
temporary memory use.
