import time
import threading
from typing import Dict

class DeduplicationCache:
    def __init__(self, window_seconds: float = 600.0):
        self.window_seconds = window_seconds
        self.cache: Dict[str, float] = {}
        self.lock = threading.Lock()
        self.running = True
        self.sweeper = threading.Thread(target=self._sweep_loop, name="dedup-sweeper", daemon=True)
        self.sweeper.start()

    def seen_recently(self, id: str) -> bool:
        if not id:
            return False
        
        now = time.time()
        with self.lock:
            seen_at = self.cache.get(id)
            if seen_at is not None and now - seen_at < self.window_seconds:
                return True
            self.cache[id] = now
            return False

    def _sweep_loop(self):
        sweep_interval = max(1.0, self.window_seconds / 4)
        while self.running:
            time.sleep(sweep_interval)
            self.sweep()

    def sweep(self):
        now = time.time()
        cutoff = now - self.window_seconds
        with self.lock:
            # Create a list of keys to delete to avoid "dictionary changed size during iteration"
            to_delete = [k for k, v in self.cache.items() if v < cutoff]
            for k in to_delete:
                del self.cache[k]

    def close(self):
        self.running = False
        with self.lock:
            self.cache.clear()
