# Releasing

Releases are immutable GitHub assets pinned by tag, version, and SHA-256
checksum. Silo discovers them through:

```text
https://github.com/buzzromain/silo-tracearr-bridge/releases/latest/download/repository.json
```

## Before tagging

1. Update the version in `main.go`, `manifest.json`, `README.md`, and
   `CHANGELOG.md`.
2. Update route or identifier documentation when compatibility changes.
3. Confirm that the manifest presentation contains current support and changelog
   URLs.
4. Run the full checks:

```sh
make test
go test -race ./...
make vet
git diff --check
```

5. Build the release locally:

```sh
make package TAG=v0.4.0
sha256sum -c dist/SHA256SUMS
dist/tracearr-bridge-linux-amd64 manifest
```

6. Verify that the printed manifest version and checksum match the release
   binary and tag.

## Publish

1. Commit the release changes.
2. Push the default branch.
3. Create and push an annotated `vMAJOR.MINOR.PATCH` tag.
4. The release workflow reruns tests and vet, builds the static Linux amd64
   artifact, packages `manifest.json`, `repository.json`, and `SHA256SUMS`, then
   publishes all four files to the GitHub release.
5. Refresh the repository in Silo and install from the published catalog.

Do not overwrite release assets or reuse tags. Publish a new patch release for
fixes.

## Release artifact

The release contains:

- `tracearr-bridge-linux-amd64`
- `manifest.json` with the binary checksum
- `repository.json` for Silo's repository catalog
- `LICENSE` with the original MIT attribution
- `SHA256SUMS`

Builds currently target Linux amd64 only. Add and test another platform before
advertising it in `supported_platforms`.

## Rollback

Download the earlier release binary and checksum, verify it, and upload it
through Silo's plugin administration. Back up plugin configuration before
changing versions. Identifier changes require special care; read the migration
notes in the changelog.
