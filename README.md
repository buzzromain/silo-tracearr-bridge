# Tracearr Bridge for Silo

Tracearr Bridge is a native Go plugin that exposes the Silo monitoring API to
[Tracearr](https://github.com/connorgallopo/Tracearr) through the Jellyfin API
routes Tracearr uses. It supports live session monitoring, users and libraries,
movie and TV catalog synchronization, artwork, watch history, and stream
termination.

This is an independent, MIT-licensed community integration. It is not part of
Silo's trusted community catalog.

This repository is a fork of
[`neilsb/silo-tracearr-bridge`](https://github.com/neilsb/silo-tracearr-bridge).
The original MIT copyright and permission notice are retained in
[`LICENSE`](LICENSE).

Current release: **0.4.0** for **Linux amd64**.

## Requirements

- A Silo server with plugin support and native API v2.
- Network access from Silo to its native API.
- Network access from Tracearr to the bridge listener.
- An administrator-owned Silo API key.
- A separate API key for Tracearr to authenticate to the bridge.

The native-ID format introduced in 0.4.0 is intended for clean installations.
If you used an earlier bridge release, read
[Migration to 0.4.0](#migration-to-040) before upgrading.

## Install

Add this repository to **Silo → Administration → Plugins → Repositories**:

```text
https://github.com/buzzromain/silo-tracearr-bridge/releases/latest/download/repository.json
```

Refresh the catalog and install **Tracearr Bridge**. Alternatively, download the
Linux amd64 binary and upload it through Silo's plugin administration.

## Configure

Open **Tracearr Bridge** settings and set:

| Setting | Purpose | Typical value |
| --- | --- | --- |
| Native Silo API URL | Native API as seen by the Silo process | `http://127.0.0.1:8080` |
| Silo administrator API key | Lets the plugin read monitoring data and terminate sessions | Secret |
| Tracearr bridge API key | Authenticates Tracearr to the plugin listener | Separate secret |
| Listener address | Address bound for Jellyfin compatibility | `0.0.0.0:8098` |
| Server name | Name shown by Tracearr | `Silo (Tracearr bridge)` |

The Silo key needs `admin:monitor:read`, `libraries:read`, and
`admin:sessions:control` when explicit scopes are enabled. An unscoped
administrator key already has the required access.

Save the settings, then open the plugin's **Tracearr Bridge** diagnostics page
or call the authenticated plugin `/status` route to start and verify the
listener. Saving settings stops the previous plugin process, so start it again
after each configuration change.

### Expose the listener

If Tracearr and Silo share a Docker network, configure Tracearr with:

```text
http://<silo-service-name>:8098
```

If they do not share a network, publish the listener from the Silo container:

```yaml
services:
  silo:
    ports:
      - "8098:8098"
```

Then use `http://<silo-host>:8098` from Tracearr. A reverse proxy may terminate
HTTPS in front of this listener, provided it preserves the authentication
header.

### Add to Tracearr

Add a **Jellyfin** server:

```text
URL: http://<silo-host>:8098
API key: the Tracearr bridge API key
```

Open the plugin's **Tracearr Bridge** diagnostics page to inspect listener
health, session polling, catalog scans, and artwork failures.

### Administrator helper

For scripted installations and checks, set `SILO_URL`, `SILO_API_KEY`, and
`BRIDGE_API_KEY`, then use `tools/plugin_admin.py`:

```sh
python3 tools/plugin_admin.py install dist/tracearr-bridge-linux-amd64
python3 tools/plugin_admin.py configure <installation-id>
python3 tools/plugin_admin.py status <installation-id>
```

The helper never prints configured secrets.

## Listener lifecycle

Silo lazily starts ordinary plugin processes. Keep the plugin's `listener`
scheduled task enabled at startup. A 60-second interval can improve recovery
after a process stop, but each run also performs a session check.

The plugin process owns the listener. Restarting Silo or stopping the plugin
process closes the listener until the startup task, diagnostics page, or
authenticated status route starts it again.

## Supported functionality

- Poll all native Silo playback sessions through the administrative monitoring
  API.
- Map account, device, network, media, progress, pause, and playback-mode data
  into Jellyfin session objects.
- Synchronize users, movie and TV libraries, media identities, external IDs,
  file versions, and derived seasons.
- Resolve same-origin Silo artwork on demand for live and library items.
- Terminate a Silo stream after Tracearr confirms the user action.
- Serve aggregated watched state through `/Users/{id}/Items`.
- Import finalized Silo playback attempts through a narrow Playback Reporting
  compatibility endpoint.

See [Tracearr compatibility](docs/compatibility.md) for route, history,
identifier, and memory details.

## Limitations

- Movie and TV libraries only. Music, ebooks, audiobooks, and other media types
  are not synchronized.
- Silo does not expose standalone season records. Seasons are derived from
  episode season numbers.
- Household profiles are grouped under their login account.
- The listener uses polling. No Tracearr SSE event source is provided.
- Only stream termination is implemented as a player control.
- Some Jellyfin metadata is absent from Silo and is omitted rather than
  invented. This includes avatar support, image resizing, and some paths,
  genres, runtimes, and dimensions.
- Jellyfin deep links generated by Tracearr do not open Silo's native UI.

## Migration to 0.4.0

Earlier releases encoded Silo IDs into prefixed Base64 values. Release 0.4.0
passes native Silo IDs through to Tracearr for users, libraries, items, files,
and sessions. Season IDs remain synthetic because Silo has no season records.

Tracearr does not automatically reconcile the two ID schemes. For an existing
installation, treat the bridge as a new media-server identity and plan the
library/history transition. A clean Tracearr installation can use 0.4.0
directly.

## Development

The project follows the layout of a standard Silo plugin: a root Go module,
embedded `manifest.json`, `Dockerfile`, tests, and release metadata.

```sh
make test
make vet
make build
```

`make build` creates `dist/tracearr-bridge-linux-amd64`. The test suite exercises
the real go-plugin subprocess, manifest validation, HTTP routes, pagination,
mapping, authentication, and failure handling against simulated Silo responses.

See [CONTRIBUTING.md](CONTRIBUTING.md) for development rules and
[RELEASING.md](RELEASING.md) for the publication process.

## Security

Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).
Never publish Silo API keys, bridge API keys, or Tracearr session data in an
issue.

## License

[MIT](LICENSE)
