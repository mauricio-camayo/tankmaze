import { useEffect, useState } from 'react';
import { useParams, Link } from 'react-router-dom';
import Layout, { cardStyle, primaryButtonStyle, ghostButtonStyle } from '../components/Layout';
import { getPublicUserProfile, listFriends, sendFriendRequest, acceptFriendRequest, rejectFriendRequest, removeFriend, blockUser, unblockUser } from '../services/api';
import { avatarSrc } from '../components/AvatarPicker';
import { useAuthStore } from '../store/authStore';
import type { PublicUserProfile } from '../types';

// 'blocked' means the CURRENT viewer placed the block — only the blocker
// sees this state (item 226); the blocked party just sees a normal profile
// and their actions fail generically, so as not to reveal the block.
type FriendStatus = 'none' | 'friends' | 'incoming' | 'outgoing' | 'blocked';

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
  const currentUser = useAuthStore((s) => s.user);
  const [profile, setProfile] = useState<PublicUserProfile | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [friendStatus, setFriendStatus] = useState<FriendStatus>('none');
  const [friendBusy, setFriendBusy] = useState(false);
  const [friendError, setFriendError] = useState<string | null>(null);

  useEffect(() => {
    if (!sub) return;
    getPublicUserProfile(sub)
      .then(setProfile)
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false));
  }, [sub]);

  function refreshFriendStatus() {
    if (!sub || !currentUser || sub === currentUser.userId) return;
    listFriends().then((data) => {
      if (data.blocked.some((f) => f.userId === sub)) setFriendStatus('blocked');
      else if (data.friends.some((f) => f.userId === sub)) setFriendStatus('friends');
      else if (data.incoming.some((f) => f.userId === sub)) setFriendStatus('incoming');
      else if (data.outgoing.some((f) => f.userId === sub)) setFriendStatus('outgoing');
      else setFriendStatus('none');
    }).catch(() => { /* leave as 'none' — non-critical */ });
  }

  useEffect(refreshFriendStatus, [sub, currentUser]);

  async function handleFriendAction(action: () => Promise<unknown>) {
    setFriendBusy(true);
    setFriendError(null);
    try {
      await action();
      refreshFriendStatus();
    } catch (e) {
      setFriendError(e instanceof Error ? e.message : 'Action failed');
    } finally {
      setFriendBusy(false);
    }
  }

  if (loading) return <Layout><p style={{ color: '#5b87a3' }}>Loading…</p></Layout>;
  if (error || !profile) return <Layout><p style={{ color: '#ff8a75' }}>{error ?? 'User not found'}</p></Layout>;

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
            background: '#ff7a29', display: 'flex', alignItems: 'center',
            justifyContent: 'center', fontSize: 24, fontWeight: 700, color: '#fff',
          }}>
            {profile.name.charAt(0).toUpperCase()}
          </div>
        )}
        <div style={{ flex: 1 }}>
          <h1 style={{ fontSize: 24, fontWeight: 700, color: '#e7f1f7', margin: 0 }}>{profile.name}</h1>
          <p style={{ color: '#5b87a3', fontSize: 13, margin: '2px 0 0' }}>
            {profile.tanks.length} tank{profile.tanks.length === 1 ? '' : 's'}
          </p>
        </div>
        {currentUser && sub !== currentUser.userId && (
          <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'flex-end', gap: 6 }}>
            {friendStatus === 'blocked' ? (
              <button
                onClick={() => handleFriendAction(() => unblockUser(sub!))}
                disabled={friendBusy}
                style={ghostButtonStyle}
              >
                Blocked · Unblock
              </button>
            ) : (
              <>
                <div style={{ display: 'flex', gap: 8 }}>
                  {friendStatus === 'none' && (
                    <button
                      onClick={() => handleFriendAction(() => sendFriendRequest(sub!))}
                      disabled={friendBusy}
                      style={primaryButtonStyle}
                    >
                      Add friend
                    </button>
                  )}
                  {friendStatus === 'outgoing' && (
                    <button
                      onClick={() => handleFriendAction(() => removeFriend(sub!))}
                      disabled={friendBusy}
                      style={ghostButtonStyle}
                    >
                      Cancel request
                    </button>
                  )}
                  {friendStatus === 'incoming' && (
                    <>
                      <button
                        onClick={() => handleFriendAction(() => acceptFriendRequest(sub!))}
                        disabled={friendBusy}
                        style={primaryButtonStyle}
                      >
                        Accept
                      </button>
                      <button
                        onClick={() => handleFriendAction(() => rejectFriendRequest(sub!))}
                        disabled={friendBusy}
                        style={ghostButtonStyle}
                      >
                        Decline
                      </button>
                    </>
                  )}
                  {friendStatus === 'friends' && (
                    <button
                      onClick={() => handleFriendAction(() => removeFriend(sub!))}
                      disabled={friendBusy}
                      style={{ ...ghostButtonStyle, borderColor: '#3a1a18', color: '#ff8a75' }}
                    >
                      Remove friend
                    </button>
                  )}
                </div>
                <button
                  onClick={() => handleFriendAction(() => blockUser(sub!))}
                  disabled={friendBusy}
                  style={{ background: 'none', border: 'none', color: '#5b87a3', fontSize: 12, cursor: 'pointer', padding: 0 }}
                >
                  Block user
                </button>
              </>
            )}
          </div>
        )}
      </div>
      {friendError && <p style={{ color: '#ff8a75', fontSize: 13, margin: '0 0 16px' }}>{friendError}</p>}

      {profile.tanks.length === 0 ? (
        <div style={{ ...cardStyle, color: '#5b87a3', textAlign: 'center', padding: '40px 24px' }}>
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
                style={{ width: 40, height: 40, borderRadius: 0, imageRendering: 'pixelated', flexShrink: 0 }}
              />
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ color: '#e7f1f7', fontSize: 15 }}>{t.name || 'Unnamed Tank'}</div>
                <div style={{ color: '#5b87a3', fontSize: 12 }}>Last active {relativeTime(t.lastActiveAt)}</div>
              </div>
              <div style={{ display: 'grid', gridTemplateColumns: 'auto auto', gap: '2px 14px', fontSize: 12, flexShrink: 0 }}>
                <span style={{ color: '#5b87a3' }}>Score</span>
                <span style={{ color: '#ffab6b', fontWeight: 600, textAlign: 'right' }}>{t.globalScore.toLocaleString()}</span>
                <span style={{ color: '#5b87a3' }}>Best</span>
                <span style={{ color: '#e7f1f7', textAlign: 'right' }}>{ordinal(t.bestFinish)}</span>
                <span style={{ color: '#5b87a3' }}>Days</span>
                <span style={{ color: '#e7f1f7', textAlign: 'right' }}>{t.gameDaysCount}</span>
              </div>
            </Link>
          ))}
        </div>
      )}
    </Layout>
  );
}
