const crypto = require("crypto");
const fs = require("fs");
const https = require("https");
const os = require("os");
const path = require("path");
const { execFileSync } = require("child_process");

const OWNER = "topcheer";
const REPO = "ggcode";
const BINARY = process.platform === "win32" ? "ggcode.exe" : "ggcode";

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

function cacheRoot() {
  if (process.platform === "win32") {
    return path.join(process.env.LOCALAPPDATA || os.tmpdir(), "ggcode", "npm");
  }
  return path.join(os.homedir(), ".cache", "ggcode", "npm");
}

function installRoot(version, target) {
  return path.join(cacheRoot(), version, `${target.platform}-${process.arch}`);
}

function binaryPath(version, target) {
  return path.join(installRoot(version, target), target.binaryName);
}

async function ensureInstalled(version, quiet) {
  const requestedVersion = normalizeVersion(version);
  const resolvedVersion = await resolveReleaseVersion(requestedVersion);
  const target = resolveTarget();
  const dest = binaryPath(resolvedVersion, target);
  if (fs.existsSync(dest)) {
    return dest;
  }

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
    process.stderr.write(`Downloading ggcode ${resolvedVersion} from GitHub Releases...\n`);
  }

  extractArchive(target, archivePath, extractDir);
  const extracted = findBinary(extractDir, target.binaryName);
  if (!extracted) {
    throw new Error(`Could not find ${target.binaryName} inside ${target.archiveName}`);
  }

  const root = installRoot(resolvedVersion, target);
  fs.mkdirSync(root, { recursive: true });
  fs.copyFileSync(extracted, dest);
  if (process.platform !== "win32") {
    fs.chmodSync(dest, 0o755);
  }
  return dest;
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
    const command = `Expand-Archive -LiteralPath '${archivePath.replace(/'/g, "''")}' -DestinationPath '${extractDir.replace(/'/g, "''")}' -Force`;
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
            get(res.headers.location);
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
};
