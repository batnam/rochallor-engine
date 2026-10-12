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

    if (this.has(id)) return true;
    this.mark(id);
    return false;
  }

  has(id: string): boolean {
    const seenAt = this.cache.get(id);
    return seenAt !== undefined && Date.now() - seenAt < this.windowMs;
  }

  mark(id: string): void {
    if (id) this.cache.set(id, Date.now());
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
