import { useEffect, useState } from 'react';
import { loadAdConfig, type AdConfig } from '../services/adConfig';

declare global {
  interface Window {
    adsbygoogle: unknown[];
  }
}

interface AdSlotProps {
  publisherId: string;
  slotId: string;
  format?: string;
}

function AdUnit({ publisherId, slotId, format = 'auto' }: AdSlotProps) {
  useEffect(() => {
    try {
      (window.adsbygoogle = window.adsbygoogle || []).push({});
    } catch { /* already initialized */ }
  }, [slotId]);

  return (
    <ins
      className="adsbygoogle"
      style={{ display: 'block' }}
      data-ad-client={publisherId}
      data-ad-slot={slotId}
      data-ad-format={format}
      data-full-width-responsive="true"
    />
  );
}

function injectAdSenseScript(publisherId: string) {
  if (document.querySelector('script[data-adsense]')) return;
  const s = document.createElement('script');
  s.async = true;
  s.src = `https://pagead2.googlesyndication.com/pagead/js/adsbygoogle.js?client=${publisherId}`;
  s.crossOrigin = 'anonymous';
  s.setAttribute('data-adsense', '1');
  document.head.appendChild(s);
}

interface AdSlotsProps {
  position: 'top' | 'bottom' | 'right';
}

export default function AdSlots({ position }: AdSlotsProps) {
  const [config, setConfig] = useState<AdConfig | null>(null);

  useEffect(() => {
    loadAdConfig().then((cfg) => {
      setConfig(cfg);
      if (cfg.enabled && cfg.publisherId) injectAdSenseScript(cfg.publisherId);
    });
  }, []);

  if (!config?.enabled || !config.publisherId) return null;

  if (position === 'top') {
    return (
      <div className="tm-ad-top-bar">
        <AdUnit publisherId={config.publisherId} slotId={config.topSlotId} format="horizontal" />
      </div>
    );
  }

  if (position === 'bottom') {
    return (
      <div className="tm-ad-bottom-bar">
        <AdUnit publisherId={config.publisherId} slotId={config.bottomSlotId} format="horizontal" />
      </div>
    );
  }

  if (position === 'right') {
    return (
      <div className="tm-ad-right-rail">
        <AdUnit publisherId={config.publisherId} slotId={config.rightSlotId} format="vertical" />
      </div>
    );
  }

  return null;
}
