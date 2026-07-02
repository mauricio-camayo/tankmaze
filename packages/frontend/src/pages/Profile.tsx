import { useState } from 'react';
import Layout, { cardStyle, primaryButtonStyle } from '../components/Layout';
import { updateMyProfile } from '../services/api';
import { useAuthStore } from '../store/authStore';

export default function Profile() {
  const { user, setUser } = useAuthStore();
  const [name, setName] = useState(user?.name ?? '');
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleSave() {
    const trimmed = name.trim();
    if (!trimmed) return;
    setSaving(true);
    setSaved(false);
    setError(null);
    try {
      await updateMyProfile(trimmed);
      if (user) setUser({ ...user, name: trimmed });
      setSaved(true);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to save profile');
    } finally {
      setSaving(false);
    }
  }

  return (
    <Layout>
      <h1 style={{ margin: '0 0 24px', color: '#e2e8f0', fontSize: 22, fontWeight: 700 }}>
        Profile
      </h1>

      <div style={{ ...cardStyle, maxWidth: 440 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 16, marginBottom: 24 }}>
          {user?.picture ? (
            <img
              src={user.picture}
              alt=""
              referrerPolicy="no-referrer"
              style={{ width: 64, height: 64, borderRadius: '50%', objectFit: 'cover' }}
            />
          ) : (
            <div style={{
              width: 64, height: 64, borderRadius: '50%',
              background: '#7c6af7', display: 'flex', alignItems: 'center',
              justifyContent: 'center', fontSize: 24, fontWeight: 700, color: '#fff',
            }}>
              {(user?.name ?? user?.username ?? '?').charAt(0).toUpperCase()}
            </div>
          )}
          <div style={{ color: '#64748b', fontSize: 12, lineHeight: 1.5 }}>
            {user?.picture
              ? 'Picture from your Google/Facebook account.'
              : 'No picture on file. Sign in with Google or Facebook to add one automatically.'}
          </div>
        </div>

        <div style={{ marginBottom: 16 }}>
          <label style={{ fontSize: 13, color: '#94a3b8', display: 'block', marginBottom: 4 }}>Name</label>
          <input
            value={name}
            onChange={(e) => { setName(e.target.value); setSaved(false); }}
            style={{
              width: '100%', background: '#0f0f1a', border: '1px solid #2d2d4e', borderRadius: 6,
              color: '#e2e8f0', padding: '8px 10px', fontSize: 14, boxSizing: 'border-box',
            }}
          />
        </div>

        <div style={{ marginBottom: 20 }}>
          <label style={{ fontSize: 13, color: '#94a3b8', display: 'block', marginBottom: 4 }}>Email</label>
          <input
            value={user?.email ?? ''}
            disabled
            style={{
              width: '100%', background: '#15151f', border: '1px solid #2d2d4e', borderRadius: 6,
              color: '#64748b', padding: '8px 10px', fontSize: 14, boxSizing: 'border-box', cursor: 'not-allowed',
            }}
          />
          <p style={{ margin: '4px 0 0', color: '#475569', fontSize: 11 }}>Email cannot be changed here.</p>
        </div>

        {error && <p style={{ color: '#f87171', fontSize: 13, margin: '0 0 12px' }}>{error}</p>}
        {saved && <p style={{ color: '#4ade80', fontSize: 13, margin: '0 0 12px' }}>Saved.</p>}

        <button
          onClick={handleSave}
          disabled={saving || !name.trim()}
          style={{ ...primaryButtonStyle, opacity: saving || !name.trim() ? 0.6 : 1 }}
        >
          {saving ? 'Saving…' : 'Save changes'}
        </button>
      </div>
    </Layout>
  );
}
