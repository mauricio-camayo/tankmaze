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
      <label style={{ display: 'block', color: '#7fa2ba', fontSize: 13, marginBottom: 6 }}>
        {label}
      </label>
      <input
        type="text"
        value={(config[key] as string) ?? ''}
        placeholder={placeholder}
        onChange={(e) => setConfig((c) => ({ ...c, [key]: e.target.value }))}
        style={{
          width: '100%', background: '#0a3550', border: '1px solid #23577a',
          borderRadius: 0, color: '#e7f1f7', padding: '8px 12px', fontSize: 14,
          boxSizing: 'border-box',
        }}
      />
    </div>
  );

  return (
    <>
      <h2 style={{ margin: '0 0 20px', color: '#e7f1f7' }}>Ads</h2>

      {loading && <div style={{ color: '#5b87a3' }}>Loading…</div>}
      {error && <div style={{ color: '#ff8a75', marginBottom: 12 }}>{error}</div>}
      {saved && <div style={{ color: '#59e6c0', marginBottom: 12 }}>Saved.</div>}

      {!loading && (
        <div style={cardStyle}>
          <div style={{ marginBottom: 20 }}>
            <label style={{ display: 'flex', alignItems: 'center', gap: 10, cursor: 'pointer', minHeight: 44 }}>
              <input
                type="checkbox"
                checked={config.enabled ?? false}
                onChange={(e) => setConfig((c) => ({ ...c, enabled: e.target.checked }))}
                style={{ width: 18, height: 18, accentColor: '#ff7a29' }}
              />
              <span style={{ color: '#e7f1f7', fontSize: 15 }}>Enable ad display globally</span>
            </label>
            <p style={{ margin: '6px 0 0 28px', color: '#5b87a3', fontSize: 12 }}>
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
