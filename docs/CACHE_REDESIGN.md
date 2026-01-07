# Cache Architecture Redesign

## Current Problems

### 1. Cache Key Collision Bug
**Severity**: Critical
**Impact**: Wrong VLANs/racks assigned to devices across sites

```go
// BROKEN: Last site loaded wins
cache["vlans"]["idrac"] = 75  // denbi-cbf
cache["vlans"]["idrac"] = 74  // denbi-steglitz (overwrites!)
```

### 2. Inconsistent Lookup Strategy
- Sites: Live API lookup
- Devices: Live API lookup
- VLANs: Mix of cache + live (current branch)
- Device Types: Cache only
- Racks: Live API lookup

**Problem**: No clear pattern, hard to maintain

## Proposed Solution: Site-Scoped Cache with Composite Keys

### Design Principles
1. **Namespaced Keys**: Prevent collisions between sites
2. **Consistent Strategy**: All site-specific resources use same pattern
3. **Performance**: Single cache load, fast lookups
4. **Type Safety**: Strongly-typed cache keys

### New Cache Structure

```go
type CacheKey struct {
    Resource string  // "vlans", "racks", etc.
    SiteID   int     // 0 for global resources
    Name     string  // Resource identifier
}

func (k CacheKey) String() string {
    if k.SiteID == 0 {
        return fmt.Sprintf("%s:%s", k.Resource, k.Name)
    }
    return fmt.Sprintf("%s:site-%d:%s", k.Resource, k.SiteID, k.Name)
}

// Cache structure
type Cache struct {
    mu    sync.RWMutex
    data  map[string]int  // Composite key -> ID
}

// Example keys:
// "vlans:site-8:idrac" -> 75
// "vlans:site-7:idrac" -> 74
// "device_types:poweredge-r740" -> 15 (global, no site)
```

### API Changes

```go
// OLD (collision-prone)
GetID(resource, name string) (int, bool)

// NEW (site-aware)
GetGlobalID(resource, name string) (int, bool)
GetSiteID(resource string, siteID int, name string) (int, bool)
```

### Migration Path

**Phase 1: Add new methods (backwards compatible)**
```go
func (cm *CacheManager) GetSiteID(resource string, siteID int, name string) (int, bool) {
    key := fmt.Sprintf("%s:site-%d:%s", resource, siteID, name)
    cm.mu.RLock()
    defer cm.mu.RUnlock()
    id, ok := cm.data[key]
    return id, ok
}
```

**Phase 2: Update callers**
- VLANs: Use GetSiteID()
- Racks: Use GetSiteID()
- Device Types: Use GetGlobalID()

**Phase 3: Remove old GetID() method**

## Alternative: Hierarchical Cache

```go
type Cache struct {
    global map[string]map[string]int           // resource -> name -> ID
    sites  map[int]map[string]map[string]int   // siteID -> resource -> name -> ID
}

// Access patterns:
cache.global["device_types"]["poweredge-r740"] // -> 15
cache.sites[8]["vlans"]["idrac"]                // -> 75
cache.sites[7]["vlans"]["idrac"]                // -> 74
```

**Pros**: More intuitive, type-safe
**Cons**: More complex, harder to iterate

## Recommendation

**Use composite string keys** for:
- Simpler implementation
- Easier serialization/debugging
- Consistent with current flat structure
- Gradual migration path

## Testing Strategy

```go
func TestCacheMultiSite(t *testing.T) {
    cache := NewCache()

    // Load VLANs from two sites with same name
    cache.SetSiteID("vlans", 8, "idrac", 75)
    cache.SetSiteID("vlans", 7, "idrac", 74)

    // Verify no collision
    id1, _ := cache.GetSiteID("vlans", 8, "idrac")
    assert.Equal(t, 75, id1)

    id2, _ := cache.GetSiteID("vlans", 7, "idrac")
    assert.Equal(t, 74, id2)
}
```

## Performance Considerations

- Single cache load per site: O(N) where N = total resources
- Lookup: O(1) with string keys
- Memory: Acceptable (IDs are integers, keys are strings)
- No API calls during reconciliation (fast)

## Rollout Plan

1. Implement new cache structure in separate PR
2. Add comprehensive tests
3. Update VLAN lookups first (highest priority)
4. Update rack lookups
5. Deprecate old API
6. Update Python to match (contribute upstream)
