import pynetbox
from rich.console import Console

from src.constants import (
    MANAGED_TAG_SLUG,
    MANAGED_TAG_NAME,
    MANAGED_TAG_COLOR,
    CACHE_RESOURCE_TYPES,
)
from src.utils import (
    log_error,
    log_warning,
    log_info,
    log_debug,
    log_success,
    log_dry_run,
)

console = Console()

class NetBoxClient:
    """
    Modern NetBox client for device and cable reconciliation.

    CACHING STRATEGY (New - Eager Loading):
    ========================================
    This client uses eager pre-loading of all required resources before reconciliation.
    All caches are populated upfront via reload_global_cache() and reload_cache().

    IMPORTANT FOR GO MIGRATION:
    - Eager loading is IDEAL for concurrent goroutines (no race conditions)
    - All data loaded once → read-only access → safe for parallel processing
    - In Go, populate caches in main goroutine, then spawn workers
    - Use sync.RWMutex if you need cache updates during reconciliation
    - Example Go pattern:
        cache := preloadAllResources()  // main goroutine
        var wg sync.WaitGroup
        for _, device := range devices {
            wg.Add(1)
            go reconcileDevice(device, cache)  // safe concurrent reads
        }
        wg.Wait()

    TAG MANAGEMENT (Single Source of Truth):
    ========================================
    - _ensure_tag() creates/verifies the GitOps managed tag on initialization
    - Stores tag ID in self.managed_tag_id
    - All syncers receive this ID (no duplicate tag creation logic)
    - Tag injection happens via apply() method for new controller engine
    - Legacy syncers inject via BaseSyncer._prepare_payload()
    """

    def __init__(self, url: str, token: str, dry_run: bool = False):
        """
        Initialize NetBox client with eager caching strategy.

        Args:
            url: NetBox instance URL
            token: API authentication token
            dry_run: Dry-run mode flag

        Note:
            Call reload_global_cache() and reload_cache(site) before reconciliation
            to populate resource caches.
        """
        self.nb = pynetbox.api(url, token=token)
        self.nb.http_session.verify = False
        self.dry_run = dry_run

        # Eager cache structure (pre-loaded before reconciliation)
        self.cache = {
            'sites': {},          # Global: all sites
            'roles': {},          # Global: all device roles
            'device_types': {},   # Global: all device types
            'racks': {},          # Site-specific: racks per site
            'vlans': {},          # Site-specific: VLANs per site
            'vrfs': {},           # Global: all VRFs
            'tags': {},           # Global: all tags
            'module_types': {},   # Global: all module types
            'manufacturers': {}   # Global: all manufacturers
        }

        # Single source of truth for managed tag
        self.managed_tag_id = self._ensure_tag(MANAGED_TAG_SLUG)

    def _ensure_tag(self, slug: str) -> int:
        """
        Ensure the gitops managed tag exists in NetBox.

        Args:
            slug: Tag slug to create/verify

        Returns:
            Tag ID or 0 in dry-run mode
        """
        if self.dry_run:
            return 0

        tag = self.nb.extras.tags.get(slug=slug)
        if not tag:
            try:
                tag = self.nb.extras.tags.create(
                    name=MANAGED_TAG_NAME,
                    slug=slug,
                    color=MANAGED_TAG_COLOR
                )
                log_success(f"Created system tag: {slug}")
            except Exception as e:
                # Handle race condition: another process may have created it
                log_warning(f"Tag creation failed, retrying lookup: {e}")
                tag = self.nb.extras.tags.get(slug=slug)

        return tag.id if tag else 0

    def _safe_load_queryset(self, queryset, cache_key: str, use_name: bool = False):
        """
        Load NetBox objects into cache with maximum safety.

        Args:
            queryset: NetBox API queryset
            cache_key: Cache key for this resource type
            use_name: Whether to index by name in addition to slug
        """
        try:
            if cache_key not in self.cache:
                self.cache[cache_key] = {}

            items = list(queryset)
            for item in items:
                data = dict(item)

                # 1. Extract slug
                slug = data.get('slug')

                # 2. Extract name/model/label
                name_val = data.get('model') or data.get('name') or data.get('label')

                # 3. Write to cache (IDs only)
                if slug:
                    self.cache[cache_key][str(slug)] = item.id
                if name_val and isinstance(name_val, str):
                    self.cache[cache_key][str(name_val)] = item.id

                # 4. Special handling for nested objects
                # Some NetBox fields are dicts with 'display' key
                if isinstance(name_val, dict):
                    display_name = name_val.get('display')
                    if display_name:
                        self.cache[cache_key][str(display_name)] = item.id

        except Exception as e:
            log_error(f"Error loading {cache_key}", e)

    def reload_cache(self, site_slug: str):
        """
        Load site-specific data into cache (EAGER LOADING).

        This method is part of the eager caching strategy - call it BEFORE reconciliation
        for each site you'll be working with. This ensures all site-specific resources
        (VLANs, racks) are pre-loaded for fast lookup during device processing.

        GO MIGRATION NOTE:
        - In Go, call this once per site in the main goroutine before spawning workers
        - Cache is then read-only during concurrent reconciliation
        - No mutex needed for reads (safe concurrent access)

        Args:
            site_slug: Site slug or name to reload cache for
        """
        log_info(f"Reloading cache for site: {site_slug}...")

        # Safely identify site
        site_obj = self.nb.dcim.sites.get(slug=site_slug)
        if not site_obj:
            log_warning(f"Site slug '{site_slug}' not found, trying name...")
            site_obj = self.nb.dcim.sites.get(name=site_slug)

        if site_obj:
            log_debug(f"Found Site: {site_obj.name} (ID: {site_obj.id})")

            # Load site-specific resources
            self._safe_load_queryset(
                self.nb.ipam.vlans.filter(site_id=site_obj.id),
                'vlans',
                use_name=True
            )

            self._safe_load_queryset(
                self.nb.dcim.racks.filter(site_id=site_obj.id),
                'racks',
                use_name=True
            )

            # Warn if no racks found
            if not self.cache['racks']:
                log_warning(f"No racks found for Site ID {site_obj.id}")
        else:
            log_error(f"Site '{site_slug}' not found!")

    def reload_global_cache(self):
        """
        Load global resources (EAGER LOADING) - not site-specific.

        This method implements the eager caching pattern by pre-loading ALL global
        resources before any device reconciliation begins. This is the preferred
        approach for concurrent processing.

        GO MIGRATION NOTE:
        - Call this ONCE in the main goroutine before spawning device reconcilers
        - After this call, cache is read-only → safe for concurrent goroutine access
        - No synchronization needed for reads (huge performance benefit)
        - Pattern:
            client.ReloadGlobalCache()      // once in main
            for site := range sites {
                client.ReloadCache(site)     // once per site in main
            }
            // Now spawn concurrent reconcilers (safe reads)

        Called once before device reconciliation loop.
        """
        log_info("Loading global caches...")

        # Device Types
        log_debug("→ device_types")
        self._safe_load_queryset(
            self.nb.dcim.device_types.all(),
            'device_types'
        )

        # Module Types
        log_debug("→ module_types")
        self._safe_load_queryset(
            self.nb.dcim.module_types.all(),
            'module_types'
        )

        # Device Roles
        log_debug("→ roles")
        self._safe_load_queryset(
            self.nb.dcim.device_roles.all(),
            'roles'
        )

        # Manufacturers
        log_debug("→ manufacturers")
        self._safe_load_queryset(
            self.nb.dcim.manufacturers.all(),
            'manufacturers'
        )

        # Sites (for cross-site references)
        log_debug("→ sites")
        self._safe_load_queryset(
            self.nb.dcim.sites.all(),
            'sites'
        )

        # VRFs (global)
        log_debug("→ vrfs")
        self._safe_load_queryset(
            self.nb.ipam.vrfs.all(),
            'vrfs',
            use_name=True
        )

        log_success("✓ Global caches loaded")

    def get_id(self, resource: str, key: str) -> int | None:
        """
        Get an ID from cache.

        Args:
            resource: Resource type (e.g., 'sites', 'device_types')
            key: Key to lookup (slug or name)

        Returns:
            Integer ID or None if not found
        """
        if not key:
            return None

        res_cache = self.cache.get(resource, {})

        # Lookup
        result = res_cache.get(str(key))

        # Debug logging only for critical missing entries
        if result is None and resource in ['module_types', 'device_types']:
            log_warning(f"⚠ '{key}' not found in {resource} cache")
            if res_cache:
                log_debug(f"Available: {list(res_cache.keys())[:5]}...")

        return result

    # -------------------------------------------------------------------------
    # Helper Methods
    # -------------------------------------------------------------------------

    def get_object(self, app, endpoint, obj_id, nested=False):
        if not obj_id: 
            return None
        api = getattr(getattr(self.nb, app), endpoint)
        obj = api.get(obj_id)
        if obj: 
            return dict(obj)
        return None
    
    def get_components(self, device_id, endpoint):
        if not device_id: 
            return []
        api = getattr(self.nb.dcim, endpoint)
        return [dict(i) for i in api.filter(device_id=device_id)]

    def get_termination(self, device_name, port_name):
        devs = list(self.nb.dcim.devices.filter(name=device_name))
        if not devs: 
            return None, None
        
        if len(devs) > 1:
            console.print(
                f"[dim yellow]Warning: Multiple devices named '{device_name}'. "
                f"Using first (ID {devs[0].id}).[/dim yellow]"
            )
        
        dev = devs[0]
        
        # Interface
        res = self.nb.dcim.interfaces.get(device_id=dev.id, name=port_name)
        if res: 
            return res, 'dcim.interface'

        # Front Port
        res = self.nb.dcim.front_ports.get(device_id=dev.id, name=port_name)
        if res: 
            return res, 'dcim.frontport'

        # Rear Port
        res = self.nb.dcim.rear_ports.get(device_id=dev.id, name=port_name)
        if res: 
            return res, 'dcim.rearport'

        return None, None

    def update_device_primary_ip(self, device_id, ip_id):
        """
        Set primary IP address for a device.

        Args:
            device_id: Device ID
            ip_id: IP address ID to set as primary
        """
        dev = self.nb.dcim.devices.get(device_id)
        if not dev:
            return

        ip = self.nb.ipam.ip_addresses.get(ip_id)
        if not ip:
            return

        field = 'primary_ip4' if ip.family.value == 4 else 'primary_ip6'
        current = getattr(dev, field)
        current_id = current.id if current else None

        if current_id != ip_id:
            if self.dry_run:
                log_dry_run("Set Primary IP", f"for {dev.name}")
            else:
                dev.update({field: ip_id})
                log_info(f"Set Primary IP for {dev.name}")

    def delete_by_id(self, app: str, endpoint: str, obj_id: int):
        """
        Delete an object by ID.

        Args:
            app: NetBox app (e.g., 'dcim', 'ipam')
            endpoint: API endpoint (e.g., 'cables', 'devices')
            obj_id: Object ID to delete
        """
        api = getattr(getattr(self.nb, app), endpoint)
        obj = api.get(obj_id)
        if obj:
            if self.dry_run:
                log_dry_run("DELETE", str(obj))
            else:
                try:
                    obj.delete()
                    log_debug(f"Deleted {endpoint} ID {obj_id}")
                except Exception as e:
                    log_error(f"Failed to delete {endpoint} ID {obj_id}", e)

    def apply(self, app: str, endpoint: str, lookup: dict, payload: dict):
        """
        Idempotent create-or-update with managed tag injection.

        Args:
            app: NetBox app (e.g., 'dcim', 'ipam')
            endpoint: API endpoint
            lookup: Lookup criteria for existing object
            payload: Data to create/update

        Returns:
            Created or updated object, or None on error
        """
        api_obj = getattr(getattr(self.nb, app), endpoint)

        res = list(api_obj.filter(**lookup))
        existing = res[0] if res else None

        final_payload = payload.copy()

        # Clean tags and inject managed tag
        if 'tags' in final_payload:
            # Keep only integers (remove strings like 'gitops')
            current_tags = [t for t in final_payload['tags'] if isinstance(t, int)]
            if self.managed_tag_id and self.managed_tag_id not in current_tags:
                current_tags.append(self.managed_tag_id)
            final_payload['tags'] = current_tags
        else:
            # Fallback if no tags in payload
            if self.managed_tag_id:
                final_payload['tags'] = [self.managed_tag_id]

        if not existing:
            # CREATE
            if self.dry_run:
                log_dry_run("Create", f"{endpoint}: {lookup}")
                # Mock object for dry-run
                return type('MockObject', (), {'id': 0, 'name': lookup.get('name')})()

            try:
                log_success(f"Create {endpoint}: {lookup}")
                return api_obj.create(**final_payload)
            except Exception as e:
                log_error(f"Error creating {lookup}", e)
                return None
        else:
            # UPDATE
            if not self.dry_run:
                try:
                    existing.update(final_payload)
                    log_info(f"Updated {endpoint}: {lookup}")
                except Exception as e:
                    log_error(f"Error updating {lookup}", e)
            else:
                log_dry_run("Update", f"{endpoint}: {lookup}")
            return existing