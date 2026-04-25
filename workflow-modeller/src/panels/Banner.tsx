import type { ReactNode } from 'react';

export type BannerTone = 'info' | 'warning' | 'error';

interface BannerProps {
  tone: BannerTone;
  messages: string[];
  onDismiss: () => void;
}

export function Banner({ tone, messages, onDismiss }: BannerProps): ReactNode {
  if (messages.length === 0) return null;
  return (
    <div className={`wm-banner wm-banner--${tone}`} role={tone === 'error' ? 'alert' : 'status'}>
      <ul className="wm-banner-list">
        {messages.map((m, i) => (
          <li key={`${i}-${m}`}>{m}</li>
        ))}
      </ul>
      <button
        type="button"
        className="wm-banner-dismiss"
        onClick={onDismiss}
        aria-label="Dismiss notification"
      >
        ×
      </button>
    </div>
  );
}
