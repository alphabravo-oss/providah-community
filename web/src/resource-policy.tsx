import {useState} from "react";
import {useOutletContext} from "react-router";
import {useMutation,useQuery} from "@tanstack/react-query";
import {z} from "zod";
import {api,queries} from "./api";
import {ActionForm,ErrorNote,PageHeader} from "./ui";
import type {Organization} from "./gen/providah/v1/console_pb";
const actionLabels:Record<string,string>={create:"Create resources / import SSH keys",start:"Start",shutdown:"Shutdown / stop",restart:"Restart",resize:"Resize",snapshot:"Create images / snapshots",tags:"Edit tags",delete:"Delete resources"};
export function ResourcePolicyPage(){
  const {org}=useOutletContext<{org:Organization}>();
  const policy=useQuery({queryKey:["resource-policy",org.id],queryFn:()=>api.getResourcePolicy({organizationId:org.id})});
  const [mode,setMode]=useState(""),[selected,setSelected]=useState<string[]>();
  const actions=selected??policy.data?.approvalActions??[];
  const currentMode=mode||(actions.length===0?"none":actions.length===Object.keys(actionLabels).length?"all":"selected");
  const save=useMutation({mutationFn:(v:Record<string,string>)=>api.saveResourcePolicy({organizationId:org.id,creationEnabled:v.mode==="create",approvalActions:currentMode==="none"?[]:currentMode==="all"?Object.keys(actionLabels):actions,expectedRevision:policy.data!.revision,reason:v.reason}),onSuccess:async()=>{await queries.invalidateQueries();setMode("");setSelected(undefined);}});
  return <><PageHeader eyebrow="ORGANIZATION SETTINGS" title="Resource management" description="Choose what members can create and which actions need another person's approval."/>
    <ErrorNote error={policy.error}/>
    {policy.data&&<section className="panel resource-policy">
      <h2>Action approvals</h2>
      <label className="field"><span>Approval mode</span><select aria-label="Approval mode" value={currentMode} onChange={e=>setMode(e.target.value)}>
        <option value="none">Confirmation only</option><option value="selected">Approval for selected actions</option><option value="all">Approval for every action</option>
      </select></label>
      <p>Confirmation only queues actions after the requester confirms. Selected actions require a different authorized person to approve; other actions queue after confirmation. Maintenance exceptions require approval unless the mode is confirmation only. Permissions, maintenance windows, deletion impact checks and audit logging always apply.</p>
      {currentMode==="selected"&&<fieldset className="check-options"><legend>Actions requiring approval</legend>{Object.entries(actionLabels).map(([action,label])=><label className="check" key={action}><input type="checkbox" checked={actions.includes(action)} onChange={e=>setSelected(e.target.checked?[...actions,action]:actions.filter(a=>a!==action))}/>{label}</label>)}</fieldset>}
      <p className="notice">Existing requests awaiting approval stay pending. Cancel and submit a fresh request to use a changed policy. Tightening approval rules can cancel queued actions that lack approval. Scheduled automation retains its separate schedule approval.</p>
      <h2>Resource creation</h2><p>Manage existing only blocks new servers, snapshots and SSH-key imports, including queued creation work. Already-submitted work continues to be tracked.</p>
      <ActionForm key={String(policy.data.revision)} fields={[
        {name:"mode",label:"Resource management mode",type:"select",defaultValue:policy.data.creationEnabled?"create":"existing",options:[{value:"existing",label:"Manage existing only"},{value:"create",label:"Manage and create"}],schema:z.enum(["existing","create"])},
        {name:"reason",label:"Change reason",type:"textarea",schema:z.string().trim().min(3).max(500)},
      ]} onSubmit={v=>save.mutateAsync(v)} submitLabel="Save resource policy" pending={save.isPending} error={save.error}/>
    </section>}
  </>;
}
