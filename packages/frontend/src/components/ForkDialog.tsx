import { useState } from 'react';
import type { Tank } from '../types';
import { forkTank, transferScore } from '../services/api';
import { primaryButtonStyle, ghostButtonStyle } from './Layout';
import ScoreTransferConfirm from './ScoreTransferConfirm';

interface ForkDialogProps {
  tank: Tank;
  version: string;
  onClose: () => void;
  onForked: (newTankId: string) => void;
}

type Disposition = 'keep' | 'transfer';

const overlayStyle: React.CSSProperties = {
  position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.7)',
  display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000,
};

const dialogStyle: React.CSSProperties = {
  background: '#1a1a2e', border: '1px solid #2d2d4e', borderRadius: 12,
  padding: '24px', width: '100%', maxWidth: 440,
};

function radioStyle(selected: boolean): React.CSSProperties {
  return {
    display: 'flex', alignItems: 'flex-start', cursor: 'pointer',
    padding: '10px 12px', borderRadius: 8,
    border: `1px solid ${selected ? '#7c6af7' : '#2d2d4e'}`,
    background: selected ? 'rgba(124,106,247,0.08)' : '#0f0f1a',
  };
}

export default function ForkDialog({ tank, version, onClose, onForked }: ForkDialogProps) {
  const hasScore = tank.globalScore > 0;
  const [disposition, setDisposition] = useState<Disposition>('keep');
  const [step, setStep] = useState<'choice' | 'confirm-transfer'>('choice');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function handleFork() {
    if (hasScore && disposition === 'transfer') {
      setStep('confirm-transfer');
      return;
    }
    await doFork(false);
  }

  async function doFork(withTransfer: boolean) {
    setLoading(true);
    setError(null);
    try {
      const newTank = await forkTank(tank.tankId, version);
      if (withTransfer) {
        await transferScore(tank.tankId, newTank.tankId);
      }
      onForked(newTank.tankId);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setLoading(false);
      setStep('choice');
    }
  }

  if (step === 'confirm-transfer') {
    return (
      <ScoreTransferConfirm
        score={tank.globalScore}
        onConfirm={() => doFork(true)}
        onCancel={() => setStep('choice')}
        loading={loading}
      />
    );
  }

  return (
    <div style={overlayStyle}>
      <div style={dialogStyle}>
        <h3 style={{ margin: '0 0 8px', color: '#e2e8f0' }}>Fork {tank.name}</h3>
        <p style={{ margin: '0 0 16px', color: '#94a3b8', fontSize: 14, lineHeight: 1.5 }}>
          Creates a new tank from <strong style={{ color: '#a78bfa' }}>{version}</strong>.
          You can develop it independently.
        </p>

        {hasScore && (
          <div style={{ marginBottom: 20 }}>
            <p style={{ margin: '0 0 10px', color: '#94a3b8', fontSize: 13 }}>
              This tank has{' '}
              <strong style={{ color: '#a78bfa' }}>{tank.globalScore.toLocaleString()} pts</strong>.
              {' '}Where should the score go?
            </p>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              <label style={radioStyle(disposition === 'keep')}>
                <input
                  type="radio"
                  checked={disposition === 'keep'}
                  onChange={() => setDisposition('keep')}
                  style={{ marginRight: 10, marginTop: 2, flexShrink: 0 }}
                />
                <div>
                  <div style={{ color: '#e2e8f0', fontSize: 14, fontWeight: 500 }}>Keep score on this tank</div>
                  <div style={{ color: '#64748b', fontSize: 12, marginTop: 1 }}>The fork starts at 0 pts.</div>
                </div>
              </label>
              <label style={radioStyle(disposition === 'transfer')}>
                <input
                  type="radio"
                  checked={disposition === 'transfer'}
                  onChange={() => setDisposition('transfer')}
                  style={{ marginRight: 10, marginTop: 2, flexShrink: 0 }}
                />
                <div>
                  <div style={{ color: '#e2e8f0', fontSize: 14, fontWeight: 500 }}>Transfer score to the fork</div>
                  <div style={{ color: '#f87171', fontSize: 12, marginTop: 1 }}>
                    This tank drops to 0 pts. Irreversible.
                  </div>
                </div>
              </label>
            </div>
          </div>
        )}

        {error && <p style={{ color: '#f87171', fontSize: 13, margin: '0 0 12px' }}>{error}</p>}

        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
          <button onClick={onClose} disabled={loading} style={ghostButtonStyle}>Cancel</button>
          <button onClick={handleFork} disabled={loading} style={primaryButtonStyle}>
            {loading ? 'Forking…' : 'Fork'}
          </button>
        </div>
      </div>
    </div>
  );
}
