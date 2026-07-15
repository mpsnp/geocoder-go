# Production operations

## Container image

Published images use:

```text
ghcr.io/gametec-live/geocoder-go
```

The runtime stage is `scratch`. It contains only `/geocoder` and an empty
`/data` directory. It has no shell, package manager, libc, or debugging tools.
The process runs as numeric UID/GID `65532:65532`.

Geographic packs are deliberately excluded from the image. This keeps code and
data release cycles independent and avoids distributing data without its
required attribution.

## Configuration

| Environment variable | Flag | Default | Description |
|---|---|---|---|
| `GEOCODER_DATA` | `--data` | `data` natively, `/data` in the image | Directory recursively containing packs. |
| `GEOCODER_LISTEN` | `--listen` | `:8080` | HTTP listen address. |
| `GEOCODER_HEALTHCHECK_URL` | healthcheck argument | `http://127.0.0.1:8080/healthz` | URL checked by `geocoder healthcheck`. |
| `GIN_MODE` | — | Gin default natively, `release` in the image | Gin logging mode. |

CLI utilities:

```sh
geocoder version
geocoder healthcheck [URL]
```

## Hardened Docker run

```sh
docker run -d \
  --name geocoder \
  --restart unless-stopped \
  --read-only \
  --user 65532:65532 \
  --cap-drop ALL \
  --security-opt no-new-privileges \
  -p 127.0.0.1:8080:8080 \
  -v /srv/geocoder/data:/data:ro \
  ghcr.io/gametec-live/geocoder-go:latest
```

The pack directory and files must be readable by UID/GID `65532`. The process
does not need to write to `/data` or elsewhere in the container filesystem.

## Kubernetes security context

Use a PVC, image volume, or init process to provide packs. A minimal container
configuration is:

```yaml
containers:
  - name: geocoder
    image: ghcr.io/gametec-live/geocoder-go:latest
    ports:
      - name: http
        containerPort: 8080
    securityContext:
      runAsNonRoot: true
      runAsUser: 65532
      runAsGroup: 65532
      allowPrivilegeEscalation: false
      readOnlyRootFilesystem: true
      capabilities:
        drop: ["ALL"]
    readinessProbe:
      httpGet: {path: /readyz, port: http}
      initialDelaySeconds: 2
      periodSeconds: 10
    startupProbe:
      httpGet: {path: /healthz, port: http}
      periodSeconds: 5
      failureThreshold: 12
    livenessProbe:
      httpGet: {path: /healthz, port: http}
      periodSeconds: 30
    volumeMounts:
      - name: packs
        mountPath: /data
        readOnly: true
```

Pin a digest or immutable version tag in production rather than relying on
`latest`.

## Startup and shutdown

Startup recursively validates every pack before listening. The process exits
non-zero if the directory contains no packs or if any discovered database has
an invalid schema or missing search index.

`SIGTERM` and `SIGINT` trigger graceful HTTP shutdown with a ten-second
deadline. The Go process is PID 1 in the scratch image and handles signals
directly.

## Health model

- `/healthz` is a liveness check and does not execute a database query.
- `/readyz` confirms startup completed and reports loaded pack names.
- Container `HEALTHCHECK` runs `/geocoder healthcheck` against `/healthz`.

Monitor actual geocode success rate and latency separately; a live process can
still receive queries absent from its source data.

## Logging

Application and Gin access logs are written to stdout/stderr. Container
platforms should collect those streams. Do not log full user search queries at
an upstream proxy unless location-data retention is intentional and documented.

## Network edge

Place the service behind a gateway that supplies:

- TLS termination.
- Authentication or network policy where required.
- Per-client rate and concurrency limits.
- Request-size and upstream timeout enforcement.
- CORS policy when called directly by browsers.
- Access-log redaction appropriate for location data.

The server itself caps HTTP headers at 1 MiB, query text at 512 Unicode
characters, and applies read/write/idle timeouts.

## Resource planning

The runtime demand-pages immutable SQLite files rather than loading whole packs.
Memory grows with active SQLite pages and operating-system file cache, so test
under the target query concurrency and storage medium.

Pack generation is materially heavier than serving. Generate packs on CI or a
development host and deploy only finalized databases to constrained devices.

## Pack updates and rollback

1. Build a new file outside the live name.
2. Run `packgen inspect` and representative API tests.
3. Drain or stop the server.
4. Rename the old and new files atomically within the same filesystem.
5. Start the server and check `/readyz`.
6. Retain the previous pack until application-level checks pass.

Because packs are opened as immutable, replacing a file underneath a running
process is unsupported even on filesystems that permit it.

## GitHub Actions and GHCR

The repository has three workflows:

- `.github/workflows/pull-request.yml` checks `gofmt`, runs `golangci-lint`,
  and executes the race-enabled unit-test suite for pull requests.
- `.github/workflows/main.yml` cross-compiles `geocoder` and `packgen` for
  Linux and Windows on amd64 and arm64. Each platform pair is uploaded as a
  GitHub Actions artifact and retained for 14 days.
- `.github/workflows/container.yml` runs tests and `go vet`, then builds
  `linux/amd64` and `linux/arm64` images. Pull requests build without
  publishing. Pushes to `main`, version tags matching `v*`, and manual
  dispatches publish to GHCR using `GITHUB_TOKEN`.

Published tags include the branch or semantic version, a `sha-...` tag, and
`latest` for the default branch. Builds include BuildKit provenance, an SBOM,
and a GitHub artifact attestation. Actions are pinned to commit SHAs.

The repository or organization must allow Actions to write packages. GHCR
package visibility is managed separately from repository visibility.
