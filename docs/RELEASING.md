# Releasing NSS CLI

## Release configuration

[`.goreleaser.yaml`](../.goreleaser.yaml) defines CGO-free binaries for macOS and
Linux on ARM64 and x86-64. GoReleaser packages versioned tar archives containing
`nss`, the README, and the MIT license, and generates SHA-256 checksums. The
release tag supplies the version reported by `nss --version`.

[The release workflow](../.github/workflows/release.yml) uses GoReleaser v2.18.2.
Use the same version for local packaging checks. The workflow runs vet and
race-enabled tests before publishing; GoReleaser also runs `go mod tidy` and
`go test ./...` before building.

## Validate locally

Run the [development checks](../CONTRIBUTING.md#build-and-test), then:

```sh
goreleaser check
goreleaser release --snapshot --clean
```

Snapshot mode builds local artifacts without publishing a release or updating
the tap. It can validate uncommitted changes. `--clean` replaces the local
`dist/` directory, which is ignored by Git. Inspect the archives, checksums, and
generated `dist/homebrew/Casks/nss.rb` before publishing.

## Homebrew publishing credentials

Stable releases require the `HOMEBREW_TAP_TOKEN` GitHub Actions secret in
**nss-cli**. The credential must grant **Contents: read and write** access to
`naturalselectionsoftware/homebrew-tap`, and the tap's `main` branch rules must
allow direct updates by its identity. Restrict the credential to the tap and
never store it in either repository.

The automatic `GITHUB_TOKEN` is scoped to nss-cli and cannot update the tap.
The workflow checks that `HOMEBREW_TAP_TOKEN` is present for stable tags; this
does not verify its permissions or compatibility with branch rules. Users need
no GitHub credential to install from the public tap.

## Publish a tagged release

After the release changes have been reviewed, validated, and committed on the
release commit, create and push a semantic version tag. For example, replacing
`vX.Y.Z` with the intended version:

```sh
git tag vX.Y.Z
git push origin vX.Y.Z
```

Pushing a tag matching `v[0-9]*` triggers the release workflow. It uses the
repository's automatic `GITHUB_TOKEN` with `contents: write` to publish the
GitHub Release, archives, and checksums.

Stable tags also regenerate `Casks/nss.rb` in
[`naturalselectionsoftware/homebrew-tap`](https://github.com/naturalselectionsoftware/homebrew-tap)
on `main` using GoReleaser's `homebrew_casks` publisher. Prerelease tags such as
`vX.Y.Z-rc.1` create prereleases and leave the stable cask unchanged
(`skip_upload: auto`).

Check the workflow result, release assets, and tap update. Verify installation
or upgrade with Homebrew and confirm `nss --version` reports the expected version.
A tap publishing failure requires checking the credential and branch rules; do
not assume a published GitHub Release means the tap was updated successfully.

## macOS quarantine

Current macOS binaries are not yet Apple Developer ID signed or notarized.
Testing confirmed that macOS terminates the unsigned v0.1.0 binary installed by
Homebrew without quarantine removal. The cask removes `com.apple.quarantine`
from the staged `nss` binary in a macOS-only `postflight_steps` hook as a temporary
compatibility measure.

Keep the tap cask and GoReleaser's `custom_block` consistent. The template escapes
`{{staged_path}}` so GoReleaser preserves it for Homebrew to resolve at install
time. Proper Developer ID signing and notarization should replace this hook.
