// Parses the integer major version out of a "v<N>" string (e.g. "v10" -> 10).
// Mirrors the backend's numeric-safe version parsing — `parseVersion` in
// `cmd/tank-api/main.go` and `parseMajorVersion`/`LatestMajorVersion` in
// `internal/db/versions.go` — so the frontend never re-derives "latest" by
// treating array position or string order as version recency (item 274:
// `ListVersionsByTank` returns versions sorted by DynamoDB sort key, i.e.
// lexicographic string order, not numeric — "v10" sorts before "v2").
// Non-numeric or malformed input sorts as -Infinity so callers that pick an
// array end still get a usable (if wrong) result instead of NaN comparisons.
export function majorVersionNum(v: string): number {
  const n = parseInt(v.replace(/^v/, ''), 10);
  return Number.isNaN(n) ? -Infinity : n;
}

// Sorts major-version strings ascending by numeric value (not lexicographic).
export function sortMajorVersions(versions: string[]): string[] {
  return [...versions].sort((a, b) => majorVersionNum(a) - majorVersionNum(b));
}
