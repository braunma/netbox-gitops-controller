import pynetbox
from pynetbox.core.response import Record
from rich.console import Console

console = Console()

MANAGED_TAG_SLUG = "gitops"
MANAGED_TAG_COLOR = "00bcd4" # Cyan

class BaseSyncer:
    def __init__(self, nb, dry_run: bool = False):
        self.nb = nb
        self.dry_run = dry_run
        self.cache = {}
        self.managed_tag_id = self._ensure_managed_tag_exists()

    def _ensure_managed_tag_exists(self):
        if self.dry_run: return 0
        try:
            tag = self.nb.extras.tags.get(slug=MANAGED_TAG_SLUG)
            if not tag:
                console.print(f"[green]Creating system tag: {MANAGED_TAG_SLUG}[/green]")
                tag = self.nb.extras.tags.create(
                    name="GitOps Managed", slug=MANAGED_TAG_SLUG,
                    color=MANAGED_TAG_COLOR, description="Automatically managed by NetBox GitOps Controller"
                )
            return tag.id
        except Exception as e:
            console.print(f"[red]Warning: Could not ensure gitops tag exists: {e}[/red]")
            return None

    def _get_cached_id(self, app, endpoint, identifier):
        if not identifier: return None
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
                    if ref: self.cache[key][str(ref)] = item.id
                    if slug and name: self.cache[key][str(name)] = item.id
            except Exception as e:
                console.print(f"[red]Cache Error {key}: {e}[/red]")
                self.cache[key] = {}
        return self.cache[key].get(str(identifier))

    def _prepare_payload(self, data):
        """Bereinigt Payload und injiziert IMMER den GitOps-Tag als ID."""
        payload = data.copy()
        
        current_tags = payload.get('tags', [])
        normalized_tags = []
        has_gitops = False
        
        # Alles zu IDs wandeln, wenn möglich
        for t in current_tags:
            if isinstance(t, int): 
                normalized_tags.append(t)
                if t == self.managed_tag_id: has_gitops = True
            elif isinstance(t, dict): 
                if t.get('slug') == MANAGED_TAG_SLUG: has_gitops = True
                normalized_tags.append(t)
            elif isinstance(t, str):
                if t == MANAGED_TAG_SLUG: has_gitops = True
                normalized_tags.append({'slug': t})

        if not has_gitops and self.managed_tag_id:
            normalized_tags.append(self.managed_tag_id)
            
        payload['tags'] = normalized_tags
        return payload

    def _get_id_from_obj(self, obj):
        if isinstance(obj, int): return obj
        if hasattr(obj, 'id'): return obj.id
        if isinstance(obj, dict) and 'id' in obj: return obj['id']
        return None

    def _diff_and_update(self, existing_obj, desired_data, endpoint_name="object"):
        changes = {}
        
        for key, desired_value in desired_data.items():
            if key == 'slug' and endpoint_name == 'racks': continue
            if desired_value is None: continue

            # Key Mapping (foo_id -> foo)
            check_key = key
            if key.endswith('_id') and not hasattr(existing_obj, key):
                candidate = key[:-3]
                if hasattr(existing_obj, candidate): check_key = candidate

            current_value = getattr(existing_obj, check_key, None)

            # --- 1. TAGS ---
            if key == 'tags':
                # WICHTIG: Templates haben keine Tags, wir müssen prüfen ob das Attribut existiert
                if not hasattr(existing_obj, 'tags'):
                    continue

                current_ids = set()
                if current_value:
                    for t in current_value:
                        tid = self._get_id_from_obj(t)
                        if tid: current_ids.add(tid)
                
                desired_ids = set()
                for t in desired_value:
                    tid = self._get_id_from_obj(t)
                    if tid: 
                        desired_ids.add(tid)
                    elif isinstance(t, dict) and t.get('slug') == MANAGED_TAG_SLUG:
                        desired_ids.add(self.managed_tag_id)
                
                if current_ids != desired_ids:
                    changes[key] = desired_value
                continue

            # --- 2. FOREIGN KEYS ---
            if isinstance(desired_value, int):
                cur_id = self._get_id_from_obj(current_value)
                if cur_id is not None:
                    current_value = cur_id
            
            # --- 3. STATUS / CHOICES ---
            if hasattr(current_value, 'value'): current_value = current_value.value

            # --- 4. PRIMITIVE NORMALISIERUNG ---
            if current_value is None and desired_value == "": current_value = ""
            if current_value == "" and desired_value is None: desired_value = ""
            
            if isinstance(current_value, str) and isinstance(desired_value, str):
                if current_value.lower() == desired_value.lower(): continue

            if current_value != desired_value:
                changes[key] = desired_value

        if changes:
            display_name = getattr(existing_obj, 'name', getattr(existing_obj, 'model', 'Item'))
            if self.dry_run:
                console.print(f"[yellow][DRY-RUN] Would UPDATE {endpoint_name} {display_name}: {list(changes.keys())}[/yellow]")
            else:
                try:
                    existing_obj.update(changes)
                    return True
                except Exception as e:
                    console.print(f"[red]Failed to update {display_name}: {e}[/red]")
        return False

    def ensure_object(self, app, endpoint, lookup_data, create_data):
        api_obj = getattr(getattr(self.nb, app), endpoint)
        
        if endpoint == 'racks' and 'slug' in lookup_data:
            lookup_data = {'site_id': lookup_data.get('site_id'), 'name': create_data['name']}

        exists = None
        if not (self.dry_run and 0 in lookup_data.values()):
            try: exists = api_obj.get(**lookup_data)
            except ValueError: pass

        display_name = create_data.get('name') or lookup_data
        final_payload = self._prepare_payload(create_data)
        if endpoint == 'racks' and 'slug' in final_payload: final_payload.pop('slug')

        if not exists:
            if self.dry_run:
                console.print(f"[yellow][DRY-RUN] Would CREATE {endpoint} (tagged): {display_name}[/yellow]")
                ref = create_data.get('slug', create_data.get('name'))
                self._update_cache(app, endpoint, ref, 0)
                return None
            else:
                try:
                    console.print(f"[green]Creating {endpoint} (tagged): {display_name}[/green]")
                    new_obj = api_obj.create(**final_payload)
                    slug = getattr(new_obj, 'slug', None)
                    name = getattr(new_obj, 'name', getattr(new_obj, 'model', getattr(new_obj, 'prefix', None)))
                    ref = slug if slug else name
                    self._update_cache(app, endpoint, ref, new_obj.id)
                    return new_obj
                except Exception as e:
                    console.print(f"[red]Failed to create {display_name}: {e}[/red]")
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
    # WICHTIG: Die korrigierte sync_children Methode für Device Types!
    # -------------------------------------------------------------------------
    def sync_children(self, app, endpoint, parent_filter, child_data_list, key_field='name'):
        api_obj = getattr(getattr(self.nb, app), endpoint)
        existing_items = list(api_obj.filter(**parent_filter))
        existing_map = {getattr(i, key_field): i for i in existing_items}
        seen_keys = set()

        for data in child_data_list:
            unique_key = data.get(key_field)
            if not unique_key: continue
            seen_keys.add(unique_key)
            
            payload = data.copy()
            payload.update(parent_filter)

            # FIX 1: ID Korrektur für Create Payload (device_type_id -> device_type)
            # NetBox API erwartet 'device_type' beim Erstellen, aber 'device_type_id' beim Filtern
            if 'device_type_id' in payload:
                payload['device_type'] = payload.pop('device_type_id')

            full_payload = self._prepare_payload(payload)

            # FIX 2: Tags entfernen für Templates 
            # (Templates unterstützen keine Tags -> sonst gibt es Error 400 oder Attribute Error)
            if endpoint in ['interface_templates', 'front_port_templates', 'rear_port_templates', 'power_port_templates', 'module_bay_templates']:
                if 'tags' in full_payload:
                    full_payload.pop('tags')

            if unique_key in existing_map:
                existing_obj = existing_map[unique_key]
                self._diff_and_update(existing_obj, full_payload, f"{endpoint} child")
            else:
                if self.dry_run:
                    console.print(f"[yellow][DRY-RUN] Would CREATE Child {endpoint}: {unique_key}[/yellow]")
                else:
                    try:
                        console.print(f"[green]Creating Child {endpoint}: {unique_key}[/green]")
                        api_obj.create(**full_payload)
                    except Exception as e:
                        console.print(f"[red]Failed Child Create {unique_key}: {e}[/red]")

        # FIX 3: Cleanup Logik sicher machen (Absturz verhindern, wenn tags-Feld fehlt)
        for key, obj in existing_map.items():
            if key not in seen_keys:
                is_managed = False
                
                # Check 1: Hat es überhaupt Tags? (Templates haben keine!)
                if hasattr(obj, 'tags') and obj.tags:
                    obj_tags = [t.slug for t in obj.tags]
                    if MANAGED_TAG_SLUG in obj_tags:
                        is_managed = True
                
                # Check 2: Ist es ein Template? (Dann ist es implizit managed, weil es in der Definition steht)
                elif endpoint in ['interface_templates', 'front_port_templates', 'rear_port_templates', 'power_port_templates']:
                     is_managed = True

                if is_managed:
                    if self.dry_run:
                        console.print(f"[red][DRY-RUN] Would DELETE Child {endpoint}: {key}[/red]")
                    else:
                        console.print(f"[red]Deleting Child {endpoint}: {key}[/red]")
                        try: obj.delete()
                        except Exception as e: console.print(f"[red]Failed Delete: {e}[/red]")
                else:
                    console.print(f"[dim]Ignoring unmanaged item in {endpoint}: {key}[/dim]")