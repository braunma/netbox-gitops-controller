import yaml
from pathlib import Path
from rich.console import Console

console = Console()

class DataLoader:
    def __init__(self, base_path: str):
        self.base_path = Path(base_path)

    def load_from_folder(self, subfolder: str, model):
        """Recursively loads .yaml files and validates them."""
        target_dir = self.base_path / subfolder
        results = []
        
        if not target_dir.exists():
            console.print(f"[yellow]Warning: Folder {subfolder} not found.[/yellow]")
            return []

        files = list(target_dir.rglob("*.yaml"))
        
        for file_path in files:
            with open(file_path, "r") as f:
                data = yaml.safe_load(f) or []
                if isinstance(data, list):
                    # Convert dicts to Pydantic Models
                    results.extend([model(**item) for item in data])
        
        console.print(f"[dim]Loaded {len(results)} items from {subfolder}[/dim]")
        return results