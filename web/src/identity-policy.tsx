import {MFAPolicyButton} from "./mfa-policy";
import { useState } from "react";
import { useOutletContext } from "react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { z } from "zod";
import { api, queries } from "./api";
import { ActionForm, DataTable, ErrorNote, Modal, PageHeader } from "./ui";
import type { Organization } from "./gen/providah/v1/console_pb";

export function IdentityPolicyPage() {
  const {org}=useOutletContext<{org:Organization}>();const [open,setOpen]=useState(false);
  const policy=useQuery({queryKey:["identity-policy",org.id],queryFn:()=>api.getIdentityPolicy({organizationId:org.id})});
  const access=useQuery({queryKey:["access",org.id],queryFn:()=>api.listAccess({organizationId:org.id}),enabled:org.permissions.includes("members.read")});
  const save=useMutation({mutationFn:(v:Record<string,string>)=>api.saveIdentityPolicy({organizationId:org.id,enabled:v.mode==="required",recoveryUsers:v.mode==="required" ? v.recovery.split(",").filter(Boolean) : [],expectedRevision:policy.data!.revision,reason:v.reason}),onSuccess:()=>{setOpen(false);void queries.invalidateQueries();}});
  const recovery=(policy.data?.recoveryUsers??[]).map(id=>({id,email:access.data?.members.find(m=>m.userId===id)?.email??id}));
  return <><PageHeader eyebrow="ORGANIZATION IDENTITY" title="Sign-in policy" description="Control how members authenticate to this organization."><MFAPolicyButton organizationId={org.id}/><button className="primary" disabled={!policy.data} onClick={()=>{save.reset();setOpen(true);}}>Edit sign-in policy</button></PageHeader>
    <ErrorNote error={policy.error}/><ErrorNote error={access.error}/>
    <section className="panel"><h2>{policy.data?.enabled ? "Organization sign-in required" : "Local and linked sign-in allowed"}</h2><p className="muted">{policy.data?.enabled ? policy.data.issuer : policy.data?.configuredIssuer || "Configure an OIDC issuer before requiring organization sign-in."}</p><p>Enforcement covers organization APIs, live updates, and new operation/schedule dispatch. Submitted work retains read-only tracking. Designated recovery administrators can use local password and MFA.</p>
      <DataTable label="Recovery administrators" data={recovery} rowId={r=>r.id} columns={[{accessorKey:"email",header:"Designated local recovery access"}]}/></section>
    <Modal open={open} onOpenChange={setOpen} title="Edit sign-in policy" description="Enabling requires a current OIDC sign-in, recent MFA, and at least one designated active administrator for local recovery.">
      {policy.data && <ActionForm key={String(policy.data.revision)} fields={[
        {name:"mode",label:"Member sign-in",type:"select",defaultValue:policy.data.enabled ? "required" : "optional",options:[{value:"optional",label:"Allow local and linked sign-in"},{value:"required",label:"Require organization sign-in"}]},
        {name:"recovery",label:"Local recovery administrators",type:"checks",when:{name:"mode",is:["required"]},defaultValue:policy.data.recoveryUsers.join(","),options:access.data?.members.filter(m=>m.active&&m.roleId==="administrator").map(m=>({value:m.userId,label:m.email}))??[],schema:z.string().min(1),description:"These accounts retain local access. Their last active administrator membership cannot be removed while enforcement is enabled."},
        {name:"reason",label:"Policy change reason",type:"textarea",schema:z.string().min(10).max(500)},
      ]} onSubmit={v=>save.mutateAsync(v)} submitLabel="Save sign-in policy" pending={save.isPending} error={save.error}/>}
    </Modal></>;
}
