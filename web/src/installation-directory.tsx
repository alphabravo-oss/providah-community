import {z} from "zod";
import type {InstallationUser,InstallationOrganization} from "./gen/providah/v1/console_pb";
import {useState} from "react";
import {Link} from "react-router";
import {useMutation,useQuery} from "@tanstack/react-query";
import {api,queries} from "./api";
import {ActionForm,Modal,DataTable,ErrorNote} from "./ui";
export function InstallationDirectory({email,mfaEnabled}:{email:string;mfaEnabled:boolean}){
 const [editing,setEditing]=useState<InstallationUser|InstallationOrganization>();
 const save=useMutation({gcTime:0,mutationFn:(v:Record<string,string>)=>{const r={id:editing!.id,active:v.state==="active",expectedRevision:editing!.revision,password:v.password,code:v.code??""};return "email" in editing! ? api.setInstallationUser({...r,globalAdmin:v.access==="global"}):api.setInstallationOrganization(r);},onSuccess:async()=>{setEditing(undefined);await queries.invalidateQueries({queryKey:["installation-directory"]});await queries.invalidateQueries({queryKey:["session"]});}});
 const [kind,setKind]=useState("organizations"),[search,setSearch]=useState(""),[state,setState]=useState(""),[page,setPage]=useState("");
 const q=useQuery({queryKey:["installation-directory",kind,search,state,page],queryFn:()=>api.listInstallationDirectory({kind,search,state,pageToken:page})});
 return <><section className="panel"><div className="panel-heading"><h2>Installation directory</h2><button onClick={()=>void q.refetch()}>Refresh directory</button></div>
 <div className="filters"><select aria-label="Directory" value={kind} onChange={e=>{setKind(e.target.value);setPage("");}}><option value="organizations">Organizations</option><option value="users">Users</option></select>
 <input aria-label="Search directory" placeholder={kind==="users"?"Search email…":"Search organizations…"} value={search} onChange={e=>{setSearch(e.target.value);setPage("");}}/>
 <select aria-label="Directory status" value={state} onChange={e=>{setState(e.target.value);setPage("");}}><option value="">All statuses</option><option value="active">Active</option><option value="disabled">Disabled</option></select></div>
 <ErrorNote error={q.error}/>{q.isPending?<p>Loading directory…</p>:q.isError?null:kind==="users"?<DataTable label="Installation users" data={q.data?.users??[]} rowId={u=>u.id} columns={[
 {accessorKey:"email",header:"Email"},{id:"status",header:"Status",cell:({row:{original:u}})=>u.active?"Active":"Disabled"},
 {id:"authority",header:"Installation access",cell:({row:{original:u}})=>u.globalAdmin?"Global administrator":"Organization grants"},
 {id:"mfa",header:"MFA",cell:({row:{original:u}})=>u.mfaEnabled?"Enabled":"Disabled"},
 {id:"actions",header:"Actions",cell:({row:{original:u}})=>u.email===email?"Your account":<button onClick={()=>{save.reset();setEditing(u);}}>Manage access</button>}
 ]}/>:<DataTable label="Organizations" data={q.data?.organizations??[]} rowId={o=>o.id} columns={[
 {id:"name",header:"Organization",cell:({row:{original:o}})=>o.active?<Link to={`/admin/access?org=${o.id}`}>{o.name}</Link>:o.name},
 {id:"status",header:"Status",cell:({row:{original:o}})=>o.active?"Active":"Disabled"},
 {id:"actions",header:"Actions",cell:({row:{original:o}})=><button onClick={()=>{save.reset();setEditing(o);}}>Manage organization</button>}
 ]}/>}
 <div className="row-actions"><button disabled={!page||q.isPending} onClick={()=>setPage("")}>First page</button><button disabled={!q.data?.nextPageToken||q.isPending||q.isError} onClick={()=>setPage(q.data!.nextPageToken)}>Next page</button></div>
 </section>
 <Modal open={!!editing} onOpenChange={open=>{if(!open)setEditing(undefined);}} title="Manage installation access" description={editing&&"email" in editing?"Change account availability and installation authority. Saving signs this user out of every session. Organization role grants are retained.":"Disabling blocks organization access and new cloud work, and pauses queued notifications. Data and cloud resources remain. Already-dispatched work may finish; cloud resources keep running and may incur charges. Enabling restores access and scheduled activity."}>
 {editing&&<><p>{"email" in editing?editing.email:editing.name}</p><ActionForm key={editing.id} fields={[
 {name:"state",label:"Account or organization status",type:"select",defaultValue:editing.active?"active":"disabled",options:[{value:"active",label:"Active"},{value:"disabled",label:"Disabled"}]},
 ...("email" in editing?[{name:"access",label:"Installation access",type:"select" as const,defaultValue:editing.globalAdmin?"global":"scoped",options:[{value:"scoped",label:"Organization grants only"},{value:"global",label:"Global administrator"}]}]:[]),
 {name:"password",label:"Current password",type:"password",autoComplete:"current-password",schema:z.string().min(12).max(72)},
 ...(mfaEnabled?[{name:"code",label:"Fresh authenticator code",autoComplete:"one-time-code",schema:z.string().regex(/^\d{6}$/)}]:[])
 ]} submitLabel={editing&&"email" in editing?"Save access and sign user out":"Save organization status"} onSubmit={v=>save.mutateAsync(v)} pending={save.isPending} error={save.error}/></>}
 </Modal></>;
}
