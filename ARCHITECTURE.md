# Architecture

## Shape

```text
Browser
  │ same-origin JSON, HTML, and media Range requests
  ▼
Go HTTP server ───── embedded static WebUI
  ├── session and path-boundary checks
  ├── library / item / job API
  └── direct file streaming via http.ServeContent
        │
        ├── SQLite (WAL): catalog and durable scan queue
        └── one background scanner: walk → batch upsert → reconcile
```

This is a modular monolith. It keeps request handling and background work in one deployable process, while package boundaries isolate configuration, authentication, persistence, scanning, and HTTP behavior. That lets the project add a second worker process or another database later without taking on service discovery and distributed transactions in the first release.

## Package ownership

- `internal/config`: validates startup settings and resolves allowed media roots.
- `internal/auth`: creates and verifies expiring HMAC-signed sessions using a random key stored in the data volume.
- `internal/database`: schema, indexed queries, library records, jobs, and media items.
- `internal/scanner`: durable queue polling, safe directory traversal, batched indexing, progress, and final reconciliation.
- `internal/httpapi`: routing, input validation, authorization, path checks, JSON APIs, health, and Range playback.
- `internal/httpapi/static`: embedded WebUI; there is no separate Node runtime in production.

## Scan and catalog consistency

1. A library creation or refresh request adds a `pending` job to SQLite. A partial unique index allows at most one `pending` or `running` scan per library; repeated requests return the active job.
2. A single worker claims work in queue order and records `running` before traversing the directory.
3. Regular media files are upserted in transactions of at most 256 rows. The scan job stores progress after each committed batch.
4. Only a completely successful walk prunes entries not observed by that job and marks the job `done` in one transaction. Permission or I/O errors leave existing catalog entries in place and mark the job failed.
5. On startup, `running` jobs are requeued. Replaying the upserts is safe because a file path has a unique key.

SQLite runs in WAL mode with a busy timeout and a small connection pool. Concurrent API reads and playback do not hold the scan's write transaction open; the scanner commits at bounded intervals. SQLite still serializes writes, so the scanner intentionally remains a single worker in this milestone.

## Playback and access boundaries

The API loads a catalog item by ID, resolves its current filesystem path, checks that the resolved path is still inside an allowed root, opens a regular file, and passes it to `http.ServeContent`. The standard library handles byte ranges, conditional requests, and `HEAD`. The server limits simultaneous media responses to 32. It does not proxy arbitrary remote URLs or transcode content.

Library roots must exist, be inside `CINEBASE_MEDIA_ROOTS`, and must not overlap another library. Directory traversal skips symlinks. Docker mounts media read-only by default. The session cookie is HttpOnly and SameSite Strict; state-changing requests with a foreign Origin are rejected. TLS should be terminated at a reverse proxy for remote access.

## Planned sequence

1. **Hardening and verification:** exercise the full build on amd64 and arm64, add unit and integration coverage, rate-limit login, add structured scan diagnostics, and measure large-library query/scan behavior.
2. **Library quality:** parse NFO and filename metadata, track movie/series/season/episode relationships, create artwork cache keys, and add incremental change detection.
3. **Playback compatibility:** subtitle discovery, audio and subtitle selection, client capability reporting, and an optional Emby-compatible API layer.
4. **Concurrency:** add cancellation and scan priority, parallelize filesystem stat work with bounded queues, and profile SQLite write contention before adding workers.
5. **WebUI:** artwork-backed posters, filters and sort preferences, responsive playback settings, and playback progress/continue watching.
6. **Deployment:** non-root image, Compose and reverse-proxy examples, health and metrics, backup/restore guidance, image provenance and SBOM, and controlled release tags.

The intended order is to keep the metadata model and playback behavior correct before multiplying scanner workers or splitting the server into services.
