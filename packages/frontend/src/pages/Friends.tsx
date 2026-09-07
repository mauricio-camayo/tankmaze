import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import Layout, { cardStyle, ghostButtonStyle, primaryButtonStyle } from '../components/Layout';
import { listFriends, acceptFriendRequest, rejectFriendRequest, removeFriend } from '../services/api';
import { isUnread } from '../utils/chatUnread';
import HelpDrawer, { HelpSection } from '../components/HelpDrawer';
import DismissibleIntro from '../components/DismissibleIntro';
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
      background: '#ff7a29', display: 'flex', alignItems: 'center',
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
      <Link to={`/users/${entry.userId}`} style={{ flex: 1, minWidth: 0, color: '#e7f1f7', fontSize: 15, textDecoration: 'none' }}>
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
      {/* Item 266: real explanatory content above the interactive list —
          Google AdSense rejected this page as "low value content". Kept
          visible by default so it counts as page content on review. */}
      <DismissibleIntro page="friends" marginBottom={24}>
        <h1 style={{ margin: '0 0 8px', fontSize: 22, fontWeight: 700, color: '#e7f1f7' }}>
          Friends
        </h1>
        <p style={{ margin: 0, color: '#7fa2ba', fontSize: 14, lineHeight: 1.65 }}>
          Friends is TankMaze's lightweight social layer, separate from Game Day competition. Add
          another Tank Author as a friend from their profile page — once they accept, you can send
          each other direct messages. It's built for coordinating test matches, swapping strategy,
          or just talking trash before your tanks meet in a bracket.
        </p>
      </DismissibleIntro>

      <HelpDrawer title="Friends" moreHref="/help#friends">
        <HelpSection heading="Overview">
          Friends is TankMaze's lightweight social layer, separate from Game Day competition. Add
          another Tank Author as a friend from their profile page — once they accept, you can send
          each other direct messages. It's built for coordinating test matches, swapping strategy,
          or just talking trash before your tanks meet in a bracket.
        </HelpSection>
        <HelpSection heading="Adding a friend">
          Friend requests are sent from a Tank Author's profile page. Once you send one it shows up
          as pending until the other person accepts or declines it.
        </HelpSection>
        <HelpSection heading="Requests">
          <strong>Friend requests</strong> are incoming requests waiting on your answer.
          <strong> Sent requests</strong> are ones you sent that are still pending — you can cancel
          those any time before they're answered.
        </HelpSection>
        <HelpSection heading="Messaging">
          Direct messages only work between accepted friends — it keeps the inbox to people you've
          actually chosen to connect with.
        </HelpSection>
      </HelpDrawer>

      {loading && <div style={{ color: '#5b87a3' }}>Loading…</div>}
      {error && <div style={{ color: '#ff8a75', marginBottom: 16 }}>{error}</div>}

      {!loading && data && (
        <>
          {data.incoming.length > 0 && (
            <div style={{ marginBottom: 28 }}>
              <h2 style={{ fontSize: 14, fontWeight: 600, color: '#7fa2ba', textTransform: 'uppercase', letterSpacing: '0.05em', margin: '0 0 12px' }}>
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
            <h2 style={{ fontSize: 14, fontWeight: 600, color: '#7fa2ba', textTransform: 'uppercase', letterSpacing: '0.05em', margin: '0 0 12px' }}>
              Friends
            </h2>
            {data.friends.length === 0 ? (
              <div style={{ ...cardStyle, color: '#5b87a3', textAlign: 'center', padding: '32px 24px' }}>
                No friends yet — add one from a tank author's profile.
              </div>
            ) : (
              data.friends.map((entry) => (
                <FriendRow
                  key={entry.userId}
                  entry={entry}
                  actions={
                    <>
                      <Link to={`/chat/${entry.userId}`} style={{ ...ghostButtonStyle, textDecoration: 'none', position: 'relative' }}>
                        Message
                        {isUnread(entry) && (
                          <span style={{
                            position: 'absolute', top: -3, right: -3, width: 8, height: 8,
                            borderRadius: '50%', background: '#ff7a29',
                          }} />
                        )}
                      </Link>
                      <button
                        onClick={() => withBusy(entry.userId, () => removeFriend(entry.userId))}
                        disabled={busyId === entry.userId}
                        style={{ ...ghostButtonStyle, borderColor: '#3a1a18', color: '#ff8a75' }}
                      >
                        Remove
                      </button>
                    </>
                  }
                />
              ))
            )}
          </div>

          {data.outgoing.length > 0 && (
            <div>
              <h2 style={{ fontSize: 14, fontWeight: 600, color: '#7fa2ba', textTransform: 'uppercase', letterSpacing: '0.05em', margin: '0 0 12px' }}>
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
