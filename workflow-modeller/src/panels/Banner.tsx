import { type ReactNode, useEffect } from 'react';

export type BannerTone = 'info' | 'warning' | 'error';

interface BannerProps {
  tone: BannerTone;
  messages: string[];
  onDismiss: () => void;
  /** When set, the banner self-dismisses after this many milliseconds. */
  autoDismissMs?: number;
  /** When true, the banner renders as a floating toast (top-right) instead of an inline strip. */
  floating?: boolean;
}

export function Banner({
  tone,
  messages,
  onDismiss,
  autoDismissMs,
  floating,
}: BannerProps): ReactNode {
  useEffect(() => {
    if (!autoDismissMs || messages.length === 0) return;
    const t = setTimeout(onDismiss, autoDismissMs);
    return () => clearTimeout(t);
  }, [autoDismissMs, messages, onDismiss]);

  if (messages.length === 0) return null;
  const classes = ['wm-banner', `wm-banner--${tone}`, floating && 'wm-banner--floating']
    .filter(Boolean)
    .join(' ');
  return (
    <div className={classes} role={tone === 'error' ? 'alert' : 'status'}>
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
