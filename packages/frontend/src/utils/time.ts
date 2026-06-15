export function formatNavClock(date: Date): string {
  const datePart = date.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
  const timePart = date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  return `${datePart} · ${timePart}`;
}
