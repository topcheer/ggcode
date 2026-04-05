#!/usr/bin/env node

const pkg = require("../package.json");
const { ensureInstalled } = require("../lib/install");

ensureInstalled(process.env.GGCODE_INSTALL_VERSION, pkg.version, false).catch((err) => {
  console.warn(`ggcode postinstall warning: ${err.message}`);
  console.warn("The wrapper will try again on first run.");
});
