import {inputFields,inputValues} from "./automation-inputs";
import {Link,useSearchParams} from "react-router";
import {useState} from "react";
import {useInfiniteQuery,useMutation,useQuery} from "@tanstack/react-query";
import {z} from "zod";
import {api,queries} from "./api";
import {ActionForm,DataTable,ErrorNote,Modal,PageHeader} from "./ui";
import type {AutomationSource,AutomationVersion,Organization} from "./gen/providah/v1/console_pb";

export function ManagedProjects({org,versions,sources,runnerConfigured}:{org:Organization;versions:AutomationVersion[];sources:AutomationSource[];runnerConfigured:boolean}){
 const [draft,setDraft]=useState<{name:string;version:string}>();
 const fields=sources.find(s=>s.id===versions.find(v=>v.id===draft?.version)?.sourceId)?.inputs??[];
 const [open,setOpen]=useState(false);
 const [params,setParams]=useSearchParams();
 const selected=(params.has("validation")||params.has("version"))?"":params.get("project")??"";
 const setSelected=(id:string)=>setParams(previous=>{const next=new URLSearchParams(previous);next.delete("validation");next.delete("version");next.delete("template");next.delete("source");if(id)next.set("project",id);else next.delete("project");return next;});
 const detail=useQuery({queryKey:["automation-project",org.id,selected],queryFn:()=>api.getAutomationProject({organizationId:org.id,id:selected}),enabled:!!selected});
 const projects=useQuery({queryKey:["automation-projects",org.id],queryFn:()=>api.listAutomationProjects({organizationId:org.id})});
 const project=detail.error?undefined:detail.data;
 const history=useInfiniteQuery({queryKey:["automation-states",org.id,selected],initialPageParam:"",enabled:!!project,queryFn:({pageParam})=>api.listAutomationStates({organizationId:org.id,projectId:selected,beforeSerial:pageParam}),getNextPageParam:last=>last.nextBeforeSerial||undefined});
 const ownership=useInfiniteQuery({queryKey:["project-ownership",org.id,selected],initialPageParam:"",enabled:!!project&&org.permissions.includes("resources.read"),queryFn:({pageParam})=>api.listProjectOwnership({organizationId:org.id,projectId:selected,pageToken:pageParam}),getNextPageParam:last=>last.nextPageToken||undefined});
 const create=useMutation({gcTime:0,retry:false,mutationFn:(v:Record<string,string>)=>api.createAutomationProject({organizationId:org.id,name:draft!.name,versionId:draft!.version,inputsJson:inputValues(fields,v)}),onSuccess:r=>{setOpen(false);setSelected(r.id);void queries.invalidateQueries({queryKey:["automation-projects",org.id]});}});
 const validate=useMutation({retry:false,mutationFn:()=>api.requestAutomationValidation({organizationId:org.id,versionId:project!.versionId,projectId:project!.id}),onSuccess:()=>{setSelected("");void queries.invalidateQueries({queryKey:["automation-validations",org.id]});}});
 const eligible=versions.filter(v=>v.status==="published"&&v.runtimeAvailable&&v.runtime!=="ansible");
 return <>
  <PageHeader eyebrow="AUTOMATION" title="Managed projects" description="Projects bind a published version to protected, versioned state. Cloud planning and apply are not available yet.">{org.permissions.includes("templates.publish")&&<button className="primary" onClick={()=>{create.reset();setDraft(undefined);setOpen(true);}}>Create managed project</button>}</PageHeader>
  <ErrorNote error={projects.error}/>
  <DataTable label="Managed projects" data={projects.data?.projects??[]} rowId={r=>r.id} columns={[
   {accessorKey:"name",header:"Project"},{id:"version",header:"Source version",cell:({row})=>{const v=versions.find(v=>v.id===row.original.versionId);return v?`${v.name} v${v.version}`:row.original.versionId;}},
   {id:"state",header:"State",cell:({row})=>row.original.serial<0n?"No state yet":`Serial ${row.original.serial}`},{id:"lock",header:"Coordination",cell:({row})=>row.original.locked?"Locked":"Available"},
   {id:"actions",header:"History",cell:({row})=><button className="secondary" onClick={()=>setSelected(row.original.id)}>Inspect project {row.original.name}</button>}
  ]}/>
  <Modal open={open} onOpenChange={setOpen} title="Create managed project" description="This reserves a state namespace. It does not create cloud resources or adopt existing infrastructure.">
   {eligible.length?draft?<><p>Inputs are fixed when the project is created. Values remain private after submission.</p>{fields.length===0&&<p>No configurable inputs are declared.</p>}<ActionForm fields={inputFields(fields)} onSubmit={v=>create.mutateAsync(v)} pending={create.isPending} error={create.error} submitLabel="Create project"/></>:<ActionForm fields={[
    {name:"name",label:"Project name",schema:z.string().trim().min(1).max(80)},
    {name:"version",label:"Published source version",type:"select",options:eligible.map(v=>({value:v.id,label:`${v.name} v${v.version} · ${v.runtime}`})),schema:z.string().min(1)}
   ]} onSubmit={v=>setDraft({name:v.name,version:v.version})} submitLabel="Configure project inputs"/>:<p className="notice">Publish an allowed Terraform or OpenTofu version before creating a project.</p>}
  </Modal>
  <Modal wide open={!!selected} onOpenChange={v=>{if(!v)setSelected("");}} title={project?.name??"Managed project"} description="State contents stay private. These records expose only identity, integrity and version metadata.">
   <nav className="breadcrumbs" aria-label="Project breadcrumb"><Link to={"/app/templates?org="+org.id}>Templates</Link><span aria-hidden="true">›</span><span aria-current="page">{project?.name??"Managed project"}</span></nav>
   <ErrorNote error={detail.error}/>{detail.isPending&&<p role="status">Loading project…</p>}
   {project&&<dl className="resource-details">{Object.entries({Lineage:project.lineage||"Not initialized",Serial:project.serial<0n?"Not initialized":String(project.serial),"Current SHA-256":project.sha256||"Not initialized","State bytes":String(project.stateBytes),"Input SHA-256":project.inputsHash,Lock:project.locked?"Held — no automatic unlock":"Available"}).map(([k,v])=><div key={k}><dt>{k}</dt><dd>{v}</dd></div>)}</dl>}
   {project&&<section aria-label="IaC protection coverage"><h3>IaC protection coverage</h3>
    <dl className="resource-details">{Object.entries({"Latest state scan":({no_state:"No state saved",pending:"Not scanned",complete:"All identities recognized",partial:"Some identities unrecognized",invalid:"State could not be scanned"} as Record<string,string>)[project.ownership?.status??""]??"Unavailable","Unrecognized instances":String(project.ownership?.unsupported??0),"Retained references":String(project.ownership?.protectedReferences??0n),"Incomplete historical scans":String(project.ownership?.incompleteVersions??0n),"Last scanned":project.ownership?.scannedAt?new Date(project.ownership.scannedAt).toLocaleString():"Not scanned"}).map(([label,value])=><div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}</dl>
    <p className="notice">These references protect resources in this project’s configured connection. Provider aliases and actual cloud account ownership are not verified. References from older state remain protected even when the latest state removes them. An empty list does not prove that resources are unmanaged.</p>
    {org.permissions.includes("resources.read")&&<><ErrorNote error={ownership.error}/><DataTable label="Protected state references" data={ownership.error?[]:ownership.data?.pages.flatMap(p=>p.references)??[]} rowId={r=>JSON.stringify([r.kind,r.region,r.nativeId])} columns={[
     {accessorKey:"kind",header:"Resource type"},{accessorKey:"nativeId",header:"Provider ID"},{accessorKey:"region",header:"Project region"},
     {id:"conflict",header:"Ownership",cell:({row})=>row.original.conflicting?"Also referenced by another project":"Referenced by this project"},
     {id:"inventory",header:"Inventory",cell:({row})=>row.original.resourceId?<Link to={`/app/resources?org=${org.id}&resource=${row.original.resourceId}`}>Open resource</Link>:"Not currently in inventory"}
    ]}/>{ownership.hasNextPage&&<button className="secondary" disabled={ownership.isFetchingNextPage} onClick={()=>void ownership.fetchNextPage()}>Load more protected references</button>}</>}
   </section>}
   <p className="notice">Run access and state recovery are not exposed here. A lock survives an interrupted writer until safely resolved.</p>
   <ErrorNote error={validate.error}/>
   {project&&org.permissions.includes("templates.publish")&&runnerConfigured&&eligible.some(v=>v.id===project.versionId)&&<button className="primary" disabled={validate.isPending} onClick={()=>validate.mutate()}>Validate project inputs</button>}
   <p>Validation checks declared input constraints and source syntax. Terraform planning is required to check provider-specific values.</p>
   <ErrorNote error={history.error}/>
   <DataTable label="State versions" data={project?(history.data?.pages.flatMap(p=>p.states)??[]):[]} rowId={r=>r.id} columns={[
    {id:"serial",header:"Serial",cell:({row})=>String(row.original.serial)},{accessorKey:"sha256",header:"SHA-256"},{accessorKey:"stateBytes",header:"Bytes"},{accessorKey:"createdAt",header:"Saved"}
   ]}/>
   {project&&history.hasNextPage&&<button className="secondary" disabled={history.isFetchingNextPage} onClick={()=>void history.fetchNextPage()}>Load older state versions</button>}
  </Modal>
 </>;
}
