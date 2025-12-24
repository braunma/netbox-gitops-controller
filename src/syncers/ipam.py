from src.syncers.base import BaseSyncer
from rich.console import Console

console = Console()

class IPAMSyncer(BaseSyncer):
    
    # --- NEU: VRF Sync ---
    def sync_vrfs(self, vrfs):
        console.rule("[bold]Syncing VRFs[/bold]")
        for vrf in vrfs:
            # Build payload
            payload = vrf.model_dump(exclude_none=True)

            # Generate slug: Since 'slug' is a property in the model,
            # it is often not included in model_dump (depending on config).
            # We set it explicitly to be safe.
            if 'slug' not in payload:
                payload['slug'] = vrf.slug

            self.ensure_object(
                app='ipam',
                endpoint='vrfs',
                lookup_data={'name': vrf.name},  # VRF names must be unique
                create_data=payload
            )

    def sync_vlan_groups(self, groups):
        console.rule("[bold]Syncing VLAN Groups[/bold]")
        
        for group in groups:
            # 1. Resolve Site ID
            site_id = None
            if group.site_slug:
                site_id = self._get_cached_id('dcim', 'sites', group.site_slug)
                if not site_id:
                    console.print(f"[red]Error: Site '{group.site_slug}' not found for VLAN Group '{group.name}'[/red]")
                    continue

            # 2. Build payload
            payload = group.model_dump(exclude={'site_slug'}, exclude_none=True)
            
            if site_id:
                payload['scope_type'] = 'dcim.site'
                payload['scope_id'] = site_id

            # 3. Ensure
            self.ensure_object(
                app='ipam',
                endpoint='vlan_groups',
                lookup_data={'slug': group.slug},
                create_data=payload
            )

    def sync_vlans(self, vlans):
        console.rule("[bold]Syncing VLANs[/bold]")
        for vlan in vlans:
            # 1. Resolve Site ID
            site_id = self._get_cached_id('dcim', 'sites', vlan.site_slug)
            if not site_id:
                console.print(f"[red]Error: Site {vlan.site_slug} not found for VLAN {vlan.name}[/red]")
                continue

            # 2. Resolve VLAN Group ID
            group_id = None
            if vlan.group_slug:
                group_id = self._get_cached_id('ipam', 'vlan_groups', vlan.group_slug)
                if not group_id:
                    console.print(f"[yellow]Warning: VLAN Group '{vlan.group_slug}' not found for VLAN {vlan.name}[/yellow]")

            # 3. Build payload
            payload = vlan.model_dump(exclude={'site_slug', 'group_slug'}, exclude_none=True)
            
            if site_id:
                payload['site'] = site_id
            
            if group_id:
                payload['group'] = group_id
            
            # 4. Ensure
            self.ensure_object(
                app='ipam',
                endpoint='vlans',
                lookup_data={'vid': vlan.vid, 'site_id': site_id},
                create_data=payload
            )

    def sync_prefixes(self, prefixes):
        console.rule("[bold]Syncing Prefixes[/bold]")

        for pfx in prefixes:
            # 1. Resolve Site
            site_id = self._get_cached_id('dcim', 'sites', pfx.site_slug)

            # 2. Resolve VRF
            vrf_id = None
            if pfx.vrf_name:
                vrf_id = self._get_cached_id('ipam', 'vrfs', pfx.vrf_name)
                if not vrf_id:
                     console.print(f"[red]Error: VRF '{pfx.vrf_name}' not found for Prefix {pfx.prefix}[/red]")
                     # Continue - prefix will be created in Global Table

            # 3. Resolve VLAN
            vlan_id = None
            if pfx.vlan_name:
                if not site_id:
                    # Without a site, the VLAN name is not unique, we only log if no global search is possible either
                    pass 
                else:
                    try:
                        found_vlan = self.nb.ipam.vlans.get(name=pfx.vlan_name, site_id=site_id)
                        if found_vlan: vlan_id = found_vlan.id
                        else:
                            console.print(f"[yellow]Warning: VLAN '{pfx.vlan_name}' not found in Site '{pfx.site_slug}'. Prefix created without VLAN.[/yellow]")
                    except Exception as e:
                        console.print(f"[red]Error checking VLAN: {e}[/red]")

            # 4. Build payload
            # IMPORTANT: Exclude vrf_name, as NetBox expects 'vrf' (ID)
            payload = pfx.model_dump(exclude={'site_slug', 'vlan_name', 'vrf_name'}, exclude_none=True)
            
            if site_id: payload['site'] = site_id
            if vlan_id: payload['vlan'] = vlan_id
            if vrf_id:  payload['vrf'] = vrf_id

            # 5. Ensure
            # IMPORTANT: A prefix is only unique by (Prefix + VRF).
            # We must specify 'vrf_id' in the lookup. 'null' means Global Table.
            lookup = {'prefix': pfx.prefix}
            lookup['vrf_id'] = vrf_id if vrf_id else 'null'

            self.ensure_object(
                app='ipam',
                endpoint='prefixes',
                lookup_data=lookup,
                create_data=payload
            )