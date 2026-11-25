/**
 * Pure helpers for the download mirror Worker (unit-tested without the Workers runtime).
 */

export const REPO = "topcheer/ggcode";

const TAG_RE = /^v\d+\.\d+\.\d+$/;
// Release asset names: letters, digits, dot, underscore, hyphen only (no slashes, no "%").
const ASSET_RE = /^[A-Za-z0-9][A-Za-z0-9._-]*$/;

export function isValidTag(tag: string | undefined | null): tag is string {
  return !!tag && TAG_RE.test(tag);
}

export function isValidAssetName(name: string): boolean {
  return name.length > 0 && name.length <= 200 && ASSET_RE.test(name);
}

/** `https://github.com/o/r/releases/tag/v1.2.3` -> `v1.2.3`; anything else -> null. */
export function parseTagFromLocation(location: string): string | null {
  const m = location.match(/\/releases\/tag\/(v\d+\.\d+\.\d+)(?:[/?#]|$)/);
  return m ? m[1] : null;
}

export interface ManifestFile {
  name: string;
  size: number; // 0 when unknown (the checksums-based manifest has no sizes)
  url: string;
  github_url: string;
  sha256?: string;
}

export interface Manifest {
  version: string;
  source: string;
  generated_at: string;
  mirror: string;
  files: ManifestFile[];
}

/**
 * Same shape as scripts/release/publish-site-branch.sh writes (version/source/files[].name/url)
 * plus size/github_url, with `url` pointing at this mirror's immutable path.
 */
export function buildManifest(
  tag: string,
  assets: Array<{ name: string; size: number; browser_download_url: string }>,
  origin: string
): Manifest {
  const files = [...assets]
    .filter((a) => isValidAssetName(a.name))
    .sort((a, b) => a.name.localeCompare(b.name))
    .map((a) => ({
      name: a.name,
      size: a.size,
      url: `${origin}/${tag}/${a.name}`,
      github_url: a.browser_download_url,
    }));
  return {
    version: tag,
    source: tag,
    generated_at: new Date().toISOString(),
    mirror: origin,
    files,
  };
}

export interface ChecksumEntry {
  name: string;
  sha256: string;
}

/** Parse GoReleaser's checksums.txt (`<sha256>  <name>` per line, optional `*` before the name). */
export function parseChecksums(text: string): ChecksumEntry[] {
  const out: ChecksumEntry[] = [];
  for (const line of text.split(/\r?\n/)) {
    const m = line.match(/^([0-9a-fA-F]{64})\s+\*?(\S+)\s*$/);
    if (!m || !isValidAssetName(m[2])) continue;
    out.push({ name: m[2], sha256: m[1].toLowerCase() });
  }
  return out;
}

/**
 * Manifest built from checksums.txt only (no GitHub API call, so no rate
 * limit): every asset of the tag with its SHA-256 and mirror URL.
 */
export function buildManifestFromChecksums(tag: string, entries: ChecksumEntry[], origin: string): Manifest {
  const files = [...entries]
    .sort((a, b) => a.name.localeCompare(b.name))
    .map((e) => ({
      name: e.name,
      size: 0,
      url: `${origin}/${tag}/${e.name}`,
      github_url: `https://github.com/${REPO}/releases/download/${tag}/${e.name}`,
      sha256: e.sha256,
    }));
  return {
    version: tag,
    source: tag,
    generated_at: new Date().toISOString(),
    mirror: origin,
    files,
  };
}

/** Route classification for a request path (no trailing slash, origin-relative). */
export type Route =
  | { kind: "health" }
  | { kind: "index" }
  | { kind: "latest"; rest: string }
  | { kind: "versioned"; tag: string; rest: string }
  | { kind: "not-found" };

export function classify(path: string): Route {
  if (path === "/healthz") return { kind: "health" };
  if (path === "" || path === "/") return { kind: "index" };
  if (path === "/latest" || path.startsWith("/latest/")) {
    return { kind: "latest", rest: path.slice("/latest".length) };
  }
  const m = path.match(/^\/(v\d+\.\d+\.\d+)(\/.*)?$/);
  if (m) return { kind: "versioned", tag: m[1], rest: m[2] ?? "" };
  return { kind: "not-found" };
}
