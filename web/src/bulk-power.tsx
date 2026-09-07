import { useState } from "react";
import { Link } from "react-router";
import { useMutation } from "@tanstack/react-query";
import { z } from "zod";
import { api, queries } from "./api";
import { ActionForm, DataTable, ErrorNote, Modal } from "./ui";
import type { Organization, Resource } from "./gen/providah/v1/console_pb";

export function BulkPowerButton({org,resources}:{org:Organization;resources:Resource[]}) {
  const [open,setOpen]=useState(false);
  const [selection,setSelection]=useState<Resource[]>([]);
  const [reason,setReason]=useState("");
  const preview=useMutation({gcTime:0,mutationFn:(v:Record<string,string>)=>api.previewBulkPower({organizationId:org.id,resourceIds:v.targets.split(","),action:v.action})});
  const submit=useMutation({gcTime:0,mutationFn:(reason:string)=>api.requestBulkPower({organizationId:org.id,reason,reviewTokens:preview.data!.targets.filter(t=>t.reviewToken).map(t=>t.reviewToken)}),onSuccess:()=>{void queries.invalidateQueries();}});
  if(!org.permissions.includes("operations.request") || !org.permissions.includes("resources.read"))return null;
  const eligible=preview.data?.targets.filter(t=>t.reviewToken).length??0;
  return <><button className="secondary" disabled={!resources.length} onClick={()=>{setSelection([...resources]);preview.reset();submit.reset();setReason("");setOpen(true);}}>Bulk power actions</button>
    <Modal wide={!!preview.data} open={open} onOpenChange={setOpen} title="Bulk server power" description="Select exact targets from this inventory page. Each server is checked and tracked independently; partial success is possible.">
      {!preview.data ? <ActionForm fields={[
        {name:"action",label:"Power action",type:"select",defaultValue:"start",options:[{value:"start",label:"Start"},{value:"shutdown",label:"Graceful shutdown"},{value:"restart",label:"Restart"}]},
        {name:"targets",label:"Exact targets",type:"checks",options:selection.map(r=>({value:r.id,label:`${r.name || r.nativeId} · ${r.provider} · ${r.region}`})),schema:z.string().min(1).refine(v=>v.split(",").length<=50,"Choose at most fifty targets."),description:"Selection is fixed when this dialog opens; filters and new discovery cannot add targets."},
      ]} onSubmit={v=>preview.mutateAsync(v)} submitLabel="Preview exact targets" pending={preview.isPending} error={preview.error}/> : <>
        <DataTable label="Bulk target review" data={preview.data.targets} rowId={t=>t.resourceId} columns={[
          {id:"target",header:"Target",cell:({row:{original:t}})=><>{t.name || "Unavailable target"}<small>{t.nativeId}</small><small>{t.provider} {t.region}</small></>},
          {accessorKey:"action",header:"Action"},{accessorKey:"status",header:"Observed state"},{accessorKey:"eligibility",header:"Eligibility"},{accessorKey:"detail",header:"Review"},
        ]}/>
        <p className="notice">{eligible} eligible targets will be requested. Starting can incur cloud charges; approval requirements follow organization policy for each server. This request does not override maintenance windows. Reviews expire after ten minutes. For resources managed by Terraform/OpenTofu, a later apply may undo power changes.</p>
        {!submit.data && eligible>0 && <ActionForm fields={[{name:"reason",label:"Reason for these operations",schema:z.string().min(3).max(500)}]} onSubmit={v=>{setReason(v.reason);return submit.mutateAsync(v.reason);}} submitLabel={`Request ${eligible} ${eligible===1 ? "operation" : "operations"}`} pending={submit.isPending} error={submit.error}/>}
        {submit.data && <>
          <DataTable label="Bulk request results" data={submit.data.results} rowId={r=>r.resourceId} columns={[
            {id:"target",header:"Target",cell:({row:{original:r}})=>preview.data!.targets.find(t=>t.resourceId===r.resourceId)?.name || "Unavailable target"},
            {id:"result",header:"Result",cell:({row:{original:r}})=>r.operation ? <>Recorded<small>{r.operation.id}</small></> : r.error},
          ]}/>
          <p>Recorded requests remain in Operations even if you close this dialog. Repeating this reviewed request returns existing operations and only attempts to record missing ones.</p>
          {submit.data.results.some(r=>r.error) && <button className="secondary" disabled={submit.isPending} onClick={()=>submit.mutate(reason)}>Retry same reviewed request</button>}
          <ErrorNote error={submit.error}/>
          <Link className="primary" to={"/app/operations?org="+org.id} onClick={()=>setOpen(false)}>Track individual operations</Link>
        </>}
      </>}
    </Modal></>;
}
