// Computes "competition ranking" (1224) positions for a list already sorted
// best-to-worst: tied entries (per isTie) share the lowest rank in their tie
// group, and the next distinct entry's rank skips ahead by the tie-group
// size (e.g. three tanks tied for 4th are all shown "4"; the next entry is
// "7", not "5"). Item 249 — replaces the naive `index + 1` that used to give
// tied tanks arbitrary, distinct-looking ranks based only on array order.
export function competitionRanks<T>(sorted: T[], isTie: (a: T, b: T) => boolean): number[] {
  const ranks: number[] = [];
  sorted.forEach((item, i) => {
    ranks.push(i > 0 && isTie(item, sorted[i - 1]) ? ranks[i - 1] : i + 1);
  });
  return ranks;
}
