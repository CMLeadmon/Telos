/**
 * This client's version. Must track the "version" field in frontend/package.json;
 * the static export has no runtime access to that file, so it is restated here.
 */
export const CLIENT_VERSION = "0.1.0";

/**
 * Compares dotted numeric versions. Returns <0, 0 or >0 like a comparator.
 * Non-numeric or absent segments count as 0, so "1.2" and "1.2.0" are equal and
 * a malformed version sorts low rather than throwing on a screen whose whole job
 * is to tell the member whether they can connect.
 */
export function compareVersions(a: string, b: string): number {
  const parse = (v: string) =>
    v
      .trim()
      .replace(/^v/, "")
      .split(/[.+-]/)
      .map((part) => {
        const n = Number.parseInt(part, 10);
        return Number.isFinite(n) ? n : 0;
      });
  const left = parse(a);
  const right = parse(b);
  const width = Math.max(left.length, right.length);
  for (let i = 0; i < width; i++) {
    const diff = (left[i] ?? 0) - (right[i] ?? 0);
    if (diff !== 0) return diff < 0 ? -1 : 1;
  }
  return 0;
}

/** True when this client is older than the node's advertised minimum. */
export function clientBelowMinimum(minClientVersion: string | null): boolean {
  if (!minClientVersion) return false;
  return compareVersions(CLIENT_VERSION, minClientVersion) < 0;
}
