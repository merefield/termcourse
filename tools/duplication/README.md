# Duplication ratchet

Termcourse uses the CI jobs and checker from [Codexometer](https://github.com/merefield/codexometer/tree/20c5f98198d27fb5342742759498bb8f72f488a0/tools/duplication), with a Go-only policy. The pinned jscpd scanner runs on tracked Go files. Vendored and generated files are excluded.

| Production scan | Minimum tokens and lines | CI policy |
| --- | --- | --- |
| `exact-50` | 50 tokens, 5 lines | Gated |
| `exact-100` | 100 tokens, 10 lines | Gated separately |
| `renamed-50` and `renamed-100` | Same thresholds, with consistent identifier and literal normalisation | Report-only |

Each exact scan independently limits clone count, duplicated lines and duplicated tokens. Test files have a separate report with the 50-token scans. The `duplication-report` artifact contains the per-scan totals, source locations and matched fragments.

## Baseline lifecycle

The first successful CI push on `master` writes the initial numerical baseline after the duplication check and the Go test matrix pass. Later checks use the checker, policy and stored baseline from the PR base or the commit before the push. For each gated metric, they use the lower of the stored limit and the freshly measured base value. Successful `master` builds commit tighter limits when the measurements improve.

The writer is the only job with repository write access. It commits `baseline.json` alone, skips stale builds and uses a normal non-force push. Repository rules must allow that push. A failed write leaves a `duplication-baseline-candidate` artifact for diagnosis.

## Local checks

Run from the repository root with Node 24:

```sh
npm ci --prefix tools/duplication --ignore-scripts
npm --prefix tools/duplication test
npm --prefix tools/duplication run check
```

For an exact PR comparison, use a separate worktree at the PR base and run that worktree's checker with `--base` pointing to it and `--root` pointing to the candidate checkout. The check scans tracked files, so stage any new Go files before running it.

When checker code changes, CI verifies that the candidate reproduces the trusted checker's results. For a policy change, run `node tools/duplication/verify.mjs --prepare-migration --root "$PWD" --base /path/to/base-worktree --reason "Explain the change"` and commit the resulting migration record. CI checks the change under both policies, and the baseline writer adopts the new measurements after merge.
