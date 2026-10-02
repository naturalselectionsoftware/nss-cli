# Contributing to NSS CLI

## Requirements

Use Go 1.23 or newer. The module uses only the Go standard library, so there is
no `go.sum`. Development does not require the NSS server source repository.

## Build and test

From the repository root:

```sh
go test -race ./...
go vet ./...
go build -trimpath -o nss .
env -u NSS_TOKEN ./nss --help
env -u NSS_TOKEN ./nss events count --help
env -u NSS_TOKEN ./nss --version
```

Format Go changes with `gofmt` and check formatting with `gofmt -l .`; the check
should print no filenames. The local `nss` binary is ignored by Git.

API commands require access to an NSS deployment and an organisation automation
token. See the [configuration guide](README.md#configure). Keep credentials out
of source files, tests, and commits.

## Continuous integration

[CI](.github/workflows/ci.yml) checks formatting, runs vet and race-enabled tests,
builds the CLI, verifies help and version without credentials, and validates the
GoReleaser configuration on Linux and macOS.

For packaging validation and tagged releases, see
[the releasing guide](docs/RELEASING.md).
