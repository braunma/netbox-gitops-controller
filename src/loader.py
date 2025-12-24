"""Data loader for loading YAML files and validating with Pydantic models."""

import yaml
from pathlib import Path
from typing import List, Type, TypeVar
from pydantic import BaseModel
from rich.console import Console

console = Console()

# Type variable for Pydantic models
T = TypeVar('T', bound=BaseModel)


class DataLoader:
    """Load and validate YAML configuration files."""

    def __init__(self, base_path: str) -> None:
        """
        Initialize the DataLoader.

        Args:
            base_path: Base directory path containing the data folders
        """
        self.base_path = Path(base_path)

    def load_from_folder(self, subfolder: str, model: Type[T]) -> List[T]:
        """
        Recursively load .yaml files from a folder and validate them.

        Args:
            subfolder: Relative path to the folder containing YAML files
            model: Pydantic model class to validate the data against

        Returns:
            List of validated Pydantic model instances

        Examples:
            >>> loader = DataLoader("data")
            >>> sites = loader.load_from_folder("definitions/sites", SiteModel)
        """
        target_dir: Path = self.base_path / subfolder
        results: List[T] = []

        if not target_dir.exists():
            console.print(f"[yellow]Warning: Folder {subfolder} not found.[/yellow]")
            return []

        files: List[Path] = list(target_dir.rglob("*.yaml"))

        for file_path in files:
            with open(file_path, "r", encoding="utf-8") as f:
                data = yaml.safe_load(f) or []
                if isinstance(data, list):
                    # Convert dicts to Pydantic Models
                    results.extend([model(**item) for item in data])

        console.print(f"[dim]Loaded {len(results)} items from {subfolder}[/dim]")
        return results