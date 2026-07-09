import { useEffect, useRef, useState } from 'react';
import { useParams, Link } from 'react-router-dom';
import Layout, { cardStyle, primaryButtonStyle } from '../components/Layout';
import { getPublicUserProfile, listMessages, sendMessage } from '../services/api';
import { markSeen } from '../utils/chatUnread';
import { useAuthStore } from '../store/authStore';
import type { ChatMessage, PublicUserProfile } from '../types';

const POLL_MS = 3000;

export default function Chat() {
  const { userId } = useParams<{ userId: string }>();
  const currentUser = useAuthStore((s) => s.user);
  const [profile, setProfile] = useState<PublicUserProfile | null>(null);
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [draft, setDraft] = useState('');
  const [sending, setSending] = useState(false);
  const bottomRef = useRef<HTMLDivElement>(null);
  const messagesRef = useRef<ChatMessage[]>([]);
  messagesRef.current = messages;

  useEffect(() => {
    if (!userId) return;
    getPublicUserProfile(userId).catch(() => null).then((p) => p && setProfile(p));
  }, [userId]);

  useEffect(() => {
    if (!userId) return;
    let cancelled = false;
    let pollTimer: ReturnType<typeof setTimeout> | null = null;

    function markLatestSeen(list: ChatMessage[]) {
      const last = list[list.length - 1];
      if (last && userId) markSeen(userId, last.sentAt);
    }

    function poll() {
      const since = messagesRef.current[messagesRef.current.length - 1]?.messageId;
      listMessages(userId!, since)
        .then((newOnes) => {
          if (cancelled) return;
          if (newOnes.length > 0) {
            setMessages((prev) => [...prev, ...newOnes]);
            markLatestSeen(newOnes);
          }
        })
        .catch(() => { /* transient — next poll retries */ })
        .finally(() => { if (!cancelled) pollTimer = setTimeout(poll, POLL_MS); });
    }

    listMessages(userId)
      .then((initial) => {
        if (cancelled) return;
        setMessages(initial);
        markLatestSeen(initial);
      })
      .catch((e: Error) => setError(e.message))
      .finally(() => {
        if (cancelled) return;
        setLoading(false);
        pollTimer = setTimeout(poll, POLL_MS);
      });

    return () => { cancelled = true; if (pollTimer) clearTimeout(pollTimer); };
  }, [userId]);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ block: 'end' });
  }, [messages.length]);

  async function handleSend(e: React.FormEvent) {
    e.preventDefault();
    if (!userId || !draft.trim() || sending) return;
    setSending(true);
    setError(null);
    try {
      const msg = await sendMessage(userId, draft.trim());
      setMessages((prev) => [...prev, msg]);
      markSeen(userId, msg.sentAt);
      setDraft('');
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to send message');
    } finally {
      setSending(false);
    }
  }

  return (
    <Layout>
      <div style={{ marginBottom: 16 }}>
        <Link to="/friends" style={{ color: '#5b87a3', fontSize: 13, textDecoration: 'none' }}>← Friends</Link>
      </div>
      <h1 style={{ margin: '0 0 20px', color: '#e7f1f7', fontSize: 20, fontWeight: 700 }}>
        {profile?.name ?? userId}
      </h1>

      {error && <div style={{ color: '#ff8a75', marginBottom: 16, fontSize: 13 }}>{error}</div>}

      <div style={{ ...cardStyle, height: 420, display: 'flex', flexDirection: 'column', padding: 16 }}>
        <div style={{ flex: 1, overflowY: 'auto', display: 'flex', flexDirection: 'column', gap: 8 }}>
          {loading ? (
            <span style={{ color: '#5b87a3', fontSize: 13 }}>Loading…</span>
          ) : messages.length === 0 ? (
            <span style={{ color: '#5b87a3', fontSize: 13 }}>No messages yet — say hello.</span>
          ) : (
            messages.map((m) => {
              const mine = m.senderId === currentUser?.userId;
              return (
                <div key={m.messageId} style={{ display: 'flex', justifyContent: mine ? 'flex-end' : 'flex-start' }}>
                  <div style={{
                    maxWidth: '75%', padding: '7px 11px', borderRadius: 0,
                    background: mine ? '#ff7a29' : '#072943',
                    border: mine ? 'none' : '1px solid #23577a',
                    color: mine ? '#0a2135' : '#e7f1f7',
                    fontSize: 14, wordBreak: 'break-word',
                  }}>
                    {m.body}
                  </div>
                </div>
              );
            })
          )}
          <div ref={bottomRef} />
        </div>

        <form onSubmit={handleSend} style={{ display: 'flex', gap: 8, marginTop: 12 }}>
          <input
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            placeholder="Message…"
            maxLength={2000}
            style={{
              flex: 1, background: '#072943', border: '1px solid #23577a', borderRadius: 0,
              color: '#e7f1f7', padding: '8px 10px', fontSize: 14,
            }}
          />
          <button type="submit" disabled={sending || !draft.trim()} style={primaryButtonStyle}>
            Send
          </button>
        </form>
      </div>
    </Layout>
  );
}
