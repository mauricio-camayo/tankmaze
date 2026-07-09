import { useState } from 'react';
import { ghostButtonStyle } from './Layout';

interface ScoreTransferConfirmProps {
  score: number;
  onConfirm: () => void;
  onCancel: () => void;
  loading: boolean;
}

const overlayStyle: React.CSSProperties = {
  position: 'fixed', inset: 0, background: 'rgba(0,0,0,0.7)',
  display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 1000,
};

const dialogStyle: React.CSSProperties = {
  background: '#082e4a', border: '1px solid #23577a', borderRadius: 0,
  padding: '24px', width: '100%', maxWidth: 440,
};

export default function ScoreTransferConfirm({ score, onConfirm, onCancel, loading }: ScoreTransferConfirmProps) {
  const [confirmed, setConfirmed] = useState(false);

  return (
    <div style={overlayStyle}>
      <div style={dialogStyle}>
        <h3 style={{ margin: '0 0 12px', color: '#ff8a75' }}>Transfer score — are you sure?</h3>
        <p style={{ margin: '0 0 8px', color: '#7fa2ba', fontSize: 14, lineHeight: 1.5 }}>
          You are about to transfer{' '}
          <strong style={{ color: '#ffab6b' }}>{score.toLocaleString()} pts</strong>{' '}
          to the fork. This is{' '}
          <strong style={{ color: '#ff8a75' }}>permanent and cannot be undone</strong>.
        </p>
        <p style={{ margin: '0 0 20px', color: '#5b87a3', fontSize: 13 }}>
          The original tank's score will be set to 0. Its game day history remains intact.
        </p>

        <label style={{ display: 'flex', alignItems: 'flex-start', gap: 10, cursor: 'pointer', marginBottom: 20 }}>
          <input
            type="checkbox"
            checked={confirmed}
            onChange={(e) => setConfirmed(e.target.checked)}
            style={{ marginTop: 2, flexShrink: 0 }}
          />
          <span style={{ color: '#7fa2ba', fontSize: 13, lineHeight: 1.4 }}>
            I understand this will permanently transfer {score.toLocaleString()} pts and cannot be reversed.
          </span>
        </label>

        <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8 }}>
          <button onClick={onCancel} disabled={loading} style={ghostButtonStyle}>Back</button>
          <button
            onClick={onConfirm}
            disabled={!confirmed || loading}
            style={{
              background: confirmed && !loading ? '#e0503a' : '#3a1a18',
              border: 'none', color: '#fff',
              padding: '8px 16px', borderRadius: 0,
              cursor: confirmed && !loading ? 'pointer' : 'not-allowed',
              fontSize: 14, fontWeight: 600,
            }}
          >
            {loading ? 'Transferring…' : 'Transfer score'}
          </button>
        </div>
      </div>
    </div>
  );
}
