import {ManagedProjects} from "./managed-projects";
import {Link,useSearchParams} from "react-router";
import {useEffect,useState} from "react";
import {useMutation,useQuery} from "@tanstack/react-query";
import {z} from "zod";
import {api,queries} from "./api";
import {ActionForm,DataTable,ErrorNote,Modal,PageHeader} from "./ui";
import type {AutomationSource,AutomationVersion,Organization} from "./gen/providah/v1/console_pb";

export function AutomationVersions({org,sources}:{org:Organization;sources:AutomationSource[]}){
 const [baseVersions,setBaseVersions]=useState<AutomationVersion[]>([]);
 const [open,setOpen]=useState(false),[source,setSource]=useState<AutomationSource>(),[action,setAction]=useState("");
 const [params,setParams]=useSearchParams();
 const selectedId=params.has("validation")?"":params.get("version")??"";
 const setSelected=(value:AutomationVersion|undefined)=>setParams(previous=>{const next=new URLSearchParams(previous);next.delete("validation");next.delete("project");next.delete("template");next.delete("source");if(value)next.set("version",value.id);else next.delete("version");return next;});
 const detail=useQuery({queryKey:["automation-version",org.id,selectedId],queryFn:()=>api.getAutomationVersion({organizationId:org.id,id:selectedId}),enabled:!!selectedId});
 const selected=detail.error?undefined:detail.data;
 useEffect(()=>setAction(""),[selectedId]);
 const catalog=useQuery({queryKey:["automation-versions",org.id],queryFn:()=>api.listAutomationVersions({organizationId:org.id})});
 const connections=useQuery({queryKey:["connections",org.id],queryFn:()=>api.listConnections({organizationId:org.id}),enabled:open});
 const refresh=()=>queries.invalidateQueries();
 const publish=useMutation({retry:false,mutationFn:(v:Record<string,string>)=>api.publishAutomationVersion({organizationId:org.id,name:v.name.trim(),sourceId:source!.id,runtimeImage:v.image,connectionId:v.connection,region:v.region,reviewNote:v.note,expectedVersion:Math.max(0,...baseVersions.filter(r=>r.name===v.name.trim()).map(r=>r.version))}),onSuccess:r=>{setOpen(false);setSource(undefined);setSelected(r);setAction("");void refresh();}});
 const update=useMutation({mutationFn:()=>api.setAutomationVersionStatus({organizationId:org.id,id:selected!.id,status:action}),onSuccess:r=>{setSelected(r);setAction("");void refresh();}});
 const images=catalog.data?.runtimes.filter(r=>r.runtime===source?.runtime)??[];
 const validationId=params.get("validation")??"";
 const validation=useQuery({queryKey:["automation-validation",org.id,validationId],queryFn:()=>api.getAutomationValidation({organizationId:org.id,id:validationId}),enabled:!!validationId});
 const result=validation.isError?undefined:validation.data;
 const historyStatus=params.get("validation_status")??"",historyPage=params.get("validation_page")??"";
 const setHistory=(status:string,page:string)=>setParams(previous=>{const next=new URLSearchParams(previous);if(status)next.set("validation_status",status);else next.delete("validation_status");if(page)next.set("validation_page",page);else next.delete("validation_page");return next;});
 const checks=useQuery({queryKey:["automation-validations",org.id,historyStatus,historyPage],queryFn:()=>api.listAutomationValidations({organizationId:org.id,status:historyStatus,pageToken:historyPage})});
 const validate=useMutation({retry:false,mutationFn:()=>api.requestAutomationValidation({organizationId:org.id,versionId:selected!.id}),onSuccess:()=>{setSelected(undefined);void queries.invalidateQueries({queryKey:["automation-validations",org.id]});}});
 const cancel=useMutation({retry:false,mutationFn:(id:string)=>api.cancelAutomationValidation({organizationId:org.id,id}),onSuccess:()=>queries.invalidateQueries()});
 const canPublish=org.permissions.includes("templates.publish");
 return <>
  <PageHeader eyebrow="AUTOMATION" title="Published automation" description="Reviewed source, runtime image, and connection bindings. Isolated validation is available; plan and apply are not yet supported.">{canPublish&&<button className="primary" onClick={()=>{setSource(undefined);setBaseVersions(catalog.data?.versions??[]);publish.reset();setOpen(true);}}>Publish automation version</button>}</PageHeader>
  <ErrorNote error={catalog.error}/>
  <DataTable label="Automation versions" data={catalog.data?.versions??[]} rowId={r=>r.id} columns={[
   {accessorKey:"name",header:"Automation"},{accessorKey:"version",header:"Version"},{accessorKey:"runtime",header:"Runtime"},{accessorKey:"status",header:"Status"},
   {id:"availability",header:"Image policy",cell:({row})=>row.original.runtimeAvailable?"Allowed":"Unavailable"},
   {id:"actions",header:"Details",cell:({row})=><button className="secondary" onClick={()=>{setSelected(row.original);setAction("");update.reset();}}>Inspect {row.original.name} v{row.original.version}</button>}
  ]}/>
  <Modal open={open} onOpenChange={setOpen} title="Publish automation version" description="Publication records your source and dependency review. It does not approve any cloud action.">
   <ErrorNote error={connections.error}/><ErrorNote error={catalog.error}/>
   {!sources.length?<p className="notice">Import a source bundle above before publishing a version.</p>:!source?<ActionForm fields={[{name:"source",label:"Imported source",type:"select",options:sources.map(s=>({value:s.id,label:`${s.name} · ${s.runtime} · ${s.sha256.slice(0,12)}`})),schema:z.string().min(1)}]} onSubmit={v=>setSource(sources.find(s=>s.id===v.source))} submitLabel="Review source binding"/>:<>
    <dl className="resource-details"><div><dt>Source hash</dt><dd>{source.sha256}</dd></div><div><dt>Entrypoint</dt><dd>{source.entrypoint}</dd></div></dl>
    {images.length===0?<p className="notice">No allowed {source.runtime} image is configured. Ask your deployment administrator to configure the automation runtime catalog.</p>:<ActionForm fields={[
     {name:"name",label:"Automation name",schema:z.string().trim().min(1).max(80)},
     {name:"image",label:"Allowed runtime image",type:"select",options:images.map(r=>({value:r.image,label:`${r.runtime} ${r.version} · ${r.image} · ${r.dependencyHosts.length ? "downloads: "+r.dependencyHosts.join(", ") : "no network"}`})),schema:z.string().min(1)},
     {name:"connection",label:"Bound cloud connection",type:"select",options:(connections.data?.connections??[]).filter(c=>c.enabled).map(c=>({value:c.id,label:c.name})),schema:z.string().min(1)},
     {name:"region",label:"Bound region",schema:z.string().regex(/^[a-z0-9-]{1,64}$/)},
     {name:"note",label:"Source and dependency review",type:"textarea",description:"Describe the reviewed source and pinned dependencies. Do not include secrets.",schema:z.string().trim().min(3).max(1000)}
    ]} onSubmit={v=>publish.mutateAsync(v)} pending={publish.isPending} error={publish.error} submitLabel="Publish reviewed version"/>}
   </>}
  </Modal>
  <Modal open={!!selectedId} onOpenChange={v=>{if(!v)setSelected(undefined);}} title={selected?`${selected.name} · version ${selected.version}`:"Automation version"} description="Bindings are immutable. Changes require a new version and fresh execution approval once running is supported.">
   <nav className="breadcrumbs" aria-label="Publication breadcrumb"><Link to={"/app/templates?org="+org.id}>Templates</Link><span aria-hidden="true">›</span><span aria-current="page">{selected?selected.name+" v"+selected.version:"Automation version"}</span></nav>
   <ErrorNote error={detail.error}/>{detail.isPending&&<p role="status">Loading version…</p>}
   {selected&&<>
    <dl className="resource-details">{Object.entries({Source:selected.sourceId,Runtime:selected.runtime,Image:selected.runtimeImage,"Runtime version":selected.runtimeVersion,Connection:selected.connectionId,"Credential revision":selected.connectionRevision.toString(),Region:selected.region,Status:selected.status,"Dependency hosts":selected.dependencyHosts.join(", ")||"None",Review:selected.reviewNote}).map(([k,v])=><div key={k}><dt>{k}</dt><dd>{v}</dd></div>)}</dl>
    <p className="notice">Validation runs without cloud credentials. Only the listed dependency hosts can be reached through the restricted proxy; other dependencies must be packaged. A successful check is not a plan or approval to apply changes.</p>
    <ErrorNote error={validate.error}/>
    {canPublish&&selected.status==="published"&&selected.runtimeAvailable&&checks.data?.runnerConfigured&&<button className="primary" disabled={validate.isPending} onClick={()=>validate.mutate()}>Validate source</button>}
    {!selected.runtimeAvailable&&<p className="notice">This runtime image is no longer allowed by deployment configuration.</p>}
    {action?<ActionForm key={selected.id+action} fields={[{name:"confirm",label:"Confirm automation name",schema:z.literal(selected.name)}]} onSubmit={()=>update.mutateAsync()} pending={update.isPending} error={update.error} submitLabel={action==="retired"?"Retire version":"Revoke version"}/>:canPublish&&<>
     {selected.status==="published"&&<button className="secondary" onClick={()=>setAction("retired")}>Retire version</button>}
     {selected.status!=="revoked"&&<button className="secondary" onClick={()=>setAction("revoked")}>Revoke version</button>}
    </>}
   </>}
  </Modal>
  <ManagedProjects org={org} sources={sources} versions={catalog.data?.versions??[]} runnerConfigured={!!checks.data?.runnerConfigured}/>
  <PageHeader eyebrow="AUTOMATION" title="Validation history" description="Browse isolated checks by status. Tool output stays private; results are advisory."/>
  <ErrorNote error={checks.error||cancel.error}/>
  <p>Cancel stops queued checks and requests termination of running validation. Cleanup may take a few seconds.</p>
  <div className="filters"><label>Validation status<select aria-label="Validation status" value={historyStatus} onChange={e=>setHistory(e.target.value,"")}>{["","queued","running","succeeded","failed","canceled"].map(value=><option key={value} value={value}>{value||"All statuses"}</option>)}</select></label></div>
  <DataTable label="Automation validations" data={checks.isError?[]:checks.data?.validations??[]} rowId={r=>r.id} columns={[
   {id:"version",header:"Version",cell:({row})=>{const v=catalog.data?.versions.find(v=>v.id===row.original.versionId);return v?`${v.name} v${v.version}`:row.original.versionId;}},
   {id:"project",header:"Scope",cell:({row})=>row.original.projectId?"Project inputs":"Source only"},{accessorKey:"status",header:"Status"},{accessorKey:"detail",header:"Result"},{accessorKey:"createdAt",header:"Requested"},
   {id:"record",header:"Details",cell:({row})=><Link to={`/app/templates?org=${org.id}&validation=${row.original.id}`}>View validation</Link>},
   {id:"actions",header:"Actions",cell:({row})=>canPublish&&["queued","running"].includes(row.original.status)?<button disabled={cancel.isPending} onClick={()=>cancel.mutate(row.original.id)}>Cancel validation</button>:null}
  ]}/>
  <div className="pagination">{historyPage&&<button onClick={()=>setHistory(historyStatus,"")}>Most recent validations</button>}{!checks.isError&&checks.data?.nextPageToken&&<button onClick={()=>setHistory(historyStatus,checks.data!.nextPageToken)}>Older validations</button>}</div>
  <Modal open={!!validationId} onOpenChange={open=>{if(!open)setParams(previous=>{const next=new URLSearchParams(previous);next.delete("validation");return next;});}} title="Automation validation" description="An isolated syntax check. Successful validation is not a plan or approval to apply changes.">
   <nav className="breadcrumbs" aria-label="Validation breadcrumb"><Link to={`/app/templates?org=${org.id}`}>Templates</Link><span aria-hidden="true">›</span><span aria-current="page">Validation</span></nav>
   <ErrorNote error={validation.error}/>{validation.isPending&&<p role="status">Loading validation…</p>}
   {result&&<>
    <dl className="resource-details">{Object.entries({Status:result.status,Result:result.detail,Requested:result.createdAt,Scope:result.projectId?"Project inputs":"Source only"}).map(([k,v])=><div key={k}><dt>{k}</dt><dd>{v}</dd></div>)}</dl>
    <p><Link to={`/app/templates?org=${org.id}&version=${result.versionId}`}>View published version</Link></p>
    {result.projectId&&<p><Link to={`/app/templates?org=${org.id}&project=${result.projectId}`}>View project</Link></p>}
    <ErrorNote error={cancel.error}/>{canPublish&&["queued","running"].includes(result.status)&&<button disabled={cancel.isPending} onClick={()=>cancel.mutate(result.id)}>Cancel validation</button>}
   </>}
  </Modal>
 </>;
}
