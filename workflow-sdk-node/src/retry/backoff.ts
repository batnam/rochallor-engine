/**
 * R-009 backoff policy: base 100 ms, factor 2.0, jitter ±20%, max 30 s.
 */
export const BASE_DELAY_MS = 100
export const FACTOR = 2.0
export const JITTER_FRAC = 0.20
export const MAX_DELAY_MS = 30_000

/**
 * Returns the back-off delay in milliseconds for the given attempt (1-based),
 * with jitter applied.
 */
export function delay(attempt: number): number {
  const a = Math.max(1, attempt)
  let ms = BASE_DELAY_MS
  for (let i = 1; i < a; i++) {
    ms *= FACTOR
    if (ms >= MAX_DELAY_MS) { ms = MAX_DELAY_MS; break }
  }
  const jitter = ms * JITTER_FRAC * (2 * Math.random() - 1)
  return Math.max(0, Math.min(MAX_DELAY_MS, Math.round(ms + jitter)))
}

/** Resolves after delay(attempt) milliseconds. */
export function sleep(attempt: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, delay(attempt)))
}
