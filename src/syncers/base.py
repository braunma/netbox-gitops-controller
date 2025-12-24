import pynetbox
from pynetbox.core.response import Record
from rich.console import Console

from src.constants import (
    MANAGED_TAG_SLUG,
    TEMPLATE_ENDPOINTS,
    FIELD_TRANSFORMS,
)
from src.utils import (
    get_id_from_object,
    log_error,
    log_warning,
    log_success,
    log_info,
    log_debug,
    log_dry_run,
)

console = Console()

class BaseSyncer:
    """
    Base syncer class for legacy NetBox resource synchronization.

    CACHING STRATEGY (Legacy - Lazy Loading):
    =========================================
    This syncer uses lazy-load caching for backward compatibility with the legacy
    Python synchronization engine. Cache entries are loaded on-demand when first accessed
    via _get_cached_id().

    IMPORTANT FOR GO MIGRATION:
    - Lazy loading reduces initial memory footprint but requires synchronization locks
    - NOT ideal for concurrent goroutines (race conditions on cache writes)
    - New Go code should use EAGER loading (see NetBoxClient)
    - Keep this strategy only for legacy Python syncers that run sequentially

    TAG MANAGEMENT:
    ==============
    - Accepts managed_tag_id from NetBoxClient (single source of truth)
    - No longer creates tags internally (removed _ensure_managed_tag_exists)
    - Injects managed tag via _prepare_payload() for all resources
    """

    def __init__(self, nb, managed_tag_id: int, dry_run: bool = False):
        """
        Initialize base syncer.

        Args:
            nb: PyNetBox API instance
            managed_tag_id: ID of the GitOps managed tag (from NetBoxClient)
            dry_run: Dry-run mode flag
        """
        self.nb = nb
        self.dry_run = dry_run
        self.cache = {}
        self.managed_tag_id = managed_tag_id

    def _get_cached_id(self, app: str, endpoint: str, identifier: str) -> int | None:
        """
        Get cached ID for an object, loading cache if needed (LAZY LOADING).

        This implements the lazy-load caching pattern: cache entries are loaded
        on-demand when first accessed. Subsequent lookups hit the in-memory cache.

        GO MIGRATION NOTE:
        - This pattern requires synchronization (mutex) in concurrent scenarios
        - Race condition: multiple goroutines could try to populate same cache key
        - Recommendation: DO NOT use this pattern in Go - use eager loading instead
        - If you must use lazy loading in Go:
            var cacheMutex sync.RWMutex
            cacheMutex.RLock()
            if cached { cacheMutex.RUnlock(); return cached }
            cacheMutex.RUnlock()
            cacheMutex.Lock()
            // double-check after acquiring write lock
            // populate cache
            cacheMutex.Unlock()

        Args:
            app: NetBox app (e.g., 'dcim', 'ipam')
            endpoint: API endpoint
            identifier: Object identifier (slug or name)

        Returns:
            Object ID or None if not found
        """
        if not identifier:
            return None

        key = f"{app}.{endpoint}"
        if key not in self.cache:
            api_obj = getattr(getattr(self.nb, app), endpoint)
            try:
                all_items = api_obj.all(limit=0)
                self.cache[key] = {}
                for item in all_items:
                    slug = getattr(item, 'slug', None)
                    name = getattr(item, 'name', getattr(item, 'model', getattr(item, 'prefix', None)))
                    ref = slug if slug else name
                    if ref:
                        self.cache[key][str(ref)] = item.id
                    if slug and name:
                        self.cache[key][str(name)] = item.id
            except Exception as e:
                log_error(f"Cache Error {key}", e)
                self.cache[key] = {}

        return self.cache[key].get(str(identifier))

    def _prepare_payload(self, data: dict) -> dict:
        """
        Clean payload and inject gitops managed tag.

        Args:
            data: Payload dictionary

        Returns:
            Cleaned payload with managed tag
        """
        payload = data.copy()

        current_tags = payload.get('tags', [])
        normalized_tags = []
        has_gitops = False

        # Convert everything to IDs where possible
        for t in current_tags:
            if isinstance(t, int):
                normalized_tags.append(t)
                if t == self.managed_tag_id:
                    has_gitops = True
            elif isinstance(t, dict):
                if t.get('slug') == MANAGED_TAG_SLUG:
                    has_gitops = True
                normalized_tags.append(t)
            elif isinstance(t, str):
                if t == MANAGED_TAG_SLUG:
                    has_gitops = True
                normalized_tags.append({'slug': t})

        if not has_gitops and self.managed_tag_id:
            normalized_tags.append(self.managed_tag_id)

        payload['tags'] = normalized_tags
        return payload

    def _diff_and_update(self, existing_obj, desired_data: dict, endpoint_name: str = "object") -> bool:
        """
        Compare existing object with desired data and update if different.

        Args:
            existing_obj: Existing NetBox object
            desired_data: Desired state dictionary
            endpoint_name: Name for logging

        Returns:
            True if updated, False otherwise
        """
        changes = {}

        for key, desired_value in desired_data.items():
            # Skip slug for racks (special case)
            if key == 'slug' and endpoint_name == 'racks':
                continue
            if desired_value is None:
                continue

            # Key mapping (foo_id -> foo)
            check_key = key
            if key.endswith('_id') and not hasattr(existing_obj, key):
                candidate = key[:-3]
                if hasattr(existing_obj, candidate):
                    check_key = candidate

            current_value = getattr(existing_obj, check_key, None)

            # --- 1. TAGS ---
            if key == 'tags':
                # IMPORTANT: Templates don't have tags, must check if attribute exists
                if not hasattr(existing_obj, 'tags'):
                    continue

                current_ids = set()
                if current_value:
                    for t in current_value:
                        tid = get_id_from_object(t)
                        if tid:
                            current_ids.add(tid)

                desired_ids = set()
                for t in desired_value:
                    tid = get_id_from_object(t)
                    if tid:
                        desired_ids.add(tid)
                    elif isinstance(t, dict) and t.get('slug') == MANAGED_TAG_SLUG:
                        desired_ids.add(self.managed_tag_id)

                if current_ids != desired_ids:
                    changes[key] = desired_value
                continue

            # --- 2. FOREIGN KEYS ---
            if isinstance(desired_value, int):
                cur_id = get_id_from_object(current_value)
                if cur_id is not None:
                    current_value = cur_id

            # --- 3. STATUS / CHOICES ---
            if hasattr(current_value, 'value'):
                current_value = current_value.value

            # --- 4. PRIMITIVE NORMALIZATION ---
            if current_value is None and desired_value == "":
                current_value = ""
            if current_value == "" and desired_value is None:
                desired_value = ""

            if isinstance(current_value, str) and isinstance(desired_value, str):
                if current_value.lower() == desired_value.lower():
                    continue

            if current_value != desired_value:
                changes[key] = desired_value

        if changes:
            display_name = getattr(existing_obj, 'name', getattr(existing_obj, 'model', 'Item'))
            if self.dry_run:
                log_dry_run("UPDATE", f"{endpoint_name} {display_name}: {list(changes.keys())}")
            else:
                try:
                    existing_obj.update(changes)
                    return True
                except Exception as e:
                    log_error(f"Failed to update {display_name}", e)
        return False

    def ensure_object(self, app: str, endpoint: str, lookup_data: dict, create_data: dict):
        """
        Ensure an object exists, creating or updating as needed.

        Args:
            app: NetBox app (e.g., 'dcim', 'ipam')
            endpoint: API endpoint
            lookup_data: Criteria to find existing object
            create_data: Data to create/update

        Returns:
            Created or updated object, or None on error/dry-run
        """
        api_obj = getattr(getattr(self.nb, app), endpoint)

        # Special handling for racks
        if endpoint == 'racks' and 'slug' in lookup_data:
            lookup_data = {'site_id': lookup_data.get('site_id'), 'name': create_data['name']}

        exists = None
        if not (self.dry_run and 0 in lookup_data.values()):
            try:
                exists = api_obj.get(**lookup_data)
            except ValueError:
                pass

        display_name = create_data.get('name') or lookup_data
        final_payload = self._prepare_payload(create_data)

        # Remove slug from racks payload
        if endpoint == 'racks' and 'slug' in final_payload:
            final_payload.pop('slug')

        if not exists:
            if self.dry_run:
                log_dry_run("CREATE", f"{endpoint} (tagged): {display_name}")
                ref = create_data.get('slug', create_data.get('name'))
                self._update_cache(app, endpoint, ref, 0)
                return None
            else:
                try:
                    log_success(f"Creating {endpoint} (tagged): {display_name}")
                    new_obj = api_obj.create(**final_payload)
                    slug = getattr(new_obj, 'slug', None)
                    name = getattr(new_obj, 'name', getattr(new_obj, 'model', getattr(new_obj, 'prefix', None)))
                    ref = slug if slug else name
                    self._update_cache(app, endpoint, ref, new_obj.id)
                    return new_obj
                except Exception as e:
                    log_error(f"Failed to create {display_name}", e)
                    return None
        else:
            self._diff_and_update(exists, final_payload, endpoint)
            return exists

    def ensure_object_and_return(self, app, endpoint, lookup_data, create_data):
        return self.ensure_object(app, endpoint, lookup_data, create_data)

    def _update_cache(self, app, endpoint, identifier, obj_id):
        key = f"{app}.{endpoint}"
        if key not in self.cache: self.cache[key] = {}
        if identifier: self.cache[key][str(identifier)] = obj_id

    # -------------------------------------------------------------------------
    # IMPORTANT: Corrected sync_children method for Device Types!
    # -------------------------------------------------------------------------
    def sync_children(self, app: str, endpoint: str, parent_filter: dict, child_data_list: list, key_field: str = 'name'):
        """
        Sync child objects (templates, ports, etc.) with parent filtering.

        Args:
            app: NetBox app
            endpoint: API endpoint for child objects
            parent_filter: Filter criteria for parent object
            child_data_list: List of child object data
            key_field: Field to use as unique key (default: 'name')
        """
        api_obj = getattr(getattr(self.nb, app), endpoint)
        existing_items = list(api_obj.filter(**parent_filter))
        existing_map = {getattr(i, key_field): i for i in existing_items}
        seen_keys = set()

        for data in child_data_list:
            unique_key = data.get(key_field)
            if not unique_key:
                continue
            seen_keys.add(unique_key)

            payload = data.copy()
            payload.update(parent_filter)

            # FIX 1: Field transformation for create payload
            # NetBox API expects different field names for create vs filter
            for old_field, new_field in FIELD_TRANSFORMS.items():
                if old_field in payload:
                    payload[new_field] = payload.pop(old_field)

            full_payload = self._prepare_payload(payload)

            # FIX 2: Remove tags for templates
            # Templates don't support tags, would cause 400 error
            if endpoint in TEMPLATE_ENDPOINTS:
                if 'tags' in full_payload:
                    full_payload.pop('tags')

            if unique_key in existing_map:
                existing_obj = existing_map[unique_key]
                self._diff_and_update(existing_obj, full_payload, f"{endpoint} child")
            else:
                if self.dry_run:
                    log_dry_run("CREATE Child", f"{endpoint}: {unique_key}")
                else:
                    try:
                        log_success(f"Creating Child {endpoint}: {unique_key}")
                        api_obj.create(**full_payload)
                    except Exception as e:
                        log_error(f"Failed Child Create {unique_key}", e)

        # FIX 3: Safe cleanup logic (prevent crash if tags field missing)
        for key, obj in existing_map.items():
            if key not in seen_keys:
                is_managed = False

                # Check 1: Does it have tags? (Templates don't!)
                if hasattr(obj, 'tags') and obj.tags:
                    obj_tags = [t.slug for t in obj.tags]
                    if MANAGED_TAG_SLUG in obj_tags:
                        is_managed = True

                # Check 2: Is it a template? (Implicitly managed if in definition)
                elif endpoint in TEMPLATE_ENDPOINTS:
                    is_managed = True

                if is_managed:
                    if self.dry_run:
                        log_dry_run("DELETE Child", f"{endpoint}: {key}")
                    else:
                        log_warning(f"Deleting Child {endpoint}: {key}")
                        try:
                            obj.delete()
                        except Exception as e:
                            log_error(f"Failed Delete", e)
                else:
                    log_debug(f"Ignoring unmanaged item in {endpoint}: {key}")