#!/usr/bin/env node

const { spawnSync } = require("child_process");
const { ensureInstalled } = require("../lib/install");

async function main() {
  const binary = await ensureInstalled(process.env.GGCODE_INSTALL_VERSION, true);
  const result = spawnSync(binary, process.argv.slice(2), { stdio: "inherit" });
  if (result.error) {
    throw result.error;
  }
  process.exit(result.status === null ? 1 : result.status);
}

main().catch((err) => {
  console.error(`ggcode npm wrapper failed: ${err.message}`);
  process.exit(1);
});
