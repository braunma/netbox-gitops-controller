import typer
import os
import sys
import urllib3
import pynetbox
from dotenv import load_dotenv
from rich.console import Console

# Import Loader
from src.loader import DataLoader

# ==========================================
# 1. IMPORT MODELS
# ==========================================
from src.models import (
    # Foundation Models
    SiteModel, 
    RackModel, 
    VlanModel, 
    VlanGroupModel,
    VRFModel,        
    TagModel,
    ModuleTypeModel, 
    DeviceTypeModel, 
    RoleModel,
    PrefixModel,
    
    # Device Models (Strict Mode for Controller)
    DeviceConfig
)

# ==========================================
# 2. IMPORT SYNCERS (LEGACY & NEW)
# ==========================================
# Legacy Syncers (for Foundation & Network)
from src.syncers.dcim import DCIMSyncer
from src.syncers.ipam import IPAMSyncer
from src.syncers.extras import ExtrasSyncer
from src.syncers.module_types import ModuleTypeSyncer
from src.syncers.device_types import DeviceTypeSyncer
from src.syncers.roles import RoleSyncer

# New Controller (for Devices & Cables)
from src.client import NetBoxClient
from src.controllers.device_controller import DeviceController

load_dotenv()

app = typer.Typer()
console = Console()


def run_sync(dry_run: bool = False):
    """
    Core sync logic - called by both the command and the callback.
    
    Phase 1: Foundation (Sites, Racks, Tags, Roles)
    Phase 2: Network & Types (VLANs, VRFs, Device Types, Module Types)
    Phase 3: Devices & Cables (Controller Engine - High Performance)
    """
    
    # Suppress SSL warnings
    urllib3.disable_warnings()
    
    url = os.getenv("NETBOX_URL")
    token = os.getenv("NETBOX_TOKEN")
    
    if not url or not token:
        console.print("[bold red]Error: NETBOX_URL or NETBOX_TOKEN not set![/bold red]")
        raise typer.Exit(code=1)

    # =========================================================================
    # INITIALIZE CLIENTS
    # =========================================================================
    
    # Legacy Client (for Phase 1 & 2)
    nb = pynetbox.api(url, token=token)
    nb.http_session.verify = False 
    
    # New Client (for Phase 3 - Devices & Cables)
    new_client = NetBoxClient(url, token, dry_run=dry_run)
    
    # =========================================================================
    # 1. LOAD DATA
    # =========================================================================
    loader = DataLoader(".")
    
    # Foundation Definitions
    tags = loader.load_from_folder("definitions/extras", TagModel)
    roles = loader.load_from_folder("definitions/roles", RoleModel)
    sites = loader.load_from_folder("definitions/sites", SiteModel)
    racks = loader.load_from_folder("definitions/racks", RackModel)
    module_types = loader.load_from_folder("definitions/module_types", ModuleTypeModel)
    device_types = loader.load_from_folder("definitions/device_types", DeviceTypeModel)
    
    # Network Definitions
    vrfs = loader.load_from_folder("definitions/vrfs", VRFModel)        
    vlan_groups = loader.load_from_folder("definitions/vlan_groups", VlanGroupModel) 
    vlans = loader.load_from_folder("definitions/vlans", VlanModel)
    prefixes = loader.load_from_folder("definitions/prefixes", PrefixModel)
    
    # Inventory (Nutzt NEUES Strict Model!)
    devices = loader.load_from_folder("inventory/hardware/active", DeviceConfig)
    panels = loader.load_from_folder("inventory/hardware/passive", DeviceConfig)

    all_devices = devices + panels
    
    console.print(f"[dim]Loaded {len(all_devices)} devices from inventory[/dim]")

    # =========================================================================
    # 2. INIT SYNCERS (LEGACY)
    # =========================================================================
    # Pass managed_tag_id from NetBoxClient (single source of truth)
    managed_tag_id = new_client.managed_tag_id

    extras = ExtrasSyncer(nb, managed_tag_id, dry_run=dry_run)
    role_syncer = RoleSyncer(nb, managed_tag_id, dry_run=dry_run)
    dcim = DCIMSyncer(nb, managed_tag_id, dry_run=dry_run)
    ipam = IPAMSyncer(nb, managed_tag_id, dry_run=dry_run)
    type_syncer = DeviceTypeSyncer(nb, managed_tag_id, dry_run=dry_run)
    mt_syncer = ModuleTypeSyncer(nb, managed_tag_id, dry_run=dry_run)

    # =========================================================================
    # 3. EXECUTE HYBRID SYNC
    # =========================================================================
    try:
        # =====================================================================
        # PHASE 1: FOUNDATION (Legacy Engine)
        # =====================================================================
        console.rule("[bold cyan]Phase 1: Foundation[/bold cyan]")
        extras.sync_tags(tags)
        role_syncer.sync_roles(roles)
        dcim.sync_sites(sites)
        dcim.sync_racks(racks)
        
        # =====================================================================
        # PHASE 2: NETWORK & TYPES (Legacy Engine)
        # =====================================================================
        console.rule("[bold cyan]Phase 2: Network & Types[/bold cyan]")
        ipam.sync_vrfs(vrfs)                
        ipam.sync_vlan_groups(vlan_groups) 
        ipam.sync_vlans(vlans)
        ipam.sync_prefixes(prefixes)
        
        mt_syncer.sync_module_types(module_types) 
        type_syncer.sync_types(device_types)
        
        # =====================================================================
        # PHASE 3: DEVICES & CABLES (New Controller Engine)
        # =====================================================================
        console.rule("[bold magenta]Phase 3: Devices & Cables (Controller Engine)[/bold magenta]")
        
        # 1. Load global caches (once)
        console.print("[cyan]Loading global caches...[/cyan]")
        new_client.reload_global_cache()

        # 2. Load site-specific caches (for all sites used)
        unique_sites = set(dev.site_slug for dev in all_devices)
        console.print(f"[cyan]Loading site caches for: {', '.join(sorted(unique_sites))}[/cyan]")

        for site_slug in sorted(unique_sites):
            new_client.reload_cache(site_slug)

        # 3. Initialize controller
        controller = DeviceController(new_client)

        # 4. Reconciliation loop (devices + cables in one pass)
        console.print(f"[cyan]Reconciling {len(all_devices)} devices...[/cyan]")
        for idx, dev in enumerate(all_devices, 1):
            console.print(f"\n[dim]──── Device {idx}/{len(all_devices)}: {dev.name} ────[/dim]")
            controller.reconcile(dev)
        
        console.print("\n[green]✓ Phase 3 complete[/green]")

    except KeyboardInterrupt:
        console.print("\n[yellow]⚠ Sync interrupted by user[/yellow]")
        raise typer.Exit(code=130)
    
    except Exception as e:
        console.print(f"\n[bold red]✗ CRITICAL ERROR: {e}[/bold red]")
        import traceback
        console.print(f"[red]{traceback.format_exc()}[/red]")
        raise typer.Exit(code=1)

    # =========================================================================
    # 4. SUMMARY
    # =========================================================================
    if dry_run:
        console.print("\n[bold yellow]⚠ DRY RUN COMPLETE: No changes applied.[/bold yellow]")
    else:
        console.print("\n[bold green]✔ SYNC COMPLETE: Changes applied.[/bold green]")


@app.command()
def sync(
    dry_run: bool = typer.Option(False, "--dry-run", help="Simulate changes without applying them.")
):
    """
    Syncs Definitions and Inventory to NetBox (Hybrid Engine).
    
    Phase 1: Foundation (Sites, Racks, Tags, Roles)
    Phase 2: Network & Types (VLANs, VRFs, Device Types, Module Types)
    Phase 3: Devices & Cables (Controller Engine - High Performance)
    """
    run_sync(dry_run=dry_run)


# =========================================================================
# CALLBACK: Makes 'sync' the default command (for CI/CD compatibility)
# =========================================================================
@app.callback(invoke_without_command=True)
def main(
    ctx: typer.Context,
    dry_run: bool = typer.Option(False, "--dry-run", help="Simulate changes without applying them.")
):
    """
    NetBox GitOps Controller
    
    Syncs Infrastructure as Code definitions to NetBox.
    """
    if ctx.invoked_subcommand is None:
        # No command specified → run 'sync' as default
        run_sync(dry_run=dry_run)


if __name__ == "__main__":
    app()