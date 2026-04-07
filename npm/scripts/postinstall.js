#!/usr/bin/env node

const { ensureInstalled } = require("../lib/install");

ensureInstalled(process.env.GGCODE_INSTALL_VERSION, false)
  .then((install) => {
    const action = install.installedNow ? "installed" : "already available";
    console.warn(`ggcode ${install.version} ${action} at ${install.binaryPath}`);
    if (install.needsRestart) {
      console.warn("Reopen your terminal, then run `ggcode` directly.");
    }
    console.warn("If you ever need to repair the bootstrap flow, run `ggcode-bootstrap`.");
  })
  .catch((err) => {
    console.warn(`ggcode postinstall warning: ${err.message}`);
    console.warn("Run `ggcode-bootstrap` to retry the native binary installation.");
  });
