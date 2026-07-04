import { useRef, useState } from 'react';
import { updateTank, uploadTankAvatar } from '../services/api';

const MAX_AVATAR_BYTES = 512 * 1024;
const ALLOWED_TYPES = ['image/png', 'image/jpeg'];

function readFileAsBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      const result = reader.result as string;
      // result is a data: URL ("data:image/png;base64,AAAA...") — strip the prefix.
      resolve(result.slice(result.indexOf(',') + 1));
    };
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(file);
  });
}

/** Deterministic default: hash tankId to one of the 16 built-in sprites. */
export function defaultAvatarUrl(tankId: string): string {
  const idx = tankId.split('').reduce((a, c) => a + c.charCodeAt(0), 0) % 16;
  return `/avatars/tank-${idx}.png`;
}

export function avatarSrc(tankId: string, avatarUrl?: string): string {
  return avatarUrl || defaultAvatarUrl(tankId);
}

const ALL_AVATARS = Array.from({ length: 16 }, (_, i) => `/avatars/tank-${i}.png`);

interface Props {
  tankId: string;
  current?: string;
  onSaved: (url: string) => void;
}

export function AvatarPicker({ tankId, current, onSaved }: Props) {
  const [selected, setSelected] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const [pendingFile, setPendingFile] = useState<File | null>(null);
  const [pendingPreview, setPendingPreview] = useState<string | null>(null);
  const [uploadError, setUploadError] = useState<string | null>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const effective = pendingPreview ?? selected ?? current;

  function handleFileChange(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    e.target.value = ''; // allow re-selecting the same file later
    if (!file) return;
    setUploadError(null);
    if (!ALLOWED_TYPES.includes(file.type)) {
      setUploadError('Only PNG or JPEG images are supported.');
      return;
    }
    if (file.size > MAX_AVATAR_BYTES) {
      setUploadError(`Image must be ${Math.floor(MAX_AVATAR_BYTES / 1024)}KB or smaller.`);
      return;
    }
    setSelected(null);
    setPendingFile(file);
    setPendingPreview(URL.createObjectURL(file));
  }

  async function handleSave() {
    if (pendingFile) {
      setSaving(true);
      setUploadError(null);
      try {
        const data = await readFileAsBase64(pendingFile);
        const { avatarUrl } = await uploadTankAvatar(tankId, data, pendingFile.type);
        onSaved(avatarUrl);
        setPendingFile(null);
        setPendingPreview(null);
      } catch (e) {
        setUploadError(e instanceof Error ? e.message : 'Upload failed');
      } finally {
        setSaving(false);
      }
      return;
    }
    if (!selected || selected === current) return;
    setSaving(true);
    try {
      await updateTank(tankId, { avatarUrl: selected });
      onSaved(selected);
      setSelected(null);
    } finally {
      setSaving(false);
    }
  }

  return (
    <div>
      <div style={{
        display: 'grid',
        gridTemplateColumns: 'repeat(8, 1fr)',
        gap: 6,
        marginBottom: 12,
      }}>
        {ALL_AVATARS.map((url) => {
          const isActive = url === effective;
          return (
            <button
              key={url}
              onClick={() => { setSelected(url); setPendingFile(null); setPendingPreview(null); setUploadError(null); }}
              title={url.replace('/avatars/', '').replace('.png', '')}
              style={{
                padding: 3,
                border: `2px solid ${isActive ? '#7c6af7' : '#2d2d4e'}`,
                borderRadius: 6,
                background: isActive ? 'rgba(124,106,247,0.12)' : '#1a1a2e',
                cursor: 'pointer',
                lineHeight: 0,
              }}
            >
              <img src={url} alt="" style={{ width: 40, height: 40, display: 'block', imageRendering: 'pixelated' }} />
            </button>
          );
        })}
      </div>

      <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 12 }}>
        <input
          ref={fileInputRef}
          type="file"
          accept="image/png,image/jpeg"
          onChange={handleFileChange}
          style={{ display: 'none' }}
        />
        <button
          onClick={() => fileInputRef.current?.click()}
          style={{
            background: 'none', border: '1px solid #2d2d4e', color: '#94a3b8',
            borderRadius: 6, padding: '5px 12px', fontSize: 12, cursor: 'pointer',
          }}
        >
          Upload your own
        </button>
        {pendingPreview && (
          <img src={pendingPreview} alt="" style={{ width: 32, height: 32, borderRadius: 4, objectFit: 'cover', border: '1px solid #2d2d4e' }} />
        )}
        <span style={{ color: '#64748b', fontSize: 11 }}>PNG or JPEG, max 512KB</span>
      </div>
      {uploadError && <p style={{ color: '#f87171', fontSize: 12, margin: '0 0 12px' }}>{uploadError}</p>}

      {((selected && selected !== current) || pendingFile) && (
        <button
          onClick={handleSave}
          disabled={saving}
          style={{
            background: '#7c6af7', color: '#fff', border: 'none',
            borderRadius: 6, padding: '6px 16px', fontSize: 13,
            fontWeight: 600, cursor: saving ? 'not-allowed' : 'pointer',
            opacity: saving ? 0.6 : 1,
          }}
        >
          {saving ? (pendingFile ? 'Uploading…' : 'Saving…') : (pendingFile ? 'Upload avatar' : 'Save avatar')}
        </button>
      )}
    </div>
  );
}
