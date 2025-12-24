"""
Utility functions for NetBox GitOps Controller.
Contains common operations, helpers, and type conversions.
"""

import time
from typing import Optional, Union, Any, Set, Tuple
from rich.console import Console

from src.constants import (
    CABLE_COLOR_MAP,
    MANAGED_TAG_SLUG,
    TERMINATION_INTERFACE,
    TERMINATION_FRONT_PORT,
    TERMINATION_REAR_PORT,
    ENDPOINT_FRONT_PORTS,
    ENDPOINT_REAR_PORTS,
)

console = Console()


# ============================================================================
# COLOR UTILITIES
# ============================================================================

def normalize_color(color_input: Optional[str]) -> str:
    """
    Normalize color input to hex format.

    Args:
        color_input: Color name or hex value

    Returns:
        Hex color string without # prefix

    Examples:
        >>> normalize_color("purple")
        '800080'
        >>> normalize_color("#FF0000")
        'FF0000'
        >>> normalize_color(None)
        ''
    """
    if not color_input:
        return ''

    raw = color_input.lower().strip()
    color = CABLE_COLOR_MAP.get(raw, raw)
    return color.replace('#', '')


# ============================================================================
# TAG UTILITIES
# ============================================================================

def extract_tag_ids_and_slugs(tags: Optional[list]) -> Tuple[Set[int], Set[str]]:
    """
    Extract tag IDs and slugs from a mixed list of tag representations.

    Args:
        tags: List of tags (can be ints, dicts, or mixed)

    Returns:
        Tuple of (set of tag IDs, set of tag slugs)
    """
    ids: Set[int] = set()
    slugs: Set[str] = set()

    for tag in tags or []:
        if isinstance(tag, int):
            ids.add(tag)
        elif isinstance(tag, dict):
            if 'id' in tag:
                ids.add(tag['id'])
            if 'slug' in tag:
                slugs.add(tag['slug'])

    return ids, slugs


def is_managed_by_gitops(obj: Optional[dict], managed_tag_id: Optional[int]) -> bool:
    """
    Check if an object is managed by GitOps based on its tags.

    Args:
        obj: NetBox object (as dict)
        managed_tag_id: ID of the gitops tag

    Returns:
        True if object has gitops tag
    """
    if not obj:
        return False

    tag_ids, tag_slugs = extract_tag_ids_and_slugs(obj.get('tags'))

    if managed_tag_id and managed_tag_id in tag_ids:
        return True

    return MANAGED_TAG_SLUG in tag_slugs


# ============================================================================
# OBJECT UTILITIES
# ============================================================================

def get_id_from_object(obj: Any) -> Optional[int]:
    """
    Extract ID from various object representations.

    Args:
        obj: NetBox object (can be int, dict, or object with .id attribute)

    Returns:
        Integer ID or None
    """
    if isinstance(obj, int):
        return obj
    if hasattr(obj, 'id'):
        return obj.id
    if isinstance(obj, dict) and 'id' in obj:
        return obj['id']
    return None


def safe_getattr(obj: Any, attr: str, default: Any = None) -> Any:
    """
    Safely get attribute from object with default value.
    Handles both dict and object attribute access.

    Args:
        obj: Object to get attribute from
        attr: Attribute name
        default: Default value if not found

    Returns:
        Attribute value or default
    """
    if isinstance(obj, dict):
        return obj.get(attr, default)
    return getattr(obj, attr, default)


# ============================================================================
# TERMINATION TYPE UTILITIES
# ============================================================================

def get_termination_type(obj: Union[dict, object, None]) -> str:
    """
    Determine the NetBox termination type from an object.

    Args:
        obj: Port/Interface object (dict or pynetbox object)

    Returns:
        Termination type string (dcim.interface, dcim.frontport, dcim.rearport)
    """
    if obj is None:
        return TERMINATION_INTERFACE

    # Check dict representation
    if isinstance(obj, dict):
        endpoint = obj.get('_endpoint')
        if endpoint == ENDPOINT_FRONT_PORTS:
            return TERMINATION_FRONT_PORT
        if endpoint == ENDPOINT_REAR_PORTS:
            return TERMINATION_REAR_PORT
        return TERMINATION_INTERFACE

    # Check object URL
    url = safe_getattr(obj, 'url', '')
    if '/front-ports/' in url:
        return TERMINATION_FRONT_PORT
    if '/rear-ports/' in url:
        return TERMINATION_REAR_PORT

    return TERMINATION_INTERFACE


# ============================================================================
# CABLE UTILITIES
# ============================================================================

def cable_connects_to(cable: dict, object_id: int) -> bool:
    """
    Check if a cable connects to a specific object.

    Args:
        cable: Cable object (as dict)
        object_id: ID of the object to check

    Returns:
        True if cable connects to the object
    """
    terminations = cable.get('a_terminations', []) + cable.get('b_terminations', [])

    for term in terminations or []:
        term_id = term.get('object_id') or term.get('id')
        if term_id == object_id:
            return True

    return False


# ============================================================================
# TIMING UTILITIES
# ============================================================================

def safe_sleep(seconds: float, dry_run: bool = False):
    """
    Sleep for specified seconds, respecting dry-run mode.

    Args:
        seconds: Number of seconds to sleep
        dry_run: If True, don't actually sleep (just log)
    """
    if not dry_run and seconds > 0:
        time.sleep(seconds)


# ============================================================================
# ROLE UTILITIES
# ============================================================================

def extract_device_role_slug(device: Any) -> Optional[str]:
    """
    Robustly extract device role slug from various NetBox device representations.

    Args:
        device: NetBox device object (pynetbox Record or dict)

    Returns:
        Role slug string or None if not found
    """
    # Try direct attribute access first
    try:
        device_role = safe_getattr(device, 'device_role', None)
        if device_role:
            role_slug = safe_getattr(device_role, 'slug', None)
            if role_slug:
                return role_slug
    except (AttributeError, TypeError):
        pass

    # Try alternate 'role' attribute (some NetBox versions)
    try:
        role = safe_getattr(device, 'role', None)
        if role:
            role_slug = safe_getattr(role, 'slug', None)
            if role_slug:
                return role_slug
    except (AttributeError, TypeError):
        pass

    # Try dict representation
    if isinstance(device, dict):
        device_role = device.get('device_role')
        if isinstance(device_role, dict):
            return device_role.get('slug')

    return None


# ============================================================================
# LOGGING UTILITIES
# ============================================================================

def log_error(message: str, exception: Optional[Exception] = None):
    """Log error message with optional exception details."""
    if exception:
        console.print(f"[red]{message}: {exception}[/red]")
    else:
        console.print(f"[red]{message}[/red]")


def log_warning(message: str):
    """Log warning message."""
    console.print(f"[yellow]{message}[/yellow]")


def log_success(message: str):
    """Log success message."""
    console.print(f"[green]{message}[/green]")


def log_info(message: str):
    """Log info message."""
    console.print(f"[cyan]{message}[/cyan]")


def log_debug(message: str):
    """Log debug message."""
    console.print(f"[dim]{message}[/dim]")


def log_dry_run(action: str, details: str):
    """Log dry-run action."""
    console.print(f"[yellow][DRY] {action}: {details}[/yellow]")
