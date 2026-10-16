# Verifying a bardraw download

bardraw installs a keyboard/mouse hook and sends input to other programs, so
you should know exactly what you are running. Every release file is built by
GitHub Actions from the public source code. You can check that yourself in
three independent ways, from quickest to most thorough. Replace `OWNER/REPO`
with this repository (e.g. `someone/bardraw`) and `v1.0.0` with the version
you downloaded.

The GitHub CLI (`gh`) is needed for checks 1 and 2: <https://cli.github.com>
(on Windows: `winget install GitHub.cli`). Run `gh auth login` once.

## 1. The file was built by this repository's GitHub Actions

```sh
gh attestation verify bardraw-v1.0.0-windows-amd64.zip --repo OWNER/REPO
```

A successful result looks like

```
✓ Verification succeeded!
- Attestation #1
  - Build repo:..... OWNER/REPO
  - Build workflow:. .github/workflows/release.yml@refs/tags/v1.0.0
  - Signer repo:.... OWNER/REPO
```

The attestation is a signed statement, logged in the public Sigstore
transparency log. It says: *this exact file (by SHA-256) was produced by
`release.yml`, running in GitHub Actions on commit X of OWNER/REPO, triggered
by tag v1.0.0.* Nobody can forge it without control of the repository's
workflow, and the maintainer's own computer is never involved in building.

To see the commit it was built from, add `--format json` and look for
`buildDefinition.resolvedDependencies[].digest.gitCommit`, or open the
attestation linked from the repository's *Actions → Attestations* page.

## 2. The file is exactly what the release published, and the release can't change

```sh
gh release verify v1.0.0 --repo OWNER/REPO
gh release verify-asset v1.0.0 bardraw-v1.0.0-windows-amd64.zip --repo OWNER/REPO
```

The release is **immutable**: its tag can't be moved to other code and its
files can't be swapped after publishing. On the release page you'll see an
**Immutable** label under the author.

Without `gh`, compare the SHA-256 with `SHA256SUMS.txt` from the release:

```powershell
Get-FileHash .\bardraw-v1.0.0-windows-amd64.zip -Algorithm SHA256   # Windows
```
```sh
sha256sum -c SHA256SUMS.txt --ignore-missing                        # Linux
```

(`SHA256SUMS.txt` is itself attested and part of the immutable release.)

## 3. Rebuild it yourself: same source, same bytes

The builds are reproducible: anyone building the tagged source with the same
Go version gets byte-identical binaries and archives.

```sh
git clone https://github.com/OWNER/REPO bardraw && cd bardraw
git checkout v1.0.0
# Go 1.24.7 exactly: https://go.dev/dl/
export CGO_ENABLED=0 GOTOOLCHAIN=local
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w -H=windowsgui" -o bardraw.exe .
sha256sum bardraw.exe
```

The hash must equal the line for `bardraw-v1.0.0-windows-amd64.exe` in
`SHA256SUMS.txt` (that file is the same `bardraw.exe` that is inside the zip).
On Windows PowerShell use `$env:CGO_ENABLED="0"; $env:GOOS="windows"; $env:GOARCH="amd64"`
and `Get-FileHash`. For `svgplan` use
`go build -trimpath -ldflags "-s -w" -o svgplan ./cmd/svgplan` with the
matching `GOOS`/`GOARCH`. The exact commands used for the release are in
`.github/workflows/release.yml`.

Every binary also records the commit it was built from:

```sh
go version -m bardraw.exe     # shows vcs.revision=<commit> and vcs.modified=false
```

## What these checks do and don't tell you

They prove the file comes, unmodified, from this repository's public code at
the tagged commit, built on GitHub's machines. They don't prove the code is
harmless. For that, read it: the input-sending parts are in
`internal/winput/` and `exec_windows.go`. The exe is not code-signed with a
paid certificate, so Windows SmartScreen may warn on first start
(*More info → Run anyway*). The attestation above is the replacement for that
signature.
