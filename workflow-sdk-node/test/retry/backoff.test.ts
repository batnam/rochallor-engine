import { describe, it, expect } from 'vitest'
import {
  delay,
  BASE_DELAY_MS,
  FACTOR,
  JITTER_FRAC,
  MAX_DELAY_MS,
} from '../../src/retry/backoff.js'

describe('delay()', () => {
  it('attempt 1 is near BASE_DELAY_MS', () => {
    const d = delay(1)
    const lo = Math.round(BASE_DELAY_MS * (1 - JITTER_FRAC))
    const hi = Math.round(BASE_DELAY_MS * (1 + JITTER_FRAC))
    expect(d).toBeGreaterThanOrEqual(lo)
    expect(d).toBeLessThanOrEqual(hi)
  })

  it('attempt 2 is near BASE * FACTOR', () => {
    // Run many samples to account for jitter
    for (let i = 0; i < 50; i++) {
      const d = delay(2)
      const base = BASE_DELAY_MS * FACTOR
      const lo = Math.round(base * (1 - JITTER_FRAC))
      const hi = Math.round(base * (1 + JITTER_FRAC))
      expect(d).toBeGreaterThanOrEqual(lo)
      expect(d).toBeLessThanOrEqual(hi)
    }
  })

  it('caps at MAX_DELAY_MS', () => {
    // High attempt count should hit the cap
    const d = delay(100)
    expect(d).toBeLessThanOrEqual(MAX_DELAY_MS)
  })

  it('is never negative', () => {
    for (let a = 1; a <= 20; a++) {
      expect(delay(a)).toBeGreaterThanOrEqual(0)
    }
  })

  it('treats attempt <= 0 same as attempt 1', () => {
    // Verify no crash and result is in range
    const d0 = delay(0)
    const d1 = delay(1)
    const lo = Math.round(BASE_DELAY_MS * (1 - JITTER_FRAC))
    const hi = Math.round(BASE_DELAY_MS * (1 + JITTER_FRAC))
    expect(d0).toBeGreaterThanOrEqual(lo)
    expect(d0).toBeLessThanOrEqual(hi)
    expect(d1).toBeGreaterThanOrEqual(lo)
    expect(d1).toBeLessThanOrEqual(hi)
  })

  it('is monotonically non-decreasing on median (no jitter bias test)', () => {
    // Compare medians across many samples
    const samples = (attempt: number) =>
      Array.from({ length: 200 }, () => delay(attempt))
    const median = (arr: number[]) => {
      const s = [...arr].sort((a, b) => a - b)
      return s[Math.floor(s.length / 2)]
    }
    const m1 = median(samples(1))
    const m2 = median(samples(2))
    const m3 = median(samples(3))
    expect(m2).toBeGreaterThan(m1)
    expect(m3).toBeGreaterThanOrEqual(m2)
  })
})
