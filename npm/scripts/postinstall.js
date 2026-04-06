#!/usr/bin/env node

const { ensureInstalled } = require("../lib/install");

ensureInstalled(process.env.GGCODE_INSTALL_VERSION, false).catch((err) => {
  console.warn(`ggcode postinstall warning: ${err.message}`);
  console.warn("The wrapper will try again on first run.");
});
