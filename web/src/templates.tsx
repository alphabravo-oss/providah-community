import {useEffect,useState} from "react";
import {Link,useSearchParams,useNavigate,useOutletContext} from "react-router";
import {useMutation,useQuery} from "@tanstack/react-query";
import {z} from "zod";
import {api,queries} from "./api";
import {ActionForm,DataTable,ErrorNote,Modal,PageHeader} from "./ui";
import {AutomationSources} from "./sources";
import {CreationFields} from "./creation";
import type {Connection,Organization,ServerTemplate} from "./gen/providah/v1/console_pb";

export function TemplatesPage(){
 const {org}=useOutletContext<{org:Organization}>();
 const navigate=useNavigate();
 const [publish,setPublish]=useState(false),[connection,setConnection]=useState<Connection>(),[draft,setDraft]=useState<Record<string,string>>(),[action,setAction]=useState(""),[key,setKey]=useState("");
 const [params,setParams]=useSearchParams();
 const selectedId=["validation","version","project"].some(k=>params.has(k))?"":params.get("template")??"";
 const setSelected=(value:ServerTemplate|undefined)=>setParams(previous=>{const next=new URLSearchParams(previous);next.delete("validation");for(const k of ["source","version","project"])next.delete(k);if(value)next.set("template",value.id);else next.delete("template");return next;});
 const detail=useQuery({queryKey:["server-template",org.id,selectedId],queryFn:()=>api.getServerTemplate({organizationId:org.id,id:selectedId}),enabled:!!selectedId});
 const selected=detail.error?undefined:detail.data;
 useEffect(()=>{setAction("");setKey(crypto.randomUUID());deploy.reset();update.reset();},[selectedId]);
 const catalog=useQuery({queryKey:["templates",org.id],queryFn:()=>api.listServerTemplates({organizationId:org.id})});
 const connections=useQuery({queryKey:["connections",org.id],queryFn:()=>api.listConnections({organizationId:org.id}),enabled:publish});
 const refresh=()=>queries.invalidateQueries();
 const create=useMutation({mutationFn:(v:Record<string,string>)=>api.publishServerTemplate({organizationId:org.id,connectionId:connection!.id,name:v.name,region:draft!.region,creation:{image:draft!.image,size:draft!.size,sshKey:draft!.sshKey,subnet:draft!.subnet??"",securityGroup:draft!.securityGroup??"",network:draft!.network??""}}),onSuccess:()=>{setPublish(false);void refresh();}});
 const update=useMutation({mutationFn:()=>api.setServerTemplateStatus({organizationId:org.id,id:selected!.id,status:action}),onSuccess:()=>{setSelected(undefined);void refresh();}});
 const deploy=useMutation({retry:false,mutationFn:(v:Record<string,string>)=>api.requestServerCreation({organizationId:org.id,connectionId:selected!.connectionId,region:selected!.region,creation:{...selected!.creation!,name:v.name},templateId:selected!.id,idempotencyKey:key,reason:v.reason}),onSuccess:()=>{void queries.invalidateQueries({queryKey:["operations",org.id]});navigate("/operations");}});
 const canPublish=org.permissions.includes("templates.publish");
 return <>
  <PageHeader eyebrow="PROVISIONING" title="Server templates" description="Immutable configurations for repeatable, independently approved server creation.">{canPublish && <button className="primary" onClick={()=>{setConnection(undefined);setDraft(undefined);create.reset();setPublish(true);}}>Publish template</button>}</PageHeader>
  <ErrorNote error={catalog.error}/>
  <DataTable label="Server templates" data={catalog.isError?[]:catalog.data?.templates??[]} rowId={r=>r.id} columns={[
   {accessorKey:"name",header:"Template"},{accessorKey:"version",header:"Version"},{accessorKey:"region",header:"Region"},{accessorKey:"status",header:"Status"},
   {id:"actions",header:"Actions",cell:({row})=><Link className="secondary" to={`/app/templates?org=${org.id}&template=${row.original.id}`} onClick={()=>{deploy.reset();update.reset();}}>View {row.original.name} v{row.original.version}</Link>}
  ]}/>
  <Modal open={publish} onOpenChange={setPublish} title="Publish server template" description="Publish an immutable configuration tied to one connection. New versions never replace existing bindings.">
   <ErrorNote error={connections.error}/>
   {!connection ? <ActionForm fields={[{name:"connection",label:"Cloud connection",type:"select",options:connections.data?.connections.filter(c=>c.enabled).map(c=>({value:c.id,label:c.name}))??[],schema:z.string().min(1)}]} onSubmit={v=>setConnection(connections.data?.connections.find(c=>c.id===v.connection))} submitLabel="Choose configuration"/> : !draft ? <CreationFields template org={org} connection={connection} submit={setDraft}/> : <>
    <dl className="resource-details">{Object.entries(draft).map(([k,v])=><div key={k}><dt>{k}</dt><dd>{v}</dd></div>)}</dl>
    <ActionForm fields={[{name:"name",label:"Template name",schema:z.string().trim().min(1).max(80)}]} onSubmit={v=>create.mutateAsync(v)} pending={create.isPending} error={create.error} submitLabel="Publish immutable version"/>
   </>}
  </Modal>
  <Modal open={!!selectedId} onOpenChange={open=>{if(!open)setSelected(undefined);}} title={selected ? selected.name+" · version "+selected.version : "Template"} description="Only the server name can change at deployment. Cloud eligibility is checked again before execution.">
   <nav className="breadcrumbs" aria-label="Template breadcrumb"><Link to={"/app/templates?org="+org.id}>Templates</Link><span aria-hidden="true">›</span><span aria-current="page">{selected?selected.name+" v"+selected.version:"Template"}</span></nav>
   <ErrorNote error={detail.error}/>{detail.isPending&&<p role="status">Loading template…</p>}
   {selected && <>
    <dl className="resource-details">{Object.entries({Connection:selected.connectionId,Region:selected.region,Image:selected.creation?.image,Size:selected.creation?.size,"SSH key":selected.creation?.sshKey,Subnet:selected.creation?.subnet,"Private network":selected.creation?.network,"Security group":selected.creation?.securityGroup,Status:selected.status}).filter(([,v])=>v).map(([k,v])=><div key={k}><dt>{k}</dt><dd>{v}</dd></div>)}</dl>
    {action ? <>
     <p className="notice">{action==="retired" ? "Retirement prevents new deployments. Already approved work keeps its pinned configuration." : "Revocation prevents new deployments and stops queued work at its next dispatch check. It cannot undo an action already sent to the provider."}</p>
     <ActionForm fields={[{name:"confirm",label:"Confirm template name",schema:z.literal(selected.name)}]} onSubmit={()=>update.mutateAsync()} pending={update.isPending} error={update.error} submitLabel={action==="retired" ? "Retire template" : "Revoke template"}/>
    </> : <>
     {selected.status==="published" && org.permissions.includes("operations.create") && org.permissions.includes("operations.request") && <>
      <p className="notice">Creates one billable server and boot disk. No automatic rollback or price quote is included. The request requires independent approval.</p>
      <ActionForm fields={[{name:"name",label:"New server name",schema:z.string().regex(/^[a-z][a-z0-9-]{0,61}[a-z0-9]$/)},{name:"reason",label:"Deployment reason",type:"textarea",schema:z.string().min(3).max(500)}]} onSubmit={v=>deploy.mutateAsync(v)} pending={deploy.isPending} error={deploy.error} submitLabel="Request deployment approval"/>
     </>}
     {canPublish && selected.status==="published" && <button className="secondary" onClick={()=>setAction("retired")}>Retire template</button>}
     {canPublish && selected.status!=="revoked" && <button className="secondary" onClick={()=>setAction("revoked")}>Revoke template</button>}
    </>}
   </>}
  </Modal>
  <AutomationSources org={org}/>
 </>;
}
