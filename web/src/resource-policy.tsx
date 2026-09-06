import { useOutletContext } from "react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { z } from "zod";
import { api, queries } from "./api";
import { ActionForm, ErrorNote, PageHeader } from "./ui";
import type { Organization } from "./gen/providah/v1/console_pb";

export function ResourcePolicyPage() {
  const {org}=useOutletContext<{org:Organization}>();
  const policy=useQuery({queryKey:["resource-policy",org.id],queryFn:()=>api.getResourcePolicy({organizationId:org.id})});
  const save=useMutation({mutationFn:(v:Record<string,string>)=>api.saveResourcePolicy({organizationId:org.id,creationEnabled:v.mode==="create",expectedRevision:policy.data!.revision,reason:v.reason}),onSuccess:()=>queries.invalidateQueries()});
  return <><PageHeader eyebrow="ORGANIZATION SETTINGS" title="Resource management" description="Choose whether this organization can provision new cloud resources."/>
    <ErrorNote error={policy.error}/>
    <section className="panel"><h2>{policy.data?.creationEnabled ? "Manage and create" : "Manage existing only"}</h2>
      <p>Manage existing only allows discovery and permitted operations on existing infrastructure. It blocks new servers, snapshots and SSH-key imports, including queued creation work. Already-submitted work continues to be tracked. Member permissions and independent approvals still apply.</p>
      {policy.data && <ActionForm key={String(policy.data.revision)} fields={[
        {name:"mode",label:"Resource management mode",type:"select",defaultValue:policy.data.creationEnabled?"create":"existing",options:[{value:"existing",label:"Manage existing only"},{value:"create",label:"Manage and create"}],schema:z.enum(["existing","create"])},
        {name:"reason",label:"Change reason",type:"textarea",schema:z.string().trim().min(3).max(500)},
      ]} onSubmit={v=>save.mutateAsync(v)} submitLabel="Save resource policy" pending={save.isPending} error={save.error}/>}
    </section></>;
}
