from src.syncers.base import BaseSyncer
from rich.console import Console

console = Console()

class ExtrasSyncer(BaseSyncer):
    
    def sync_tags(self, tags):
        console.rule("[bold]Syncing Tags[/bold]")
        for tag in tags:
            self.ensure_object(
                app='extras',
                endpoint='tags',
                lookup_data={'slug': tag.slug},
                create_data=tag.model_dump(exclude_none=True)
            )