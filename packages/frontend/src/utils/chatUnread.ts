// Chat has no server-side read-receipt schema (item 223 Part 2) — "unread"
// is computed client-side from a per-conversation last-seen timestamp
// stashed in localStorage, compared against FriendEntry.lastMessageAt.
import type { FriendEntry } from '../types';

const KEY_PREFIX = 'tankmaze-chat-lastseen-';

export function getLastSeen(friendUserId: string): number {
  const raw = localStorage.getItem(KEY_PREFIX + friendUserId);
  return raw ? Number(raw) || 0 : 0;
}

export function markSeen(friendUserId: string, atSentAt: number): void {
  localStorage.setItem(KEY_PREFIX + friendUserId, String(atSentAt));
}

export function isUnread(entry: FriendEntry): boolean {
  if (!entry.lastMessageAt || entry.lastMessageFromMe) return false;
  return entry.lastMessageAt > getLastSeen(entry.userId);
}
