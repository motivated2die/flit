# Releasing flit

How versions, tags and releases work, step by step. Building in general is in [BUILDING.md](BUILDING.md); why it works this way is [decisions/0010](decisions/0010-updates-from-github-releases.md).

## The idea in one picture

```
 branch ──PR──▶ main ──(test locally)──▶ git tag v0.2.0 ──push──▶ GitHub Actions
                 │                                                 │ build: engine, vet, test, exe
               CI checks                                           │ sign: (later, SignPath)
               every change                                        ▼
                                                              DRAFT release with flit.exe
                                                                           │ you review it
                                                                           ▼
                                                     Published release: installed copies offer it
```

- **`main`** always holds the latest finished work. Changes reach it through **pull requests** from a branch; CI ([ci.yml](../.github/workflows/ci.yml)) checks every pull request and every push to `main`: formatting, vet, the tests (with the real engine and the small test models), govulncheck and a build. `main` takes no direct pushes: the ruleset below lets a commit in only once CI has passed on it.
- **Nothing is released automatically from `main`.** You decide which commit becomes a release by putting a **version tag** on it.
- **A tag** is a permanent name for one commit: `v0.2.0` → commit `3f37898`. Unlike a branch, it never moves. Pushing a tag that looks like a version starts the release workflow ([release.yml](../.github/workflows/release.yml)).
- The workflow creates a **draft** release. Drafts are visible only to you. The release becomes public, and **installed copies of flit start offering it** (*Update available* at the bottom of the sidebar), only when you click **Publish**.

## Version numbers

flit uses [semantic versioning](https://semver.org): `MAJOR.MINOR.PATCH`, and the tag is the version with a `v` in front.

| Change since the last release | Bump | Example |
|---|---|---|
| Only bug fixes | PATCH | 0.2.0 → 0.2.1 |
| New features (settings, recordings and transcripts stay compatible) | MINOR | 0.2.1 → 0.3.0 |
| Incompatible changes; or "1.0, it's stable now" | MAJOR | 0.9.3 → 1.0.0 |

- **Pre-releases** add a suffix: `v0.3.0-rc.1`, `v0.3.0-rc.2`, then `v0.3.0`. They become GitHub pre-releases, which the "latest release" link and the in-app update skip, so you can hand them to testers safely.
- **A version is used once.** Once a release is published, never publish different files under its number; release the next PATCH instead.
- The version in the source (`Version = "X.Y.Z-dev"` in `internal/app/app_windows.go`, and `winres/winres.json`) is only the default for development builds. Release builds take the version from the tag. You don't edit any file to make a release. After a release, raising the dev version (to `0.2.0-dev`) is optional and only changes what dev builds call themselves.

## One-time setup on GitHub

Done once per repository. Repository: `https://github.com/motivated2die/flit` (public).

1. **Actions** (Settings → Actions → General): allow GitHub Actions. *Workflow permissions* stay at "Read repository contents and packages permissions"; the release workflow asks for write access itself where it needs it.
2. **Protect `main`** (Settings → Rules → Rulesets → New ruleset → New branch ruleset):
   - Name `main`, Enforcement: Active, Target branches: *Include default branch*.
   - Rules: *Restrict deletions*, *Block force pushes*, *Require status checks to pass* → add `test` (the CI job; it can be picked once CI has run once).
   - Required status checks also refuse **direct pushes** to `main`: a commit must have passed `test` on another branch first, which is what a pull request does (see [Everyday changes](#everyday-changes)). *Require a pull request before merging* is not needed on top; add it once others contribute.
3. **Protect version tags** (New ruleset → New tag ruleset): name `versions`, target *Include by pattern* `v*`, rules *Restrict updates* and *Restrict deletions*. A published version then can't be moved or removed by accident. Add *Repository admin* to the bypass list, to clean up a failed attempt (see [Fixing mistakes](#fixing-mistakes)).
4. **Security** (Settings → Advanced Security, "Code security" on older layouts): enable *Private vulnerability reporting* ([SECURITY.md](../SECURITY.md) points there), *Dependabot alerts* and *Dependabot security updates*. [dependabot.yml](../.github/dependabot.yml) adds weekly version updates for the actions and Go modules.
5. **Repository details** (the ⚙ next to *About* on the main page): description "Meeting transcripts on your Windows PC: records both sides, transcribes locally", topics such as `windows`, `transcription`, `speech-to-text`, `meetings`, `go`. Untick *Packages* and *Deployments* if you like; tick *Releases*.
6. **Two-factor authentication** on your GitHub account (also required for code signing later).

## Everyday changes

Work on a branch, open a pull request, merge it when CI is green:

```powershell
git switch -c fix/notch-position          # a new branch from main
# ... change, gofmt, go vet, go test ...
git commit -am "fix(notch): ..."
git push -u origin fix/notch-position
gh pr create --fill                       # or the "Compare & pull request" button on GitHub
gh pr checks --watch                      # wait for the green check
gh pr merge --squash --delete-branch      # or "Squash and merge" on GitHub
git switch main; git pull
```

Dependabot opens its pull requests the same way; merge them when CI passes.

## Making a release

Example: releasing 0.2.0. Commands are for PowerShell in the repository folder.

### 1. Get `main` ready

Everything that belongs in the release is merged into `main` and CI has passed (the green check next to the latest commit on GitHub). Then:

```powershell
git switch main
git pull
git status            # must say "nothing to commit, working tree clean"
```

### 2. Build and test locally

```powershell
.\scripts\build-release.ps1 -Version 0.2.0
```

This fetches the engine, runs vet and the tests, and puts `flit.exe` and `SHA256SUMS.txt` into `Desktop\flit-0.2.0`. If it warns about uncommitted changes, you're not testing exactly what you'll release: commit or discard them first.

Go through the [release checklist](#release-checklist) with this exe. Found a problem? Fix it on `main` and start again at step 1. Nothing has been published yet.

### 3. Tag the commit and push the tag

```powershell
git log -1 --oneline                         # the commit you just tested
git tag -a v0.2.0 -m "flit 0.2.0"            # an annotated tag with a message
git push origin v0.2.0                       # pushing the tag starts the release workflow
```

`git push` alone doesn't push tags, so you push it by name. The workflow refuses tags whose commit is not on `main`, and tags that don't look like `v1.2.3` or `v1.2.3-rc.1`.

### 4. Watch the workflow

On GitHub: **Actions** → **Release** → the run named after your tag (or `gh run watch` in the terminal). It takes about ten minutes:

| Job | What it does |
|---|---|
| `build` | Checks the tag, restores the engine and test models from CI's cache, installs go-winres, runs the same `scripts\build-release.ps1` as you did in step 2, and uploads the unsigned exe as a workflow artifact |
| `sign` | Skipped until code signing is set up ([below](#code-signing-later)) |
| `release` | Computes `SHA256SUMS.txt`, attests the build's provenance, writes the release notes from the commit messages since the last release, and creates the **draft** release |

If a job fails, open it to read the log, and see [Fixing mistakes](#fixing-mistakes).

### 5. Review and publish the draft

**Releases** (right side of the repository's main page) → the draft "flit 0.2.0":

1. Read the notes. They list every commit since the previous release: tidy them up here, e.g. a short summary of what's new at the top. The first release gets a one-paragraph introduction instead.
2. Optionally download `flit.exe`, run it on a test PC, and check that its hash matches the notes.
3. **Publish release.**

Done: the release is public and `https://github.com/motivated2die/flit/releases/latest` points at it (not for pre-releases). Installed copies show *Update available* in the sidebar at their next start or within 12 hours. **Publishing is what ships an update to every user**, so only publish an exe you have tested.

### Release candidates

To have a version tested before it's final, tag `v0.2.0-rc.1` in step 3 instead. The draft is marked *pre-release*; publish it and share its link with testers. Fixes go to `main` and become `v0.2.0-rc.2`. When everything is fine, tag the last tested commit `v0.2.0` (a commit can carry several tags). The notes of the final release cover everything since the previous final release.

## Fixing mistakes

| Situation | What to do |
|---|---|
| The workflow failed (e.g. a test), nothing was published | Fix it on `main`. Delete the tag (below) and the draft release if one exists, then tag the fixed commit with the **same** version |
| The workflow failed for a passing reason (GitHub outage, a download timed out) | Actions → the failed run → **Re-run failed jobs**. A re-run replaces the draft's files |
| You tagged the wrong commit, nothing published yet | Delete the tag (below) and tag the right commit |
| A **published** release has a bug | Don't change or delete its tag. Fix it on `main` and release the next PATCH. If the bug is serious, edit the bad release and mark it as pre-release, so "latest" (and the in-app update) falls back to the previous one |

Deleting a tag, locally and on GitHub (needs the bypass from the tag ruleset):

```powershell
git tag -d v0.2.0
git push origin --delete v0.2.0
```

Always create tags with `git tag` as above, not on GitHub's "Draft a new release" page: a release created there gets no exe, because the release workflow makes its own.

## Verifying a release

Anyone can check that a downloaded `flit.exe` is the one published:

```powershell
(Get-FileHash .\flit.exe -Algorithm SHA256).Hash                  # compare with the notes / SHA256SUMS.txt
gh attestation verify .\flit.exe --repo motivated2die/flit         # built by this repository's release workflow
```

The attestation proves the exe was built by the release workflow from the tagged commit, not uploaded by hand. Your local build and the workflow's use the same script and the same Go release (`toolchain` in `go.mod`), but don't expect identical hashes: the workflow's exe is the one that's published.

## Code signing (later)

The release workflow already has a `sign` job for [SignPath Foundation](https://signpath.org) code signing, free for open-source projects. It is skipped while the repository variable `SIGNPATH_ORGANIZATION_ID` doesn't exist. Once the project is accepted:

1. **In SignPath** (with the account SignPath creates):
   - Add the predefined *GitHub.com* trusted build system and link it to the project. Install the SignPath GitHub App on the repository if asked.
   - Create an artifact configuration. The workflow uploads the exe as a zipped artifact, so its root is a zip file:
     ```xml
     <?xml version="1.0" encoding="utf-8"?>
     <artifact-configuration xmlns="http://signpath.io/artifact-configuration/v1">
       <zip-file>
         <pe-file path="flit.exe" product-name="flit">
           <authenticode-sign />
         </pe-file>
       </zip-file>
     </artifact-configuration>
     ```
   - Note the organization ID, the project slug and the signing policy slug (usually `release-signing`). Create an API token for a CI user with submitter rights.
2. **In GitHub** (Settings → Secrets and variables → Actions):
   - Secret `SIGNPATH_API_TOKEN`: the API token.
   - Variables `SIGNPATH_ORGANIZATION_ID`, `SIGNPATH_PROJECT_SLUG`, `SIGNPATH_SIGNING_POLICY_SLUG`.
3. **README**: replace the "not code-signed" sentence with SignPath's required wording: "Free code signing provided by [SignPath.io](https://about.signpath.io), certificate by [SignPath Foundation](https://signpath.org)".

From then on every release waits in the `sign` job until an approver approves the signing request in SignPath, and the draft gets the signed exe. The release notes drop the SmartScreen hint by themselves.

## Release checklist

Before tagging (step 2), with the locally built exe, ideally on a second Windows user account or a VM (an install there doesn't touch yours):

1. `.\scripts\build-release.ps1 -Version X.Y.Z` passes without warnings.
2. **Install**: run `flit.exe` from the Desktop folder. It installs to `%LOCALAPPDATA%\Programs\flit`, the notch says hello, Start Menu and Installed apps show the new version.
3. **Setup**: the Welcome page; the models download (or copy them in, [BUILDING.md](BUILDING.md#trying-an-installed-build)).
4. **A meeting**: a short call in Teams or Zoom (or Start recording by hand); the transcript appears, both sides, speakers.
5. **Update over the previous release**: recordings and settings are kept, and "flit was updated" is shown. Also the **in-app update**: with the previous version installed, run `go run ./cmd/flitdev release-server -exe <this flit.exe> -version X.Y.Z`, start the installed flit with `FLIT_UPDATE_API` set ([BUILDING.md](BUILDING.md#testing-updates)), and update from *Update available*; once while a recording runs (it waits for the recording to stop).
6. **Uninstall** (Settings → Uninstall…), keeping and deleting the data.

After publishing:

7. The release page shows `flit.exe` and `SHA256SUMS.txt`, and `releases/latest` points at it (not for pre-releases).
8. An installed previous version shows *Update available* (Settings → App → *Check now*) and updates to it.
