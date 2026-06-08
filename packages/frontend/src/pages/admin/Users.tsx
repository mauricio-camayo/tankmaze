import { useEffect, useState } from 'react';
import Layout, { cardStyle, primaryButtonStyle, ghostButtonStyle } from '../../components/Layout';
import {
  adminListUsers, adminUpdateUser, adminToggleUserRole, adminDeleteUser,
  type AdminUser,
} from '../../services/api';
import { useAuthStore } from '../../store/authStore';

export default function AdminUsers() {
  const currentUser = useAuthStore((s) => s.user);
  const [users, setUsers] = useState<AdminUser[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [nextToken, setNextToken] = useState<string | undefined>(undefined);
  const [tokenStack, setTokenStack] = useState<string[]>([]);

  function loadPage(token?: string) {
    setLoading(true);
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
    <Layout>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 20 }}>
        <h2 style={{ margin: 0, color: '#e2e8f0' }}>Admin — Users</h2>
        <a href="/admin/tanks" style={{ color: '#64748b', fontSize: 13 }}>→ Tanks</a>
      </div>

      {error && <p style={{ color: '#f87171' }}>{error}</p>}
      {loading ? (
        <p style={{ color: '#64748b' }}>Loading…</p>
      ) : (
        <>
        <div style={cardStyle}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
            <thead>
              <tr style={{ color: '#64748b', textAlign: 'left' }}>
                <th style={th}>Name</th>
                <th style={th}>Email</th>
                <th style={th}>Status</th>
                <th style={th}>Admin</th>
                <th style={th}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {users.map((u) => {
                const isSelf = u.sub === currentUser?.userId;
                const isBusy = busy === u.sub;
                return (
                  <tr key={u.sub} style={{ borderTop: '1px solid #2d2d4e' }}>
                    <td style={td}>{u.name || '—'}</td>
                    <td style={td}>{u.email}</td>
                    <td style={td}>
                      <span style={{ color: u.enabled ? '#4ade80' : '#f87171' }}>
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
                              style={{ ...primaryButtonStyle, fontSize: 12, padding: '3px 10px', background: '#dc2626' }}
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
                            style={{ ...ghostButtonStyle, fontSize: 12, padding: '3px 10px', color: '#f87171' }}
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
                setTokenStack((s) => [...s, users[0]?.sub ?? '']);
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
    </Layout>
  );
}

const th: React.CSSProperties = { padding: '8px 12px', fontWeight: 600, fontSize: 12 };
const td: React.CSSProperties = { padding: '10px 12px', color: '#e2e8f0', verticalAlign: 'middle' };
