import {AutomationVersions} from "./automation-versions";
import {Link,useSearchParams} from "react-router";
import {useState} from "react";
import {useMutation,useQuery} from "@tanstack/react-query";
import {z} from "zod";
import {api,queries} from "./api";
import {ActionForm,DataTable,ErrorNote,Modal,PageHeader} from "./ui";
import type {Organization} from "./gen/providah/v1/console_pb";

export function AutomationSources({org}:{org:Organization}) {
 const [open,setOpen]=useState(false),[file,setFile]=useState<File>();
 const [params,setParams]=useSearchParams();
 const selectedId=["validation","version","project","template"].some(k=>params.has(k))?"":params.get("source")??"";
 const detail=useQuery({queryKey:["automation-source",org.id,selectedId],queryFn:()=>api.getAutomationSource({organizationId:org.id,id:selectedId}),enabled:!!selectedId});
 const selected=detail.error?undefined:detail.data;
 const close=()=>setParams(previous=>{const next=new URLSearchParams(previous);next.delete("validation");next.delete("source");return next;});
 const catalog=useQuery({queryKey:["automation-sources",org.id],queryFn:()=>api.listAutomationSources({organizationId:org.id})});
 const upload=useMutation({gcTime:0,retry:false,mutationFn:async(v:Record<string,string>)=>{
  if(!file || file.size>4*1024*1024)throw new Error("Choose a ZIP file up to 4 MiB.");
  await api.importAutomationSource({organizationId:org.id,name:v.name,runtime:v.runtime,entrypoint:v.entrypoint,archive:new Uint8Array(await file.arrayBuffer())});
 },onSuccess:()=>{setOpen(false);setFile(undefined);void queries.invalidateQueries({queryKey:["automation-sources",org.id]});}});
 return <>
  <PageHeader eyebrow="AUTOMATION" title="Imported sources" description="Immutable Terraform, OpenTofu, and Ansible bundles. Importing preserves source. Publish a version to request offline validation.">{org.permissions.includes("templates.publish")&&<button className="primary" onClick={()=>{setFile(undefined);upload.reset();setOpen(true);}}>Import source bundle</button>}</PageHeader>
  <ErrorNote error={catalog.error}/>
  <DataTable label="Imported sources" data={catalog.isError?[]:catalog.data?.sources??[]} rowId={r=>r.id} columns={[
   {accessorKey:"name",header:"Source"},{accessorKey:"runtime",header:"Runtime"},{accessorKey:"entrypoint",header:"Entrypoint"},
   {id:"actions",header:"Details",cell:({row})=><Link className="secondary" to={`/app/templates?org=${org.id}&source=${row.original.id}`}>Inspect {row.original.name}</Link>}
  ]}/>
  <Modal open={open} onOpenChange={v=>{setOpen(v);if(!v)setFile(undefined);}} title="Import automation source" description="Upload a ZIP up to 4 MiB, with at most 256 files and 16 MiB expanded. Keep secrets in credential references, never in source.">
   <p className="notice">State, environment files, private keys, links, and unsafe paths are rejected. This does not scan arbitrary source for secrets or approve it to run.</p>
   <label className="field">Source ZIP<input type="file" accept=".zip,application/zip" disabled={upload.isPending} onChange={e=>setFile(e.target.files?.[0])}/></label>
   <ActionForm fields={[
    {name:"name",label:"Source name",schema:z.string().trim().min(1).max(80)},
    {name:"runtime",label:"Runtime",type:"select",options:[{value:"opentofu",label:"OpenTofu"},{value:"terraform",label:"Terraform"},{value:"ansible",label:"Ansible"}],schema:z.enum(["opentofu","terraform","ansible"])},
    {name:"entrypoint",label:"Entrypoint path inside ZIP",schema:z.string().min(1).max(240)}
   ]} onSubmit={v=>upload.mutateAsync(v)} pending={upload.isPending} error={upload.error} submitLabel="Import immutable source"/>
  </Modal>
  <Modal open={!!selectedId} onOpenChange={v=>{if(!v)close();}} title={selected?.name??"Source details"} description="The original archive is encrypted and preserved. Source contents are not returned to the browser.">
   <nav className="breadcrumbs" aria-label="Source breadcrumb"><Link to={"/app/templates?org="+org.id}>Templates</Link><span aria-hidden="true">›</span><span aria-current="page">{selected?.name??"Source"}</span></nav>
   <ErrorNote error={detail.error}/>{detail.isPending&&<p role="status">Loading source…</p>}
   {selected&&<><dl className="resource-details"><div><dt>SHA-256</dt><dd>{selected.sha256}</dd></div><div><dt>Runtime</dt><dd>{selected.runtime}</dd></div><div><dt>Entrypoint</dt><dd>{selected.entrypoint}</dd></div><div><dt>Archive / expanded bytes</dt><dd>{selected.archiveBytes} / {selected.expandedBytes.toString()}</dd></div></dl><h3>Declared inputs</h3><DataTable label="Source inputs" data={selected.inputs} rowId={r=>r.name} columns={[{accessorKey:"label",header:"Input"},{accessorKey:"type",header:"Type"},{id:"constraints",header:"Allowed values",cell:({row})=>row.original.type==="string"?row.original.choices.join(", "):row.original.type==="boolean"?"true / false":`${row.original.min}–${row.original.max}`}]} /><h3>Files</h3><ul>{selected.files.map(f=><li key={f}>{f}</li>)}</ul></>}
  </Modal>
  <AutomationVersions org={org} sources={catalog.isError?[]:catalog.data?.sources??[]}/>
 </>;
}
