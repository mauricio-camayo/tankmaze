const BASE = (import.meta.env.VITE_API_ENDPOINT as string) ?? '';

export interface AdConfig {
  enabled: boolean;
  publisherId: string;
  topSlotId: string;
  rightSlotId: string;
  bottomSlotId: string;
}

let cached: AdConfig | null = null;

export async function loadAdConfig(): Promise<AdConfig> {
  if (cached) return cached;
  try {
    const res = await fetch(`${BASE}/config/ads`);
    if (!res.ok) throw new Error('ad config unavailable');
    cached = await res.json() as AdConfig;
  } catch {
    cached = { enabled: false, publisherId: '', topSlotId: '', rightSlotId: '', bottomSlotId: '' };
  }
  return cached;
}

export function getAdConfig(): AdConfig | null {
  return cached;
}
