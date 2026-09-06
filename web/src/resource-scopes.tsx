import {z} from "zod";
import {useState} from "react";
import {useQuery} from "@tanstack/react-query";
import {api} from "./api";
import {ActionForm,ErrorNote,Modal} from "./ui";
export const emptyScope={connectionId:"",region:"",status:""};
export function ResourceScopes({org,value,onChange}:{org:string;value:typeof emptyScope;onChange:(v:typeof emptyScope)=>void}){
 const [open,setOpen]=useState(false);
 const q=useQuery({queryKey:["resource-scopes",org],queryFn:()=>api.listResourceScopes({organizationId:org}),enabled:open});
 const options=(kind:string,current:string)=>{const items=q.data?.scopes.filter(s=>s.kind===kind).map(s=>({value:s.value,label:s.label}))??[];if(current&&!items.some(i=>i.value===current))items.push({value:current,label:kind==="connection"?"Saved account selection":current});return [{value:"",label:"All"},...items];};
 return <><button className="secondary" onClick={()=>setOpen(true)}>Scope resources</button>
 {(value.connectionId||value.region||value.status)&&<p className="notice">Resource scope active{value.region?` · ${value.region}`:""}{value.status?` · ${value.status}`:""} <button onClick={()=>onChange(emptyScope)}>Clear resource scope</button></p>}
 <Modal open={open} onOpenChange={setOpen} title="Scope resources" description="Choose an account connection, region or status from observed inventory. These filters combine with provider, type, search and tags.">
 <ErrorNote error={q.error}/>{q.isPending?<p>Loading scope choices…</p>:!q.isError&&<ActionForm fields={[
 {name:"connectionId",label:"Account connection",type:"select",defaultValue:value.connectionId,schema:z.string().max(64),options:options("connection",value.connectionId)},
 {name:"region",label:"Resource region",type:"select",defaultValue:value.region,schema:z.string().max(80),options:options("region",value.region)},
 {name:"status",label:"Resource status",type:"select",defaultValue:value.status,schema:z.string().max(80),options:options("status",value.status)}
 ]} submitLabel="Apply resource scope" onSubmit={v=>{onChange({connectionId:v.connectionId,region:v.region,status:v.status});setOpen(false);}}/>}
 </Modal></>;
}
