# NSS CLI

`nss` is a command-line client for Natural Selection Software (NSS) operational
event APIs. Search, count, group, and investigate events from your terminal or
scripts using an organisation automation token. Prebuilt binaries have no runtime
dependencies.

## Install

Install with Homebrew:

```sh
brew install naturalselectionsoftware/tap/nss
nss --version
```

Homebrew 7 or newer is required. To upgrade:

```sh
brew upgrade --cask naturalselectionsoftware/tap/nss
```

### Manual archive installation

Download the archive for your platform and the matching checksums file from
[GitHub Releases](https://github.com/naturalselectionsoftware/nss-cli/releases).
Prebuilt binaries do not require Go or access to the SaaS source repository.
Archives contain `nss`, this README, and the MIT license.

| Platform | Archive suffix |
| --- | --- |
| macOS Apple Silicon | `darwin_arm64.tar.gz` |
| macOS Intel | `darwin_amd64.tar.gz` |
| Linux x86-64 | `linux_amd64.tar.gz` |
| Linux ARM64 | `linux_arm64.tar.gz` |

For example, for v0.1.0 on macOS Apple Silicon, download
`nss_0.1.0_darwin_arm64.tar.gz` and `nss_0.1.0_checksums.txt` into the same directory.
On macOS verify and install with:

```sh
shasum -a 256 --ignore-missing -c nss_0.1.0_checksums.txt
tar -xzf nss_0.1.0_darwin_arm64.tar.gz nss
mkdir -p "$HOME/.local/bin"
install -m 755 nss "$HOME/.local/bin/nss"
export PATH="$HOME/.local/bin:$PATH"
nss --version
nss --help
```

On Linux use your platform's archive and `sha256sum --ignore-missing -c` instead
of `shasum -a 256 --ignore-missing -c`. Add the PATH setting to your shell profile
if needed. To update, repeat these steps with the desired release and compare
`nss --version`.

## Configure

Ask an NSS organisation administrator to create an **organisation automation
token** with the required scopes (`events:read` for the event commands below).
Copy the token when issued and store it securely. An event ingestion key is not
an automation token.

Set the hosted API URL and your automation token, then verify the organisation
and scopes:

```sh
export NSS_URL='https://api.naturalselectionsoftware.com'
export NSS_TOKEN='nss_at_...'
nss whoami --json
```

`nss_at_...` is a placeholder. Avoid putting real long-lived tokens in scripts,
documentation, shell history, or source control. For interactive use in bash or
zsh, enter the token without echoing it or saving it in shell history:

```sh
export NSS_URL='https://api.naturalselectionsoftware.com'
read -r -s NSS_TOKEN
export NSS_TOKEN
nss whoami --json
```

`https://api.naturalselectionsoftware.com` is the normal hosted NSS API endpoint.
Self-hosted and development deployments may override `NSS_URL`. The CLI's default,
`http://localhost:8080`, is only for local development. Global `--url URL`
overrides `NSS_URL`. Use HTTPS for remote deployments.

`NSS_TOKEN` is required for API operations and is accepted only from the
environment. The CLI redacts its value from output, including echoed responses
and errors. Help and version commands work without a token or network access.

## Investigate events

Discover the organisation's vocabulary, then investigate errors in a fixed time
window (replace the dates and source with values from your deployment):

```sh
nss whoami --json
nss catalog --json
nss semantics --terms checkout,failed --json
nss events count --query 'source:checkout AND severity:ERROR' --from 2026-10-01T00:00:00Z --to 2026-10-02T00:00:00Z --json
nss events search --query 'source:checkout AND severity:ERROR' --from 2026-10-01T00:00:00Z --to 2026-10-02T00:00:00Z --limit 20 --json
nss events aggregate --query 'severity:ERROR' --group-by source --from 2026-10-01T00:00:00Z --to 2026-10-02T00:00:00Z --json
nss events series --query 'severity:ERROR' --bucket HOUR --from 2026-10-01T00:00:00Z --to 2026-10-02T00:00:00Z --json
```

Retrieve one event with `nss events get EVENT_ID --json`.

### Pagination and snapshots

Use the returned `continuationCursor` with `events search --cursor` for the next
page, keeping the same query and time bounds. The cursor preserves the search
snapshot. Count, aggregate, and series accept `--snapshot` from a prior response
for deterministic follow-up operations.

### EQL examples

Queries use EQL v1 field predicates with `AND`, `OR`, `NOT`, and parentheses:

| Query | Finds |
| --- | --- |
| `type:"entry-completed"` | Events of a specific type |
| `source:checkout AND severity:ERROR` | Errors from checkout |
| `(severity:ERROR OR severity:WARN) AND NOT source:test` | Warnings and errors outside the test source |
| `correlation:"request-123"` | Events associated with a correlation ID |

Fields include `source`, `type`, `severity`, `message`, `tag`, `correlation`,
`occurred`, and `received`; use RFC 3339 values for timestamps. Source/type
equality is case-insensitive. `--from`/`--to` bound event occurrence time. See [Command help](#command-help) for exact flags
and examples.

## Output and exit codes

`--json` may appear anywhere and emits JSON for scripts/agents. Errors go to stderr
and use a JSON error object when `--json` is enabled. Help remains human-readable.

| Exit code | Meaning |
| --- | --- |
| `0` | Success |
| `3` | Client, usage, or other API errors (including HTTP 4xx except 401/403) |
| `4` | Authentication or authorization failure (HTTP 401/403) |
| `5` | Server failure (HTTP 5xx) |

## Command help

Run `nss --help` for the command overview or `nss <command> --help` for flags and
examples, such as `nss events search --help`. Help and version commands require
neither credentials nor network access.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for local development and checks, and
[docs/RELEASING.md](docs/RELEASING.md) for release maintenance.

## License

Licensed under the [MIT license](LICENSE).
