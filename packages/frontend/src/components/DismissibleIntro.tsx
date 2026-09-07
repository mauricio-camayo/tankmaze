import { useState } from 'react';
import { cardStyle } from './Layout';

/**
 * Wraps the item-266 explanatory intro block on each AdSense-flagged page
 * (Dashboard/Leaderboard/GameDayList/GameDay/Friends) so a returning visitor
 * can dismiss it and free up the space, while a first-time visitor — and
 * critically, an AdSense review crawler, which never carries this browser's
 * localStorage — still sees it expanded by default. Deliberately keyed under
 * "tm-" rather than "tankmaze-": Layout.tsx's sign-out flow wipes every
 * "tankmaze-*" localStorage key (item 222's per-session draft cleanup), and
 * this preference should survive a sign-out/sign-in, not reset with it.
 */

function storageKey(page: string): string {
  return `tm-intro-dismissed-${page}`;
}

function readDismissed(page: string): boolean {
  try {
    return localStorage.getItem(storageKey(page)) === '1';
  } catch {
    return false;
  }
}

export default function DismissibleIntro({ page, marginBottom = 24, children }: { page: string; marginBottom?: number; children: React.ReactNode }) {
  const [dismissed, setDismissed] = useState(() => readDismissed(page));

  if (dismissed) return null;

  function dismiss() {
    try {
      localStorage.setItem(storageKey(page), '1');
    } catch {
      // ignore — worst case it re-shows next visit
    }
    setDismissed(true);
  }

  return (
    <div style={{ ...cardStyle, marginBottom, position: 'relative', paddingRight: 44 }}>
      <button
        onClick={dismiss}
        aria-label="Dismiss — you can still find this in the Help drawer"
        title="Dismiss — you can still find this in the Help drawer"
        style={{
          position: 'absolute', top: 14, right: 14, background: 'none', border: 'none',
          color: '#5b87a3', cursor: 'pointer', fontSize: 20, lineHeight: 1, padding: 4,
        }}
      >
        ×
      </button>
      {children}
    </div>
  );
}
