import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import Layout, { cardStyle, primaryButtonStyle } from '../components/Layout';
import { getMySettings, listTanks, updateMyProfile, uploadProfilePicture } from '../services/api';
import { changePassword } from '../services/auth';
import { ALLOWED_IMAGE_TYPES, MAX_AVATAR_BYTES, readFileAsBase64 } from '../components/AvatarPicker';
import { useAuthStore } from '../store/authStore';
import type { UserSettings } from '../types';

const tierColors: Record<string, string> = {
  free: '#64748b',
  builder: '#7c6af7',
  pro: '#f59e0b',
};

const tierLabels: Record<string, string> = {
  free: 'Free',
  builder: 'Builder',
  pro: 'Pro',
};

// Usage status thresholds: yellow kicks in at this fraction of the limit,
// red at 100%+. Configurable via VITE_USAGE_WARNING_THRESHOLD (0-1).
const WARNING_THRESHOLD = Number(import.meta.env.VITE_USAGE_WARNING_THRESHOLD ?? 0.9);

const STATUS_GREEN = '#4ade80';
const STATUS_YELLOW = '#fbbf24';
const STATUS_RED = '#f87171';

function usageStatusColor(value: number, max: number): string {
  const pct = value / Math.max(1, max);
  if (pct >= 1) return STATUS_RED;
  if (pct >= WARNING_THRESHOLD) return STATUS_YELLOW;
  return STATUS_GREEN;
}

function worstStatusColor(colors: string[]): string {
  if (colors.includes(STATUS_RED)) return STATUS_RED;
  if (colors.includes(STATUS_YELLOW)) return STATUS_YELLOW;
  return STATUS_GREEN;
}

function ProgressBar({ value, max, color }: { value: number; max: number; color: string }) {
  const pct = Math.min(100, (value / Math.max(1, max)) * 100);
  return (
    <div style={{ background: '#2d2d4e', borderRadius: 4, height: 8, overflow: 'hidden' }}>
      <div style={{ width: `${pct}%`, background: color, height: '100%', borderRadius: 4, transition: 'width 0.3s' }} />
    </div>
  );
}

function windowResetLabel(windowStart: string): string {
  if (!windowStart) return '';
  const start = new Date(windowStart);
  const resets = new Date(start.getTime() + 30 * 24 * 60 * 60 * 1000);
  const now = new Date();
  const diffMs = resets.getTime() - now.getTime();
  if (diffMs <= 0) return 'resets now';
  const days = Math.ceil(diffMs / (1000 * 60 * 60 * 24));
  return `resets in ${days} day${days === 1 ? '' : 's'}`;
}

export default function Account() {
  const { user, setUser } = useAuthStore();
  const [settings, setSettings] = useState<UserSettings | null>(null);
  const [tankCount, setTankCount] = useState<number>(0);
  const [error, setError] = useState('');

  const [name, setName] = useState(user?.name ?? '');
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [profileError, setProfileError] = useState<string | null>(null);

  const [uploadingPicture, setUploadingPicture] = useState(false);
  const [pictureError, setPictureError] = useState<string | null>(null);
  const pictureInputRef = useRef<HTMLInputElement>(null);

  const [currentPassword, setCurrentPassword] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [confirmNewPassword, setConfirmNewPassword] = useState('');
  const [changingPassword, setChangingPassword] = useState(false);
  const [passwordSaved, setPasswordSaved] = useState(false);
  const [passwordError, setPasswordError] = useState<string | null>(null);

  useEffect(() => {
    Promise.all([getMySettings(), listTanks()])
      .then(([s, tanks]) => {
        setSettings(s);
        setTankCount(tanks.length);
      })
      .catch((e) => setError(e.message));
  }, []);

  async function handleChangePassword(e: React.FormEvent) {
    e.preventDefault();
    setPasswordError(null);
    setPasswordSaved(false);
    // Leaving current password blank simply means "not changing it now" —
    // this form is independent from the profile Save above, so nothing else
    // is blocked by skipping it.
    if (!currentPassword) return;
    if (newPassword !== confirmNewPassword) {
      setPasswordError('New passwords do not match');
      return;
    }
    setChangingPassword(true);
    try {
      await changePassword(currentPassword, newPassword);
      setCurrentPassword('');
      setNewPassword('');
      setConfirmNewPassword('');
      setPasswordSaved(true);
    } catch (e) {
      setPasswordError(e instanceof Error ? e.message : 'Failed to change password');
    } finally {
      setChangingPassword(false);
    }
  }

  async function handleSaveProfile() {
    const trimmed = name.trim();
    if (!trimmed) return;
    setSaving(true);
    setSaved(false);
    setProfileError(null);
    try {
      await updateMyProfile(trimmed);
      if (user) setUser({ ...user, name: trimmed });
      setSaved(true);
    } catch (e) {
      setProfileError(e instanceof Error ? e.message : 'Failed to save profile');
    } finally {
      setSaving(false);
    }
  }

  async function handlePictureChange(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file) return;
    setPictureError(null);
    if (!ALLOWED_IMAGE_TYPES.includes(file.type)) {
      setPictureError('Only PNG or JPEG images are supported.');
      return;
    }
    if (file.size > MAX_AVATAR_BYTES) {
      setPictureError(`Image must be ${Math.floor(MAX_AVATAR_BYTES / 1024)}KB or smaller.`);
      return;
    }
    setUploadingPicture(true);
    try {
      const data = await readFileAsBase64(file);
      const { picture } = await uploadProfilePicture(data, file.type);
      if (user) setUser({ ...user, picture });
    } catch (e) {
      setPictureError(e instanceof Error ? e.message : 'Upload failed');
    } finally {
      setUploadingPicture(false);
    }
  }

  const tier = settings?.tier ?? 'free';
  const tierColor = tierColors[tier] ?? '#7c6af7';

  const tankStatusColor = settings ? usageStatusColor(tankCount, settings.tankLimit) : STATUS_GREEN;
  const compStatusColor = settings ? usageStatusColor(settings.compilationsThisWindow, settings.compilationLimit) : STATUS_GREEN;
  const overallStatusColor = worstStatusColor([tankStatusColor, compStatusColor]);

  return (
    <Layout>
      <h1 style={{ fontSize: 28, fontWeight: 700, color: '#e2e8f0', margin: '0 0 20px' }}>Account</h1>

      {error && (
        <div style={{ ...cardStyle, borderColor: '#7f1d1d', color: '#fca5a5', marginBottom: 16 }}>{error}</div>
      )}

      {/* Profile */}
      <div style={{ ...cardStyle, marginBottom: 20 }}>
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
          <div>
            <input
              ref={pictureInputRef}
              type="file"
              accept="image/png,image/jpeg"
              onChange={handlePictureChange}
              style={{ display: 'none' }}
            />
            <button
              onClick={() => pictureInputRef.current?.click()}
              disabled={uploadingPicture}
              style={{
                background: 'none', border: '1px solid #2d2d4e', color: '#94a3b8',
                borderRadius: 6, padding: '5px 12px', fontSize: 12,
                cursor: uploadingPicture ? 'not-allowed' : 'pointer', marginBottom: 6,
              }}
            >
              {uploadingPicture ? 'Uploading…' : 'Upload photo'}
            </button>
            <div style={{ color: '#64748b', fontSize: 12, lineHeight: 1.5 }}>
              {user?.picture
                ? 'From your Google/Facebook account, or a photo you uploaded.'
                : 'No picture on file — upload one, or sign in with Google/Facebook to add one automatically.'}
              {' '}PNG or JPEG, max 512KB.
            </div>
            {pictureError && <p style={{ color: '#f87171', fontSize: 12, margin: '4px 0 0' }}>{pictureError}</p>}
          </div>
        </div>

        <div style={{ marginBottom: 16 }}>
          <label style={{ fontSize: 13, color: '#94a3b8', display: 'block', marginBottom: 4 }}>Name</label>
          <input
            value={name}
            onChange={(e) => { setName(e.target.value); setSaved(false); }}
            placeholder="Add your name"
            style={{
              width: '100%', background: '#0f0f1a', border: '1px solid #2d2d4e', borderRadius: 6,
              color: '#e2e8f0', padding: '8px 10px', fontSize: 14, boxSizing: 'border-box',
            }}
          />
          {!name && (
            <p style={{ margin: '4px 0 0', color: '#475569', fontSize: 11 }}>
              No name set yet — add one so other players see it instead of your account ID.
            </p>
          )}
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

        {profileError && <p style={{ color: '#f87171', fontSize: 13, margin: '0 0 12px' }}>{profileError}</p>}
        {saved && <p style={{ color: '#4ade80', fontSize: 13, margin: '0 0 12px' }}>Saved.</p>}

        <button
          onClick={handleSaveProfile}
          disabled={saving || !name.trim()}
          style={{ ...primaryButtonStyle, opacity: saving || !name.trim() ? 0.6 : 1 }}
        >
          {saving ? 'Saving…' : 'Save changes'}
        </button>
      </div>

      {/* Password change — email+password accounts only; Google/Facebook
          sign-ins have no Cognito password to change. */}
      {!user?.isFederated && (
        <div style={{ ...cardStyle, marginBottom: 20 }}>
          <h2 style={{ margin: '0 0 4px', fontSize: 16, fontWeight: 700, color: '#e2e8f0' }}>Change password</h2>
          <p style={{ margin: '0 0 16px', color: '#64748b', fontSize: 12, lineHeight: 1.5 }}>
            Leave these fields blank to keep your current password — this doesn't affect saving your name or photo above.
          </p>
          <form onSubmit={handleChangePassword}>
            <div style={{ marginBottom: 12 }}>
              <label style={{ fontSize: 13, color: '#94a3b8', display: 'block', marginBottom: 4 }}>Current password</label>
              <input
                type="password"
                value={currentPassword}
                onChange={(e) => { setCurrentPassword(e.target.value); setPasswordSaved(false); }}
                style={{
                  width: '100%', background: '#0f0f1a', border: '1px solid #2d2d4e', borderRadius: 6,
                  color: '#e2e8f0', padding: '8px 10px', fontSize: 14, boxSizing: 'border-box',
                }}
              />
            </div>
            <div style={{ marginBottom: 12 }}>
              <label style={{ fontSize: 13, color: '#94a3b8', display: 'block', marginBottom: 4 }}>New password</label>
              <input
                type="password"
                value={newPassword}
                onChange={(e) => { setNewPassword(e.target.value); setPasswordSaved(false); }}
                minLength={8}
                style={{
                  width: '100%', background: '#0f0f1a', border: '1px solid #2d2d4e', borderRadius: 6,
                  color: '#e2e8f0', padding: '8px 10px', fontSize: 14, boxSizing: 'border-box',
                }}
              />
            </div>
            <div style={{ marginBottom: 16 }}>
              <label style={{ fontSize: 13, color: '#94a3b8', display: 'block', marginBottom: 4 }}>Confirm new password</label>
              <input
                type="password"
                value={confirmNewPassword}
                onChange={(e) => { setConfirmNewPassword(e.target.value); setPasswordSaved(false); }}
                minLength={8}
                style={{
                  width: '100%', background: '#0f0f1a', border: '1px solid #2d2d4e', borderRadius: 6,
                  color: '#e2e8f0', padding: '8px 10px', fontSize: 14, boxSizing: 'border-box',
                }}
              />
            </div>
            {passwordError && <p style={{ color: '#f87171', fontSize: 13, margin: '0 0 12px' }}>{passwordError}</p>}
            {passwordSaved && <p style={{ color: '#4ade80', fontSize: 13, margin: '0 0 12px' }}>Password changed.</p>}
            <button
              type="submit"
              disabled={changingPassword || !currentPassword || !newPassword || !confirmNewPassword}
              style={{
                ...primaryButtonStyle,
                opacity: changingPassword || !currentPassword || !newPassword || !confirmNewPassword ? 0.6 : 1,
              }}
            >
              {changingPassword ? 'Changing…' : 'Change password'}
            </button>
          </form>
        </div>
      )}

      {settings && (
        <>
          {/* Tier badge + usage */}
          <div style={{ ...cardStyle, marginBottom: 20 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 16 }}>
              <span style={{
                background: tierColor,
                color: '#fff',
                borderRadius: 20,
                padding: '4px 16px',
                fontWeight: 700,
                fontSize: 16,
                letterSpacing: 1,
                textTransform: 'uppercase',
              }}>
                {tierLabels[tier] ?? tier}
              </span>
              <span style={{ color: '#64748b', fontSize: 14, display: 'flex', alignItems: 'center', gap: 8 }}>
                <span style={{
                  width: 8, height: 8, borderRadius: '50%', background: overallStatusColor, flexShrink: 0,
                }} />
                Current plan
              </span>
            </div>

            {/* Tank usage */}
            <div style={{ marginBottom: 16 }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 6, fontSize: 14 }}>
                <span style={{ color: '#94a3b8' }}>Tanks</span>
                <span style={{ color: '#e2e8f0', fontWeight: 600 }}>
                  {tankCount} / {settings.tankLimit}
                </span>
              </div>
              <ProgressBar value={tankCount} max={settings.tankLimit} color={tankStatusColor} />
            </div>

            {/* Compilation usage */}
            <div>
              <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 6, fontSize: 14 }}>
                <span style={{ color: '#94a3b8' }}>
                  Compilations
                  {settings.windowStart && (
                    <span style={{ color: '#64748b', marginLeft: 8, fontSize: 12 }}>
                      ({windowResetLabel(settings.windowStart)})
                    </span>
                  )}
                </span>
                <span style={{ color: '#e2e8f0', fontWeight: 600 }}>
                  {settings.compilationsThisWindow} / {settings.compilationLimit}
                </span>
              </div>
              <ProgressBar value={settings.compilationsThisWindow} max={settings.compilationLimit} color={compStatusColor} />
            </div>
          </div>

          {/* Upgrade CTA */}
          {tier !== 'pro' && (
            <div style={{ ...cardStyle, textAlign: 'center', padding: '28px 24px' }}>
              <p style={{ color: '#94a3b8', marginBottom: 16, fontSize: 15 }}>
                {tier === 'free'
                  ? 'Upgrade to Builder for 5 tanks and 50 compilations per month.'
                  : 'Upgrade to Pro for 15 tanks and 200 compilations per month.'}
              </p>
              <Link to="/upgrade">
                <button style={{ ...primaryButtonStyle, fontSize: 15, padding: '10px 28px' }}>
                  Upgrade Plan
                </button>
              </Link>
            </div>
          )}
        </>
      )}
    </Layout>
  );
}
