// Strips the "#N" suffix Game Day autofill appends to a built-in AI's
// TankID when the same AI is registered more than once in one event
// (item 248) — e.g. "builtin-scout#2" → "builtin-scout". The suffix exists
// only for that event's own standings/seeding/bracket-tier bookkeeping; it
// is never a real tank record, so any link to a tank's own page must
// resolve to the real ID.
export function realTankId(tankId: string): string {
  const i = tankId.indexOf('#');
  return i === -1 ? tankId : tankId.slice(0, i);
}
