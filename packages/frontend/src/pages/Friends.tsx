import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import Layout, { cardStyle, ghostButtonStyle, primaryButtonStyle } from '../components/Layout';
import { listFriends, acceptFriendRequest, rejectFriendRequest, removeFriend } from '../services/api';
import type { FriendEntry, FriendsResponse } from '../types';

function FriendAvatar({ name, picture }: { name: string; picture?: string }) {
  if (picture) {
    return (
      <img
        src={picture}
        alt=""
        referrerPolicy="no-referrer"
        style={{ width: 40, height: 40, borderRadius: '50%', objectFit: 'cover', flexShrink: 0 }}
      />
    );
  }
  return (
    <div style={{
      width: 40, height: 40, borderRadius: '50%', flexShrink: 0,
      background: '#7c6af7', display: 'flex', alignItems: 'center',
      justifyContent: 'center', fontSize: 16, fontWeight: 700, color: '#fff',
    }}>
      {name.charAt(0).toUpperCase()}
    </div>
  );
}

function FriendRow({ entry, actions }: { entry: FriendEntry; actions: React.ReactNode }) {
  return (
    <div style={{ ...cardStyle, display: 'flex', alignItems: 'center', gap: 14, marginBottom: 10 }}>
      <FriendAvatar name={entry.name} picture={entry.picture} />
      <Link to={`/users/${entry.userId}`} style={{ flex: 1, minWidth: 0, color: '#e2e8f0', fontSize: 15, textDecoration: 'none' }}>
        {entry.name}
      </Link>
      <div style={{ display: 'flex', gap: 8, flexShrink: 0 }}>{actions}</div>
    </div>
  );
}

export default function Friends() {
  const [data, setData] = useState<FriendsResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);

  function load() {
    setLoading(true);
    listFriends()
      .then(setData)
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false));
  }

  useEffect(load, []);

  async function withBusy(userId: string, fn: () => Promise<unknown>) {
    setBusyId(userId);
    try {
      await fn();
      load();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Action failed');
    } finally {
      setBusyId(null);
    }
  }

  return (
    <Layout>
      <h1 style={{ margin: '0 0 24px', color: '#e2e8f0', fontSize: 22, fontWeight: 700 }}>Friends</h1>

      {loading && <div style={{ color: '#64748b' }}>Loading…</div>}
      {error && <div style={{ color: '#f87171', marginBottom: 16 }}>{error}</div>}

      {!loading && data && (
        <>
          {data.incoming.length > 0 && (
            <div style={{ marginBottom: 28 }}>
              <h2 style={{ fontSize: 14, fontWeight: 600, color: '#94a3b8', textTransform: 'uppercase', letterSpacing: '0.05em', margin: '0 0 12px' }}>
                Friend requests
              </h2>
              {data.incoming.map((entry) => (
                <FriendRow
                  key={entry.userId}
                  entry={entry}
                  actions={
                    <>
                      <button
                        onClick={() => withBusy(entry.userId, () => acceptFriendRequest(entry.userId))}
                        disabled={busyId === entry.userId}
                        style={primaryButtonStyle}
                      >
                        Accept
                      </button>
                      <button
                        onClick={() => withBusy(entry.userId, () => rejectFriendRequest(entry.userId))}
                        disabled={busyId === entry.userId}
                        style={ghostButtonStyle}
                      >
                        Decline
                      </button>
                    </>
                  }
                />
              ))}
            </div>
          )}

          <div style={{ marginBottom: 28 }}>
            <h2 style={{ fontSize: 14, fontWeight: 600, color: '#94a3b8', textTransform: 'uppercase', letterSpacing: '0.05em', margin: '0 0 12px' }}>
              Friends
            </h2>
            {data.friends.length === 0 ? (
              <div style={{ ...cardStyle, color: '#64748b', textAlign: 'center', padding: '32px 24px' }}>
                No friends yet — add one from a tank author's profile.
              </div>
            ) : (
              data.friends.map((entry) => (
                <FriendRow
                  key={entry.userId}
                  entry={entry}
                  actions={
                    <button
                      onClick={() => withBusy(entry.userId, () => removeFriend(entry.userId))}
                      disabled={busyId === entry.userId}
                      style={{ ...ghostButtonStyle, borderColor: '#7f1d1d', color: '#f87171' }}
                    >
                      Remove
                    </button>
                  }
                />
              ))
            )}
          </div>

          {data.outgoing.length > 0 && (
            <div>
              <h2 style={{ fontSize: 14, fontWeight: 600, color: '#94a3b8', textTransform: 'uppercase', letterSpacing: '0.05em', margin: '0 0 12px' }}>
                Sent requests
              </h2>
              {data.outgoing.map((entry) => (
                <FriendRow
                  key={entry.userId}
                  entry={entry}
                  actions={
                    <button
                      onClick={() => withBusy(entry.userId, () => removeFriend(entry.userId))}
                      disabled={busyId === entry.userId}
                      style={ghostButtonStyle}
                    >
                      Cancel
                    </button>
                  }
                />
              ))}
            </div>
          )}
        </>
      )}
    </Layout>
  );
}
