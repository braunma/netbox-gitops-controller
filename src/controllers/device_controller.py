from typing import Optional, Literal, List, Union, Set
from src.models import DeviceConfig, InterfaceConfig
from src.client import NetBoxClient
from src.syncers.base import MANAGED_TAG_SLUG
from rich.console import Console
import time 

console = Console()

# --- Typdefinitionen ---
TerminationType = Literal['dcim.interface', 'dcim.frontport', 'dcim.rearport']
TERM_INTERFACE: TerminationType = 'dcim.interface'
TERM_FRONT: TerminationType = 'dcim.frontport'
TERM_REAR: TerminationType = 'dcim.rearport'
# -----------------------

class DeviceController:
    def __init__(self, client: NetBoxClient):
        self.client = client

    # --------------------------------------------------------------------------
    # HILFSFUNKTIONEN
    # --------------------------------------------------------------------------
    
    COLOR_MAP = {
        'purple': '800080', 'blue': '0000ff', 'yellow': 'ffff00', 
        'red': 'ff0000', 'white': 'ffffff', 'black': '000000', 
        'gray': '808080', 'orange': 'ffa500', 'green': '008000'
    }

    def _normalize_color(self, color_input):
        if not color_input: return ''
        raw = color_input.lower().strip()
        return self.COLOR_MAP.get(raw, raw).replace('#', '')

    def _get_termination_type(self, obj: Union[dict, object]) -> TerminationType:
        """Ermittelt den NetBox Type String (dcim.interface, frontport, rearport)."""
        if obj is None: return TERM_INTERFACE
        if isinstance(obj, dict):
            ep = obj.get('_endpoint')
            if ep == 'front_ports': return TERM_FRONT
            if ep == 'rear_ports': return TERM_REAR
            return TERM_INTERFACE

        url = getattr(obj, 'url', '')
        if '/front-ports/' in url: return TERM_FRONT
        if '/rear-ports/' in url: return TERM_REAR
        
        return TERM_INTERFACE

    def _extract_tag_ids_and_slugs(self, tags: list) -> tuple[Set[int], Set[str]]:
        ids: Set[int] = set()
        slugs: Set[str] = set()
        for tag in tags or []:
            if isinstance(tag, int): ids.add(tag)
            elif isinstance(tag, dict):
                if 'id' in tag: ids.add(tag['id'])
                if 'slug' in tag: slugs.add(tag['slug'])
        return ids, slugs

    def _is_managed(self, cable_obj: Optional[dict]) -> bool:
        if not cable_obj: return False
        tag_ids, tag_slugs = self._extract_tag_ids_and_slugs(cable_obj.get('tags'))
        if self.client.managed_tag_id in tag_ids: return True
        return MANAGED_TAG_SLUG in tag_slugs

    def _cable_connects_to(self, cable: dict, object_id: int) -> bool:
        terminations = cable.get('a_terminations', []) + cable.get('b_terminations', [])
        for term in terminations or []:
            term_id = term.get('object_id') or term.get('id')
            if term_id == object_id: return True
        return False

    def _safe_delete(self, cable_obj: Optional[dict], reason: str, force: bool = False) -> bool:
        if not cable_obj: return False
        if not force and not self._is_managed(cable_obj):
            console.print(f"[dim yellow]Skipping unmanaged cable deletion: {reason}[/dim yellow]")
            return False
        cable_id = cable_obj.get('id')
        if not cable_id:
            console.print("[red]Refusing to delete cable without ID[/red]")
            return False

        try:
            self.client.delete_by_id('dcim', 'cables', cable_id)
            console.print(f"[red]- Deleted Cable (ID {cable_id}) because {reason}[/red]")
            time.sleep(1.0)  # Increased wait time after deletion
            return True
        except Exception as e:
            console.print(f"[red]Failed to delete cable: {e}[/red]")
            return False

    # --------------------------------------------------------------------------
    # DEVICE BAYS (Self-Healing für Chassis)
    # --------------------------------------------------------------------------
    def _reconcile_device_bays(self, nb_device: object):
        """
        Prüft, ob das Gerät alle Bays hat, die sein Device Type vorschreibt.
        Legt fehlende Bays am Gerät an (Self-Healing für Chassis).
        """
        # Sicherstellen, dass wir eine Device Type ID haben
        if not hasattr(nb_device, 'device_type') or not nb_device.device_type:
            return

        # Device Type Object sicher laden
        dt_obj = nb_device.device_type
        if not hasattr(dt_obj, 'id'):
            try:
                dt_obj = self.client.nb.dcim.device_types.get(dt_obj)
            except Exception:
                return
        
        dt_id = dt_obj.id

        # FIX: Korrekter API-Parameter für NetBox
        templates = self.client.nb.dcim.device_bay_templates.filter(
            device_type_id=dt_id  # ← WICHTIG: device_type_id, nicht devicetype_id
        )
        
        if not templates:
            # Kein Template = Kein Bay-fähiges Gerät → Silent Skip (kein Spam)
            return

        console.print(f"[dim][BAYS] Checking {len(templates)} bay template(s) for {nb_device.name}[/dim]")

        # Die existierenden Bays am Gerät holen (Die Realität)
        existing_bays = {
            b.name: b for b in self.client.nb.dcim.device_bays.filter(device_id=nb_device.id)
        }

        # Abgleich: Fehlt was?
        for tmpl in templates:
            if tmpl.name not in existing_bays:
                console.print(f"[yellow][BAYS] Missing bay '{tmpl.name}' on {nb_device.name} – creating...[/yellow]")
                try:
                    if not self.client.dry_run:
                        self.client.nb.dcim.device_bays.create(
                            device=nb_device.id,
                            name=tmpl.name,
                            label=tmpl.label or ""
                        )
                        console.print(f"[green]+ Created Device Bay '{tmpl.name}' on {nb_device.name}[/green]")
                    else:
                        console.print(f"[yellow][DRY] Would create Device Bay '{tmpl.name}'[/yellow]")
                except Exception as e:
                    console.print(f"[red]Failed to create bay {tmpl.name}: {e}[/red]")


        
# --------------------------------------------------------------------------
    # HAUPT-LOGIK (reconcile) - BAY-CENTRIC APPROACH
    # --------------------------------------------------------------------------
    def reconcile(self, desired_device: DeviceConfig):
        # 1. Basis-IDs auflösen
        site_id = self.client.get_id('sites', desired_device.site_slug)
        role_id = self.client.get_id('roles', desired_device.role_slug)
        type_id = self.client.get_id('device_types', desired_device.device_type_slug)

        if not all([site_id, role_id, type_id]):
            console.print(f"[red]Missing dependencies for {desired_device.name}[/red]")
            return

        # A. Rack & Parent Logik
        rack_slug = desired_device.rack_slug or None 
        yaml_rack_id = self.client.get_id('racks', rack_slug) if rack_slug else None
        
        device_bay_id = None
        parent_rack_id = None
        
        if desired_device.parent_device:
            parent_obj = self.client.nb.dcim.devices.get(name=desired_device.parent_device)
            if not parent_obj:
                console.print(f"[red]Parent {desired_device.parent_device} not found[/red]")
                return
            
            if parent_obj.rack:
                parent_rack_id = parent_obj.rack.id
            
            bay_obj = self.client.nb.dcim.device_bays.get(device_id=parent_obj.id, name=desired_device.device_bay)
            if not bay_obj:
                console.print(f"[red]Bay {desired_device.device_bay} not found[/red]")
                return
            device_bay_id = bay_obj.id

        # B. Payload für Device Erstellung
        # Wir erstellen es ZUERST immer im Rack (oder inherited Rack), damit es valide ist.
        final_rack_id = yaml_rack_id if yaml_rack_id else parent_rack_id
        
        exclude_fields = {'interfaces', 'site_slug', 'role_slug', 'device_type_slug', 'rack_slug', 
                          'front_ports', 'rear_ports', 'modules', 'parent_device', 'device_bay'}
        
        device_payload = desired_device.model_dump(exclude=exclude_fields, exclude_none=True)
        device_payload.update({'site': site_id, 'role': role_id, 'device_type': type_id})

        if final_rack_id:
            device_payload['rack'] = final_rack_id

        # Child-Cleaning für Erstellung (noch nicht im Bay, aber Position weg, falls Child)
        if device_bay_id:
            device_payload.pop('position', None)
            device_payload.pop('face', None)
        elif final_rack_id:
            pass # Rack Device
        else:
            device_payload.pop('rack', None)
            device_payload.pop('position', None)

        # C. Create / Update Device
        lookup = {'name': desired_device.name, 'site_id': site_id}
        nb_device = self.client.apply('dcim', 'devices', lookup, device_payload)
        
        # Dry Run
        if nb_device and getattr(nb_device, 'id', 0) == 0 and self.client.dry_run:
            console.print(f"[yellow][DRY] Simulated {desired_device.name}[/yellow]")
            return

        # =====================================================================
        # D. INSTALLATION IN DEN SLOT (Der "Bay-Centric" Weg)
        # =====================================================================
        if nb_device and device_bay_id:
            # Check: Ist der Node schon drin?
            current_bay = getattr(nb_device, 'device_bay', None)
            
            if not current_bay or current_bay.id != device_bay_id:
                console.print(f"[dim]Installing into Device Bay...[/dim]")
                
                try:
                    if not self.client.dry_run:
                        # SCHRITT 1: Node "frei machen"
                        # Ein Gerät kann nicht in einen Slot, wenn es:
                        # a) Eine eigene Rack-ID hat
                        # b) Eine Position (HE) hat
                        # c) Ein 'face' hat
                        # Wir löschen das jetzt alles, damit es "schwebt".
                        console.print(f"[dim]  1. Detaching node from rack...[/dim]")
                        
                        # Fix für dein GUI Problem: Wir vergeben KEIN Rack beim Update.
                        # Wir löschen es. NetBox zieht sich das Rack später vom Parent.
                        nb_device.update({
                            'rack': None,
                            'position': None,
                            'face': None
                        })
                        
                        # SCHRITT 2: Den SLOT updaten (nicht das Gerät!)
                        # Wir greifen uns den Slot und sagen "Du hast jetzt Inhalt"
                        console.print(f"[dim]  2. Updating Bay {desired_device.device_bay}...[/dim]")
                        bay_obj = self.client.nb.dcim.device_bays.get(device_bay_id)
                        
                        # Das ist der API-Standardweg für "Insert Blade"
                        success = bay_obj.update({'installed_device': nb_device.id})
                        
                        if success:
                            console.print(f"[green]✓ Installed {nb_device.name} into {desired_device.device_bay}[/green]")
                        else:
                            console.print(f"[red]✗ Installation failed via Bay-Update![/red]")
                            
                    else:
                        console.print(f"[yellow][DRY] Would install into Bay {desired_device.device_bay}[/yellow]")
                        
                except Exception as e:
                    console.print(f"[red bold]✗ Failed to install module: {e}[/red bold]")
                    # DEBUG INFO: Hilft zu verstehen, warum NetBox ablehnt
                    console.print(f"[dim red]Info: Ensure Device Type {desired_device.device_type_slug} has u_height=0![/dim red]")
            else:
                console.print(f"[dim green]✓ Already in correct Device Bay[/dim green]")

        # =====================================================================

        # E. Komponenten
        if nb_device:
            self._reconcile_device_bays(nb_device)
            nb_device_data = {'id': nb_device.id, 'name': nb_device.name, 'role_slug': desired_device.role_slug}
            self._reconcile_rear_ports(nb_device_data, getattr(desired_device, 'rear_ports', []))
            self._reconcile_front_ports(nb_device_data, getattr(desired_device, 'front_ports', []))
            self._reconcile_interfaces(nb_device_data, desired_device.interfaces)
            self._reconcile_modules(nb_device_data, getattr(desired_device, 'modules', []))
            self._reconcile_cables(nb_device_data, desired_device)

    # --------------------------------------------------------------------------
    # PORTS
    # --------------------------------------------------------------------------
    def _reconcile_rear_ports(self, nb_device_data: dict, rear_ports: list):
        if not rear_ports: return
        for port_cfg in rear_ports:
            payload = port_cfg.model_dump(exclude={'link'}, exclude_none=True)
            payload['device'] = nb_device_data['id']
            if hasattr(port_cfg, 'positions'): 
                payload['positions'] = port_cfg.positions
            self.client.apply('dcim', 'rear_ports', {'device_id': nb_device_data['id'], 'name': port_cfg.name}, payload)

    def _reconcile_front_ports(self, nb_device_data: dict, front_ports: list):
        if not front_ports: return
        for port_cfg in front_ports:
            payload = port_cfg.model_dump(exclude={'link', 'rear_port'}, exclude_none=True)
            payload['device'] = nb_device_data['id']
            if port_cfg.rear_port:
                rp = self.client.nb.dcim.rear_ports.get(device_id=nb_device_data['id'], name=port_cfg.rear_port)
                if rp: 
                    payload['rear_port'] = rp.id
            self.client.apply('dcim', 'front_ports', {'device_id': nb_device_data['id'], 'name': port_cfg.name}, payload)

    # --------------------------------------------------------------------------
    # INTERFACES & IPs
    # --------------------------------------------------------------------------
    def _reconcile_interfaces(self, nb_device_data: dict, interfaces: list):
        for iface_config in interfaces:
            payload = iface_config.model_dump(exclude={'ip', 'untagged_vlan', 'tagged_vlans', 'link', 'address_role'}, exclude_none=True)
            payload['device'] = nb_device_data['id']
            
            untagged = self.client.get_id('vlans', iface_config.untagged_vlan)
            if untagged: 
                payload['untagged_vlan'] = untagged
            
            tagged = [self.client.get_id('vlans', v) for v in iface_config.tagged_vlans]
            if tagged: 
                payload['tagged_vlans'] = [x for x in tagged if x]

            nb_iface = self.client.apply('dcim', 'interfaces', {'device_id': nb_device_data['id'], 'name': iface_config.name}, payload)
            
            if nb_iface and iface_config.ip:
                self._reconcile_ip(dict(nb_iface), iface_config)

    def _reconcile_ip(self, nb_iface: dict, iface_config: InterfaceConfig):
        ip_config = iface_config.ip
        vrf_id = self.client.get_id('vrfs', ip_config.vrf)
        ip_payload = ip_config.model_dump(exclude={'vrf'}, exclude_none=True)
        if vrf_id: 
            ip_payload['vrf'] = vrf_id
        ip_payload.update({'assigned_object_type': 'dcim.interface', 'assigned_object_id': nb_iface['id']})
        
        nb_ip = self.client.apply('ipam', 'ip_addresses', {'address': ip_config.address, 'vrf_id': vrf_id} if vrf_id else {'address': ip_config.address}, ip_payload)
        
        if nb_ip and iface_config.address_role == 'primary':
             self.client.update_device_primary_ip(nb_iface['device'], nb_ip.id)

    # --------------------------------------------------------------------------
    # MODULES
    # --------------------------------------------------------------------------
    def _reconcile_modules(self, nb_device_data: dict, modules_cfg: list):
        if not modules_cfg:
            return

        device_id = nb_device_data["id"]
        console.print(f"[bold cyan][MODULE][/bold cyan] Reconciling modules for {nb_device_data['name']}")

        # 1. Vorhandene Module Bays am Gerät finden (Die Slots)
        # Wir bauen ein Mapping: Name -> ID
        bays = {b.name: b.id for b in self.client.nb.dcim.module_bays.filter(device_id=device_id)}
        
        # 2. Bereits installierte Module finden
        installed_modules = {m.module_bay.id: m for m in self.client.nb.dcim.modules.filter(device_id=device_id)}

        for mod_cfg in modules_cfg:
            bay_id = bays.get(mod_cfg.name)
            if not bay_id:
                console.print(f"[yellow][MODULE] Bay '{mod_cfg.name}' not found on device – skipping[/yellow]")
                continue

            # get_id() returns an integer ID, not an object
            module_type_id = self.client.get_id('module_types', mod_cfg.module_type_slug)
            
            if not module_type_id:
                console.print(f"[red][MODULE] Module Type '{mod_cfg.module_type_slug}' not found[/red]")
                continue

            # Fetch module type to get its description
            description = ""
            if hasattr(mod_cfg, 'description') and mod_cfg.description:
                # Use description from module config if provided
                description = mod_cfg.description
            else:
                # Otherwise, use description from the module type
                try:
                    mt_obj = self.client.nb.dcim.module_types.get(module_type_id)
                    if mt_obj and hasattr(mt_obj, 'description'):
                        description = mt_obj.description or ""
                except Exception:
                    pass

            # Payload für das Modul zusammenbauen
            payload = {
                "device": device_id,
                "module_bay": bay_id,
                "module_type": module_type_id,
                "status": mod_cfg.status or "active",
                "description": description,
            }
            
            # Add serial from config if present (otherwise empty to avoid 400 errors)
            if hasattr(mod_cfg, 'serial') and mod_cfg.serial:
                payload["serial"] = mod_cfg.serial
            else:
                payload["serial"] = ""
            
            # Add managed tag if available
            if self.client.managed_tag_id and self.client.managed_tag_id > 0:
                payload["tags"] = [self.client.managed_tag_id]

            # 3. Reconciliation Logik (Idempotenz)
            existing_mod = installed_modules.get(bay_id)
            
            if existing_mod:
                # Prüfen, ob es der richtige Typ ist
                if existing_mod.module_type.id == module_type_id:
                    console.print(f"[dim][MODULE] Correct module already in {mod_cfg.name} – skipping[/dim]")
                    
                    # Check if existing module has the managed tag
                    if hasattr(existing_mod, 'tags'):
                        existing_tag_ids = [t.id if hasattr(t, 'id') else t for t in existing_mod.tags]
                        
                        if self.client.managed_tag_id not in existing_tag_ids:
                            console.print(f"[yellow][MODULE] Existing module missing gitops tag - updating[/yellow]")
                            try:
                                if not self.client.dry_run:
                                    # Add the missing tag
                                    new_tags = existing_tag_ids + [self.client.managed_tag_id]
                                    existing_mod.update({"tags": new_tags})
                                    console.print(f"[green][MODULE] Added gitops tag to existing module[/green]")
                            except Exception as e:
                                console.print(f"[red][MODULE] Failed to add tag: {e}[/red]")
                        else:
                            console.print(f"[dim green][MODULE] Module already has gitops tag ✓[/dim green]")
                    continue
                else:
                    # Falsches Modul: Löschen und neu setzen
                    console.print(f"[red][MODULE] Wrong module in {mod_cfg.name} – deleting[/red]")
                    if not self.client.dry_run:
                        existing_mod.delete()
                        time.sleep(0.1)
                    else:
                        console.print(f"[yellow][DRY-RUN] Would delete module in {mod_cfg.name}[/yellow]")
                        continue

            # 4. Modul anlegen
            try:
                if not self.client.dry_run:
                    new_mod = self.client.nb.dcim.modules.create(payload)
                    console.print(f"[green]+ Module installed:[/green] {mod_cfg.module_type_slug} in {mod_cfg.name}")
                else:
                    console.print(f"[yellow][DRY-RUN] Would install {mod_cfg.module_type_slug} in {mod_cfg.name}[/yellow]")
            except Exception as e:
                console.print(f"[red]FAILED to install module: {e}[/red]")
                console.print(f"[red]Payload was: {payload}[/red]")

    # --------------------------------------------------------------------------
    # Kabel-Logik (Fixed Version)
    # --------------------------------------------------------------------------
    
    def _reconcile_cables(self, nb_device_data: dict, config: DeviceConfig):
        if not nb_device_data or not nb_device_data.get("id"):
            return

        device_id = nb_device_data["id"]
        device_name = config.name
        device_role = nb_device_data.get("role_slug")

        console.print(f"[bold cyan][CABLE][/bold cyan] Reconciling cables for {device_name} (ID {device_id})")

        # ------------------------------------------------------------------
        # 1. Lokale Ports sammeln
        # ------------------------------------------------------------------
        local_ports_dict: dict[str, dict] = {}

        for endpoint in ("interfaces", "front_ports", "rear_ports"):
            ports = self.client.get_components(device_id, endpoint)
            for p in ports:
                p["_endpoint"] = endpoint
                local_ports_dict[p["name"]] = p

        console.print(f"[CABLE:1] Local ports: {list(local_ports_dict.keys())}")

        # ------------------------------------------------------------------
        # 2. Alle konfigurierten Ports mit Link sammeln
        # ------------------------------------------------------------------
        config_ports = []
        config_ports.extend(getattr(config, "interfaces", []))
        config_ports.extend(getattr(config, "front_ports", []))
        config_ports.extend(getattr(config, "rear_ports", []))

        linked_ports = [p for p in config_ports if getattr(p, "link", None)]
        console.print(f"[CABLE:1] Ports with links: {[p.name for p in linked_ports]}")

        # ------------------------------------------------------------------
        # 3. Verarbeitung je Link
        # ------------------------------------------------------------------
        for port_cfg in linked_ports:
            link = port_cfg.link
            local = local_ports_dict.get(port_cfg.name)

            if not local:
                console.print(f"[yellow][CABLE] Local port {port_cfg.name} not found – skipping[/yellow]")
                continue

            console.print(f"\n[bold][CABLE:2][/bold] {device_name}:{port_cfg.name}")

            # --------------------------------------------------------------
            # A. Peer-Gerät auflösen und Rolle GARANTIEREN
            # --------------------------------------------------------------
            peer_device = self.client.nb.dcim.devices.get(name=link.peer_device)
            if not peer_device:
                console.print(f"[red]Peer device {link.peer_device} not found[/red]")
                continue

            peer_role = None
            
            # Rolle robustly auflösen
            try:
                peer_role = getattr(getattr(peer_device, 'device_role', None), 'slug', None)
            except (AttributeError, TypeError):
                pass
            
            if not peer_role:
                try:
                    full_peer_device = self.client.nb.dcim.devices.get(peer_device.id)
                    if full_peer_device:
                        peer_data = dict(full_peer_device)
                        if 'device_role' in peer_data and isinstance(peer_data['device_role'], dict):
                            peer_role = peer_data['device_role'].get('slug')
                        elif hasattr(full_peer_device, 'role'):
                            # Some NetBox versions use 'role' instead of 'device_role'
                            role_obj = getattr(full_peer_device, 'role', None)
                            if role_obj:
                                peer_role = getattr(role_obj, 'slug', None)
                except Exception as e:
                    console.print(f"[red]CRITICAL ROLE RE-FETCH FAILED for {link.peer_device}: {e}[/red]")
            
            if not peer_role:
                console.print(f"[red bold]FAILED: Peer device {link.peer_device} role could not be resolved. Skipping.[/red bold]")
                continue

            is_src_pp = device_role == "patch-panel"
            is_dst_pp = peer_role == "patch-panel"

            console.print(f"[CABLE:2] Peer = {peer_device.name} (role={peer_role})")

            # --------------------------------------------------------------
            # B. Peer-Port EXPLIZIT bestimmen
            # --------------------------------------------------------------
            peer = None
            term_b_type = None

            try:
                if is_src_pp and is_dst_pp:
                    # Patchpanel ↔ Patchpanel = Rear ↔ Rear (Backbone)
                    peer = self.client.nb.dcim.rear_ports.get(
                        device_id=peer_device.id,
                        name=link.peer_port,
                    )
                    term_b_type = "dcim.rearport"

                elif is_dst_pp:
                    # Device → Patchpanel = FrontPort (Server/Switch Access)
                    peer = self.client.nb.dcim.front_ports.get(
                        device_id=peer_device.id,
                        name=link.peer_port,
                    )
                    term_b_type = "dcim.frontport"

                else:
                    # Device → Device (Interface)
                    peer = self.client.nb.dcim.interfaces.get(
                        device_id=peer_device.id,
                        name=link.peer_port,
                    )
                    term_b_type = "dcim.interface"

            except Exception as e:
                console.print(f"[red]Peer resolution error: {e}[/red]")
                continue

            if not peer:
                console.print(f"[red]Peer port {link.peer_device}:{link.peer_port} not found[/red]")
                continue
 # --------------------------------------------------------------
            # C. Termination-Typen festlegen
            # --------------------------------------------------------------
            term_a_type = {
                "interfaces": "dcim.interface",
                "front_ports": "dcim.frontport",
                "rear_ports": "dcim.rearport",
            }[local["_endpoint"]]
            
            peer_obj_id = getattr(peer, 'id', None)
            if not peer_obj_id:
                console.print(f"[red]Peer object {link.peer_device}:{link.peer_port} has no ID - skipping.[/red]")
                continue

            console.print(
                f"[CABLE:2] Terminations: "
                f"{term_a_type}:{local['id']} → {term_b_type}:{peer_obj_id}"
            )

            # --------------------------------------------------------------
            # D. Bestehendes Kabel am lokalen Port prüfen
            # --------------------------------------------------------------
            existing = local.get("cable")
            if existing:
                try:
                    existing = self.client.nb.dcim.cables.get(existing["id"])
                    if existing: 
                        existing = dict(existing)
                except Exception:
                    existing = None
                
                if not existing:
                    console.print("[CABLE:3] Existing cable vanished during fetch – skipping idempotency check")
                elif self._cable_connects_to(existing, peer.id):
                    console.print("[CABLE:3] Correct cable already exists – skipping")
                    continue
                else:
                    console.print("[CABLE:3] Wrong cable on local port – deleting")
                    self._safe_delete(existing, "wrong peer connection", force=True)

            # --------------------------------------------------------------
            # E. Peer-Port prüfen (Stray cables)
            # --------------------------------------------------------------
            fresh_peer = None
            try:
                if term_b_type == "dcim.frontport":
                    fresh_peer = self.client.nb.dcim.front_ports.get(peer_obj_id)
                elif term_b_type == "dcim.rearport":
                    fresh_peer = self.client.nb.dcim.rear_ports.get(peer_obj_id)
                else:
                    fresh_peer = self.client.nb.dcim.interfaces.get(peer_obj_id)
            except Exception as e:
                console.print(f"[yellow]Warning fetching fresh peer: {e}[/yellow]")

            if fresh_peer and hasattr(fresh_peer, 'cable') and fresh_peer.cable:
                try:
                    peer_cable = self.client.nb.dcim.cables.get(fresh_peer.cable.id)
                    if peer_cable: 
                        peer_cable = dict(peer_cable)
                    
                    if peer_cable:
                        if term_b_type == "dcim.rearport" and is_dst_pp:
                            if not self._cable_connects_to(peer_cable, local["id"]):
                                console.print("[CABLE:3] Wrong backbone cable – deleting")
                                self._safe_delete(peer_cable, "wrong backbone", force=True)
                            else:
                                console.print("[CABLE:3] Backbone cable correct – keeping")
                                continue 
                        else:
                            console.print("[CABLE:3] Peer port blocked – deleting")
                            self._safe_delete(peer_cable, "blocking target port", force=True)
                except Exception as e:
                    console.print(f"[yellow]Warning processing peer cable: {e}[/yellow]")

            # --------------------------------------------------------------
            # F. Kabel anlegen (FIXED)
            # --------------------------------------------------------------
            cable_data = {
                "a_terminations": [
                    {"object_type": term_a_type, "object_id": local["id"]}
                ],
                "b_terminations": [
                    {"object_type": term_b_type, "object_id": peer_obj_id}
                ],
                "status": "connected",
                "type": link.cable_type or "cat6a",
                "tags": [self.client.managed_tag_id],
            }
            
            # Add color only if present
            color = self._normalize_color(link.color)
            if color:
                cable_data['color'] = color
            
            # Add length if present
            if link.length:
                cable_data['length'] = link.length
                cable_data['length_unit'] = link.length_unit or 'm'

            console.print(f"[CABLE:4] Creating cable payload: {cable_data}")

            created_cable = None
            try:
                # FIXED: Use proper pynetbox method
                created_cable = self.client.nb.dcim.cables.create(cable_data)
                
                if created_cable and hasattr(created_cable, 'id') and created_cable.id:
                    console.print(
                        f"[green]+ Cable {created_cable.id}:[/green] "
                        f"{device_name}:{port_cfg.name} → "
                        f"{link.peer_device}:{link.peer_port}"
                    )
                else:
                    console.print(
                        f"[red]Cable creation returned invalid response for "
                        f"{device_name}:{port_cfg.name} → {link.peer_device}:{link.peer_port}[/red]"
                    )
                    
            except Exception as e:
                console.print(
                    f"[red bold]FAILED to create cable "
                    f"{device_name}:{port_cfg.name} → {link.peer_device}:{link.peer_port}[/red bold]"
                )
                console.print(f"[red]Error: {e}[/red]")
                console.print(f"[red]Payload was: {cable_data}[/red]")