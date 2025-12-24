from .base import BaseSyncer
from rich.console import Console

console = Console()

class ModuleTypeSyncer(BaseSyncer):
    def sync_module_types(self, module_types):
        console.rule("[bold]Syncing Module Types[/bold]")
        
        for mt in module_types:
            # 1. Prepare payload
            payload = mt.model_dump(exclude_none=True)

            # 2. Get manufacturer ID
            manufacturer_slug = mt.manufacturer.lower().replace(" ", "-")
            manufacturer_id = self._get_cached_id('dcim', 'manufacturers', manufacturer_slug)

            if not manufacturer_id and self.dry_run: manufacturer_id = 0
            payload['manufacturer'] = manufacturer_id

            # 3. Ensure in NetBox
            self.ensure_object(
                app='dcim',
                endpoint='module_types',
                lookup_data={'slug': mt.slug},
                create_data=payload
            )