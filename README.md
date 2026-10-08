# Cinebase

Cinebase is a new, direct-play home media library service. This repository starts with a small, self-contained vertical slice: an authenticated WebUI, local media libraries, a restart-safe scan queue, a SQLite catalog, and HTTP Range playback. It is a fresh implementation; it does not copy the existing zemby service.

## What works in this first milestone

- Add movie, series, or music libraries beneath configured media roots.
- Queue a scan automatically when a library is added, or request a refresh later.
- Walk directories without following symlinks, index video and audio formats in the matching library type, and write catalog batches of 256 files.
- Persist scan jobs in SQLite. A server restart requeues an interrupted scan; an incomplete scan never removes catalog entries.
- Browse the indexed files in a responsive WebUI and play them with byte-range requests.
- Restrict file access to configured media roots, require an administrator password, and store sessions in signed, HttpOnly cookies.
- Run from Docker Compose or publish multi-architecture `linux/amd64` and `linux/arm64` images to GHCR with GitHub Actions.
- Bound simultaneous direct-play responses with a configurable semaphore (32 by default) to protect file descriptors during client bursts.

This milestone indexes filenames and file attributes only. It does not yet extract technical metadata, download artwork, parse NFO files, transcode media, support remote `.strm` links, or implement Emby client APIs. Browser direct play depends on the browser supporting the file's container and codecs.

## Start with Docker Compose

```bash
cp .env.example .env
# Edit .env and set CINEBASE_ADMIN_PASSWORD to a unique password with 12+ characters.
mkdir -p data media
docker compose up --build -d
```

PowerShell equivalent for the setup commands:

```powershell
Copy-Item .env.example .env
# Edit .env and set CINEBASE_ADMIN_PASSWORD to a unique password with 12+ characters.
New-Item -ItemType Directory -Force data, media | Out-Null
docker compose up --build -d
```

Open `http://localhost:8097`. The compose file mounts `MEDIA_PATH` read-only at `/media`, persists the SQLite database and session signing key in `./data`, and checks `/api/health` for container health. On Windows, use a Docker Desktop shared path for `MEDIA_PATH`, for example `D:/Media`.

Use the path as it appears inside the container when adding a library, such as `/media/Movies`. Set `CINEBASE_MEDIA_ROOTS` to a colon-separated list of allowed paths on Linux; on Windows, use the platform path-list separator. Keep the data directory persistent: removing it invalidates sessions and loses the catalog and queued jobs.

For access beyond a trusted home network, place Cinebase behind a TLS reverse proxy that forwards `X-Forwarded-Proto`. The first version has one administrator password and no per-user permissions. On Linux, set `PUID` and `PGID` in `.env` to the owner of `./data` so the non-root container can write the database and session key.

## Run from source

Install Go 1.27 or newer, then:

```bash
go mod download
go mod tidy
mkdir -p data media
export CINEBASE_ADMIN_PASSWORD='replace-with-a-unique-password-of-12-or-more-characters'
export CINEBASE_DATA_DIR=./data
export CINEBASE_MEDIA_ROOTS=./media
go run ./cmd/cinebase
```

The server listens on `:8097` by default. Configuration:

| Variable | Default | Purpose |
| --- | --- | --- |
| `CINEBASE_ADDR` | `:8097` | HTTP listen address |
| `CINEBASE_DATA_DIR` | `./data` | SQLite database and session key directory |
| `CINEBASE_ADMIN_PASSWORD` | required | Initial and ongoing administrator password, at least 12 characters |
| `CINEBASE_MEDIA_ROOTS` | `./media` | Allowed media roots; paths outside them cannot be indexed or streamed |
| `CINEBASE_MAX_STREAMS` | `32` | Concurrent media responses, from 1 to 256 |

## API in this milestone

| Method | Route | Purpose |
| --- | --- | --- |
| `GET` | `/api/health` | Unauthenticated health check |
| `POST` | `/api/login` | Create an administrator session |
| `POST` | `/api/logout` | Clear the current session |
| `GET` | `/api/libraries` | List libraries and indexed file counts |
| `POST` | `/api/libraries` | Add a movie, series, or music library and queue its initial scan |
| `POST` | `/api/libraries/{id}/scan` | Queue a refresh scan; duplicate active scans collapse to the same job |
| `GET` | `/api/jobs` | Get recent scan jobs and progress |
| `GET` | `/api/items?libraryId=1&limit=50&offset=0` | List indexed files |
| `GET`, `HEAD` | `/api/items/{id}/stream` | Direct playback with HTTP Range support |

All routes except health, login, and the embedded WebUI require a valid session cookie. The WebUI uses same-origin requests; cross-origin state-changing requests are rejected.

## Build and publish images

`.github/workflows/container.yml` builds the container on pushes to `main`, `codex`, or `codx1`, version tags, pull requests, and manual dispatch. It publishes branch, version, and default-branch `latest` tags to `ghcr.io/<owner>/<repository>` for both amd64 and arm64. Pull requests build without publishing. The build also attaches provenance and an SBOM.

This project is currently published on the `codx1` branch of `haixing1001/zemby`. A push to that branch publishes `ghcr.io/haixing1001/zemby:codx1` after the GitHub Actions build succeeds. For another repository, the image name follows that repository's owner and name.

## Design notes

See [ARCHITECTURE.md](ARCHITECTURE.md) for the module boundaries, data flow, consistency rules, and planned next milestones.
