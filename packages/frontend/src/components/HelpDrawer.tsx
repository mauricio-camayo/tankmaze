import { useState } from 'react';
import { Link } from 'react-router-dom';

/**
 * Floating, page-context help drawer (item 266 follow-on) — a persistent
 * vertical tab pinned to the left edge of the viewport that slides out a
 * panel of plain-language help for whatever page it's mounted on. Modeled
 * on Hattrick's "Ajuda rápida" drawer. This is a supplementary UX affordance,
 * not a substitute for the static explanatory content item 266 requires on
 * each page: that content must stay visible by default so it reads as real
 * page content to both users and the AdSense review crawler, so this panel
 * stays collapsed until a visitor asks for it and never carries the only
 * copy of the required content.
 */

interface HelpDrawerProps {
  title: string;
  /** Link to the matching section of the public /help guide (e.g. "/help#gamedays"). */
  moreHref?: string;
  children: React.ReactNode;
}

export default function HelpDrawer({ title, moreHref, children }: HelpDrawerProps) {
  const [open, setOpen] = useState(false);

  return (
    <>
      {!open && (
        <button
          onClick={() => setOpen(true)}
          aria-label={`Open help for ${title}`}
          style={{
            position: 'fixed', left: 0, top: '50%', zIndex: 60,
            transform: 'translateY(-50%) rotate(180deg)', writingMode: 'vertical-rl',
            background: 'var(--bp-panel)', border: '1px solid var(--bp-hairline)', borderLeft: 'none',
            color: 'var(--bp-hazard-light)', cursor: 'pointer',
            padding: '14px 7px', fontSize: 11, fontWeight: 600, textTransform: 'uppercase',
            letterSpacing: '0.08em', fontFamily: 'var(--font-mono)',
            display: 'flex', alignItems: 'center', gap: 6,
          }}
        >
          <span aria-hidden="true">?</span> Help
        </button>
      )}

      {open && (
        <>
          <div
            onClick={() => setOpen(false)}
            style={{ position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.5)', zIndex: 65 }}
          />
          <div
            role="dialog"
            aria-label={`${title} help`}
            style={{
              position: 'fixed', left: 0, top: 0, bottom: 0, zIndex: 66,
              width: 'min(380px, 92vw)', overflowY: 'auto',
              background: 'var(--bp-panel)', borderRight: '1px solid var(--bp-hairline)',
              boxShadow: '10px 0 28px rgba(0,0,0,0.35)', padding: '22px 24px 32px',
            }}
          >
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', marginBottom: 14 }}>
              <span style={{ color: 'var(--bp-steel-dim)', fontSize: 11, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.06em' }}>
                Help
              </span>
              <button
                onClick={() => setOpen(false)}
                aria-label="Close help"
                style={{ background: 'none', border: 'none', color: 'var(--bp-steel)', cursor: 'pointer', fontSize: 20, lineHeight: 1, padding: 0 }}
              >
                ×
              </button>
            </div>
            <h2 style={{ margin: '0 0 16px', color: 'var(--bp-line)', fontSize: 19, fontWeight: 700 }}>
              {title}
            </h2>
            {children}
            {moreHref && (
              <div style={{ marginTop: 20, paddingTop: 16, borderTop: '1px solid var(--bp-hairline)' }}>
                <Link
                  to={moreHref}
                  onClick={() => setOpen(false)}
                  style={{ color: 'var(--bp-hazard-light)', fontSize: 13, fontWeight: 600, textDecoration: 'none' }}
                >
                  Full help guide →
                </Link>
              </div>
            )}
          </div>
        </>
      )}
    </>
  );
}

export function HelpSection({ heading, children }: { heading: string; children: React.ReactNode }) {
  return (
    <div style={{ marginBottom: 18 }}>
      <h3 style={{ margin: '0 0 6px', color: 'var(--bp-hazard-light)', fontSize: 12.5, fontWeight: 600, textTransform: 'uppercase', letterSpacing: '0.04em' }}>
        {heading}
      </h3>
      <div style={{ color: 'var(--bp-steel)', fontSize: 13.5, lineHeight: 1.6 }}>
        {children}
      </div>
    </div>
  );
}
