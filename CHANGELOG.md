## Unreleased

### Improvements

- Added `Snapshot` and `Restore` to `lru.Cache` and `simplelru.LRU` for
  exporting and rebuilding an ordered (oldest to newest) view of the cache
  without changing recency. `Restore` rejects duplicate keys while leaving
  the existing cache unchanged, drops the oldest surplus entries when the
  snapshot exceeds capacity, and fires the eviction callback for entries
  removed by the replacement.

### Changes

### Fixed

### Security
