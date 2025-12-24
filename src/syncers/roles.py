from src.syncers.base import BaseSyncer  
from rich.console import Console

console = Console()

class RoleSyncer(BaseSyncer):
    def sync_roles(self, roles):
        console.rule("[bold]Syncing Device Roles[/bold]")
        for role in roles:
            self.ensure_object(
                app='dcim',
                endpoint='device_roles',
                lookup_data={'slug': role.slug},
                # FIX: exclude_none=True for consistency
                create_data=role.model_dump(exclude_none=True)
            )