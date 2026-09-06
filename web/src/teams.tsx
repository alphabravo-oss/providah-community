import {useEffect,useState} from "react";
import {useSearchParams} from "react-router";
import {useMutation,useQuery} from "@tanstack/react-query";
import {z} from "zod";
import {api,queries} from "./api";
import {ActionForm,DataTable,ErrorNote,Modal} from "./ui";
import type {Organization,Role,Member} from "./gen/providah/v1/console_pb";
export function Teams({org,roles,members}:{org:Organization;roles:Role[];members:Member[]}){
 const [params,setParams]=useSearchParams(),id=params.get("team")??"";
 const q=useQuery({queryKey:["teams",org.id],queryFn:()=>api.listTeams({organizationId:org.id})});
 const team=q.data?.teams.find(t=>t.id===id),manage=org.permissions.includes("members.manage");
 const assignable=roles.filter(r=>r.permissions.every(p=>org.permissions.includes(p)));
 const [ids,setIds]=useState<string[]>([]),[deleting,setDeleting]=useState(false);
 useEffect(()=>{setIds(team?.userIds??[]);setDeleting(false);},[id,team?.revision]);
 const select=(value:string)=>setParams(p=>{const next=new URLSearchParams(p);if(value)next.set("team",value);else next.delete("team");return next;});
 const remove=useMutation({mutationFn:()=>api.deleteTeam({organizationId:org.id,id:team!.id,expectedRevision:team!.revision}),onSuccess:async()=>{select("");await queries.invalidateQueries();}});
 const save=useMutation({mutationFn:(v:Record<string,string>)=>api.saveTeam({organizationId:org.id,id:team?.id??"",name:v.name,roleId:v.role,active:v.active==="true",expectedRevision:team?.revision??0n,userIds:ids}),onSuccess:async()=>{select("");await queries.invalidateQueries();}});
 return <><section className="panel"><div className="panel-heading"><h2>Teams</h2>{manage&&<button disabled={!assignable.length} onClick={()=>{save.reset();select("new");}}>Create team</button>}</div>
 <ErrorNote error={q.error}/>{q.isPending?<p>Loading teams…</p>:q.isError?null:<DataTable label="Teams" data={q.data?.teams??[]} rowId={t=>t.id} columns={[
 {id:"name",header:"Team",cell:({row:{original:t}})=><button onClick={()=>{save.reset();select(t.id);}}>{t.name}</button>},
 {id:"role",header:"Added role",cell:({row:{original:t}})=>roles.find(r=>r.id===t.roleId)?.name??t.roleId},
 {id:"members",header:"Members",cell:({row:{original:t}})=>t.userIds.length},
 {id:"state",header:"State",cell:({row:{original:t}})=>t.active?"Enabled":"Disabled"}
 ]}/>}</section>
 <Modal wide open={!!id} onOpenChange={open=>{if(!open)select("");}} title={deleting?"Delete team":team?.name??(id==="new"?"Create team":"Team unavailable")} description="Team roles add to direct member roles. Disabled teams grant no access. Members must already belong to this organization; disabling their membership or account blocks team grants too.">
 <ErrorNote error={q.error}/>{q.isPending?<p>Loading team…</p>:q.isError?null:id!=="new"&&!team?<p role="alert">This team is unavailable in this organization.</p>:<>
 <nav className="breadcrumbs" aria-label="Team breadcrumb"><button onClick={()=>select("")}>Team & access</button><span aria-current="page">{team?.name??"New team"}</span></nav>
 {deleting&&team?<><p>Deleting this team removes its added role from all members. Direct roles and other teams remain.</p><ActionForm fields={[{name:"confirmation",label:"Type the team name to delete",schema:z.string().refine(v=>v===team.name,"Enter the exact team name.")}]} submitLabel="Delete team permanently" onSubmit={()=>remove.mutateAsync()} pending={remove.isPending} error={remove.error}/><button disabled={remove.isPending} onClick={()=>setDeleting(false)}>Keep team</button></>:<>
 <fieldset disabled={!manage||save.isPending}><legend>Organization members</legend>{members.map(m=><label key={m.userId}><input type="checkbox" checked={ids.includes(m.userId)} onChange={e=>setIds(old=>e.target.checked?[...old,m.userId]:old.filter(v=>v!==m.userId))}/>{m.email}{!m.active?" (inactive)":""}</label>)}</fieldset>
 {manage?<ActionForm key={id+String(team?.revision)} fields={[
 {name:"name",label:"Team name",defaultValue:team?.name??"",schema:z.string().trim().min(1).max(80)},
 {name:"role",label:"Team role",type:"select",defaultValue:team?.roleId??"",options:[{value:"",label:"Choose a role…"},...assignable.map(r=>({value:r.id,label:r.name}))]},
 {name:"active",label:"Team status",type:"select",defaultValue:team?.active===false?"false":"true",options:[{value:"true",label:"Enabled"},{value:"false",label:"Disabled"}]}
 ]} submitLabel="Save team" onSubmit={v=>save.mutateAsync(v)} pending={save.isPending} error={save.error}/>:<p>Read-only team access.</p>}
 {manage&&team&&<button disabled={save.isPending} onClick={()=>{remove.reset();setDeleting(true);}}>Delete team</button>}
 </>}
 </>}
 </Modal></>;
}
