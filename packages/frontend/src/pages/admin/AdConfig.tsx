import { useEffect, useState } from 'react';
import { cardStyle, primaryButtonStyle, ghostButtonStyle } from '../../components/Layout';
import { adminGetAdConfig, adminUpdateAdConfig, type AdConfigBody } from '../../services/api';

export default function AdminAdConfig() {
  const [config, setConfig] = useState<AdConfigBody & { enabled: boolean }>({
    enabled: false, publisherId: '', topSlotId: '', rightSlotId: '', bottomSlotId: '',
  });
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    adminGetAdConfig()
      .then(setConfig)
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false));
  }, []);

  async function handleSave() {
    setSaving(true);
    setSaved(false);
    setError(null);
    try {
      await adminUpdateAdConfig(config);
      setSaved(true);
    } catch (e: unknown) {
      setError((e as Error).message);
    } finally {
      setSaving(false);
    }
  }

  async function handleReset() {
    setSaving(true);
    setError(null);
    try {
      const blank = { enabled: false, publisherId: '', topSlotId: '', rightSlotId: '', bottomSlotId: '' };
      await adminUpdateAdConfig(blank);
      setConfig(blank);
      setSaved(true);
    } catch (e: unknown) {
      setError((e as Error).message);
    } finally {
      setSaving(false);
    }
  }

  const field = (label: string, key: keyof AdConfigBody, placeholder?: string) => (
    <div style={{ marginBottom: 16 }}>
      <label style={{ display: 'block', color: '#94a3b8', fontSize: 13, marginBottom: 6 }}>
        {label}
      </label>
      <input
        type="text"
        value={(config[key] as string) ?? ''}
        placeholder={placeholder}
        onChange={(e) => setConfig((c) => ({ ...c, [key]: e.target.value }))}
        style={{
          width: '100%', background: '#0f0f1a', border: '1px solid #2d2d4e',
          borderRadius: 6, color: '#e2e8f0', padding: '8px 12px', fontSize: 14,
          boxSizing: 'border-box',
        }}
      />
    </div>
  );

  return (
    <>
      <h2 style={{ margin: '0 0 20px', color: '#e2e8f0' }}>Ads</h2>

      {loading && <div style={{ color: '#64748b' }}>Loading…</div>}
      {error && <div style={{ color: '#f87171', marginBottom: 12 }}>{error}</div>}
      {saved && <div style={{ color: '#4ade80', marginBottom: 12 }}>Saved.</div>}

      {!loading && (
        <div style={cardStyle}>
          <div style={{ marginBottom: 20 }}>
            <label style={{ display: 'flex', alignItems: 'center', gap: 10, cursor: 'pointer', minHeight: 44 }}>
              <input
                type="checkbox"
                checked={config.enabled ?? false}
                onChange={(e) => setConfig((c) => ({ ...c, enabled: e.target.checked }))}
                style={{ width: 18, height: 18, accentColor: '#7c6af7' }}
              />
              <span style={{ color: '#e2e8f0', fontSize: 15 }}>Enable ad display globally</span>
            </label>
            <p style={{ margin: '6px 0 0 28px', color: '#64748b', fontSize: 12 }}>
              When off, no ad script or slot divs are rendered on any page. A page reload is required after changing this setting.
            </p>
          </div>

          {field('Publisher ID (data-ad-client)', 'publisherId', 'ca-pub-XXXXXXXXXXXXXXXX')}
          {field('Top bar slot ID', 'topSlotId', '1234567890')}
          {field('Right rail slot ID (tablet/desktop)', 'rightSlotId', '0987654321')}
          {field('Bottom bar slot ID (mobile)', 'bottomSlotId', '1122334455')}

          <div style={{ display: 'flex', gap: 12, marginTop: 8 }}>
            <button onClick={handleSave} disabled={saving} style={primaryButtonStyle}>
              {saving ? 'Saving…' : 'Save'}
            </button>
            <button onClick={handleReset} disabled={saving} style={ghostButtonStyle}>
              Reset to disabled
            </button>
          </div>
        </div>
      )}
    </>
  );
}
