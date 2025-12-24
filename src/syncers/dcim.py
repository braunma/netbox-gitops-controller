from src.syncers.base import BaseSyncer
from rich.console import Console

console = Console()

class DCIMSyncer(BaseSyncer):
    
    def sync_sites(self, sites):
        console.rule("[bold]Syncing Sites[/bold]")
        for site in sites:
            # Das war gut so, lassen wir so!
            self.ensure_object(
                app='dcim', 
                endpoint='sites', 
                lookup_data={'slug': site.slug}, 
                create_data=site.model_dump(exclude_none=True)
            )

    def sync_racks(self, racks):
        console.rule("[bold]Syncing Racks[/bold]")
        for rack in racks:
            # FIX: Nicht den Cache fragen, sondern NetBox LIVE fragen!
            # Wenn die Site gerade erst erstellt wurde, kennt der Cache sie noch nicht.
            # Wir nutzen self.nb (pynetbox object), das im BaseSyncer verfügbar sein sollte.
            
            site_obj = self.nb.dcim.sites.get(slug=rack.site_slug)
            
            # Fallback: Falls Slug fehlschlägt, versuche Name (für Robustheit)
            if not site_obj:
                 site_obj = self.nb.dcim.sites.get(name=rack.site_slug)

            if not site_obj:
                console.print(f"[red]Error: Site '{rack.site_slug}' not found for Rack '{rack.name}' (Live Lookup failed)[/red]")
                continue

            # 2. Daten vorbereiten
            # Wir setzen explizit die ID, die wir gerade frisch von der API bekommen haben
            payload = rack.model_dump(exclude={'site_slug'}, exclude_none=True)
            payload['site'] = site_obj.id

            # 3. Sync
            self.ensure_object(
                app='dcim', 
                endpoint='racks', 
                # WICHTIG: Lookup muss site_id enthalten, sonst findet er das Rack evtl. global
                lookup_data={'site_id': site_obj.id, 'name': rack.name}, 
                create_data=payload
            )