import { execFileSync } from "node:child_process";
import {
  existsSync,
  lstatSync,
  mkdirSync,
  readFileSync,
  realpathSync,
  writeFileSync,
  appendFileSync,
} from "node:fs";
import { dirname, isAbsolute, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";
import {
  commandData,
  commandProperty,
  limits,
  metrics,
  policyHash,
  ratchet,
  regressions,
  relativeClone,
  validateBaseline,
  validateMigration,
} from "./metrics.mjs";

const directory = dirname(fileURLToPath(import.meta.url));
const read = (path) => JSON.parse(readFileSync(path, "utf8"));
const write = (path, data) =>
  writeFileSync(path, JSON.stringify(data, null, 2) + "\n");
const git = (root, ...args) =>
  execFileSync("git", ["-C", root, ...args], { encoding: "utf8" }).trim();

export function sourceFiles(root, policy, cohort) {
  const tracked = execFileSync("git", ["-C", root, "ls-files", "-z"], {
    encoding: "utf8",
  })
    .split("\0")
    .filter(Boolean)
    .sort();
  return tracked.filter((file) => {
    if (
      !policy[cohort].some((pattern) => new RegExp(pattern).test(file)) ||
      policy.exclude.some((pattern) => new RegExp(pattern).test(file)) ||
      (cohort === "production" && file.endsWith("_test.go"))
    )
      return false;
    const path = resolve(root, file),
      within = relative(root, realpathSync(path));
    if (
      isAbsolute(within) ||
      within.startsWith("..") ||
      !lstatSync(path).isFile()
    )
      throw new Error(`Refusing non-regular/outside source: ${file}`);
    if (lstatSync(path).size > policy.maxBytes)
      throw new Error(`Source exceeds scanner size limit: ${file}`);
    return !/(^|\n)\/\/ Code generated [^\n]*DO NOT EDIT\./.test(
      readFileSync(path, "utf8").slice(0, 4096),
    );
  });
}

function scan(
  root,
  policy,
  cohort,
  spec,
  artifactRoot,
  {
    group = cohort,
    nativeBaseline,
    seed = false,
    scanner = resolve(directory, "node_modules/jscpd/run-jscpd.js"),
  } = {},
) {
  const destination = resolve(artifactRoot, group, spec.id);
  mkdirSync(destination, { recursive: true });
  const files = sourceFiles(root, policy, cohort);
  if (!files.length) throw new Error(`No ${cohort} sources selected`);
  const config = resolve(destination, "config.json");
  write(config, policy.jscpd);
  write(resolve(destination, "inputs.json"), {
    commit: git(root, "rev-parse", "HEAD"),
    files,
  });
  const args = [
    scanner,
    "--config",
    config,
    "--workers",
    "1",
    "--absolute",
    "--no-colors",
    "--no-tips",
    "--fail-on-empty",
    "--min-tokens",
    String(spec.minTokens),
    "--min-lines",
    String(spec.minLines),
    "--output",
    destination,
    ...(spec.renamed
      ? ["--ignore-identifiers=consistent", "--ignore-literals"]
      : []),
    ...(nativeBaseline
      ? ["--baseline", nativeBaseline, ...(seed ? ["--update-baseline"] : [])]
      : []),
    ...files,
  ];
  execFileSync(process.execPath, args, {
    cwd: root,
    encoding: "utf8",
    maxBuffer: 16 * 1024 * 1024,
  });
  const path = resolve(destination, "jscpd-report.json"),
    report = read(path);
  report.duplicates = report.duplicates.map((clone) =>
    relativeClone(clone, root),
  );
  write(path, report);
  return {
    metrics: metrics(report),
    report: relative(artifactRoot, path).replaceAll("\\", "/"),
    clones: report.duplicates,
  };
}

export function markdown(result) {
  const value = (scan, metric) =>
    `${scan.metrics[metric]} / ${scan.limits?.[metric] ?? "initial"}`;
  const table = (scans, reportOnly = false) => {
    const headings = [
      "Scan",
      "Clones",
      "Duplicated lines",
      "Duplicated tokens",
      "Result",
    ];
    const rows = scans.map((scan) => [
      scan.id,
      ...["clones", "duplicatedLines", "duplicatedTokens"].map((metric) =>
        reportOnly ? String(scan.metrics[metric]) : value(scan, metric),
      ),
      reportOnly
        ? "REPORT ONLY"
        : scan.regressions.length
          ? "FAIL"
          : result.bootstrap
            ? "BOOTSTRAP"
            : "PASS",
    ]);
    const line = (row) => `| ${row.join(" | ")} |`;
    return [
      line(headings),
      "| --- | ---: | ---: | ---: | --- |",
      ...rows.map(line),
    ];
  };
  const locations = result.scans
    .flatMap((scan) =>
      scan.clones
        .filter((clone) => clone.isNew)
        .map((clone) => ({ scan, clone })),
    )
    .slice(0, 20);
  const safe = (text) =>
    String(text)
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;")
      .replaceAll("|", "&#124;")
      .replaceAll("`", "&#96;")
      .replaceAll("\n", " ");
  return [
    result.migration
      ? "## Duplication ratchet — proposed policy"
      : "## Duplication ratchet",
    result.bootstrap
      ? "Initial measurement: no historical limit applied. The first successful master build persists the baseline."
      : "Production must not increase clone counts, duplicated lines or duplicated tokens in either exact scan.",
    "",
    "Values below are actual / limit; percentages are informational only.",
    "",
    ...table(result.scans),
    ...(result.advisory?.length
      ? [
          "",
          "### Normalised production duplication (report-only)",
          "",
          ...table(result.advisory, true),
        ]
      : []),
    ...(result.tests?.length
      ? [
          "",
          "### Test-source duplication (report-only)",
          "",
          ...table(result.tests, true),
        ]
      : []),
    "",
    ...result.scans.flatMap((scan) =>
      scan.regressions.map(
        (v) =>
          `- **${scan.id}: ${v.metric} ${v.limit} → ${v.actual} (+${v.increase})**`,
      ),
    ),
    "",
    "### New clone candidates relative to the PR base",
    ...locations.map(
      ({ scan, clone }) =>
        `- ${scan.id}: <code>${safe(clone.firstFile.name)}:${clone.firstFile.start}-${clone.firstFile.end}</code> ↔ <code>${safe(clone.secondFile.name)}:${clone.secondFile.start}-${clone.secondFile.end}</code>`,
    ),
    "",
    "Download the duplication-report artifact: result.json contains limits/deltas and clone pairs; each jscpd-report.json contains exact source fragments. Tests are report-only. Normalised matches are candidates, not proof of equivalent behaviour.",
    "Reproduce: npm ci --prefix tools/duplication --ignore-scripts && npm --prefix tools/duplication run check",
    "",
  ].join("\n");
}

export function run(options = {}) {
  const root = realpathSync(
    resolve(options.root || resolve(directory, "../..")),
  );
  const base = options.base ? realpathSync(resolve(options.base)) : null;
  const output = resolve(options.output || resolve(directory, "reports"));
  mkdirSync(output, { recursive: true });
  const policy = read(
    resolve(options.policy || resolve(directory, "policy.json")),
  );
  const baselinePath =
    options.baseline ||
    options.baselinePath ||
    resolve(directory, "baseline.json");
  const baseline = read(baselinePath);
  const migration =
    baseline.status === "established" &&
    baseline.policyHash !== policyHash(policy)
      ? validateMigration(
          read(resolve(root, "tools/duplication/migration.json")),
          baseline,
          policy,
        )
      : null;
  if (!migration) validateBaseline(baseline, policy);
  const scanner = resolve(
    options.scanner || resolve(directory, "node_modules/jscpd/run-jscpd.js"),
  );
  const version = execFileSync(process.execPath, [scanner, "--version"], {
    encoding: "utf8",
  }).trim();
  if (version !== `jscpd ${policy.toolVersion}`)
    throw new Error(`Unexpected scanner version: ${version}`);
  const baseHasPolicy =
    base && existsSync(resolve(base, "tools/duplication/policy.json"));
  const bootstrap = baseline.status === "pending-bootstrap" && !baseHasPolicy;
  if (
    baseHasPolicy &&
    !readFileSync(resolve(root, "tools/duplication/baseline.json")).equals(
      readFileSync(resolve(base, "tools/duplication/baseline.json")),
    )
  ) {
    throw new Error("The baseline is owned by master CI; PRs must not edit it");
  }
  if (migration && !base && !options["update-baseline"])
    throw new Error(
      "A policy migration requires an actual master-base comparison",
    );
  const sourceCommit = git(root, "rev-parse", "HEAD");
  const result = {
    schemaVersion: 1,
    toolVersion: policy.toolVersion,
    policyHash: policyHash(policy),
    sourceCommit,
    baseCommit: base ? git(base, "rev-parse", "HEAD") : null,
    bootstrap,
    migration: migration
      ? {
          fromPolicyHash: policyHash(migration.fromPolicy),
          toPolicyHash: policyHash(policy),
          reason: migration.reason,
        }
      : null,
    scans: [],
    advisory: [],
    tests: [],
  };
  for (const spec of policy.scans) {
    const native = resolve(output, `base-${spec.id}.json`);
    const previous = base
      ? scan(base, policy, "production", spec, output, {
          group: "base",
          nativeBaseline: native,
          seed: true,
          scanner,
        })
      : null;
    const current = scan(root, policy, "production", spec, output, {
      nativeBaseline: base ? native : null,
      scanner,
    });
    const ceiling =
      baseline.status === "established" && !migration
        ? baseline.scans[spec.id]
        : null;
    const baseMetrics = bootstrap ? null : previous?.metrics;
    result.scans.push({
      id: spec.id,
      ...current,
      ceiling,
      baseMetrics,
      limits: limits(ceiling, baseMetrics),
      regressions: regressions(current.metrics, ceiling, baseMetrics),
    });
  }
  for (const spec of policy.advisoryScans || []) {
    result.advisory.push({
      id: spec.id,
      ...scan(root, policy, "production", spec, output, {
        group: "advisory",
        scanner,
      }),
    });
  }
  const testScans = [...policy.scans, ...(policy.advisoryScans || [])].filter(
    (spec) => spec.minTokens === 50,
  );
  for (const spec of testScans) {
    if (sourceFiles(root, policy, "tests").length)
      result.tests.push({
        id: spec.id,
        ...scan(root, policy, "tests", spec, output, { scanner }),
      });
  }
  const observed = Object.fromEntries(
    result.scans.map((scan) => [scan.id, scan.metrics]),
  );
  const failed = result.scans.some((scan) => scan.regressions.length);
  write(resolve(output, "result.json"), result);
  write(resolve(output, "baseline.json"), {
    schemaVersion: 1,
    status: "established",
    toolVersion: policy.toolVersion,
    policyHash: policyHash(policy),
    sourceCommit,
    scans: observed,
  });
  const summary = markdown(result);
  writeFileSync(resolve(output, "summary.md"), summary);
  if (
    !options["no-summary"] &&
    options.reportToGitHub !== false &&
    process.env.GITHUB_STEP_SUMMARY
  )
    appendFileSync(process.env.GITHUB_STEP_SUMMARY, summary);
  process.stdout.write(
    process.env.GITHUB_ACTIONS || options["no-summary"]
      ? `Duplication check ${failed ? "failed" : "passed"}; see the job summary and duplication-report artifact.\n`
      : summary + "\n",
  );
  let annotations = 0;
  for (const scan of result.scans.filter((scan) => scan.regressions.length)) {
    const message = `${scan.id}: ${scan.regressions.map((v) => `${v.metric} ${v.limit} → ${v.actual}`).join("; ")}`;
    const candidates = scan.clones.filter((clone) => clone.isNew);
    for (const clone of (candidates.length ? candidates : scan.clones).slice(
      0,
      5,
    )) {
      if (
        annotations++ >= 20 ||
        options.reportToGitHub === false ||
        !process.env.GITHUB_ACTIONS
      )
        break;
      process.stdout.write(
        `::error file=${commandProperty(clone.firstFile.name)},line=${clone.firstFile.start}::${commandData(message + `; matching code: ${clone.secondFile.name}:${clone.secondFile.start}`)}\n`,
      );
    }
  }
  if (failed)
    throw new Error(
      "Duplication regressed; see result.json and source-location reports. Do not raise the baseline to make CI pass.",
    );
  if (options["update-baseline"]) {
    if (base)
      throw new Error("Baseline updates are not allowed during PR comparisons");
    const next = ratchet(
      migration ? { schemaVersion: 1, status: "pending-bootstrap" } : baseline,
      observed,
      sourceCommit,
      policy,
    );
    if (next) write(baselinePath, next);
  }
  return result;
}

if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  try {
    const { values } = parseArgs({
      options: {
        root: { type: "string" },
        base: { type: "string" },
        output: { type: "string" },
        policy: { type: "string" },
        baseline: { type: "string" },
        scanner: { type: "string" },
        "no-summary": { type: "boolean" },
        "update-baseline": { type: "boolean" },
      },
    });
    run(values);
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
