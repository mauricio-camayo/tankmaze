import { useEffect, useState } from 'react';
import { useParams, Link } from 'react-router-dom';
import Layout, { cardStyle } from '../components/Layout';
import { getPublicUserProfile } from '../services/api';
import { avatarSrc } from '../components/AvatarPicker';
import type { PublicUserProfile } from '../types';

function relativeTime(ts: number | null): string {
  if (!ts) return '—';
  const days = Math.floor((Date.now() / 1000 - ts) / 86400);
  if (days === 0) return 'today';
  if (days === 1) return '1 day ago';
  if (days < 30) return `${days} days ago`;
  const months = Math.floor(days / 30);
  return months === 1 ? '1 month ago' : `${months} months ago`;
}

function ordinal(n: number | null): string {
  if (n === null) return '—';
  const s = ['th', 'st', 'nd', 'rd'];
  const v = n % 100;
  return `${n}${s[(v - 20) % 10] ?? s[v] ?? s[0]}`;
}

export default function UserProfile() {
  const { sub } = useParams<{ sub: string }>();
  const [profile, setProfile] = useState<PublicUserProfile | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!sub) return;
    getPublicUserProfile(sub)
      .then(setProfile)
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false));
  }, [sub]);

  if (loading) return <Layout><p style={{ color: '#64748b' }}>Loading…</p></Layout>;
  if (error || !profile) return <Layout><p style={{ color: '#f87171' }}>{error ?? 'User not found'}</p></Layout>;

  return (
    <Layout>
      <div style={{ display: 'flex', alignItems: 'center', gap: 16, marginBottom: 28 }}>
        {profile.picture ? (
          <img
            src={profile.picture}
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
            {profile.name.charAt(0).toUpperCase()}
          </div>
        )}
        <div>
          <h1 style={{ fontSize: 24, fontWeight: 700, color: '#e2e8f0', margin: 0 }}>{profile.name}</h1>
          <p style={{ color: '#64748b', fontSize: 13, margin: '2px 0 0' }}>
            {profile.tanks.length} tank{profile.tanks.length === 1 ? '' : 's'}
          </p>
        </div>
      </div>

      {profile.tanks.length === 0 ? (
        <div style={{ ...cardStyle, color: '#64748b', textAlign: 'center', padding: '40px 24px' }}>
          No tanks yet.
        </div>
      ) : (
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          {profile.tanks.map((t) => (
            <Link
              key={t.tankId}
              to={`/tanks/${t.tankId}`}
              style={{ ...cardStyle, display: 'flex', alignItems: 'center', gap: 14, textDecoration: 'none' }}
            >
              <img
                src={avatarSrc(t.tankId, t.avatarUrl)}
                alt=""
                style={{ width: 40, height: 40, borderRadius: 8, imageRendering: 'pixelated', flexShrink: 0 }}
              />
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ color: '#e2e8f0', fontSize: 15 }}>{t.name || 'Unnamed Tank'}</div>
                <div style={{ color: '#64748b', fontSize: 12 }}>Last active {relativeTime(t.lastActiveAt)}</div>
              </div>
              <div style={{ display: 'grid', gridTemplateColumns: 'auto auto', gap: '2px 14px', fontSize: 12, flexShrink: 0 }}>
                <span style={{ color: '#64748b' }}>Score</span>
                <span style={{ color: '#a78bfa', fontWeight: 600, textAlign: 'right' }}>{t.globalScore.toLocaleString()}</span>
                <span style={{ color: '#64748b' }}>Best</span>
                <span style={{ color: '#e2e8f0', textAlign: 'right' }}>{ordinal(t.bestFinish)}</span>
                <span style={{ color: '#64748b' }}>Days</span>
                <span style={{ color: '#e2e8f0', textAlign: 'right' }}>{t.gameDaysCount}</span>
              </div>
            </Link>
          ))}
        </div>
      )}
    </Layout>
  );
}
