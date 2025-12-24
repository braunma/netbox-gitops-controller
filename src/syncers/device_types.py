from .base import BaseSyncer
from rich.console import Console

console = Console()

class DeviceTypeSyncer(BaseSyncer):
    
    def sync_types(self, device_types):
        console.rule("[bold]Syncing Device Types[/bold]")
        
        for dt in device_types:
            # 1. Device Type Payload
            # IMPORTANT: 'device_bays' must be excluded here
            payload = dt.model_dump(
                exclude={
                    'interfaces', 'front_ports', 'rear_ports', 
                    'module_bays', 'device_bays'
                },
                exclude_none=True  
            )
            
            # Manufacturer
            manufacturer_slug = dt.manufacturer.lower().replace(" ", "-")
            manufacturer_id = self._get_cached_id('dcim', 'manufacturers', manufacturer_slug)
            
            # Dry Run Fallback for Manufacturer
            if not manufacturer_id and self.dry_run:
                manufacturer_id = 0
            
            payload['manufacturer'] = manufacturer_id

            # =====================================================
            # CRITICAL: Explicitly set subdevice_role
            # =====================================================
            if hasattr(dt, 'subdevice_role') and dt.subdevice_role:
                payload['subdevice_role'] = dt.subdevice_role
                console.print(f"[dim]Setting subdevice_role={dt.subdevice_role} for {dt.model}[/dim]")
            # =====================================================
            
            # Ensure Parent Object (Device Type)
            dt_obj = self.ensure_object_and_return(
                app='dcim', 
                endpoint='device_types', 
                lookup_data={'slug': dt.slug}, 
                create_data=payload
            )

            if not dt_obj:
                continue

            # =========================================================
            # A. REAR PORTS (First)
            # =========================================================
            if hasattr(dt, 'rear_ports') and dt.rear_ports:
                rp_payloads = [p.model_dump(exclude_none=True) for p in dt.rear_ports]
                
                self.sync_children(
                    app='dcim',
                    endpoint='rear_port_templates',
                    parent_filter={'device_type_id': dt_obj.id},
                    child_data_list=rp_payloads,
                    key_field='name'
                )

            # =========================================================
            # B. FRONT PORTS (With Mapping)
            # =========================================================
            if hasattr(dt, 'front_ports') and dt.front_ports:

                # Build mapping
                rear_port_map = {}
                if not self.dry_run:
                    all_rps = self.nb.dcim.rear_port_templates.filter(device_type_id=dt_obj.id)
                    rear_port_map = {rp.name: rp.id for rp in all_rps}

                fp_payloads = []
                for port in dt.front_ports:
                    p_data = port.model_dump(exclude={'rear_port'}, exclude_none=True)
                    
                    if port.rear_port:
                        if self.dry_run:
                            p_data['rear_port'] = 0
                        else:
                            rp_id = rear_port_map.get(port.rear_port)
                            if rp_id:
                                p_data['rear_port'] = rp_id
                            else:
                                console.print(f"[red]Warning: Rear Port '{port.rear_port}' missing[/red]")
                    
                    fp_payloads.append(p_data)

                self.sync_children(
                    app='dcim',
                    endpoint='front_port_templates',
                    parent_filter={'device_type_id': dt_obj.id},
                    child_data_list=fp_payloads,
                    key_field='name'
                )

            # =========================================================
            # C. INTERFACES
            # =========================================================
            if hasattr(dt, 'interfaces') and dt.interfaces:
                if_payloads = [i.model_dump(exclude_none=True) for i in dt.interfaces]
                
                self.sync_children(
                    app='dcim',
                    endpoint='interface_templates',
                    parent_filter={'device_type_id': dt_obj.id},
                    child_data_list=if_payloads,
                    key_field='name'
                )
                
            # =========================================================
            # D. MODULE BAYS (GPU Slots)
            # =========================================================
            if hasattr(dt, 'module_bays') and dt.module_bays:
                mb_payloads = [m.model_dump(exclude_none=True) for m in dt.module_bays]
                
                self.sync_children(
                    app='dcim',
                    endpoint='module_bay_templates',
                    parent_filter={'device_type_id': dt_obj.id},
                    child_data_list=mb_payloads,
                    key_field='name'
                )

            # =========================================================
            # E. DEVICE BAYS (NEW: For Isilon/Blade Slots)
            # =========================================================
            if hasattr(dt, 'device_bays') and dt.device_bays:
                db_payloads = [b.model_dump(exclude_none=True) for b in dt.device_bays]
                
                console.print(f"[dim]Syncing {len(db_payloads)} device bay templates for {dt.model}[/dim]")
                
                self.sync_children(
                    app='dcim',
                    endpoint='device_bay_templates',
                    parent_filter={'device_type_id': dt_obj.id},
                    child_data_list=db_payloads,
                    key_field='name'
                )