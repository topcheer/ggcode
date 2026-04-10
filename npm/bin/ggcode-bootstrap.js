#!/usr/bin/env node

const { spawnSync } = require("child_process");
const { ensureInstalled } = require("../lib/install");

async function main() {
  const install = await ensureInstalled(process.env.GGCODE_INSTALL_VERSION, true);
  if (install.needsRestart) {
    console.error(`ggcode was installed to ${install.installDir}.`);
    console.error("Reopen your terminal, then run `ggcode` directly.");
  }
  const result = spawnSync(install.binaryPath, process.argv.slice(2), {
    stdio: "inherit",
    env: { ...process.env, GGCODE_WRAPPER_KIND: "npm" },
  });
  if (result.error) {
    throw result.error;
  }
  process.exit(result.status === null ? 1 : result.status);
}

main().catch((err) => {
  console.error(`ggcode npm bootstrap failed: ${err.message}`);
  process.exit(1);
});
