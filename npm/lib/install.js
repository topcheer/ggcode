const crypto = require("crypto");
const fs = require("fs");
const https = require("https");
const os = require("os");
const path = require("path");
const { execFileSync } = require("child_process");

const OWNER = "topcheer";
const REPO = "ggcode";
const BINARY = process.platform === "win32" ? "ggcode.exe" : "ggcode";
const MARKER_START = "# >>> ggcode PATH >>>";
const MARKER_END = "# <<< ggcode PATH <<<";
const METADATA = ".ggcode-wrapper.json";

function normalizeVersion(version) {
  const selected = (version || "").trim();
  if (!selected || selected === "latest") {
    return "latest";
  }
  return selected.startsWith("v") ? selected : `v${selected}`;
}

function resolveTarget() {
  const platform = process.platform;
  let arch = process.arch;
  if (arch === "x64") {
    arch = "x86_64";
  } else if (arch === "arm64") {
    arch = "arm64";
  } else {
    throw new Error(`Unsupported architecture: ${process.arch}`);
  }

  let ext = ".tar.gz";
  if (platform === "win32") {
    ext = ".zip";
  } else if (platform !== "linux" && platform !== "darwin") {
    throw new Error(`Unsupported platform: ${platform}`);
  }

  return {
    platform,
    archiveName: `ggcode_${platform}_${arch}${ext}`,
    archiveExt: ext,
    binaryName: BINARY,
  };
}

function releaseBase(version) {
  if (version === "latest") {
    return `https://github.com/${OWNER}/${REPO}/releases/latest/download`;
  }
  return `https://github.com/${OWNER}/${REPO}/releases/download/${version}`;
}

function preferredInstallDirs() {
  const home = os.homedir();
  if (process.platform === "win32") {
    return [
      path.join(home, "AppData", "Local", "Programs", "ggcode", "bin"),
      path.join(home, ".local", "bin"),
    ];
  }
  return ["/usr/local/bin", path.join(home, ".local", "bin")];
}

function metadataPath(dir) {
  return path.join(dir, METADATA);
}

function pathEntries(value) {
  return (value || "")
    .split(path.delimiter)
    .map((entry) => entry.trim())
    .filter(Boolean);
}

function samePath(a, b) {
  const normalize = (value) => {
    let result = path.resolve(value);
    if (process.platform === "win32") {
      result = result.replace(/[\\/]+$/, "").toLowerCase();
    }
    return result;
  };
  return normalize(a) === normalize(b);
}

function isPermissionError(err) {
  return Boolean(err && ["EACCES", "EPERM"].includes(err.code));
}

function readMetadata(dir) {
  try {
    return JSON.parse(fs.readFileSync(metadataPath(dir), "utf8"));
  } catch {
    return null;
  }
}

function writeMetadata(dir, version) {
  fs.writeFileSync(metadataPath(dir), JSON.stringify({ version }, null, 2) + "\n", "utf8");
}

function findInstalledBinary(requestedVersion, target) {
  for (const dir of preferredInstallDirs()) {
    const binary = path.join(dir, target.binaryName);
    if (!fs.existsSync(binary)) {
      continue;
    }
    if (requestedVersion === "latest") {
      return { dir, binary, version: readMetadata(dir)?.version || "unknown" };
    }
    const metadata = readMetadata(dir);
    if (metadata && metadata.version === requestedVersion) {
      return { dir, binary, version: metadata.version };
    }
  }
  return null;
}

function installBinary(dir, target, extracted, version) {
  fs.mkdirSync(dir, { recursive: true });
  const dest = path.join(dir, target.binaryName);
  const temp = `${dest}.tmp-${process.pid}`;
  fs.copyFileSync(extracted, temp);
  if (process.platform !== "win32") {
    fs.chmodSync(temp, 0o755);
  }
  fs.rmSync(dest, { force: true });
  fs.renameSync(temp, dest);
  writeMetadata(dir, version);
  return dest;
}

function ensureInstalledPath(dir) {
  const currentEntries = pathEntries(process.env.PATH);
  if (currentEntries.some((entry) => samePath(entry, dir))) {
    return false;
  }
  if (process.platform === "win32") {
    return ensureWindowsUserPath(dir);
  }
  return ensureUnixPath(dir);
}

function ensureWindowsUserPath(dir) {
  const script = [
    `$dir = ${powershellString(dir)}`,
    "$current = [Environment]::GetEnvironmentVariable('Path', 'User')",
    "$parts = @()",
    "if ($current) { $parts = $current -split ';' | Where-Object { $_ -and $_.Trim() -ne '' } }",
    "$exists = $parts | Where-Object { $_.TrimEnd('\\\\') -ieq $dir.TrimEnd('\\\\') }",
    "if (-not $exists) {",
    "  $new = @($dir) + $parts",
    "  [Environment]::SetEnvironmentVariable('Path', ($new -join ';'), 'User')",
    "  Write-Output 'updated'",
    "} else {",
    "  Write-Output 'unchanged'",
    "}",
  ].join("; ");
  const output = execFileSync("powershell", ["-NoProfile", "-Command", script], {
    encoding: "utf8",
    stdio: ["ignore", "pipe", "pipe"],
  }).trim();
  process.env.PATH = [dir, ...pathEntries(process.env.PATH)].join(path.delimiter);
  return output === "updated";
}

function ensureUnixPath(dir) {
  const files = profileTargets();
  let changed = false;
  for (const file of files) {
    const before = fs.existsSync(file) ? fs.readFileSync(file, "utf8") : "";
    const after = upsertPathBlock(before, dir);
    if (after !== before) {
      fs.mkdirSync(path.dirname(file), { recursive: true });
      fs.writeFileSync(file, after, "utf8");
      changed = true;
    }
  }
  process.env.PATH = [dir, ...pathEntries(process.env.PATH)].join(path.delimiter);
  return changed;
}

function profileTargets() {
  const home = os.homedir();
  const shell = path.basename(process.env.SHELL || "");
  const preferred = [];
  if (shell === "zsh") {
    preferred.push(".zshrc", ".zprofile");
  } else if (shell === "bash") {
    preferred.push(".bashrc", ".bash_profile");
  }
  preferred.push(".profile");

  const existing = [".zshrc", ".zprofile", ".bashrc", ".bash_profile", ".profile"].filter((name) =>
    fs.existsSync(path.join(home, name)),
  );
  const targets = [];
  for (const name of [...preferred, ...existing]) {
    const file = path.join(home, name);
    if (!targets.some((entry) => samePath(entry, file))) {
      targets.push(file);
    }
  }
  return targets;
}

function upsertPathBlock(content, dir) {
  const block = `${MARKER_START}\nexport PATH="${dir}:$PATH"\n${MARKER_END}\n`;
  const pattern = new RegExp(`${escapeRegExp(MARKER_START)}[\\s\\S]*?${escapeRegExp(MARKER_END)}\\n?`, "m");
  if (pattern.test(content)) {
    return content.replace(pattern, block);
  }
  const suffix = content && !content.endsWith("\n") ? "\n" : "";
  return `${content}${suffix}${block}`;
}

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

function powershellString(value) {
  return `'${String(value).replace(/'/g, "''")}'`;
}

async function ensureInstalled(version, quiet) {
  const requestedVersion = normalizeVersion(version);
  const target = resolveTarget();
  const existing = findInstalledBinary(requestedVersion, target);
  if (existing) {
    const pathUpdated = ensureInstalledPath(existing.dir);
    return {
      binaryPath: existing.binary,
      installDir: existing.dir,
      version: existing.version,
      pathUpdated,
      installedNow: false,
      needsRestart: pathUpdated,
    };
  }

  const resolvedVersion = await resolveReleaseVersion(requestedVersion);
  const base = releaseBase(resolvedVersion);
  const archiveURL = `${base}/${target.archiveName}`;
  const checksumsURL = `${base}/checksums.txt`;
  const archive = await downloadBuffer(archiveURL);
  const checksums = await downloadText(checksumsURL);
  verifyChecksum(target.archiveName, archive, checksums);

  const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), "ggcode-npm-"));
  const archivePath = path.join(tempDir, target.archiveName);
  const extractDir = path.join(tempDir, "extract");
  fs.mkdirSync(extractDir, { recursive: true });
  fs.writeFileSync(archivePath, archive);

  if (!quiet) {
    process.stderr.write(`Installing ggcode ${resolvedVersion}...\n`);
  }

  extractArchive(target, archivePath, extractDir);
  const extracted = findBinary(extractDir, target.binaryName);
  if (!extracted) {
    throw new Error(`Could not find ${target.binaryName} inside ${target.archiveName}`);
  }

  let installDir = null;
  let usedFallback = false;
  let lastErr = null;
  const installDirs = preferredInstallDirs();
  for (const [index, dir] of installDirs.entries()) {
    try {
      const binaryPath = installBinary(dir, target, extracted, resolvedVersion);
      installDir = dir;
      usedFallback = index > 0;
      const pathUpdated = ensureInstalledPath(dir);
      return {
        binaryPath,
        installDir,
        version: resolvedVersion,
        pathUpdated,
        installedNow: true,
        needsRestart: pathUpdated,
        usedFallback,
      };
    } catch (err) {
      if (!isPermissionError(err) || index === installDirs.length - 1) {
        lastErr = err;
        break;
      }
    }
  }

  throw lastErr || new Error("Failed to install ggcode");
}

async function resolveReleaseVersion(version) {
  if (version !== "latest") {
    return version;
  }

  const latestURL = await resolveFinalURL(`https://github.com/${OWNER}/${REPO}/releases/latest`);
  const match = latestURL.match(/\/releases\/tag\/([^/?#]+)/);
  if (!match) {
    throw new Error(`Could not resolve latest ggcode release from ${latestURL}`);
  }
  return decodeURIComponent(match[1]);
}

function verifyChecksum(assetName, archive, checksumsText) {
  const lines = checksumsText.split(/\r?\n/);
  let expected = null;
  for (const line of lines) {
    const parts = line.trim().split(/\s+/);
    if (parts.length >= 2 && parts[parts.length - 1] === assetName) {
      expected = parts[0];
      break;
    }
  }
  if (!expected) {
    throw new Error(`Checksum for ${assetName} not found`);
  }
  const actual = crypto.createHash("sha256").update(archive).digest("hex");
  if (actual.toLowerCase() !== expected.toLowerCase()) {
    throw new Error(`Checksum mismatch for ${assetName}`);
  }
}

function extractArchive(target, archivePath, extractDir) {
  if (target.archiveExt === ".zip") {
    const command = `Expand-Archive -LiteralPath ${powershellString(archivePath)} -DestinationPath ${powershellString(extractDir)} -Force`;
    execFileSync("powershell", ["-NoProfile", "-Command", command], { stdio: "ignore" });
    return;
  }
  execFileSync("tar", ["-xzf", archivePath, "-C", extractDir], { stdio: "ignore" });
}

function findBinary(dir, binaryName) {
  const entries = fs.readdirSync(dir, { withFileTypes: true });
  for (const entry of entries) {
    const fullPath = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      const nested = findBinary(fullPath, binaryName);
      if (nested) {
        return nested;
      }
      continue;
    }
    if (entry.isFile() && path.basename(entry.name) === binaryName) {
      return fullPath;
    }
  }
  return null;
}

async function downloadText(url) {
  return (await downloadBuffer(url)).toString("utf8");
}

function downloadBuffer(url) {
  return new Promise((resolve, reject) => {
    const get = (target) => {
      https
        .get(target, (res) => {
          if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
            get(new URL(res.headers.location, target).toString());
            return;
          }
          if (res.statusCode !== 200) {
            reject(new Error(`${target} returned ${res.statusCode}`));
            return;
          }
          const chunks = [];
          res.on("data", (chunk) => chunks.push(chunk));
          res.on("end", () => resolve(Buffer.concat(chunks)));
        })
        .on("error", reject);
    };
    get(url);
  });
}

function resolveFinalURL(url) {
  return new Promise((resolve, reject) => {
    const get = (target) => {
      https
        .get(target, (res) => {
          if (res.statusCode >= 300 && res.statusCode < 400 && res.headers.location) {
            res.resume();
            get(new URL(res.headers.location, target).toString());
            return;
          }
          if (res.statusCode !== 200) {
            reject(new Error(`${target} returned ${res.statusCode}`));
            return;
          }
          res.resume();
          resolve(target);
        })
        .on("error", reject);
    };
    get(url);
  });
}

module.exports = {
  ensureInstalled,
  normalizeVersion,
  resolveReleaseVersion,
  resolveTarget,
  upsertPathBlock,
  preferredInstallDirs,
};
