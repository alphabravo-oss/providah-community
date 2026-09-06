import {useState} from "react";
import {Link,useOutletContext,useSearchParams} from "react-router";
import {useMutation,useQuery} from "@tanstack/react-query";
import {create} from "@bufbuild/protobuf";
import {z} from "zod";
import {actionLabel,statuses} from "./operations";
import {api,queries} from "./api";
import {ActionForm,DataTable,ErrorNote,Modal,PageHeader} from "./ui";
import {DashboardSpecSchema,DashboardWidgetSchema,type Dashboard,type DashboardWidget,type Organization} from "./gen/providah/v1/console_pb";

function ResourceWidget({org,widget}:{org:string;widget:DashboardWidget}){
 const [page,setPage]=useState("");
 const filters=widget.filters!;
 const [visibility,setVisibility]=useState<Record<string,boolean>>(()=>Object.fromEntries(filters.hiddenColumns.map(c=>[c,false])));
 const q=useQuery({queryKey:["resources",org,"dashboard",filters,page],queryFn:()=>api.listResources({organizationId:org,connectionId:filters.connectionId,region:filters.region,status:filters.status,search:filters.search,provider:filters.provider,kind:filters.kind,sortBy:filters.sortBy,descending:filters.descending,tagKey:filters.tagKey,tagValue:filters.tagValue,tagName:filters.tagName,tagExists:filters.tagExists,tagConditions:filters.tagConditions,tagMatchAny:filters.tagMatchAny,pageToken:page,pageSize:20})});
 return <section className="panel"><div className="panel-heading"><h2>{widget.title}</h2><p>Latest observed inventory</p></div><ErrorNote error={q.error}/>
 {q.isPending?<p>Loading resources…</p>:q.isError?null:!q.data?.resources.length?<p>No matching resources.</p>:<DataTable label={widget.title+" resources"} data={q.data.resources} rowId={r=>r.id} columnVisibility={visibility} onColumnVisibilityChange={setVisibility} columns={[
 {accessorKey:"name",header:"Resource",cell:({row:{original:r}})=><Link to={`/app/resources?org=${encodeURIComponent(org)}&resource=${encodeURIComponent(r.id)}`}>{r.name||r.id}</Link>},
 {accessorKey:"provider",header:"Provider"},{accessorKey:"kind",header:"Type"},{accessorKey:"region",header:"Region"},{accessorKey:"status",header:"Status"},
 {id:"observedAt",header:"Observed",cell:({row:{original:r}})=>r.observedAt?new Date(r.observedAt).toLocaleString():"Unknown"}
 ]}/>}
 <div className="row-actions"><button disabled={!page||q.isPending} onClick={()=>setPage("")}>First page</button><button disabled={!q.data?.nextPageToken||q.isPending||q.isError} onClick={()=>setPage(q.data!.nextPageToken)}>Next page</button></div>
 </section>;
}
function SummaryWidget({org,widget}:{org:string;widget:DashboardWidget}){
 const q=useQuery({queryKey:["resource-summary",org,widget.filters,widget.summaryBy],queryFn:()=>api.getResourceSummary({organizationId:org,filters:widget.filters,groupBy:widget.summaryBy})});
 return <section className="panel"><div className="panel-heading"><h2>{widget.title}</h2><p>Count by {widget.summaryBy==="kind"?"type":widget.summaryBy}</p></div><ErrorNote error={q.error}/>
 {q.isPending?<p>Loading summary…</p>:q.isError?null:<><p className="notice">{q.data?.total.toString()??"0"} matching resources</p>{!q.data?.groups.length?<p>No matching resources.</p>:<DataTable label={widget.title+" summary"} data={q.data.groups} rowId={r=>r.label} columns={[
 {accessorKey:"label",header:"Group",cell:({row:{original:r}})=>r.label||"Unknown"},
 {id:"total",header:"Resources",cell:({row:{original:r}})=>r.total.toString()},
 {id:"oldestObservation",header:"Oldest observation",cell:({row:{original:r}})=>r.oldestObservation?new Date(r.oldestObservation).toLocaleString():"Unknown"}
 ]}/>}</>}
 </section>;
}
function ActivityWidget({org,widget}:{org:Organization;widget:DashboardWidget}){
 const [page,setPage]=useState("");
 const allowed=org.permissions.includes("operations.read");
 const q=useQuery({queryKey:["operations",org.id,"dashboard",widget.filters,page],queryFn:()=>api.listOperations({organizationId:org.id,resourceFilters:widget.filters,pageToken:page}),enabled:allowed});
 return <section className="panel"><div className="panel-heading"><h2>{widget.title}</h2><p>Operations for currently matching resources</p></div>
 {!allowed?<p>Operation-read access is required.</p>:<><ErrorNote error={q.error}/>{q.isPending?<p>Loading activity…</p>:q.isError?null:!q.data?.operations.length?<p>No matching operations.</p>:<DataTable label={widget.title+" activity"} data={q.data.operations} rowId={o=>o.id} columns={[
 {id:"action",header:"Operation",cell:({row:{original:o}})=><Link to={`/app/operations?org=${encodeURIComponent(org.id)}&operation=${o.id}`}>{actionLabel(o.action,o.resourceKind)} · {o.resourceName}</Link>},
 {id:"status",header:"Status",cell:({row:{original:o}})=>statuses[o.status]},
 {id:"created",header:"Requested",cell:({row:{original:o}})=>new Date(o.createdAt).toLocaleString()}
 ]}/>}<div className="row-actions"><button disabled={!page||q.isPending} onClick={()=>setPage("")}>First page</button><button disabled={!q.data?.nextPageToken||q.isPending||q.isError} onClick={()=>setPage(q.data!.nextPageToken)}>Next page</button></div></>}
 </section>;
}
export function DashboardsPage(){
 const {org}=useOutletContext<{org:Organization}>();
 const manageShared=org.permissions.includes("dashboards.manage");
 const [params,setParams]=useSearchParams();const id=params.get("dashboard");
 const [editor,setEditor]=useState(false),[draft,setDraft]=useState<DashboardWidget[]>([]),[editing,setEditing]=useState<Dashboard>();
 const q=useQuery({queryKey:["dashboards",org.id],queryFn:()=>api.listDashboards({organizationId:org.id})});
 const views=useQuery({queryKey:["inventory-views",org.id],queryFn:()=>api.listInventoryViews({organizationId:org.id}),enabled:editor});
 const teams=useQuery({queryKey:["dashboard-teams",org.id],queryFn:()=>api.listDashboardTeams({organizationId:org.id}),enabled:editor&&manageShared});
 const teamOptions=(teams.isError?[]:teams.data?.teams??[]).map(t=>({value:t.id,label:t.name+(t.active?"":" (disabled)")}));
 for(const teamId of editing?.teamIds??[]) if(!teamOptions.some(t=>t.value===teamId)) teamOptions.push({value:teamId,label:"Unavailable team · "+teamId.slice(0,8)});
 const selected=q.isError?undefined:q.data?.dashboards.find(d=>d.id===id);
 const select=(value?:string)=>setParams(p=>{value?p.set("dashboard",value):p.delete("dashboard");return p;});
 const save=useMutation({mutationFn:(v:Record<string,string>)=>{const teamIds=v.visibility==="teams"?(v.teams??"").split(",").filter(Boolean):[];if(v.visibility==="teams"&&teamIds.length===0)throw new Error("Choose at least one team.");return api.saveDashboard({organizationId:org.id,id:editing?.id??"",expectedRevision:editing?.revision??0n,name:v.name,shared:v.visibility==="shared",teamIds,spec:create(DashboardSpecSchema,{widgets:draft})});},onSuccess:async d=>{await queries.invalidateQueries({queryKey:["dashboards",org.id]});setEditor(false);select(d.id);}});
 const remove=useMutation({mutationFn:(d:Dashboard)=>api.deleteDashboard({organizationId:org.id,id:d.id,expectedRevision:d.revision}),onSuccess:async()=>{select();await queries.invalidateQueries({queryKey:["dashboards",org.id]});}});
 const begin=(d?:Dashboard)=>{setEditing(d);setDraft(d?.spec?.widgets??[]);save.reset();setEditor(true);};
 const move=(i:number,delta:number)=>setDraft(items=>{const next=[...items];[next[i],next[i+delta]]=[next[i+delta],next[i]];return next;});
 return <><PageHeader eyebrow="Your workspace" title={selected?.name??"Dashboards"} description="Arrange private, team or organization-shared resource views. Results follow your current access."><button className="primary" disabled={(q.data?.dashboards.filter(d=>d.owned).length??0)>=50} onClick={()=>begin()}>Create dashboard</button></PageHeader>
 <ErrorNote error={q.error}/><ErrorNote error={remove.error}/>
 {id&&<nav className="breadcrumbs" aria-label="Dashboard breadcrumb"><button onClick={()=>select()}>Dashboards</button><span aria-current="page">{selected?.name??"Dashboard"}</span></nav>}
 {q.isPending?<p>Loading dashboards…</p>:q.isError?null:id&&!selected?<p role="alert">Dashboard is unavailable.</p>:selected?<>
 <p>{selected.shared?"Shared with this organization":selected.teamIds.length?"Shared with selected teams":"Private to you"}</p>
 {((selected.shared||selected.teamIds.length)?manageShared:selected.owned)&&<div className="row-actions"><button onClick={()=>begin(selected)}>Edit dashboard</button><button disabled={remove.isPending} onClick={()=>remove.mutate(selected)}>Delete dashboard</button></div>}
 {!selected.spec?.widgets.length?<p>Edit this dashboard to add resource views.</p>:<div className="dashboard-grid">{selected.spec.widgets.map((w,i)=>w.activity?<ActivityWidget key={`${selected.id}-${selected.revision}-${i}`} org={org} widget={w}/>:w.summaryBy?<SummaryWidget key={`${selected.id}-${selected.revision}-${i}`} org={org.id} widget={w}/>:<ResourceWidget key={`${selected.id}-${selected.revision}-${i}`} org={org.id} widget={w}/>)}</div>}
 </>:!q.data?.dashboards.length?<p>No dashboards yet. Save inventory views, then add them as widgets.</p>:<DataTable label="Dashboards" data={q.data.dashboards} rowId={d=>d.id} columns={[{accessorKey:"name",header:"Dashboard",cell:({row:{original:d}})=><Link to={`?org=${encodeURIComponent(org.id)}&dashboard=${d.id}`}>{d.name}</Link>},{id:"visibility",header:"Visibility",cell:({row:{original:d}})=>d.shared?"Organization":d.teamIds.length?"Teams":"Private"},{id:"widgets",header:"Widgets",cell:({row:{original:d}})=>d.spec?.widgets.length??0}]}/>}
 <Modal wide open={editor} onOpenChange={setEditor} title={editing?"Edit dashboard":"Create dashboard"} description="Add up to 12 saved views. Filters are copied here; later edits to the saved view do not change this dashboard.">
 <ErrorNote error={views.error}/><ErrorNote error={teams.error}/>
 <label>Add saved view<select aria-label="Add saved view" value="" disabled={draft.length>=12||save.isPending} onChange={e=>{const v=views.data?.views.find(v=>v.id===e.target.value);if(v?.spec)setDraft(d=>[...d,create(DashboardWidgetSchema,{title:v.name,filters:v.spec})]);}}><option value="">Choose a view…</option>{views.data?.views.map(v=><option key={v.id} value={v.id}>{v.name}</option>)}</select></label>
 {!views.isPending&&!views.data?.views.length&&<p>Create saved views in Inventory first.</p>}
 <ol>{draft.map((w,i)=><li key={i}>{w.title} <select aria-label={`Widget ${i+1} display`} value={w.activity?"activity":w.summaryBy} disabled={save.isPending} onChange={e=>{const display=e.target.value;const summaryBy=display==="activity"?"":display;setDraft(d=>d.map((old,n)=>n===i?create(DashboardWidgetSchema,{...old,summaryBy,activity:display==="activity"}):old));}}><option value="">Resource table</option><option value="provider">Count by provider</option><option value="kind">Count by type</option><option value="status">Count by status</option><option value="activity">Recent operations</option></select> <button aria-label={`Move ${w.title} up`} disabled={i===0||save.isPending} onClick={()=>move(i,-1)}>Up</button> <button aria-label={`Move ${w.title} down`} disabled={i===draft.length-1||save.isPending} onClick={()=>move(i,1)}>Down</button> <button aria-label={`Remove ${w.title}`} disabled={save.isPending} onClick={()=>setDraft(d=>d.filter((_,n)=>n!==i))}>Remove</button></li>)}</ol>
 <ActionForm key={editing?.id??"new"} fields={[{name:"name",label:"Dashboard name",defaultValue:editing?.name??"",schema:z.string().trim().min(1).max(80)},...(manageShared?[{name:"visibility",label:"Dashboard visibility",type:"select" as const,defaultValue:editing?.shared?"shared":editing?.teamIds.length?"teams":"private",options:[{value:"private",label:"Private"},{value:"shared",label:"Shared with organization"},{value:"teams",label:"Selected teams"}]},{name:"teams",label:"Dashboard teams",type:"checks" as const,when:{name:"visibility",is:["teams"]},defaultValue:editing?.teamIds.join(",")??"",options:teamOptions,schema:z.string(),description:"Choose up to 20 teams. Disabled teams receive no access; resource permissions still apply."}]:[])]} submitLabel="Save dashboard" onSubmit={v=>save.mutateAsync(v)} pending={save.isPending} error={save.error}/>
 </Modal></>;
}
