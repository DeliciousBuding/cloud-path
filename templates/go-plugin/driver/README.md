# CloudPath Driver Plugin Template

A minimal, copyable Driver Plugin for CloudPath. It implements the Driver
Protocol v1 (`sdk/go/cloudpath/v1/driver`) with a fully simulated capability:
it never touches real hardware. It is the canonical reference for building an
external Driver Plugin using only the public SDK and the standard library.

## What it implements

The `plugin` package implements `driver.DriverServer`:

- `Initialize` / `Describe` / `Health` / `Shutdown`
- `ConfigureInstance` / `Discover` / `OpenDevice` / `CloseDevice`
- `Watch` (publishes `DeviceUpsert`, `EntityUpsert` and simulated temperature
  `Observation` messages)
- `Execute` (whitelisted `read` action, returns `SUCCEEDED`)

`main.go` (in `cmd/cloudpath-driver-template`) reads the launch identity from
the environment, prints the CloudPath handshake line, then serves the protocol
over a loopback TCP transport.

## Layout

```text
driver/
  go.mod                 module github.com/DeliciousBuding/cloud-path-plugin-template-go/driver
  plugin.yaml            Driver manifest (schema: spec/plugin-manifest.schema.json)
  config.example.json    per-instance configuration example
  plugin/
    driver_plugin.go     DriverServer implementation (simulated capability)
    driver_plugin_test.go
  cmd/
    cloudpath-driver-template/
      main.go            handshake + serve
  nettransport/          reference transport.Transport over net.Conn
  .github/workflows/     ci.yml + release.yml
  scripts/               rename.py + validate_manifest.py
```

## Copy, rename, build, test

1. Copy the `driver/` directory into your new plugin repository.
2. Rename the template values:

   ```bash
   python scripts/rename.py --dir .        --plugin-id io.github.<owner>.cloud-path-driver-<name>        --module github.com/<owner>/cloud-path-driver-<name>        --binary cloudpath-driver-<name>        --title "Your Driver Title"
   ```

   `scripts/rename.py` rewrites the plugin id, Go module path, binary name and
   title across file contents, and renames `cmd/<binary>`. It rejects empty
   values and path-traversal inputs. It never invokes git or GitHub.

3. Build and test:

   ```bash
   go build ./...
   go vet ./...
   go test ./... -count=1
   test -z "$(gofmt -l .)"
   python scripts/validate_manifest.py plugin.yaml --dir .
   ```

   The `go.mod` ships with a `replace` pointing at the local cloud-path
   checkout so it builds in the monorepo. Once the public
   `github.com/DeliciousBuding/cloud-path` module is published, remove the
   `replace` and pin the released version.

## Permissions

This template driver is simulated, so its `plugin.yaml` declares no hardware
and no network access. A real driver must list exactly the hardware/network it
uses (for example `serial`, `usb`, `loopback-tcp`) and keep that list identical
between the manifest and what `Describe` reports. The permission surface is
disclosed at install time and must never grow silently in a patch release.

## Compatibility

- Protocol: Driver Protocol v1 (`protocol: 1`)
- Capability: `cloudpath.dev/capability/drivertemplate@1` (bump to `@2` only on
  a breaking change; never mutate `@1` in place)
- Core: `>=0.2.0 <0.4.0` (adjust to your tested range)

## Release

Push a `v*` tag; `.github/workflows/release.yml` builds linux/amd64,
linux/arm64 and windows/amd64 binaries, writes a `sha256` checksum for each,
attaches `plugin.yaml`, and creates a GitHub Release. The checksums are what
the CloudPath Registry and `cloudpath plugin install` verify before executing
the binary.

## Publish to the ecosystem

After a release:

1. Add the GitHub Topic `cloudpath-plugin` to the repository so it is
   discoverable by `cloudpath plugin search`.
2. Submit the repository to the curated Registry (`cloud-path-registry`) so it
   is shown by `cloudpath plugin inspect` as a reviewed plugin; the Registry
   records the pinned version, asset digest, source and verified publisher.
3. Never treat the Topic as trust: the Registry entry, digest and
   `verifiedPublisher` are what establish trust.

See `docs/architecture/{plugin-system,github-ecosystem,registry}.md` in the
cloud-path core repository for the contracts.
