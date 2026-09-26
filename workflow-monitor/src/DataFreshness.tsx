import { type ReactNode, useEffect, useState } from "react";

export function DataFreshness({
  label,
  updatedAt,
}: {
  label: string;
  updatedAt: number;
}): ReactNode {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1_000);
    return () => window.clearInterval(timer);
  }, []);

  if (!updatedAt) return null;
  const timestamp = new Date(updatedAt).toISOString();
  const age = Math.max(0, Math.floor((now - updatedAt) / 1_000));
  return (
    <p className="rm-muted">
      {label} updated <time dateTime={timestamp}>{timestamp}</time> ({age}s ago)
    </p>
  );
}
