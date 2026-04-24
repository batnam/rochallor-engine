export class DeduplicationCache {
  private cache: Map<string, number> = new Map();
  private timer: NodeJS.Timeout | null = null;

  constructor(private windowMs: number = 10 * 60 * 1000) {
    this.timer = setInterval(() => this.sweep(), Math.max(1000, this.windowMs / 4));
  }

  /**
   * seenRecently returns true if the id was seen within the window, and
   * records the current observation if not seen or expired.
   */
  seenRecently(id: string): boolean {
    if (!id) return false;

    const now = Date.now();
    const seenAt = this.cache.get(id);

    if (seenAt !== undefined && now - seenAt < this.windowMs) {
      return true;
    }

    this.cache.set(id, now);
    return false;
  }

  private sweep(): void {
    const cutoff = Date.now() - this.windowMs;
    for (const [id, seenAt] of this.cache.entries()) {
      if (seenAt < cutoff) {
        this.cache.delete(id);
      }
    }
  }

  close(): void {
    if (this.timer) {
      clearInterval(this.timer);
      this.timer = null;
    }
    this.cache.clear();
  }
}
