# Releasing bardraw (maintainer guide)

Two workflows live in `.github/workflows/`:

| workflow | runs on | does |
|---|---|---|
| `ci.yml` | every push to `main`, every pull request, manual | `go mod verify`, gofmt, `go vet`, `go test` on Linux **and** Windows, and builds every release target |
| `release.yml` | pushing a tag `vX.Y.Z` | runs CI, builds reproducibly, signs a provenance attestation, creates a draft release, attaches the files, publishes |

Release files:

| file | contents |
|---|---|
| `bardraw-vX.Y.Z-windows-amd64.zip` / `-arm64.zip` | `bardraw.exe` (the overlay), `svgplan.exe`, README |
| `bardraw-vX.Y.Z-windows-amd64.exe` / `-arm64.exe` | the overlay alone (same bytes as inside the zip) |
| `svgplan-vX.Y.Z-linux-amd64.tar.gz` / `-arm64.tar.gz` | `svgplan`, the planner and preview tool (the overlay itself is Windows-only) |
| `SHA256SUMS.txt` | SHA-256 of every file above |

## One-time setup

Do these once, in the repository on github.com.

1. **Commit the files.** Copy `.github/`, `docs/` and `.gitignore` into the repository root and push to `main`. The *Actions* tab should show a green **CI** run. If your default branch is not `main`, change `branches: [main]` in `ci.yml`.

2. **Allow Actions to create releases.** *Settings → Actions → General → Workflow permissions*: leave the default at **Read repository contents** (the release job asks for write access itself). If your organisation restricts which actions may run, allow `actions/*` (the workflows only use GitHub's own actions, pinned to commit SHAs).

3. **Turn on immutable releases.** Repository *Settings* (General page) → scroll to **Releases** → ✅ **Enable release immutability**. (For an organisation, it can be enforced for all repositories under *Org settings → Repository → General → Releases*.) From then on, a published release cannot be changed:
   * the tag stays on its commit; it can't be moved, and while the release exists it can't be deleted;
   * attached files can't be replaced, added or removed;
   * GitHub automatically creates a signed *release attestation* (tag + commit + file digests).

   Only the title and release notes stay editable. The workflow builds everything as a draft and publishes only when all files are attached, which is exactly the flow immutability expects. Immutability applies only to releases published after you enable it.

4. **Protect the release tags** (recommended). *Settings → Rules → Rulesets → New ruleset → **New tag ruleset***:
   * Target: *Include by pattern* `v*`
   * Rules: ✅ *Restrict creations*, ✅ *Restrict updates*, ✅ *Restrict deletions*
   * Bypass list: yourself (Repository admin)

   Now only you can create version tags, so only you can trigger a release.

5. **Protect `main`** (recommended). Add a *branch ruleset* for `main` with *Require status checks to pass* and select the **Test (ubuntu-latest)**, **Test (windows-latest)** and **Build all release targets** checks. The checks appear in the list after CI has run once.

6. **Optional manual approval.** The publish job uses an environment called `release`. GitHub creates it automatically on the first release. To approve every release by hand, open *Settings → Environments → release* and add yourself under *Required reviewers*. You can also limit it to tags `v*` (*Deployment branches and tags*).

7. **Add a licence** (if you haven't yet). If a `LICENSE` file exists, the release archives include it. Without one, people may legally use the code only as GitHub's terms allow.

## Making a release

```sh
git switch main && git pull
git tag -a v1.0.0 -m "bardraw v1.0.0"
git push origin v1.0.0
```

(Or create the tag with GitHub's web UI: *Releases → Draft a new release → Choose a tag → create `v1.0.0`*. **Don't** publish that draft yourself; just create the tag and discard the draft, so the workflow can create the release.)

Then watch *Actions → Release*:

1. **ci**: the same checks as `ci.yml`. If anything fails, nothing is published.
2. **build**: cross-compiles all targets on Ubuntu with Go 1.24.7, `CGO_ENABLED=0`, `-trimpath`, and fixed archive timestamps, then writes `SHA256SUMS.txt`.
3. **publish**: signs a [SLSA build provenance](https://slsa.dev/provenance) attestation for every file (keyless, through Sigstore and GitHub's OIDC token: no secrets to manage), creates the release as a draft with auto-generated notes plus verification instructions, uploads the files and publishes it. The last step runs `gh release verify` and warns if immutability is not enabled.

The tag must look like `vMAJOR.MINOR.PATCH` (pre-releases such as `v1.1.0-rc.1` work too). A tag whose release already exists fails fast, because immutable releases can't be overwritten. **Fixing a bad release means tagging a new version.** You can still delete a broken release, but its tag name can never be reused.

## Keeping it healthy

* **Dependabot** (`.github/dependabot.yml`) opens monthly PRs that bump the pinned action SHAs and Go modules. CI checks them; you merge them.
* **Go version.** The exact version is set in three places: `GO_VERSION` in `ci.yml` and `release.yml`, and in `docs/VERIFY.md`. Change all three together; reproducible builds depend on it.
* **Why actions are pinned to SHAs.** A tag like `@v4` can be moved by whoever controls that repository; a commit SHA can't. The comment after each SHA shows the version.
* **No build cache.** `setup-go` caching is off on purpose, so nothing from an earlier run (e.g. from a pull request) can influence a release build.

## Troubleshooting

| symptom | fix |
|---|---|
| `Resource not accessible by integration` when creating the release | Organisation policy blocks write tokens: *Org settings → Actions → General → Workflow permissions* must allow the job's `contents: write`. |
| Attestation step fails on a **private** repo | Artifact attestations for private repositories need GitHub Enterprise Cloud. Public repositories work on every plan. |
| `checksum mismatch` / `SECURITY ERROR` in `go mod verify` | `go.sum` disagrees with the checksum database. Run `go mod tidy` locally with the normal Go proxy, then commit `go.sum`. |
| Warning "Release is NOT immutable" | Step 3 of the setup wasn't done before publishing (it doesn't apply retroactively). Enable it; future releases will be immutable. |
| Workflow didn't start | The tag must match `v*.*.*`. Tags pushed by another workflow using `GITHUB_TOKEN` don't trigger workflows; push the tag yourself. |
