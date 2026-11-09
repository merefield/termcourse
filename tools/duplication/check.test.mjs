import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import {
  mkdtempSync,
  readFileSync,
  rmSync,
  symlinkSync,
  writeFileSync,
  mkdirSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { markdown, run as check, sourceFiles } from "./check.mjs";
import { changedTooling, prepareMigration, verify } from "./verify.mjs";
import {
  commandData,
  commandProperty,
  metrics,
  policyHash,
  ratchet,
  regressions,
  relativeClone,
  validateBaseline,
  validateMigration,
} from "./metrics.mjs";

const policy = JSON.parse(
  readFileSync(new URL("./policy.json", import.meta.url), "utf8"),
);
const pending = { schemaVersion: 1, status: "pending-bootstrap" };
const counts = { clones: 4, duplicatedLines: 40, duplicatedTokens: 400 };
const observed = () =>
  Object.fromEntries(policy.scans.map((scan) => [scan.id, { ...counts }]));
const baseline = () => ratchet(pending, observed(), "a".repeat(40), policy);
// Expected regression fixtures must not emit real CI errors or job summaries.
const run = (options) => check({ ...options, reportToGitHub: false });

test("first measurement establishes the benchmark without an arbitrary limit", () => {
  const current = observed();
  current["exact-50"] = {
    clones: 999,
    duplicatedLines: 9999,
    duplicatedTokens: 99999,
  };
  const established = ratchet(pending, current, "a".repeat(40), policy);
  assert.equal(established.status, "established");
  assert.deepEqual(established.scans, current);
  assert.deepEqual(regressions(current["exact-50"]), []);
  assert.equal(validateBaseline(established, policy), established);
});

test("every metric must be equal or lower, independently of other improvements", () => {
  assert.deepEqual(regressions(counts, counts), []);
  for (const metric of Object.keys(counts)) {
    const current = {
      clones: 0,
      duplicatedLines: 0,
      duplicatedTokens: 0,
      [metric]: counts[metric] + 1,
    };
    assert.deepEqual(regressions(current, counts), [
      {
        metric,
        actual: counts[metric] + 1,
        limit: counts[metric],
        increase: 1,
      },
    ]);
  }
  assert.equal(regressions(counts, { ...counts, clones: 3 }).length, 1);
});

test("actual base metrics prevent stale committed ceilings from hiding regressions", () => {
  const stale = { clones: 10, duplicatedLines: 100, duplicatedTokens: 1000 };
  assert.equal(
    regressions({ ...counts, duplicatedLines: 41 }, stale, counts)[0].limit,
    40,
  );
});

test("ratcheting only tightens and unchanged metrics do not rewrite provenance", () => {
  const original = baseline();
  assert.equal(ratchet(original, observed(), "b".repeat(40), policy), null);
  const improved = observed();
  improved["exact-50"].duplicatedTokens--;
  const tighter = ratchet(original, improved, "b".repeat(40), policy);
  assert.equal(tighter.sourceCommit, "b".repeat(40));
  assert.equal(tighter.scans["exact-50"].duplicatedTokens, 399);
  improved["exact-100"].clones++;
  assert.throws(
    () => ratchet(original, improved, "b".repeat(40), policy),
    /Refusing to increase/,
  );
});

test("missing, corrupt and incompatible baselines fail rather than rebootstrap", () => {
  for (const invalid of [
    null,
    {},
    { ...pending, scans: {} },
    { ...baseline(), policyHash: "wrong" },
    { ...baseline(), sourceCommit: "not-a-sha" },
    { ...baseline(), scans: {} },
  ]) {
    assert.throws(() => validateBaseline(invalid, policy));
  }
  for (const invalid of [null, -1, NaN, Infinity, 1.5, "4"]) {
    assert.throws(() =>
      metrics({ statistics: { total: { ...counts, clones: invalid } } }),
    );
  }
  assert.notEqual(
    policyHash(policy),
    policyHash({ ...policy, maxBytes: policy.maxBytes + 1 }),
  );
});

test("policy migrations preserve the old master ceiling and require exact provenance", () => {
  const next = { ...policy, toolVersion: "5.4.3" };
  const migration = {
    schemaVersion: 1,
    fromPolicy: policy,
    toPolicyHash: policyHash(next),
    reason: "Upgrade the pinned scanner",
  };
  assert.equal(validateMigration(migration, baseline(), next), migration);
  for (const invalid of [
    null,
    { ...migration, reason: "  " },
    { ...migration, toPolicyHash: "wrong" },
    { ...migration, fromPolicy: next },
    { ...migration, fromPolicy: { ...policy, maxBytes: 1 } },
  ]) {
    assert.throws(() => validateMigration(invalid, baseline(), next));
  }
});

test("reports retain actionable paths, ranges and exact source payload", () => {
  const clone = {
    kind: "renamed",
    fragment: 'const text = "unchanged";\n',
    firstFile: { name: "/repo/a.go", start: 3, end: 9 },
    secondFile: { name: "/repo/b.go", start: 13, end: 19 },
  };
  const relative = relativeClone(clone, "/repo");
  assert.deepEqual(relative, {
    ...clone,
    firstFile: { ...clone.firstFile, name: "a.go" },
    secondFile: { ...clone.secondFile, name: "b.go" },
  });
  assert.throws(() =>
    relativeClone(
      { ...clone, firstFile: { ...clone.firstFile, name: "/outside/a.go" } },
      "/repo",
    ),
  );
  const result = {
    bootstrap: false,
    scans: [
      {
        id: "exact-50",
        metrics: counts,
        regressions: [{ metric: "clones", limit: 3, actual: 4, increase: 1 }],
        clones: [{ ...relative, isNew: true }],
      },
    ],
  };
  const summary = markdown(result);
  assert.match(summary, /clones 3 → 4/);
  assert.match(summary, /a.go:3-9/);
  assert.match(summary, /b.go:13-19/);
  assert.match(summary, /source fragments/);
  assert.match(
    summary,
    /\| Scan \| Clones \| Duplicated lines \| Duplicated tokens \| Result \|/,
  );
  assert.match(
    summary,
    /\| exact-50 \| 4 \/ initial \| 40 \/ initial \| 400 \/ initial \| FAIL \|/,
  );
  const withTests = markdown({
    ...result,
    tests: [{ id: "exact-50", metrics: counts }],
  });
  assert.match(withTests, /Test-source duplication \(report-only\)/);
  assert.match(withTests, /REPORT ONLY/);
});

test("untrusted names cannot inject workflow commands or summary HTML", () => {
  assert.equal(commandData("x%\r\n::error::bad"), "x%25%0D%0A::error::bad");
  assert.equal(commandProperty("a,b:1\nx"), "a%2Cb%3A1%0Ax");
  const result = {
    bootstrap: false,
    scans: [
      {
        id: "exact-50",
        metrics: counts,
        regressions: [],
        clones: [
          {
            isNew: true,
            firstFile: { name: "<script>|`\nx", start: 1, end: 2 },
            secondFile: { name: "safe.go", start: 3, end: 4 },
          },
        ],
      },
    ],
  };
  const summary = markdown(result);
  assert.ok(!summary.includes("<script>"));
  assert.match(summary, /&lt;script&gt;&#124;&#96; x/);
});

test("only tracked, regular source files are measured and tests remain separate", (t) => {
  const root = mkdtempSync(resolve(tmpdir(), "termcourse-duplication-test-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  execFileSync("git", ["init", "--quiet", root]);
  for (const file of [
    "main.go",
    "main_test.go",
    "web/src/App.svelte",
    "web/src/contracts.generated.ts",
    "web/tests/browser.spec.ts",
    "internal/web/dist/bundle.js",
    "generated.go",
  ]) {
    mkdirSync(resolve(root, file, ".."), { recursive: true });
    writeFileSync(
      resolve(root, file),
      file === "generated.go"
        ? "// Code generated by a fixture. DO NOT EDIT.\npackage fixture\n"
        : "package fixture\n",
    );
  }
  execFileSync("git", ["-C", root, "add", "."]);
  writeFileSync(resolve(root, "untracked.go"), "package ignored\n");
  assert.deepEqual(sourceFiles(root, policy, "production"), ["main.go"]);
  assert.deepEqual(sourceFiles(root, policy, "tests"), ["main_test.go"]);
  symlinkSync(resolve(root, "main.go"), resolve(root, "link.go"));
  execFileSync("git", ["-C", root, "add", "link.go"]);
  assert.throws(() => sourceFiles(root, policy, "production"), /non-regular/);
});

test("real scanner bootstraps once, then reports actionable clone regressions", (t) => {
  const directory = mkdtempSync(
    resolve(tmpdir(), "termcourse-duplication-scan-"),
  );
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const code = `package fixture
func score(values []int) int {
 total := 0
 for index, value := range values {
  if value < 0 { total -= value } else { total += value * index }
  if value % 2 == 0 { total += index } else { total -= index }
  if total > 100 { total -= 10 } else { total += 10 }
 }
 minimum := total
 maximum := total
 for _, value := range values {
  if value < minimum { minimum = value }
  if value > maximum { maximum = value }
 }
 if minimum < 0 { total -= minimum }
 if maximum > 100 { total += maximum }
 return total
}
`;
  const init = (name, copies) => {
    const root = resolve(directory, name);
    execFileSync("git", ["init", "--quiet", root]);
    for (let i = 0; i < copies; i++)
      writeFileSync(resolve(root, `copy${i}.go`), code);
    execFileSync("git", ["-C", root, "add", "."]);
    execFileSync("git", [
      "-C",
      root,
      "-c",
      "user.name=Fixture",
      "-c",
      "user.email=fixture@example.invalid",
      "-c",
      "commit.gpgsign=false",
      "commit",
      "--quiet",
      "-m",
      "fixture",
    ]);
    return root;
  };
  const base = init("base", 1),
    root = init("candidate", 2);
  const authority = resolve(directory, "authority.json");
  writeFileSync(authority, JSON.stringify(pending));
  const initial = run({
    root,
    base,
    baselinePath: authority,
    output: resolve(directory, "initial"),
  });
  assert.equal(initial.bootstrap, true);
  assert.ok(initial.scans.some((scan) => scan.metrics.clones > 0));
  assert.ok(initial.scans.every((scan) => scan.regressions.length === 0));
  assert.deepEqual(
    initial.advisory.map((scan) => scan.id),
    ["renamed-50", "renamed-100"],
  );
  const measured = JSON.parse(
    readFileSync(resolve(directory, "initial/baseline.json"), "utf8"),
  );
  assert.equal(measured.status, "established");
  assert.deepEqual(Object.keys(measured.scans), ["exact-50", "exact-100"]);
  assert.deepEqual(JSON.parse(readFileSync(authority, "utf8")), pending); // Read-only checks never mutate authority.

  mkdirSync(resolve(base, "tools/duplication"), { recursive: true });
  writeFileSync(
    resolve(base, "tools/duplication/policy.json"),
    JSON.stringify(policy),
  );
  const output = resolve(directory, "regression");
  for (const repo of [base, root]) {
    mkdirSync(resolve(repo, "tools/duplication"), { recursive: true });
    writeFileSync(
      resolve(repo, "tools/duplication/baseline.json"),
      JSON.stringify(pending),
    );
  }
  assert.throws(
    () => run({ root, base, baselinePath: authority, output }),
    /Duplication regressed/,
  );
  const result = JSON.parse(
    readFileSync(resolve(output, "result.json"), "utf8"),
  );
  assert.equal(result.bootstrap, false);
  const failed = result.scans.find((scan) => scan.regressions.length);
  assert.ok(failed);
  const clone = failed.clones.find((clone) => clone.isNew);
  assert.ok(
    clone,
    "native fingerprints must identify new clones relative to the actual base",
  );
  assert.match(clone.firstFile.name, /^copy\d\.go$/);
  assert.match(clone.secondFile.name, /^copy\d\.go$/);
  assert.ok(
    clone.firstFile.start > 0 && clone.firstFile.end >= clone.firstFile.start,
  );
  assert.ok(
    clone.fragment.includes("score") || clone.fragment.includes("total"),
  );
  assert.ok(readFileSync(resolve(output, failed.report), "utf8"));
  assert.match(failed.report, /^production\/[^/]+\/jscpd-report\.json$/);

  // jscpd 5.4.2 annotates known clones; --baseline does not filter totals.
  // Keep an existing clone in the base and compare full, unbaselined totals.
  writeFileSync(resolve(base, "copy1.go"), code);
  execFileSync("git", ["-C", base, "add", "copy1.go"]);
  const unchanged = run({
    root,
    base,
    baselinePath: authority,
    output: resolve(directory, "known"),
  });
  for (const scan of unchanged.scans) {
    const complete = initial.scans.find((previous) => previous.id === scan.id);
    assert.deepEqual(scan.metrics, complete.metrics);
    assert.equal(
      scan.metrics.clones,
      1,
      "known clones must remain in absolute totals",
    );
    assert.equal(scan.clones.length, complete.clones.length);
    assert.ok(scan.clones.every((clone) => clone.isNew === false));
    const report = JSON.parse(
      readFileSync(resolve(directory, "known", scan.report), "utf8"),
    );
    assert.deepEqual(metrics(report), complete.metrics);
    assert.equal(report.duplicates.length, 1);
  }
  // A further clone is still detected despite the pre-existing baseline match.
  writeFileSync(resolve(root, "copy2.go"), code);
  execFileSync("git", ["-C", root, "add", "copy2.go"]);
  const addedOutput = resolve(directory, "known-plus-new");
  assert.throws(
    () => run({ root, base, baselinePath: authority, output: addedOutput }),
    /Duplication regressed/,
  );
  const added = JSON.parse(
    readFileSync(resolve(addedOutput, "result.json"), "utf8"),
  );
  assert.ok(
    added.scans.every(
      (scan) =>
        scan.metrics.clones > 1 && scan.clones.some((clone) => clone.isNew),
    ),
  );
  // Restore the two-copy fixture without deleting a tracked file.
  writeFileSync(resolve(root, "copy2.go"), "package fixture\n");

  const established = baseline();
  writeFileSync(authority, JSON.stringify(established));
  writeFileSync(
    resolve(base, "tools/duplication/baseline.json"),
    JSON.stringify(established),
  );
  mkdirSync(resolve(root, "tools/duplication"), { recursive: true });
  writeFileSync(
    resolve(root, "tools/duplication/baseline.json"),
    JSON.stringify(pending),
  );
  assert.throws(
    () =>
      run({
        root,
        baselinePath: authority,
        output: resolve(directory, "reset"),
        base,
      }),
    /owned by master/,
  );
  writeFileSync(
    resolve(root, "tools/duplication/baseline.json"),
    JSON.stringify({
      ...established,
      scans: { ...established.scans, "exact-50": { ...counts, clones: 5 } },
    }),
  );
  assert.throws(
    () =>
      run({
        root,
        baselinePath: authority,
        output: resolve(directory, "raised"),
        base,
      }),
    /owned by master/,
  );

  // Exercise the actual main-only write path, not just the pure ratchet helper.
  writeFileSync(authority, JSON.stringify(pending));
  run({
    root,
    baselinePath: authority,
    output: resolve(directory, "persist"),
    "update-baseline": true,
  });
  const saved = readFileSync(authority, "utf8");
  assert.equal(JSON.parse(saved).status, "established");
  writeFileSync(resolve(root, "tools/duplication/baseline.json"), saved);
  run({
    root,
    baselinePath: authority,
    output: resolve(directory, "unchanged"),
    "update-baseline": true,
  });
  assert.equal(
    readFileSync(authority, "utf8"),
    saved,
    "unchanged measurements must not rewrite the baseline",
  );
  writeFileSync(resolve(root, "copy1.go"), "package fixture\n");
  run({
    root,
    baselinePath: authority,
    output: resolve(directory, "improved"),
    "update-baseline": true,
  });
  assert.ok(
    Object.values(JSON.parse(readFileSync(authority, "utf8")).scans).every(
      (scan) => scan.clones === 0,
    ),
  );

  // Real pre-merge validation: ordinary PRs do not rescan. Changes to the
  // checker must agree with main, and policy upgrades pass both rule sets.
  writeFileSync(resolve(root, "copy1.go"), code);
  writeFileSync(authority, saved);
  const actualTools = fileURLToPath(new URL(".", import.meta.url));
  for (const repo of [base, root]) {
    const tools = resolve(repo, "tools/duplication");
    for (const file of [
      "check.mjs",
      "metrics.mjs",
      "verify.mjs",
      "policy.json",
    ])
      writeFileSync(
        resolve(tools, file),
        readFileSync(resolve(actualTools, file)),
      );
    writeFileSync(resolve(tools, "baseline.json"), saved);
    symlinkSync(
      resolve(actualTools, "node_modules"),
      resolve(tools, "node_modules"),
      "dir",
    );
    execFileSync("git", ["-C", repo, "add", "tools"]);
  }
  const trustedOutput = resolve(directory, "trusted");
  const trusted = run({
    root,
    base,
    baselinePath: authority,
    output: trustedOutput,
  });
  const verifyOptions = {
    root,
    base,
    output: trustedOutput,
    authority: resolve(base, "tools/duplication"),
    reportToGitHub: false,
  };
  assert.deepEqual(changedTooling(root, base), []);
  verify(verifyOptions);
  assert.equal(
    JSON.parse(
      readFileSync(resolve(trustedOutput, "verification.json"), "utf8"),
    ).status,
    "not-needed",
  );

  const checkerPath = resolve(root, "tools/duplication/check.mjs");
  const honest = readFileSync(checkerPath, "utf8");
  writeFileSync(
    checkerPath,
    honest + "\n// An implementation change needs validation.\n",
  );
  verify(verifyOptions);
  assert.equal(
    JSON.parse(
      readFileSync(resolve(trustedOutput, "verification.json"), "utf8"),
    ).status,
    "passed",
  );
  writeFileSync(checkerPath, "process.exit(0);\n");
  assert.throws(() => verify(verifyOptions), /did not produce a usable report/);
  writeFileSync(
    checkerPath,
    honest.replace(
      "metrics: metrics(report),",
      "metrics: { clones: 0, duplicatedLines: 0, duplicatedTokens: 0 },",
    ),
  );
  assert.throws(
    () => verify(verifyOptions),
    /disagrees with the trusted checker/,
  );
  writeFileSync(checkerPath, honest);

  const nextPolicy = { ...policy, jscpd: { ...policy.jscpd, maxGapLines: 1 } };
  const candidatePolicy = resolve(root, "tools/duplication/policy.json");
  writeFileSync(candidatePolicy, JSON.stringify(nextPolicy));
  assert.throws(() => verify(verifyOptions), /migration.json/);
  prepareMigration({
    root,
    base,
    reason: "Allow a one-line gap in clone matching",
  });
  const untouched = readFileSync(
    resolve(root, "tools/duplication/baseline.json"),
    "utf8",
  );
  verify(verifyOptions);
  const proposed = JSON.parse(
    readFileSync(resolve(trustedOutput, "proposed-policy/result.json"), "utf8"),
  );
  assert.equal(proposed.policyHash, policyHash(nextPolicy));
  assert.equal(proposed.migration.fromPolicyHash, trusted.policyHash);
  assert.ok(
    proposed.scans.every(
      (scan) => scan.regressions.length === 0 && scan.limits.clones !== null,
    ),
  );
  assert.equal(
    readFileSync(resolve(root, "tools/duplication/baseline.json"), "utf8"),
    untouched,
    "PR verification must not edit main-owned baseline",
  );

  // A successful main writer can then migrate the inherited baseline. No PR
  // baseline edit or permanently failing hash mismatch is needed.
  run({
    root,
    policy: candidatePolicy,
    baselinePath: authority,
    output: resolve(directory, "migrated-main"),
    "update-baseline": true,
  });
  const migrated = JSON.parse(readFileSync(authority, "utf8"));
  assert.equal(migrated.policyHash, policyHash(nextPolicy));
  assert.equal(migrated.status, "established");
});
