import {Link} from "react-router";
import {useState} from "react";
import {useMutation,useQuery} from "@tanstack/react-query";
import {z} from "zod";
import {api,queries} from "./api";
import {ActionForm,DataTable,ErrorNote,Modal} from "./ui";
import type {InventoryView,InventoryViewSpec} from "./gen/providah/v1/console_pb";

export type InventorySettings=Pick<InventoryViewSpec,"search"|"provider"|"kind"|"sortBy"|"descending"|"hiddenColumns"|"tagKey"|"tagValue"|"tagName"|"tagExists"|"tagConditions"|"tagMatchAny"|"connectionId"|"region"|"status">;
export function InventoryViews({org,spec,onApply}:{org:string;spec:InventorySettings;onApply:(value:InventorySettings,id:string)=>void}){
 const [open,setOpen]=useState(false),[saving,setSaving]=useState(false);
 const [editing,setEditing]=useState<InventoryView>();
 const views=useQuery({queryKey:["inventory-views",org],queryFn:()=>api.listInventoryViews({organizationId:org}),enabled:open});
 const refresh=()=>queries.invalidateQueries({queryKey:["inventory-views",org]});
 const save=useMutation({mutationFn:(name:string)=>api.saveInventoryView({organizationId:org,id:editing?.id??"",expectedRevision:editing?.revision??0n,name,spec}),onSuccess:()=>{setSaving(false);void refresh();}});
 const remove=useMutation({mutationFn:(view:InventoryView)=>api.deleteInventoryView({organizationId:org,id:view.id,expectedRevision:view.revision}),onSuccess:()=>{void refresh();}});
 const begin=(view?:InventoryView)=>{setEditing(view);save.reset();setSaving(true);};
 return <><button className="secondary" onClick={()=>{setSaving(false);remove.reset();setOpen(true);}}>Saved views</button>
 <Modal wide open={open} onOpenChange={setOpen} title={saving ? (editing ? "Replace saved view" : "Save inventory view") : "Your inventory views"} description="Views are private to you in this organization. They store filters, sorting and columns; results always use your current permissions.">
 {saving ? <>
 <p>Save the current inventory settings{editing ? ` over “${editing.name}”` : ""}. {spec.hiddenColumns.length} columns hidden; {spec.sortBy ? `sorted by ${spec.sortBy} ${spec.descending ? "descending" : "ascending"}` : "default order"}.</p>
 <ActionForm fields={[{name:"name",label:"View name",defaultValue:editing?.name??"",schema:z.string().trim().min(1).max(80)}]} onSubmit={v=>save.mutateAsync(v.name)} submitLabel="Save view" pending={save.isPending} error={save.error}/>
 <button className="secondary" disabled={save.isPending} onClick={()=>setSaving(false)}>Back to saved views</button>
 </> : <>
 <ErrorNote error={views.error}/><ErrorNote error={remove.error}/>
 <button className="primary" onClick={()=>begin()} disabled={(views.data?.views.length??0)>=50}>Save current view</button>
 {views.isPending ? <p>Loading views…</p> : !views.data?.views.length ? <p>No saved views yet.</p> :
 <DataTable label="Saved inventory views" data={views.data.views} rowId={v=>v.id} columns={[
 {accessorKey:"name",header:"View",cell:({row:{original:v}})=><button onClick={()=>{if(v.spec){onApply(v.spec,v.id);setOpen(false);}}}>{v.name}</button>},
 {id:"actions",header:"Actions",cell:({row:{original:v}})=><div className="row-actions"><Link to={`/app/resources?org=${encodeURIComponent(org)}&view=${v.id}`}>Open view link</Link><button disabled={remove.isPending} onClick={()=>begin(v)}>Replace with current settings</button><button disabled={remove.isPending} onClick={()=>remove.mutate(v)}>Delete view</button></div>}
 ]}/>}
 </>}
 </Modal></>;
}
