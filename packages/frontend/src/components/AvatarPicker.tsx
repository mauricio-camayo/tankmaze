import { useState } from 'react';
import { updateTank } from '../services/api';

/** Deterministic default: hash tankId to one of the 16 built-in sprites. */
export function defaultAvatarUrl(tankId: string): string {
  const idx = tankId.split('').reduce((a, c) => a + c.charCodeAt(0), 0) % 16;
  return `/avatars/tank-${idx}.png`;
}

export function avatarSrc(tankId: string, avatarUrl?: string): string {
  return avatarUrl || defaultAvatarUrl(tankId);
}

const ALL_AVATARS = Array.from({ length: 16 }, (_, i) => `/avatars/tank-${i}.png`);

interface Props {
  tankId: string;
  current?: string;
  onSaved: (url: string) => void;
}

export function AvatarPicker({ tankId, current, onSaved }: Props) {
  const [selected, setSelected] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  const effective = selected ?? current;

  async function handleSave() {
    if (!selected || selected === current) return;
    setSaving(true);
    try {
      await updateTank(tankId, { avatarUrl: selected });
      onSaved(selected);
      setSelected(null);
    } finally {
      setSaving(false);
    }
  }

  return (
    <div>
      <div style={{
        display: 'grid',
        gridTemplateColumns: 'repeat(8, 1fr)',
        gap: 6,
        marginBottom: 12,
      }}>
        {ALL_AVATARS.map((url) => {
          const isActive = url === effective;
          return (
            <button
              key={url}
              onClick={() => setSelected(url)}
              title={url.replace('/avatars/', '').replace('.png', '')}
              style={{
                padding: 3,
                border: `2px solid ${isActive ? '#7c6af7' : '#2d2d4e'}`,
                borderRadius: 6,
                background: isActive ? 'rgba(124,106,247,0.12)' : '#1a1a2e',
                cursor: 'pointer',
                lineHeight: 0,
              }}
            >
              <img src={url} alt="" style={{ width: 40, height: 40, display: 'block', imageRendering: 'pixelated' }} />
            </button>
          );
        })}
      </div>
      {selected && selected !== current && (
        <button
          onClick={handleSave}
          disabled={saving}
          style={{
            background: '#7c6af7', color: '#fff', border: 'none',
            borderRadius: 6, padding: '6px 16px', fontSize: 13,
            fontWeight: 600, cursor: saving ? 'not-allowed' : 'pointer',
            opacity: saving ? 0.6 : 1,
          }}
        >
          {saving ? 'Saving…' : 'Save avatar'}
        </button>
      )}
    </div>
  );
}
