import { useState } from "react";
import { useNavigate } from "react-router";
import { useInfiniteQuery, useMutation, useQuery } from "@tanstack/react-query";
import { z } from "zod";
import { api, queries } from "./api";
import { ActionForm, ErrorNote, Modal, type FormField } from "./ui";
import type { Connection, Organization } from "./gen/providah/v1/console_pb";

export function useCatalog(org: string, connection: string, kind: string, enabled = true) {
  return useInfiniteQuery({
    enabled,
    queryKey: ["resources", org, "creation", connection, kind], initialPageParam: "",
    queryFn: ({ pageParam }) => api.listResources({ includeCatalog: true, organizationId: org, connectionId: connection, kind, pageSize: 200, pageToken: pageParam }),
    getNextPageParam: (last) => last.nextPageToken || undefined,
  });
}
export function CreationFields({ org, connection, submit, template = false }: { org: Organization; connection: Connection; template?: boolean; submit: (values: Record<string,string>) => void }) {
  const images = useCatalog(org.id, connection.id, "compute.image"), sizes = useCatalog(org.id, connection.id, "compute.type"), keys = useCatalog(org.id, connection.id, "access.ssh_key");
  const networks=useCatalog(org.id,connection.id,"network.network",connection.provider!=="aws");
  const catalogs = [["images", images], ["sizes", sizes], ["SSH keys", keys]] as const;
  const options = (query: typeof images) => [...new Map(query.data?.pages.flatMap(p => p.resources).filter(r => r.status !== "unavailable" && (!connection.region || r.region === "global" || r.region === connection.region)).map(r => [r.nativeId, { value:r.kind==="compute.type" && connection.provider==="hetzner" ? r.name : r.nativeId, label:`${r.name} · ${r.nativeId}${r.size ? ` · ${r.size}` : ""}` }]) ?? []).values()];
  const fields: FormField[] = [
    ...(!template ? [{ name:"name", label:"Server name", schema:z.string().regex(/^[a-z][a-z0-9-]{0,61}[a-z0-9]$/, "Use 2–63 lowercase letters, digits, and hyphens.") }] : []),
    { name:"region", label:"Region / location", defaultValue:connection.region, schema:z.string().regex(/^[a-z0-9-]{1,64}$/), description:connection.region ? "Must match this connection’s region." : "Choose a location supported by the selected image and size." },
    { name:"image", label:"Image", type:"select", options:options(images), schema:z.string().min(1) },
    { name:"size", label:"Server size", type:"select", options:options(sizes), schema:z.string().min(1) },
    { name:"sshKey", label:"SSH key", type:"select", options:options(keys), schema:z.string().min(1), description:"Uses an existing public key. Private keys are never requested." },
  ];
  if (connection.provider === "aws") fields.push({name:"subnet", label:"Subnet ID",schema:z.string().regex(/^subnet-[a-f0-9]{8,17}$/)}, {name:"securityGroup",label:"Security group ID",schema:z.string().regex(/^sg-[a-f0-9]{8,17}$/)});
  if(connection.provider!=="aws")fields.push({name:"network",label:connection.provider==="digitalocean"?"VPC":"Private network",type:"select",defaultValue:"",schema:z.string(),options:[{value:"",label:connection.provider==="digitalocean"?"Provider default VPC":"No private network"},...(networks.isError?[]:options(networks))],description:"Optional. The selected network is checked again before creation. Public networking remains enabled."});
  return <>
    {connection.provider!=="aws"&&<><ErrorNote error={networks.error}/>{networks.hasNextPage&&<button disabled={networks.isFetchingNextPage} onClick={()=>void networks.fetchNextPage()}>Load more networks</button>}</>}
    <p className="notice">Catalogs reflect the last refresh. The worker checks current eligibility before creation; provider capacity is not guaranteed.</p>
    {catalogs.map(([label,q]) => <div key={label}><ErrorNote error={q.error}/>{q.hasNextPage && <button className="secondary" disabled={q.isFetchingNextPage} onClick={()=>void q.fetchNextPage()}>Load more {label}</button>}</div>)}
    {catalogs.some(([,q])=>q.isPending) ? <p>Loading provisioning choices…</p> : <ActionForm fields={fields} onSubmit={submit} submitLabel="Review configuration"/>}
    {!catalogs.some(([,q])=>q.isPending) && catalogs.some(([,q])=>!options(q).length) && <p className="notice">Refresh this connection to discover images, server sizes, and SSH keys before continuing.</p>}
  </>;
}
export function CreateResourceButton({ org, sshKey = false }: { org: Organization; sshKey?: boolean }) {
  const title = sshKey ? "Import SSH key" : "Create server";
  const [open,setOpen]=useState(false), [connection,setConnection]=useState<Connection>(), [draft,setDraft]=useState<Record<string,string>>(), [requestKey,setRequestKey]=useState("");
  const navigate=useNavigate();
  const connections=useQuery({queryKey:["connections",org.id],queryFn:()=>api.listConnections({organizationId:org.id}),enabled:open});
  const create=useMutation({retry:false, mutationFn:(v:Record<string,string>)=>sshKey ? api.requestSSHKeyCreation({organizationId:org.id,connectionId:connection!.id,region:draft!.region,creation:{name:draft!.name,publicKey:draft!.publicKey},reason:v.reason,idempotencyKey:requestKey}) : api.requestServerCreation({organizationId:org.id,connectionId:connection!.id,region:draft!.region,creation:{name:draft!.name,image:draft!.image,size:draft!.size,sshKey:draft!.sshKey,subnet:draft!.subnet??"",securityGroup:draft!.securityGroup??"",network:draft!.network??""},reason:v.reason,idempotencyKey:requestKey}),onSuccess:()=>{void queries.invalidateQueries({queryKey:["operations",org.id]});setOpen(false);navigate("/app/operations?org="+org.id);}});
  if (!org.permissions.includes("operations.create") || !org.permissions.includes("operations.request") || !org.permissions.includes("connections.read")) return null;
  return <><button className="primary" onClick={()=>{setConnection(undefined);setDraft(undefined);create.reset();setOpen(true);}}>{title}</button>
    <Modal open={open} onOpenChange={setOpen} title={title} description="Review the exact configuration before requesting independent approval.">
      <ErrorNote error={connections.error}/>
      {!connection ? <ActionForm key="connection" fields={[{name:"connection",label:"Cloud connection",type:"select",options:connections.data?.connections.filter(c=>c.enabled).map(c=>({value:c.id,label:`${c.name} · ${c.provider}`}))??[],schema:z.string().min(1)}]} onSubmit={v=>setConnection(connections.data?.connections.find(c=>c.id===v.connection))} submitLabel="Choose configuration"/> : !draft ? sshKey ? <ActionForm key="key" fields={[
        {name:"name",label:"Key name",schema:z.string().regex(/^[a-z][a-z0-9-]{0,61}[a-z0-9]$/, "Use 2–63 lowercase letters, digits, and hyphens.")},
        ...(connection.provider==="aws" ? [{name:"region",label:"AWS region",defaultValue:connection.region,schema:z.string().regex(/^[a-z0-9-]{1,64}$/).refine(v=>v!=="global","Choose an EC2 region.")}] : []),
        {name:"publicKey",label:"OpenSSH public key",type:"textarea",schema:z.string().max(16384).refine(v=>/^(ssh-ed25519|ssh-rsa) [A-Za-z0-9+/]+={0,2}(?:[ \t][^\r\n]*)?$/.test(v.trim()),"Paste one Ed25519 or RSA public key line."),description:"Public key only. RSA requires at least 2048 bits. Private keys and authorized_keys options are rejected."},
      ]} onSubmit={v=>{setDraft({...v,region:connection.provider==="aws"?v.region:"global"});setRequestKey(crypto.randomUUID());}} submitLabel="Review configuration"/> : <CreationFields org={org} connection={connection} submit={v=>{setDraft(v);setRequestKey(crypto.randomUUID());}}/> : <>
        <dl className="resource-details">{Object.entries({Connection:connection.name,Name:draft.name,Region:draft.region,Image:draft.image,Size:draft.size,"SSH key":draft.sshKey,Subnet:draft.subnet,"Private network":draft.network,"Security group":draft.securityGroup}).filter(([,v])=>v).map(([k,v])=><div key={k}><dt>{k}</dt><dd>{v}</dd></div>)}</dl>
        {sshKey ? <><h3>Reviewed public key</h3><pre className="notice multiline">{draft.publicKey}</pre><p className="notice">The imported name includes this operation’s unique ID. Adds an account public key; existing servers’ authorized_keys files are unchanged. An import response requires read-only confirmation. Resolve an uncertain result before another request.</p></> : <p className="notice">Creates one billable server and its boot disk. No price quote is available. {connection.provider==="aws" ? "AWS uses the selected security group, no public IPv4, encrypted root storage, and required IMDSv2." : "The provider creates public networking; review access rules and provider charges."} No boot scripts or automatic rollback are included. A lost response requires reconciliation before another request.</p>}
        <ActionForm fields={[{name:"reason",label:sshKey?"Import reason":"Creation reason",type:"textarea",schema:z.string().min(3).max(500)},{name:"confirm",label:sshKey?"Confirm the reviewed key name":"Confirm the reviewed server name",schema:z.literal(draft.name)}]} onSubmit={v=>create.mutateAsync(v)} submitLabel="Request independent approval" pending={create.isPending} error={create.error}/>
        <button className="secondary" disabled={create.isPending} onClick={()=>{create.reset();setDraft(undefined);}}>Edit configuration</button>
      </>}
    </Modal></>;
}
