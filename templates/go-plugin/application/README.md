# CloudPath Application Plugin Template

A minimal, copyable Application Plugin for CloudPath. It implements the
Application Protocol v1 (`sdk/go/cloudpath/v1/application`) and binds to a
single Capability by semantic id  -  never by a Driver id. `HandleEvents`
translates inbound events into the closed, Core-approved `ApplicationEffect`
set. It opens no Core database and holds no global token.

## What it implements

The `plugin` package implements `application.ApplicationServer`:

- `Initialize` / `Describe` / `ConfigureInstance` / `Health` / `Shutdown`
- `ValidateBinding` (checks each binding satisfies the declared requirement)
- `HandleEvents` (bidi stream: event in -> safe `ApplicationEffect` out)
- `HandleRequest` (plugin HTTP subroute, scoped by Core)
- `RunJob`

`main.go` (in `cmd/cloudpath-app-template`) uses the shared
`sdk/go/pluginmain` entrypoint: it reads and validates the launch identity from
the environment, prints the single CloudPath handshake line, dials the host's
loopback endpoint and serves the Application Protocol v1 over that
authenticated transport.

## Layout

```text
application/
  go.mod                 module github.com/DeliciousBuding/cloud-path-plugin-template-go/application
  plugin.yaml            Application manifest (schema: spec/plugin-manifest.schema.json)
  config.example.json    per-instance configuration example
  plugin/
    application_plugin.go  ApplicationServer implementation
    application_plugin_test.go
  cmd/
    cloudpath-app-template/
      main.go            handshake + serve
  nettransport/          reference transport.Transport over net.Conn
  .github/workflows/     ci.yml + release.yml
  scripts/               rename.py + validate_manifest.py
```

## Copy, rename, build, test

1. Copy the `application/` directory into your new plugin repository.
2. Rename the template values:

   ```bash
   python scripts/rename.py --dir .        --plugin-id io.github.<owner>.cloud-path-app-<name>        --module github.com/<owner>/cloud-path-app-<name>        --binary cloudpath-app-<name>        --title "Your Application Title"
   ```

3. Build and test:

   ```bash
   go build ./...
   go vet ./...
   go test ./... -count=1
   test -z "$(gofmt -l .)"
   python scripts/validate_manifest.py plugin.yaml --dir .
   ```

The `go.mod` ships with a `replace` pointing at the local cloud-path checkout
so it builds in the monorepo. Once the public
`github.com/DeliciousBuding/cloud-path` module is published, remove the
`replace` and pin the released version.

## Binding model

The application declares a `RequirementDescriptor` in `Describe` and mirrors it
in `plugin.yaml` `requirements`. It binds to a Capability, not a Driver, so it
is unaffected by which driver produces the data. `ValidateBinding` enforces
that at least one entity is bound to the requirement.

## Permissions

The template application requests only `plugin-data` filesystem access. It
never opens Core SQLite and never requests a global token. Plugin HTTP
requests arrive with tenant/actor/instance/scope context injected by Core and
are routed under `/api/plugins/{plugin_id}/instances/{instance_id}/...`.
Permission growth must never be silent in a patch release.

## Compatibility

- Protocol: Application Protocol v1 (`protocol: 1`)
- Capability: `cloudpath.dev/capability/drivertemplate@1`
- Core: `>=0.2.0 <0.4.0` (adjust to your tested range)

## Release

Push a `v*` tag; `.github/workflows/release.yml` builds linux/amd64,
linux/arm64 and windows/amd64 binaries, writes a `sha256` checksum for each,
attaches `plugin.yaml`, and creates a GitHub Release.

## Publish to the ecosystem

After a release:

1. Add the GitHub Topic `cloudpath-plugin` to the repository.
2. Submit the repository to the curated Registry (`cloud-path-registry`) so it
   is shown by `cloudpath plugin inspect` as a reviewed plugin.

See `docs/architecture/{plugin-system,github-ecosystem,registry}.md` in the
cloud-path core repository for the contracts.
