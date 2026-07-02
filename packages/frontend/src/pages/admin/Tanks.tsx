import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { cardStyle, primaryButtonStyle, ghostButtonStyle } from '../../components/Layout';
import { adminListTanks, adminUpdateTank, adminDeleteTank } from '../../services/api';
import type { Tank } from '../../types';

export default function AdminTanks() {
  const [tanks, setTanks] = useState<Tank[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [editId, setEditId] = useState<string | null>(null);
  const [editName, setEditName] = useState('');
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [nextToken, setNextToken] = useState<string | undefined>(undefined);
  const [currentToken, setCurrentToken] = useState<string | undefined>(undefined);
  const [tokenStack, setTokenStack] = useState<Array<string | undefined>>([]);

  function loadPage(token?: string) {
    setLoading(true);
    setCurrentToken(token);
    adminListTanks(token)
      .then((r) => { setTanks(r.tanks); setNextToken(r.nextToken); })
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false));
  }

  useEffect(() => { loadPage(); }, []);

  async function saveName(tankId: string) {
    if (!editName.trim()) return;
    setBusy(tankId);
    try {
      await adminUpdateTank(tankId, editName.trim());
      setTanks((prev) => prev.map((t) => t.tankId === tankId ? { ...t, name: editName.trim() } : t));
      setEditId(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed');
    } finally {
      setBusy(null);
    }
  }

  async function deleteTank(tankId: string) {
    setBusy(tankId);
    try {
      await adminDeleteTank(tankId);
      setTanks((prev) => prev.filter((t) => t.tankId !== tankId));
      setConfirmDelete(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed');
    } finally {
      setBusy(null);
    }
  }

  return (
    <>
      <h2 style={{ margin: '0 0 20px', color: '#e2e8f0' }}>Tanks</h2>

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
                <th style={th}>Author</th>
                <th style={th}>Score</th>
                <th style={th}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {tanks.map((t) => {
                const isBusy = busy === t.tankId;
                return (
                  <tr key={t.tankId} style={{ borderTop: '1px solid #2d2d4e' }}>
                    <td style={td}>
                      {editId === t.tankId ? (
                        <input
                          value={editName}
                          onChange={(e) => setEditName(e.target.value)}
                          onKeyDown={(e) => e.key === 'Enter' && saveName(t.tankId)}
                          style={{
                            background: '#0f0f1a', border: '1px solid #4c4c7a', color: '#e2e8f0',
                            padding: '3px 8px', borderRadius: 4, fontSize: 13, width: 180,
                          }}
                          autoFocus
                        />
                      ) : (
                        <Link to={`/tanks/${t.tankId}`} style={{ color: '#e2e8f0', textDecoration: 'none' }}>
                          {t.name}
                        </Link>
                      )}
                    </td>
                    <td style={{ ...td, color: '#94a3b8' }}>{t.authorName || t.userId.slice(0, 8)}</td>
                    <td style={td}>{t.globalScore.toLocaleString()}</td>
                    <td style={{ ...td, display: 'flex', gap: 8, alignItems: 'center' }}>
                      {editId === t.tankId ? (
                        <>
                          <button
                            onClick={() => saveName(t.tankId)}
                            disabled={isBusy}
                            style={{ ...primaryButtonStyle, fontSize: 12, padding: '3px 10px' }}
                          >
                            Save
                          </button>
                          <button
                            onClick={() => setEditId(null)}
                            style={{ ...ghostButtonStyle, fontSize: 12, padding: '3px 10px' }}
                          >
                            Cancel
                          </button>
                        </>
                      ) : (
                        <button
                          onClick={() => { setEditId(t.tankId); setEditName(t.name); }}
                          style={{ ...ghostButtonStyle, fontSize: 12, padding: '3px 10px' }}
                        >
                          Rename
                        </button>
                      )}
                      {confirmDelete === t.tankId ? (
                        <>
                          <button
                            onClick={() => deleteTank(t.tankId)}
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
                          onClick={() => setConfirmDelete(t.tankId)}
                          style={{ ...ghostButtonStyle, fontSize: 12, padding: '3px 10px', color: '#f87171' }}
                        >
                          Delete
                        </button>
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
const td: React.CSSProperties = { padding: '10px 12px', color: '#e2e8f0', verticalAlign: 'middle' };
