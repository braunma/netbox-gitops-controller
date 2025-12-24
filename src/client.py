import pynetbox
from rich.console import Console
from src.syncers.base import MANAGED_TAG_SLUG

console = Console()

class NetBoxClient:
    def __init__(self, url: str, token: str, dry_run: bool = False):
        self.nb = pynetbox.api(url, token=token)
        self.nb.http_session.verify = False 
        self.dry_run = dry_run
        
        self.cache = {
            'sites': {}, 'roles': {}, 'device_types': {}, 'racks': {},
            'vlans': {}, 'vrfs': {}, 'tags': {}, 'module_types': {},
            'manufacturers': {}
        }
        
        self.managed_tag_id = self._ensure_tag(MANAGED_TAG_SLUG)

    def _ensure_tag(self, slug):
        if self.dry_run: return 0
        tag = self.nb.extras.tags.get(slug=slug)
        if not tag:
            try:
                tag = self.nb.extras.tags.create(name="GitOps Managed", slug=slug, color="00bcd4")
            except Exception:
                tag = self.nb.extras.tags.get(slug=slug)
        return tag.id if tag else 0

    def _safe_load_queryset(self, queryset, cache_key, use_name=False):
        """Lädt NetBox-Objekte mit maximaler Sicherheit in den Cache."""
        try:
            if cache_key not in self.cache:
                self.cache[cache_key] = {}
                
            items = list(queryset)
            for item in items:
                data = dict(item)
                
                # 1. Slug extrahieren
                slug = data.get('slug')
                
                # 2. Name/Model extrahieren
                name_val = data.get('model') or data.get('name') or data.get('label')
                
                # 3. In den Cache schreiben (nur IDs!)
                if slug:
                    self.cache[cache_key][str(slug)] = item.id
                if name_val and isinstance(name_val, str):
                    self.cache[cache_key][str(name_val)] = item.id
                
                # 4. Spezial-Handling für verschachtelte Objekte
                # Manche NetBox-Felder sind dicts mit 'display' Key
                if isinstance(name_val, dict):
                    display_name = name_val.get('display')
                    if display_name:
                        self.cache[cache_key][str(display_name)] = item.id
                        
        except Exception as e:
            console.print(f"[red]Error loading {cache_key}: {e}[/red]")

    def reload_cache(self, site_slug: str):
        """
        Lädt Site-spezifische Daten in den Cache.
        Globale Daten (Device Types, Module Types, etc.) werden in reload_global_cache() geladen.
        """
        console.print(f"[cyan]Reloading cache for site: {site_slug}...[/cyan]")
        
        # Site sicher identifizieren
        site_obj = self.nb.dcim.sites.get(slug=site_slug)
        if not site_obj:
            console.print(f"[dim yellow]Site slug '{site_slug}' not found, trying name...[/dim yellow]")
            site_obj = self.nb.dcim.sites.get(name=site_slug)

        if site_obj:
            console.print(f"[dim]Found Site: {site_obj.name} (ID: {site_obj.id})[/dim]")
            
            # Site-spezifische Ressourcen laden
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
            
            # Warnung wenn keine Racks gefunden
            if not self.cache['racks']:
                console.print(f"[yellow]Warning: No racks found for Site ID {site_obj.id}[/yellow]")
        else:
            console.print(f"[red]Error: Site '{site_slug}' not found![/red]")

    def reload_global_cache(self):
        """
        Lädt globale Ressourcen (nicht site-spezifisch).
        Wird einmal vor dem Device-Reconcile aufgerufen.
        """
        console.print("[cyan]Loading global caches...[/cyan]")
        
        # Device Types
        console.print("[dim]→ device_types[/dim]")
        self._safe_load_queryset(
            self.nb.dcim.device_types.all(), 
            'device_types'
        )
        
        # Module Types
        console.print("[dim]→ module_types[/dim]")
        self._safe_load_queryset(
            self.nb.dcim.module_types.all(), 
            'module_types'
        )
        
        # Device Roles
        console.print("[dim]→ roles[/dim]")
        self._safe_load_queryset(
            self.nb.dcim.device_roles.all(), 
            'roles'
        )
        
        # Manufacturers
        console.print("[dim]→ manufacturers[/dim]")
        self._safe_load_queryset(
            self.nb.dcim.manufacturers.all(), 
            'manufacturers'
        )
        
        # Sites (für Cross-Site Referenzen)
        console.print("[dim]→ sites[/dim]")
        self._safe_load_queryset(
            self.nb.dcim.sites.all(), 
            'sites'
        )
        
        # VRFs (global)
        console.print("[dim]→ vrfs[/dim]")
        self._safe_load_queryset(
            self.nb.ipam.vrfs.all(), 
            'vrfs', 
            use_name=True
        )
        
        console.print("[green]✓ Global caches loaded[/green]")

    def get_id(self, resource: str, key: str) -> int | None:
        """
        Holt eine ID aus dem Cache.
        Returns None wenn nicht gefunden.
        """
        if not key:
            return None
            
        res_cache = self.cache.get(resource, {})
        
        # Lookup (case-insensitive für bessere Robustheit)
        result = res_cache.get(str(key))
        
        # Debug-Logging nur bei Problemen
        if result is None and resource in ['module_types', 'device_types']:
            console.print(f"[dim yellow]⚠ '{key}' not found in {resource} cache[/dim yellow]")
            if res_cache:
                console.print(f"[dim]Available: {list(res_cache.keys())[:5]}...[/dim]")
        
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
                console.print(f"[yellow][DRY] Set Primary IP for {dev.name}[/yellow]")
            else:
                dev.update({field: ip_id})
                console.print(f"[blue]Set Primary IP for {dev.name}[/blue]")

    def delete_by_id(self, app, endpoint, obj_id):
        api = getattr(getattr(self.nb, app), endpoint)
        obj = api.get(obj_id)
        if obj:
            if self.dry_run:
                console.print(f"[yellow][DRY] DELETE {obj}[/yellow]")
            else:
                try:
                    obj.delete()
                except Exception as e:
                    console.print(f"[red]Failed delete: {e}[/red]")

    def apply(self, app: str, endpoint: str, lookup: dict, payload: dict):
        """
        Idempotentes Create-or-Update mit Managed Tag Injection.
        """
        api_obj = getattr(getattr(self.nb, app), endpoint)
        
        res = list(api_obj.filter(**lookup))
        existing = res[0] if res else None
        
        final_payload = payload.copy()
        
        # Tags bereinigen und Managed Tag hinzufügen
        if 'tags' in final_payload:
            # Nur Integers behalten (Strings wie 'gitops' rauswerfen)
            current_tags = [t for t in final_payload['tags'] if isinstance(t, int)]
            if self.managed_tag_id and self.managed_tag_id not in current_tags:
                current_tags.append(self.managed_tag_id)
            final_payload['tags'] = current_tags
        else:
            # Fallback wenn keine Tags im Payload
            if self.managed_tag_id:
                final_payload['tags'] = [self.managed_tag_id]

        if not existing:
            # CREATE
            if self.dry_run:
                console.print(f"[yellow][DRY] Create {endpoint}: {lookup}[/yellow]")
                # Mock-Objekt für Dry-Run
                return type('MockObject', (), {'id': 0, 'name': lookup.get('name')})()
            
            try:
                console.print(f"[green]Create {endpoint}: {lookup}[/green]")
                return api_obj.create(**final_payload)
            except Exception as e:
                console.print(f"[red]Error creating {lookup}: {e}[/red]")
                return None
        else:
            # UPDATE
            if not self.dry_run:
                try:
                    existing.update(final_payload)
                    console.print(f"[blue]Updated {endpoint}: {lookup}[/blue]")
                except Exception as e:
                    console.print(f"[red]Error updating {lookup}: {e}[/red]")
            else:
                console.print(f"[yellow][DRY] Update {endpoint}: {lookup}[/yellow]")
            return existing