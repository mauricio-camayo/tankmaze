// Item 253: the backend's auto-generated display name formats its date
// suffix in UTC (`gameDayDisplayName` in tank-api), while every schedule
// timestamp shown elsewhere on the page renders in the viewer's local
// timezone (`toLocaleString`/`toLocaleDateString`, no explicit `timeZone`).
// For a viewer in a UTC-negative timezone, a same-day event that crosses
// local midnight relative to UTC can end up with a name showing a different
// calendar day than everything else on the page.
//
// Fix: keep the backend's stored `name` exactly as-is (other logic —
// PATCH's base-name preservation, search-by-name — depends on it staying
// stable), and recompute the *displayed* date suffix here, client-side, in
// the viewer's local time, mirroring gameDayDisplayName's own shape.

/** Strip the " · <date suffix>" appended by the backend. */
export function gameDayBaseName(displayName: string): string {
  const idx = displayName.lastIndexOf(' · ');
  return idx >= 0 ? displayName.slice(0, idx) : displayName;
}

function monthDay(iso: string): string {
  return new Date(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
}

/** Recomputes gd.name's date suffix in the viewer's local time. */
export function localGameDayName(name: string | undefined, roundRobinISO: string, finalISO: string): string {
  const base = gameDayBaseName(name ?? '');
  const rrDate = monthDay(roundRobinISO);
  const finalDate = monthDay(finalISO);
  const suffix = rrDate === finalDate ? rrDate : `${rrDate} – ${finalDate}`;
  return base ? `${base} · ${suffix}` : suffix;
}
