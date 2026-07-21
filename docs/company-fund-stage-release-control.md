# Company-fund stage release control

> **Status (2026-07):** Cutover multi-mode release control is **retired**.
> Stage and production backend deploys use a single **standard** path:
> compile → upload → controlled migrate (expected ceiling) → replace server → restart.
> Stage is push-triggered on `stage`; production is manual `workflow_dispatch` on `main`.
> The historical cutover modes (`migration-only`, `workers-off-current`, `server-dark`,
> `workers-on-installed`) and dual-lock ceremony are no longer supported by
> `.github/workflows/deploy-backend-*.yml` or `scripts/deploy-remote.sh`.
>
> The remainder of this document is retained as historical context for past
> company-fund cutovers and offline `company-fund-release` evidence tooling.

The stage workflow keeps automatic deployment limited to pushes on `stage`.
Historical manual release operations (retired) were registered by the same workflow definition but
were accepted only when the event ref is exactly `refs/heads/stage` and the
requested run id matches the run component of the repository control mirror
and the approved stage-environment lock.

The repository-scoped `COMPANY_FUND_STAGE_CUTOVER_LOCK_CONTROL` is a trusted,
structured control token: `<run_id>@<64-lowercase-hex-baseline-digest>`. The
environment-scoped `COMPANY_FUND_STAGE_CUTOVER_LOCK` remains the exact run id,
and `COMPANY_FUND_STAGE_CUTOVER_STARTED_AT` is
`<run_id>@<RFC3339Z>@<same-baseline-digest>`. The preflight job parses and
captures the repository token. After stage approval, the deploy job reads the
repository token again through the `vars` context, requires the token to be
unchanged, and compares the started tuple digest to the trusted preflight
digest before checkout or remote work.

## Release modes

| Mode | Server binary | Migration binary | Migration | Worker flag | Restart |
| --- | --- | --- | --- | --- | --- |
| `migration-only` | unchanged | exact SHA | yes, exact ceiling | unchanged | no |
| `workers-off-current` | installed SHA only | unchanged | no | `false` | yes |
| `server-dark` | exact SHA | unchanged | no | must already be `false` | yes |
| `workers-on-installed` | installed SHA only | unchanged | no | `true` | yes |
| `standard` | exact SHA | exact SHA | all registered | unchanged | yes |

Every artifact reference is a lowercase 40-character commit SHA. The installed
server SHA is written atomically to `/opt/monera-digital/release-manifest.json`.
The server and migration process attach
`monera-digital/<sha12>/<systemd-invocation-id>` as the PostgreSQL
`application_name`, so the short database-session identity can be resolved
through the full-SHA manifest and systemd journal.

Cutover uses four distinct immutable commits in one ancestry chain:
`CURRENT < A < V2 < B`. CURRENT has migration ceiling `051`; A adds `052` and
has ceiling `052`; V2 contains the server changes but retains the byte-identical
`052` blob, contains no `053`, and still has ceiling `052`; B adds `053` and has
ceiling `053`. The cutover-template command validates the exact Git trees,
rejects migration `054` or higher, archives every exact SHA into an isolated
directory, builds its real `monera-migrate`, and runs `-print-ceiling`. It also
parses the controlled migration method contracts and rejects a plain migration
that could bypass the pinned transaction path. Source-string claims alone are
not release evidence.

The verifier also runs the machine-readable `-print-versions` command from
each built artifact. CURRENT's complete ordered registration list must exactly
match its production migration filenames. A must equal CURRENT plus `052`, V2
must equal A byte-for-byte at the registration level, and B must equal A plus
`053`; duplicates, reordering, and hidden low-version registrations are
rejected even when `-print-ceiling` still looks correct. A SHA-256 manifest of
every production migration below `052` is built from CURRENT, and every path
and blob must remain byte-identical in A, V2, and B. The evidence JSON records
all four complete version lists and all four pre-052 manifest digests.
The public verifier additionally pins the complete approved `052` and `053`
source blobs to code-owned SHA-256 constants. Its readable AST checks remain
defense-in-depth, but a source edit cannot be authorized through a runtime flag,
environment variable, manifest, or test-only mutable global.

Checkpoint roles have a code-owned changed-file contract. A is limited to
Migration 052, the controlled migrator/runner, its explicit occurrence helper,
and their tests. V2 must actually change the frozen policyless runtime paths
(`safeheron_coin_catalog.go`, `safeheron_runtime_resolvers.go`,
`safeheron_webhook_eligibility.go`, and `valuation_runtime.go`) and may not be
an empty bridge commit. B is limited to Migration 053, final fingerprint and
provenance inspection, release-control/workflow/script/documentation surfaces,
and their tests. This rejects V2 business code hidden in A and unrelated
business changes hidden in B.

The combined work-in-progress HEAD is never a promotable checkpoint artifact.
A and B are created later as separate commits only after explicit user
authorization. Immediately before creating A, fetch the current `stage` ref
and prove its migration ceiling is still `051`. Immediately before creating B,
fetch `stage` again and prove that the approved ancestry and ceilings still
hold (`CURRENT=051`, `A=052`, `V2=052`, `B=053`) with no newly allocated
migration conflict. If another migration has landed, stop and renumber the
files, runner ceilings, tests, documentation, and release contract together.
Passing the verifier produces evidence only; it does not authorize a deploy,
migration, ref update, or promotion.

```bash
go run ./cmd/company-fund-release cutover-template \
  --repo . --utc <RFC3339Z> --current-sha <CURRENT> --a-sha <A> \
  --v2-sha <V2> --b-sha <B> --expected-ceiling 052 \
  --evidence-dir <absolute-path> --fixture-tx-key <key> \
  --fixture-occurrence-key <key>
```

Migration `052` is checkpoint A. Migration `053` is an independent,
forward-only checkpoint B: it requires `052` to have been recorded before the
runner starts and can only run with `EXPECTED_MIGRATION_CEILING=053`. A standard
run cannot apply a pending `053`, and one invocation cannot apply both `052`
and `053`. After `053` is recorded, later standard runs skip it normally.
Both controlled migrations write their DDL and their row in
`public.migrations` in the same pinned transaction. All company-fund DDL and
catalog inspection are explicitly bound to the canonical `public` schema;
`search_path` and same-named objects in attacker-controlled schemas cannot
redirect migration or release evidence. Disposable-schema substitution exists
only in opt-in PostgreSQL integration tests and is never a production runtime
input.

Before promoting the B artifact, validate the live final schema and persist the
returned catalog report, canonical JSON, and SHA-256:

```bash
RUN_COMPANY_FUND_SCHEMA_FINGERPRINT=1 \
DATABASE_URL=<stage-database-url> \
go run ./cmd/company-fund-release schema-fingerprint
```

The snapshot must prove the two `052` occurrence columns and the exact valid,
ready, partial unique index definition. Constraints are bound to their exact
schema and owning table, must be validated CHECK constraints, and must equal
the single canonical `052`/`053` expressions after catalog normalization. That
normalization removes PostgreSQL's harmless text casts, whitespace, equivalent
`ANY (ARRAY[...])` rendering, and redundant outer parentheses while preserving
the recursive `AND`/`OR` grouping; token-equivalent regrouping is rejected. The
MANUAL function must be the exact zero-argument, trigger-returning, normal
`plpgsql` function body extracted from the `052` migration source. Its trigger
must be the enabled, non-internal `BEFORE UPDATE FOR EACH ROW` trigger bound to
that function OID and the exact ordered protected-column tuple.
Schema A alone is intentionally rejected as final evidence. The command gates
before reading `DATABASE_URL`, opens the database only after the explicit gate,
and reads PostgreSQL catalogs plus the `migrations` provenance row directly;
caller-supplied JSON cannot serve as release evidence.

Failure to install the artifact, print its ceiling, or match the requested
ceiling fails immediately without invoking schema reconciliation. Once the
migration process starts, only the dedicated exit code `75` emitted for a typed
controlled-commit outcome-indeterminate error may reconcile success. For
ceiling `052`, reconciliation requires physical schema A, `052` recorded, and
`053` absent. For ceiling `053`, it requires physical schema B with both `052`
and `053` recorded. Any ordinary nonzero exit remains a release failure even
when inspection proves the intended commit; it is traced as an unexpected
commit and is never reported as success. During checkpoint B, exact schema A
with `052` recorded and no `053` is the known atomic-failure state. During
checkpoint A there is no separately fingerprinted pre-A baseline, so A without
`052` provenance, `PARTIAL`, or `UNKNOWN` always enters the hard-stop path.
Likewise B without either provenance row, `PARTIAL`, or `UNKNOWN` hard-stops:
stop the service, verify it is inactive, normalize workers to `false`, and
persist alarm/manual-quiescence evidence. That path never performs an ordinary
migration rollback or installs an older server. Ordinary `standard` failures
do not use this A/B classifier; they restore the migration artifacts normally.
The inspector database URL is taken only from exactly one canonical, non-empty
`DATABASE_URL=...` assignment in the installed `/opt/monera-digital/.env`.
Duplicate, exported, quoted, whitespace-bearing, command-substitution, or
non-PostgreSQL assignments classify as `UNKNOWN`; the value is never traced or
logged, and a process-level `DATABASE_URL` cannot override the installed
artifact.
`MONERA_DEPLOY_SCHEMA_INSPECTOR_COMMAND` exists only for the fake release
harness. Real releases always execute the installed
`/opt/monera-digital/company-fund-release` artifact.

Package-based modes also require `artifact-sha` inside the extracted package to
equal the approved 40-character SHA before any installation or migration.
Each workflow attempt uploads to a unique run-id/run-attempt directory and
cleans it on exit. `workers-off-current` receives the exact approved package
runner so a legacy stage install can enter the controlled release protocol. If
that install predates `release-manifest.json`, the mode accepts only a server
binary containing the exact approved 40-character build SHA before changing
the worker flag. `workers-on-installed` continues to use only the trusted
installed deploy runner; neither mode falls back to an arbitrary fixed `/tmp`
runner left by an interrupted or stale deployment.

## Manual workflow inputs

- `mode`
- `artifact_ref`
- `run_id`
- `installed_server_sha` (required only by `workers-off-current`)
- `expected_migration_ceiling` (required only by `migration-only`)

Manual dispatch remains disabled until an operator deliberately sets matching
repository and stage-environment locks. The `control-preflight` job performs no
checkout, environment approval, upload, SSH, migration, or restart. Only the
dependent `deploy-stage` job may perform remote actions after stage approval and
lock revalidation.

## Bootstrap manifest

Generate auditable D0/B0 evidence from real Git objects before asking the user
to promote either artifact:

```bash
go run ./cmd/company-fund-release bootstrap-manifest \
  --repo . \
  --phase D0 \
  --base-ref <exact-base-ref> \
  --head-ref <exact-head-ref> \
  --main-workflow-ref <exact-main-workflow-ref> \
  --stage-workflow-ref <exact-stage-workflow-ref>
```

The command resolves commits and the head tree, obtains the changed-file set
with `git diff`, applies the phase allowlist, reads workflow blobs with
`git ls-tree`/`git show`, and compares canonical `workflow_dispatch` input and
`control-preflight` hashes. Its JSON output records the exact SHAs, files,
workflow blob/content hashes, contract hashes, and a warning that only the user
may promote D0 to `main` or B0 to `stage`. It never changes refs or performs a
promotion.

The workflow-only dispatcher may be promoted separately to the repository's
default branch so GitHub registers `workflow_dispatch`. Its push filter still
contains only `stage`, and a manual run using `--ref main` fails in
`control-preflight`, before checkout or any remote action.

## Safeheron alias repair evidence

The alias scanner uses the process clock as its trusted time source; JSON
artifacts cannot provide or override `now`. The final account-hash and drain
samples must not be in the future and must be no more than 10 seconds old. This
freshness check runs before the serializable transaction, again inside it, and
again immediately before commit. The pre-commit check also repeats live schema,
lease/session drain, canonical account-policy hash, installed-manifest, and
workers-off validation. Evidence that expires while a scan is running causes a
rollback.

If a fake Migration B non-atomic failure is injected, the release harness stops
the service and verifies it is inactive before normalizing workers to `false`.
It retains partial-schema and manual-quiescence evidence without restoring an
old server. A stop or inactive-verification failure remains a hard stop and
emits an explicit alarm trace.

## Local verification

```bash
actionlint .github/workflows/deploy-backend-stage.yml
shellcheck scripts/deploy-remote.sh
go test ./internal/releasecontrol ./internal/buildinfo ./internal/migration
go test ./cmd/company-fund-release ./cmd/migrate ./cmd/server
```

`internal/releasecontrol` runs the deployment script with a fake remote harness
to verify action ordering, forbidden side effects, failure rollback, installed
SHA checks, and the worker-off prerequisite.
