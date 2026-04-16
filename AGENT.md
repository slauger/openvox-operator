# Agent Instructions

## Git Workflow

- **Development branch:** `develop`
- **Release branch:** `main` (only receives merges from `develop` via auto-PR)
- **Always branch from `origin/develop`**, never from `main`
- **PRs target `develop`** (`--base develop`)
- Branch naming: `feat/<topic>` or `fix/<topic>`

```bash
git fetch origin develop
git checkout -b feat/my-feature origin/develop
# ... work ...
gh pr create --base develop
```

## Merge Strategy

- **Feature/fix PRs into `develop`:** Use standard merge by default. Use squash only when the branch has many noisy commits that logically belong together (e.g. lots of trial-and-error or CI fix-ups).
- **`develop` into `main`:** Always rebase, so that `develop` and `main` stay identical after merge.

## Commit Messages

Follow [Conventional Commits](https://www.conventionalcommits.org/). Semantic-release on `main` uses these to determine version bumps.

```
feat: add NetworkPolicy support for Server and Database
fix: use FQDN for Database status.URL
docs: update README for CNPG setup
ci: add openvox-db to test images workflow
chore: update dependencies
refactor: simplify reconcile loop
test: add unit tests for helpers
```

- Lowercase subject, no trailing period
- `feat:` triggers a minor version bump
- `fix:` triggers a patch version bump
- Append `BREAKING CHANGE:` in the body for major bumps

## Pull Requests

- Title follows conventional commit format
- Keep the title short (under 72 chars)
- Body should include a `## Summary` with bullet points and a `## Test plan`
- Reference related issues with `Closes #<number>`
- PRs always target `develop`, not `main`

## Build & Test

```bash
make generate manifests   # regenerate deepcopy + CRD YAML + Helm CRDs
go build ./...
go vet ./...
go test ./internal/controller/ -v
make ci                   # full CI: lint + test + helm-lint + check-manifests
```

Always run `make generate manifests` after modifying `api/v1alpha1/*_types.go`.
Always run `make check-manifests` before committing to verify generated files are up to date.

## Project Structure

```
api/v1alpha1/              CRD type definitions (Go structs)
internal/controller/       Reconcilers, deployment builders, rendering, tests
config/crd/bases/          Generated CRD YAML
charts/openvox-operator/   Helm chart (CRDs copied from config/crd/bases/)
charts/openvox-stack/      Example stack chart
images/                    Containerfiles (openvox-server, openvox-db, openvox-operator)
tests/e2e/                 Chainsaw E2E tests
cmd/                       Entrypoints (operator, ENC, autosign, report, mock)
docs/                      Documentation
```

## Code Conventions

- Shared API types (e.g. `PDBSpec`, `NetworkPolicySpec`, `ImageSpec`, `CodeSpec`) live in `api/v1alpha1/server_types.go`
- Controller tests use the builder pattern from `testutil_test.go` (`newServer()`, `newDatabase()` with option funcs like `withPDBEnabled()`, `withReplicas()`)
- Follow existing patterns when adding sub-resources to a controller:
  1. Add RBAC marker comment (`+kubebuilder:rbac:...`)
  2. Add `Owns(&<type>{})` in `SetupWithManager`
  3. Add event reason constants (`EventReasonFooCreated`, etc.)
  4. Call `r.reconcileFoo()` in `Reconcile()`
  5. Implement `reconcileFoo` (delete if disabled, create if not found, update if exists)
  6. Implement `buildFoo` (construct the desired object, set controller reference)
  7. Add tests for creation, deletion, and any special behavior

## Text Quality

- **No Unicode em-dashes, smart quotes, or other non-ASCII punctuation.** Use plain ASCII (`-`, `--`, `'`, `"`).
- CI runs a unicode-lint check that rejects zero-width characters, soft hyphens, word joiners, and similar invisible characters (potential AI watermarks).
- When in doubt, stick to plain ASCII in all files (code, docs, comments, commit messages).

## Documentation

- `README.md` is the primary user-facing doc - update when adding user-visible features
- `CONTRIBUTING.md` covers developer setup and workflow
- `docs/` contains design docs and architecture diagrams
- No CHANGELOG - release notes are auto-generated from commit messages by semantic-release

### Feature List Synchronization

The canonical feature list lives in `docs/_snippets/features.md`.

- **`docs/index.md`** includes it automatically via `pymdownx.snippets` (`--8<-- "docs/_snippets/features.md"`)
- **`README.md`** contains a copy between `<!-- features -->` and `<!-- /features -->` HTML comment markers

When updating features:
1. Edit `docs/_snippets/features.md` (single source of truth)
2. Copy the same content into the `<!-- features -->` block in `README.md`
