"""
DEPRECATED: This module is no longer used.
Cable synchronization is now handled by DeviceController in Phase 3.
This file is kept for reference but should not be imported.
"""

from src.syncers.base import BaseSyncer, MANAGED_TAG_SLUG
from src.utils import normalize_color, extract_tag_ids_and_slugs
from src.constants import CABLE_COLOR_MAP
from rich.console import Console

console = Console()

class CableSyncer(BaseSyncer):
    
    # Mapping table: API Endpoint -> NetBox Content Type
    TYPE_MAPPING = {
        'interfaces': 'dcim.interface',
        'front_ports': 'dcim.frontport',
        'rear_ports': 'dcim.rearport'
    }

    def sync_cables(self, devices):
        console.rule("[bold]Syncing Cables[/bold]")
        
        for dev_def in devices:
            dev_id = self._get_cached_id('dcim', 'devices', dev_def.name)
            if not dev_id: continue
            
            sources = [
                (dev_def.interfaces, 'interfaces'), 
                (getattr(dev_def, 'front_ports', []), 'front_ports'), 
                (getattr(dev_def, 'rear_ports', []), 'rear_ports')
            ]

            for item_list, endpoint in sources:
                for item in item_list:
                    if hasattr(item, 'link') and item.link:
                        self._process_link(dev_id, dev_def.name, item.name, endpoint, item.link)

    def _is_managed(self, cable_obj):
        """Check if a cable has the GitOps tag."""
        if not cable_obj or not cable_obj.tags:
            return False
        # Convert tags to dict format for extraction
        tags_list = []
        for t in cable_obj.tags:
            if isinstance(t, dict):
                tags_list.append(t)
            elif hasattr(t, 'slug'):
                tag_dict = {'slug': t.slug}
                if hasattr(t, 'id'):
                    tag_dict['id'] = t.id
                tags_list.append(tag_dict)

        tag_ids, tag_slugs = extract_tag_ids_and_slugs(tags_list)

        # Check both tag ID and slug
        if self.managed_tag_id and self.managed_tag_id in tag_ids:
            return True
        return MANAGED_TAG_SLUG in tag_slugs

    def _safe_delete(self, cable_obj, reason_msg):
        """Delete cable ONLY if it is managed."""
        if self._is_managed(cable_obj):
            if self.dry_run:
                console.print(f"[yellow][DRY-RUN] Would DELETE {reason_msg} (Tag: {MANAGED_TAG_SLUG})[/yellow]")
            else:
                console.print(f"[red]Deleting {reason_msg} (Tag: {MANAGED_TAG_SLUG})...[/red]")
                try: cable_obj.delete()
                except Exception as e: console.print(f"[red]Failed to delete: {e}[/red]")
            return True
        else:
            console.print(f"[dim]  Skipping DELETE of {reason_msg} (Manual/Unmanaged)[/dim]")
            return False

    def _process_link(self, source_dev_id, source_dev_name, source_port_name, source_endpoint, link_def):
        # 1. Get Source Port
        source_api = getattr(self.nb.dcim, source_endpoint)
        term_a = source_api.get(device_id=source_dev_id, name=source_port_name)
        
        if not term_a:
            console.print(f"[red]  Source {source_port_name} not found on {source_dev_name}[/red]")
            return

        # 2. Get Peer Device ID
        peer_id = self._get_cached_id('dcim', 'devices', link_def.peer_device)
        if not peer_id:
            console.print(f"[red]  Peer Device {link_def.peer_device} not found[/red]")
            return

        # 3. Find Peer Port
        term_b = self._find_best_peer_port(peer_id, link_def.peer_port, term_a.id)
        
        if not term_b:
            console.print(f"[red]  Peer Port {link_def.peer_port} not found (or all occupied) on {link_def.peer_device}[/red]")
            return

        # 4. Prepare Desired Attributes
        desired_color = normalize_color(link_def.color)
        desired_type = link_def.cable_type or 'cat6'
        
        # 5. Idempotency Check
        existing_cable = term_a.cable
        
        if existing_cable:
            endpoints_ids = []
            for t in existing_cable.a_terminations + existing_cable.b_terminations:
                tid = self._get_termination_id(t)
                if tid: endpoints_ids.append(tid)
            
            if term_b.id in endpoints_ids:
                # Connection is CORRECT. Check attributes.
                changes = {}
                current_color = existing_cable.color or ''
                current_type = existing_cable.type or ''
                
                if current_color != desired_color: changes['color'] = desired_color
                if current_type != desired_type: changes['type'] = desired_type
                
                # Check TAGS
                if not self._is_managed(existing_cable):
                    # If the cable is correct but has no tag -> Tag it ("Adopt")
                    changes['tags'] = [self.managed_tag_id]

                if changes:
                    if self.dry_run:
                        console.print(f"[yellow][DRY-RUN] Would UPDATE cable {source_dev_name}:{source_port_name}: {changes}[/yellow]")
                    else:
                        existing_cable.update(changes)
                        console.print(f"[blue]Updated Cable Attributes: {source_dev_name} <-> {link_def.peer_device}[/blue]")
                return # Done! matches perfectly.
            else:
                # Connection is WRONG -> Delete only if managed
                deleted = self._safe_delete(existing_cable, f"wrong cable on {source_dev_name}:{source_port_name}")
                if not deleted: return # Cannot proceed if port is blocked by manual cable

        # Check if Target (term_b) is occupied by a DIFFERENT cable
        if term_b.cable:
             deleted = self._safe_delete(term_b.cable, f"stray cable on target {link_def.peer_device}:{link_def.peer_port}")
             if not deleted: return # Cannot proceed

        # 6. Create New Cable
        if self.dry_run:
            console.print(f"[yellow][DRY-RUN] + Cable: {source_dev_name}:{source_port_name} -> {link_def.peer_device}:{link_def.peer_port} (tagged)[/yellow]")
            return

        type_a = self._get_type_str(term_a)
        type_b = self._get_type_str(term_b)

        try:
            self.nb.dcim.cables.create(
                a_terminations=[{'object_type': type_a, 'object_id': term_a.id}],
                b_terminations=[{'object_type': type_b, 'object_id': term_b.id}],
                type=desired_type,
                status='connected',
                length=link_def.length,
                length_unit='m' if link_def.length else None,
                color=desired_color,
                tags=[self.managed_tag_id] # <--- WICHTIG: Tag setzen!
            )
            console.print(f"[green]Created Cable (tagged): {source_dev_name} <-> {link_def.peer_device}[/green]")
        except Exception as e:
            console.print(f"[red]Cable Error: {e}[/red]")

    def _find_best_peer_port(self, device_id, port_name, source_term_id):
        candidates = []
        for endpoint in ['interfaces', 'front_ports', 'rear_ports']:
            api = getattr(self.nb.dcim, endpoint)
            res = api.get(device_id=device_id, name=port_name)
            if res: candidates.append(res)
        
        if not candidates: return None

        # 1. Check for existing connection to us
        for c in candidates:
            if c.cable:
                eps = []
                for t in c.cable.a_terminations + c.cable.b_terminations:
                    tid = self._get_termination_id(t)
                    if tid: eps.append(tid)
                if source_term_id in eps: return c 

        # 2. Return first FREE candidate
        for c in candidates:
            if c.cable is None: return c

        return candidates[0]

    def _get_termination_id(self, term_obj):
        if isinstance(term_obj, dict):
            return term_obj.get('object_id') or term_obj.get('id')
        else:
            if hasattr(term_obj, 'object_id'): return term_obj.object_id
            if hasattr(term_obj, 'id'): return term_obj.id
        return None

    def _get_type_str(self, term_obj):
        if 'interfaces' in term_obj.url: return 'dcim.interface'
        if 'front-ports' in term_obj.url: return 'dcim.frontport'
        if 'rear-ports' in term_obj.url: return 'dcim.rearport'
        return 'dcim.interface'