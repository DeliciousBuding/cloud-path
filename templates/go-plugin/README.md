# CloudPath Go Plugin Templates

Official, copyable Go plugin templates for CloudPath. This directory is the
source for the future independent repository
`cloud-path-plugin-template-go`. It contains two self-contained plugin
templates, CI/Release examples, a manifest, and a stdlib-only renamer.

## Templates

| Template | Kind | Directory |
|---|---|---|
| Minimal Driver Plugin (simulated capability) | Driver | `driver/` |
| Minimal Application Plugin (capability binding) | Application | `application/` |

Each template builds and tests on its own (`go build ./...`, `go vet ./...`,
`go test ./... -count=1`) and depends only on stdlib + the public CloudPath SDK
(`sdk/go/cloudpath/v1/*`). It imports no `internal/*` package.

## End-to-end path: copy -> rename -> test -> release -> topic -> registry

1. **Copy.** Copy the `driver/` or `application/` directory into a new Git
   repository (for example `cloud-path-driver-stcb`).

2. **Rename.** Run the stdlib-only renamer inside the new repository:

   ```bash
   python scripts/rename.py --dir . \
       --plugin-id io.github.<owner>.cloud-path-driver-<name> \
       --module github.com/<owner>/cloud-path-driver-<name> \
       --binary cloudpath-driver-<name> \
       --title "<Driver Title>"
   ```

   `scripts/rename.py` rewrites the plugin id, Go module path, binary name,
   title and capability literal across file contents and renames
   `cmd/<binary>`. It rejects empty values and path-traversal inputs and never
   calls git or GitHub. `python scripts/rename.py --self-test` runs a smoke test.
   The template go.mod uses a `replace` to the local cloud-path checkout; drop
   it and pin the published `github.com/DeliciousBuding/cloud-path` version once
   the SDK is released.

3. **Test.** In the new repository:

   ```bash
   go build ./...
   go vet ./...
   go test ./... -count=1
   test -z "$(gofmt -l .)"
   python scripts/validate_manifest.py plugin.yaml --dir .
   ```

   `scripts/validate_manifest.py` asserts the manifest required fields and that
   no Go file imports `github.com/DeliciousBuding/cloud-path/internal/*`.

4. **Release.** Push a `v*` tag. `.github/workflows/release.yml` builds the
   linux/amd64, linux/arm64 and windows/amd64 binaries, writes `sha256`
   checksums, attaches `plugin.yaml`, and creates a GitHub Release. The
   checksums are verified by the Registry and by `cloudpath plugin install`.

5. **Add the topic.** Add the GitHub Topic `cloudpath-plugin` to the repository
   so it is discoverable by `cloudpath plugin search`.

6. **Submit to the Registry.** Submit the repository to the curated
   `cloud-path-registry` so `cloudpath plugin inspect` shows it as reviewed.
   The Registry records pinned version, asset digest, source and
   `verifiedPublisher`. The topic alone is not trust; the Registry entry and
   digest are.

## Guards in the core repository

- `go build ./...` / `go vet ./...` / `go test ./... -count=1` at the core root
  does not descend into these nested modules; run the per-template commands
  above to test each template.
- `python scripts/fmtcheck.py` (core) verifies formatting across templates.
- The templates are independently covered by a scan that fails if any
  `templates/go-plugin/**/*.go` file imports `internal/*`.

## References

- `docs/architecture/plugin-system.md` (protocol/manifest/runtime)
- `docs/architecture/github-ecosystem.md` (discovery, trust)
- `docs/architecture/registry.md` (Registry + CLI contract)
- `docs/architecture/repository-strategy.md` (repo naming, split gate)
- `spec/plugin-manifest.schema.json` (manifest JSON Schema)
