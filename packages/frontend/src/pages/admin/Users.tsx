import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { cardStyle, primaryButtonStyle, ghostButtonStyle } from '../../components/Layout';
import {
  adminListUsers, adminUpdateUser, adminToggleUserRole, adminDeleteUser, adminSetUserTier,
  type AdminUser,
} from '../../services/api';
import { useAuthStore } from '../../store/authStore';

const TIERS = ['free', 'builder', 'pro'] as const;

function fmtDate(value: string | number | null): string {
  if (!value) return '—';
  const d = typeof value === 'number' ? new Date(value * 1000) : new Date(value);
  if (Number.isNaN(d.getTime())) return '—';
  return d.toLocaleDateString();
}

export default function AdminUsers() {
  const currentUser = useAuthStore((s) => s.user);
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [nextToken, setNextToken] = useState<string | undefined>(undefined);
  const [currentToken, setCurrentToken] = useState<string | undefined>(undefined);
  const [tokenStack, setTokenStack] = useState<Array<string | undefined>>([]);

  function loadPage(token?: string) {
    setLoading(true);
    setCurrentToken(token);
    adminListUsers(token)
      .then((r) => { setUsers(r.users); setNextToken(r.nextToken); })
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false));
  }

  useEffect(() => { loadPage(); }, []);

  async function toggleEnabled(u: AdminUser) {
    setBusy(u.sub);
    try {
      await adminUpdateUser(u.sub, u.enabled);
      setUsers((prev) => prev.map((x) => x.sub === u.sub ? { ...x, enabled: !x.enabled } : x));
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed');
    } finally {
      setBusy(null);
    }
  }

  async function toggleAdmin(u: AdminUser) {
    setBusy(u.sub);
    try {
      const res = await adminToggleUserRole(u.sub);
      setUsers((prev) => prev.map((x) => x.sub === u.sub ? { ...x, isAdmin: res.isAdmin } : x));
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed');
    } finally {
      setBusy(null);
    }
  }

  async function changeTier(u: AdminUser, tier: string) {
    if (tier === u.tier) return;
    setBusy(u.sub);
    try {
      await adminSetUserTier(u.sub, tier);
      setUsers((prev) => prev.map((x) => x.sub === u.sub ? { ...x, tier } : x));
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed');
    } finally {
      setBusy(null);
    }
  }

  async function deleteUser(sub: string) {
    setBusy(sub);
    try {
      await adminDeleteUser(sub);
      setUsers((prev) => prev.filter((x) => x.sub !== sub));
      setConfirmDelete(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed');
    } finally {
      setBusy(null);
    }
  }

  return (
    <>
      <h2 style={{ margin: '0 0 20px', color: '#e7f1f7' }}>Users</h2>

      {error && <p style={{ color: '#ff8a75' }}>{error}</p>}
      {loading ? (
        <p style={{ color: '#5b87a3' }}>Loading…</p>
      ) : (
        <>
        <div style={cardStyle}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
            <thead>
              <tr style={{ color: '#5b87a3', textAlign: 'left' }}>
                <th style={th}>Name</th>
                <th style={th}>Email</th>
                <th style={th}>Status</th>
                <th style={th}>Admin</th>
                <th style={th}>Tier</th>
                <th style={th}>IdP</th>
                <th style={th}>First seen</th>
                <th style={th}>Last seen</th>
                <th style={th}>Tanks</th>
                <th style={th}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {users.map((u) => {
                const isSelf = u.sub === currentUser?.userId;
                const isBusy = busy === u.sub;
                return (
                  <tr key={u.sub} style={{ borderTop: '1px solid #23577a' }}>
                    <td style={td}>
                      <Link to={`/users/${u.sub}`} style={{ color: '#e7f1f7', textDecoration: 'none' }}>
                        {u.name || u.email}
                      </Link>
                    </td>
                    <td style={td}>{u.email}</td>
                    <td style={td}>
                      <span style={{ color: u.enabled ? '#59e6c0' : '#ff8a75' }}>
                        {u.enabled ? 'Active' : 'Disabled'}
                      </span>
                    </td>
                    <td style={td}>
                      <input
                        type="checkbox"
                        checked={u.isAdmin}
                        disabled={isSelf || isBusy}
                        onChange={() => toggleAdmin(u)}
                        title={isSelf ? 'Cannot modify your own role' : ''}
                      />
                    </td>
                    <td style={td}>
                      <select
                        value={u.tier}
                        disabled={isBusy}
                        onChange={(e) => changeTier(u, e.target.value)}
                        style={{
                          background: '#0a3550', border: '1px solid #23577a', borderRadius: 0,
                          color: '#e7f1f7', padding: '3px 8px', fontSize: 12,
                        }}
                      >
                        {TIERS.map((t) => (
                          <option key={t} value={t}>{t.charAt(0).toUpperCase() + t.slice(1)}</option>
                        ))}
                      </select>
                    </td>
                    <td style={td}>{u.idp}</td>
                    <td style={td}>{fmtDate(u.createdAt)}</td>
                    <td style={td}>{fmtDate(u.lastLoginAt)}</td>
                    <td style={td}>{u.tankCount}/{u.tankLimit}</td>
                    <td style={{ ...td, display: 'flex', gap: 8 }}>
                      {!isSelf && (
                        <button
                          onClick={() => toggleEnabled(u)}
                          disabled={isBusy}
                          style={{ ...ghostButtonStyle, fontSize: 12, padding: '3px 10px' }}
                        >
                          {u.enabled ? 'Disable' : 'Enable'}
                        </button>
                      )}
                      {!isSelf && (
                        confirmDelete === u.sub ? (
                          <>
                            <button
                              onClick={() => deleteUser(u.sub)}
                              disabled={isBusy}
                              style={{ ...primaryButtonStyle, fontSize: 12, padding: '3px 10px', background: '#e0503a' }}
                            >
                              Confirm
                            </button>
                            <button
                              onClick={() => setConfirmDelete(null)}
                              style={{ ...ghostButtonStyle, fontSize: 12, padding: '3px 10px' }}
                            >
                              Cancel
                            </button>
                          </>
                        ) : (
                          <button
                            onClick={() => setConfirmDelete(u.sub)}
                            style={{ ...ghostButtonStyle, fontSize: 12, padding: '3px 10px', color: '#ff8a75' }}
                          >
                            Delete
                          </button>
                        )
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
        {(tokenStack.length > 0 || nextToken) && (
          <div style={{ display: 'flex', gap: 8, marginTop: 12 }}>
            <button
              disabled={tokenStack.length === 0}
              onClick={() => {
                const stack = [...tokenStack];
                const prev = stack.pop();
                setTokenStack(stack);
                loadPage(prev);
              }}
              style={{ ...ghostButtonStyle, fontSize: 12, padding: '4px 14px' }}
            >
              Prev
            </button>
            <button
              disabled={!nextToken}
              onClick={() => {
                setTokenStack((s) => [...s, currentToken]);
                loadPage(nextToken);
              }}
              style={{ ...ghostButtonStyle, fontSize: 12, padding: '4px 14px' }}
            >
              Next
            </button>
          </div>
        )}
        </>
      )}
    </>
  );
}

const th: React.CSSProperties = { padding: '8px 12px', fontWeight: 600, fontSize: 12 };
const td: React.CSSProperties = { padding: '10px 12px', color: '#e7f1f7', verticalAlign: 'middle' };
