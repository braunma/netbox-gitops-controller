"""
DEPRECATED: This module is no longer used.
Device synchronization is now handled by DeviceController in Phase 3.
This file is kept for reference but should not be imported.
"""

from src.syncers.base import BaseSyncer
from rich.console import Console

console = Console()

class DeviceSyncer(BaseSyncer):
    
    def sync_devices(self, devices):
        console.rule("[bold]Syncing Devices[/bold]")
        
        for dev in devices:
            # 1. Abhängigkeiten auflösen
            site_id = self._get_cached_id('dcim', 'sites', dev.site_slug)
            role_id = self._get_cached_id('dcim', 'device_roles', dev.role_slug)
            type_id = self._get_cached_id('dcim', 'device_types', dev.device_type_slug)
            
            # Bessere Fehleranzeige: Prüfen wir alles auf einmal
            if not all([site_id, role_id, type_id]):
                console.print(f"[red]Skipping {dev.name}: Missing dependencies. Site: {site_id}, Role: {role_id}, Type: {type_id}[/red]")
                continue

            rack_id = None
            if dev.rack_slug:
                rack_id = self._get_cached_id('dcim', 'racks', dev.rack_slug)
                if rack_id is None:
                    console.print(f"[red]Error: Rack '{dev.rack_slug}' not found for Device '{dev.name}'[/red]")
                    continue

            # 2. Payload bauen
            payload = dev.model_dump(
                exclude={'site_slug', 'role_slug', 'device_type_slug', 'rack_slug', 
                         'interfaces', 'front_ports', 'rear_ports'},
                exclude_none=True
            )
            
            payload.update({
                'site': site_id,
                'role': role_id,
                'device_type': type_id,
                'rack': rack_id
            })

            # 3. Device erstellen/updaten
            device_obj = self.ensure_object_and_return(
                app='dcim',
                endpoint='devices',
                lookup_data={'name': dev.name, 'site_id': site_id},
                create_data=payload
            )

            # 4. Interfaces konfigurieren
            if device_obj:
                self._sync_interfaces(device_obj, dev.interfaces, site_id)

    def _sync_interfaces(self, device_obj, interfaces_config, site_id):
        # Cache aufbauen: Wir laden einmal alle VLANs der Site
        vlan_map = {}
        if not self.dry_run:
            # Wir holen nur Name und ID, das ist schneller
            vlans = self.nb.ipam.vlans.filter(site_id=site_id)
            for v in vlans:
                vlan_map[v.name] = v.id

        for iface_cfg in interfaces_config:
            
            # Payload bauen
            iface_payload = iface_cfg.model_dump(
                exclude={'ip', 'address_role', 'members', 'link'}, 
                exclude_none=True
            )
            iface_payload['device'] = device_obj.id

            # VLANs auflösen (mit Debug-Info)
            if iface_cfg.untagged_vlan:
                # Prüfen ob Map leer ist (kann passieren bei Verbindungsfehlern oder leerer Site)
                if not vlan_map and not self.dry_run:
                     console.print(f"[yellow]Warning: No VLANs found in Site ID {site_id}![/yellow]")

                vid = vlan_map.get(iface_cfg.untagged_vlan)
                if vid: 
                    iface_payload['untagged_vlan'] = vid
                else: 
                    # Hier geben wir genau aus, welches Interface und welches VLAN Probleme macht
                    console.print(f"[yellow]Warning: Interface {iface_cfg.name} -> VLAN '{iface_cfg.untagged_vlan}' not found in Site![/yellow]")

            # Tagged VLANs
            if iface_cfg.tagged_vlans:
                vids = []
                for v_name in iface_cfg.tagged_vlans:
                    vid = vlan_map.get(v_name)
                    if vid: vids.append(vid)
                    else: console.print(f"[yellow]Warning: Tagged VLAN '{v_name}' not found![/yellow]")
                if vids: iface_payload['tagged_vlans'] = vids

            # Interface sicherstellen
            interface_obj = self.ensure_object_and_return(
                app='dcim',
                endpoint='interfaces',
                lookup_data={'device_id': device_obj.id, 'name': iface_cfg.name},
                create_data=iface_payload
            )

            if not interface_obj:
                continue

            # C) LAG Members
            if iface_cfg.type == 'lag' and iface_cfg.members:
                for member_name in iface_cfg.members:
                    member_obj = self.nb.dcim.interfaces.get(device_id=device_obj.id, name=member_name)
                    if member_obj:
                        current_lag = getattr(member_obj.lag, 'id', None) if member_obj.lag else None
                        if current_lag != interface_obj.id:
                            if self.dry_run:
                                console.print(f"[yellow][DRY-RUN] Would add {member_name} to LAG {iface_cfg.name}[/yellow]")
                            else:
                                try:
                                    member_obj.update({'lag': interface_obj.id})
                                    console.print(f"[blue]Added {member_name} to bond {iface_cfg.name}[/blue]")
                                except Exception as e:
                                    console.print(f"[red]Failed to add member {member_name}: {e}[/red]")

            # D) IP Adressen
            if iface_cfg.ip:
                ip_lookup = {'address': iface_cfg.ip}
                ip_create = {
                    'address': iface_cfg.ip,
                    'status': 'active',
                    'assigned_object_type': 'dcim.interface',
                    'assigned_object_id': interface_obj.id
                }
                
                ip_obj = self.ensure_object_and_return(
                    app='ipam', endpoint='ip_addresses',
                    lookup_data=ip_lookup, create_data=ip_create
                )

                if ip_obj and not self.dry_run:
                    current_assignment = getattr(ip_obj, 'assigned_object_id', None)
                    if current_assignment != interface_obj.id:
                        console.print(f"[blue]Moving IP {iface_cfg.ip} to {iface_cfg.name}[/blue]")
                        ip_obj.update({
                            'assigned_object_type': 'dcim.interface',
                            'assigned_object_id': interface_obj.id
                        })

                # E) Primary IP
                if iface_cfg.address_role == 'primary' and ip_obj and not self.dry_run:
                    field = 'primary_ip6' if ':' in iface_cfg.ip else 'primary_ip4'
                    current_primary = getattr(device_obj, field)
                    current_primary_id = getattr(current_primary, 'id', None) if current_primary else None
                    
                    if current_primary_id != ip_obj.id:
                        try:
                            device_obj.update({field: ip_obj.id})
                            console.print(f"[blue]Set {iface_cfg.ip} as primary IP for Device[/blue]")
                        except Exception as e:
                            console.print(f"[red]Failed to set primary IP: {e}[/red]")