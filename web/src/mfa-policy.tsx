import {useState} from "react";
import {useMutation,useQuery} from "@tanstack/react-query";
import {z} from "zod";
import {api,queries} from "./api";
import {ActionForm,ErrorNote,Modal} from "./ui";

export function MFAPolicyButton({organizationId=""}:{organizationId?:string}){
 const [open,setOpen]=useState(false);
 const session=useQuery({queryKey:["session"],queryFn:()=>api.getSession({})});
 const policy=useQuery({queryKey:["mfa-policy",organizationId],queryFn:()=>api.getMFAPolicy({organizationId}),enabled:open});
 const save=useMutation({gcTime:0,mutationFn:(v:Record<string,string>)=>api.saveMFAPolicy({organizationId,required:v.mode==="required",expectedRevision:policy.data!.revision,password:v.password,code:v.code??""}),onSuccess:()=>{setOpen(false);void queries.invalidateQueries();}});
 const title=organizationId ? "Organization MFA policy" : "Global MFA policy";
 return <>
  <button className="text-button" onClick={()=>{save.reset();setOpen(true);}}>{title}</button>
  <Modal open={open} onOpenChange={setOpen} title={title} description="Choose whether MFA is optional or required for organization access. A global requirement takes precedence over organization settings.">
   <ErrorNote error={policy.error}/>
   {policy.isPending ? <p>Loading policy…</p> : policy.data && <>
    {organizationId && policy.data.globalRequired && <p className="notice">Global policy currently requires MFA. Choosing optional here cannot override it.</p>}
    <p className="muted">Enable MFA on your own account before requiring it. Accounts without MFA can sign in to manage security settings, but cannot access affected cloud organizations. Queued work rechecks this policy before dispatch.</p>
    <ActionForm key={String(policy.data.revision)} fields={[
     {name:"mode",label:"MFA requirement",type:"select",defaultValue:policy.data.required ? "required" : "optional",options:[{value:"optional",label:"Optional"},{value:"required",label:"Required"}]},
     {name:"password",label:"Current password",type:"password",autoComplete:"current-password",schema:z.string().min(12).max(72)},
     ...(session.data?.mfaEnabled ? [{name:"code",label:"Fresh authenticator code",autoComplete:"one-time-code",schema:z.string().regex(/^\d{6}$/)}] : []),
    ]} onSubmit={v=>save.mutateAsync(v)} pending={save.isPending} error={save.error} submitLabel="Save MFA policy"/>
   </>}
  </Modal>
 </>;
}
