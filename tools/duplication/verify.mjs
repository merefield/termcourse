import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";
import {
  comparisonMetrics,
  policyHash,
  validateMigration,
} from "./metrics.mjs";

const directory = dirname(fileURLToPath(import.meta.url));
const read = (path) => JSON.parse(readFileSync(path, "utf8"));

export function changedTooling(root, base) {
  const tracked = (repo) =>
    execFileSync("git", ["-C", repo, "ls-files", "-z", "tools/duplication"], {
      encoding: "utf8",
    })
      .split("\0")
      .filter(Boolean);
  return [...new Set([...tracked(root), ...tracked(base)])]
    .filter((path) => {
      if (
        !/\.(mjs|json)$/.test(path) ||
        /(?:\.test\.mjs|\/(?:baseline|migration)\.json)$/.test(path)
      )
        return false;
      const left = resolve(root, path),
        right = resolve(base, path);
      return (
        !existsSync(left) ||
        !existsSync(right) ||
        !readFileSync(left).equals(readFileSync(right))
      );
    })
    .sort();
}

export function prepareMigration({ root, base, reason }) {
  const migration = {
    schemaVersion: 1,
    fromPolicy: read(resolve(base, "tools/duplication/policy.json")),
    toPolicyHash: policyHash(
      read(resolve(root, "tools/duplication/policy.json")),
    ),
    reason,
  };
  validateMigration(
    migration,
    read(resolve(base, "tools/duplication/baseline.json")),
    read(resolve(root, "tools/duplication/policy.json")),
  );
  writeFileSync(
    resolve(root, "tools/duplication/migration.json"),
    JSON.stringify(migration, null, 2) + "\n",
  );
}

export function verify({
  root,
  base,
  output,
  authority = directory,
  reportToGitHub = true,
}) {
  root = resolve(root);
  base = resolve(base);
  output = resolve(output);
  const changedFiles = changedTooling(root, base);
  const status = (state) =>
    writeFileSync(
      resolve(output, "verification.json"),
      JSON.stringify({ status: state, changedFiles }, null, 2) + "\n",
    );
  if (!changedFiles.length) {
    status("not-needed");
    console.log(
      "Duplication tooling unchanged; the authoritative comparison is sufficient.",
    );
    return;
  }
  status("running");
  const trusted = read(resolve(output, "result.json"));
  const candidateTools = resolve(root, "tools/duplication");
  const oldPolicy = read(resolve(authority, "policy.json"));
  const newPolicy = read(resolve(candidateTools, "policy.json"));
  const policyChanged = policyHash(oldPolicy) !== policyHash(newPolicy);
  if (policyChanged) {
    const migration = validateMigration(
      read(resolve(candidateTools, "migration.json")),
      read(resolve(authority, "baseline.json")),
      newPolicy,
    );
    assert.equal(
      policyHash(migration.fromPolicy),
      policyHash(oldPolicy),
      "Migration must start from the actual master policy",
    );
  }
  const installed = execFileSync(
    process.execPath,
    [resolve(candidateTools, "node_modules/jscpd/run-jscpd.js"), "--version"],
    { encoding: "utf8" },
  ).trim();
  assert.equal(
    installed,
    `jscpd ${newPolicy.toolVersion}`,
    "Candidate dependency lock must match its declared scanner version",
  );
  const candidate = (destination, args = []) => {
    mkdirSync(destination, { recursive: true });
    // A no-op checker must not reuse a previous successful local result.
    writeFileSync(resolve(destination, "result.json"), "null\n");
    execFileSync(
      process.execPath,
      [
        resolve(candidateTools, "check.mjs"),
        "--root",
        root,
        "--base",
        base,
        "--output",
        destination,
        ...args,
        ...(reportToGitHub ? [] : ["--no-summary"]),
      ],
      {
        encoding: "utf8",
        maxBuffer: 16 * 1024 * 1024,
        stdio: ["ignore", "pipe", "pipe"],
      },
    );
    const report = read(resolve(destination, "result.json"));
    assert.ok(
      report?.schemaVersion === 1 &&
        Array.isArray(report.scans) &&
        Array.isArray(report.tests),
      "Candidate checker did not produce a usable report",
    );
    return report;
  };
  mkdirSync(resolve(output, "candidate-validation"), { recursive: true });
  // Exercise the proposed implementation/dependencies, but with master's rules
  // and ceiling. A no-op or mismeasuring candidate cannot silently go live.
  const compatible = candidate(resolve(output, "candidate-validation"), [
    "--policy",
    resolve(authority, "policy.json"),
    "--baseline",
    resolve(authority, "baseline.json"),
    "--scanner",
    resolve(authority, "node_modules/jscpd/run-jscpd.js"),
    "--no-summary",
  ]);
  assert.deepEqual(
    comparisonMetrics(compatible),
    comparisonMetrics(trusted),
    "Candidate checker disagrees with the trusted checker under master policy",
  );
  if (policyChanged) {
    // Also gate head against the real base under the proposed new rules. The
    // inherited baseline stays untouched; only successful master CI migrates it.
    candidate(resolve(output, "proposed-policy"));
  }
  status("passed");
  console.log(
    "Candidate checker matches master policy" +
      (policyChanged
        ? "; proposed policy also passes its base comparison."
        : "."),
  );
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
        "prepare-migration": { type: "boolean" },
        reason: { type: "string" },
      },
    });
    if (!values.root || !values.base)
      throw new Error("--root and --base are required");
    if (values["prepare-migration"]) prepareMigration(values);
    else {
      if (!values.output) throw new Error("--output is required");
      verify(values);
    }
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
