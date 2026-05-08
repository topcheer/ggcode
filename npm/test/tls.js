#!/usr/bin/env node
"use strict";

const path = require("path");
const fs = require("fs");

let passed = 0;
let failed = 0;

function test(name, fn) {
  try {
    fn();
    console.log("  ✓ " + name);
    passed++;
  } catch (e) {
    console.log("  ✗ " + name + ": " + e.message);
    failed++;
  }
}

function assert(condition, msg) {
  if (!condition) throw new Error(msg || "assertion failed");
}

const src = fs.readFileSync(path.join(__dirname, "..", "lib", "install.js"), "utf8");

test("rejectUnauthorized defaults to true (env-gated on '1')", function () {
  var match = src.match(/const INSECURE_TLS\s*=\s*process\.env\.GGCODE_INSECURE_TLS\s*===\s*["']1["']/);
  assert(match, "INSECURE_TLS should be env-gated on '1'");
});

test("TLS_OPTS uses INSECURE_TLS variable", function () {
  var match = src.match(/const TLS_OPTS\s*=\s*\{[^}]*rejectUnauthorized:\s*!INSECURE_TLS/);
  assert(match, "TLS_OPTS should have rejectUnauthorized: !INSECURE_TLS");
});

test("no hard-coded rejectUnauthorized: false anywhere", function () {
  var falseCount = (src.match(/rejectUnauthorized:\s*false/g) || []).length;
  assert(falseCount === 0, "found " + falseCount + " hard-coded rejectUnauthorized: false, expected 0");
});

test("warning printed when GGCODE_INSECURE_TLS=1", function () {
  assert(src.indexOf("GGCODE_INSECURE_TLS=1 is set") !== -1, "should contain insecure TLS warning");
  assert(src.indexOf("TLS certificate verification is DISABLED") !== -1, "should mention disabled verification");
});

test("suggests NODE_EXTRA_CA_CERTS as alternative", function () {
  assert(src.indexOf("NODE_EXTRA_CA_CERTS") !== -1, "should suggest NODE_EXTRA_CA_CERTS");
});

console.log("\n" + passed + " passed, " + failed + " failed");
process.exit(failed > 0 ? 1 : 0);
