from src.syncers.base import BaseSyncer
from rich.console import Console

console = Console()

class DCIMSyncer(BaseSyncer):
    
    def sync_sites(self, sites):
        console.rule("[bold]Syncing Sites[/bold]")
        for site in sites:
            # This was good, let's keep it!
            self.ensure_object(
                app='dcim', 
                endpoint='sites', 
                lookup_data={'slug': site.slug}, 
                create_data=site.model_dump(exclude_none=True)
            )

    def sync_racks(self, racks):
        console.rule("[bold]Syncing Racks[/bold]")
        for rack in racks:
            # FIX: Don't query the cache, query NetBox LIVE!
            # If the site was just created, the cache doesn't know it yet.
            # We use self.nb (pynetbox object), which should be available in BaseSyncer.
            
            site_obj = self.nb.dcim.sites.get(slug=rack.site_slug)
            
            # Fallback: If slug fails, try name (for robustness)
            if not site_obj:
                 site_obj = self.nb.dcim.sites.get(name=rack.site_slug)

            if not site_obj:
                console.print(f"[red]Error: Site '{rack.site_slug}' not found for Rack '{rack.name}' (Live Lookup failed)[/red]")
                continue

            # 2. Prepare data
            # We explicitly set the ID that we just got fresh from the API
            payload = rack.model_dump(exclude={'site_slug'}, exclude_none=True)
            payload['site'] = site_obj.id

            # 3. Sync
            self.ensure_object(
                app='dcim', 
                endpoint='racks', 
                # IMPORTANT: Lookup must contain site_id, otherwise it might find the rack globally
                lookup_data={'site_id': site_obj.id, 'name': rack.name}, 
                create_data=payload
            )