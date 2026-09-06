import { useState } from "react";
import { useOutletContext } from "react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { z } from "zod";
import { api, queries } from "./api";
import { ActionForm, DataTable, ErrorNote, Modal, PageHeader } from "./ui";
import type { Organization, ProviderModule } from "./gen/providah/v1/console_pb";

function admission(runtime:{publisherKeyId:string;approvalExpiresAt:string;runtimeId?:string;runtimes?:{image:string}[]}) {
 if(runtime.runtimes&&!runtime.runtimes.some(r=>r.image===runtime.runtimeId))return "Unavailable";
 if(!runtime.publisherKeyId)return "Deployment approved";
 return Date.parse(runtime.approvalExpiresAt)<=Date.now()?"Publisher approval expired":"Publisher verified";
}
export function ModulesPage() {
  const { org } = useOutletContext<{ org: Organization }>();
  const [runtimeEditing, setRuntimeEditing] = useState(false);
  const [selected, setSelected] = useState<ProviderModule | null>(null);
  const q = useQuery({ queryKey: ["provider-modules", org.id], queryFn: () => api.listProviderModules({ organizationId: org.id }) });
  const current = q.isError?null:q.data?.modules.find((m) => m.provider === selected?.provider) ?? selected;
  const change = useMutation({ mutationFn: (v: Record<string, string>) => api.setProviderModule({ organizationId: org.id, provider: current!.provider, enabled: !current!.enabled, confirmation: v.confirmation }), onSuccess: () => { setSelected(null); void queries.invalidateQueries(); } });
  const runtimeChange = useMutation({ mutationFn: (v: Record<string, string>) => api.setProviderRuntime({ organizationId: org.id, provider: selected!.provider, runtimeId: v.runtime, expectedRevision: selected!.revision, confirmation: v.confirmation }), onSuccess: () => { setSelected(null); setRuntimeEditing(false); void queries.invalidateQueries(); } });
  return <>
    <PageHeader eyebrow="PROVIDER CAPABILITIES" title="Provider modules" description="Control which bundled providers can start work in this organization." />
    <ErrorNote error={q.error} />
    <section className="panel"><DataTable label="Provider modules" data={q.isError?[]:q.data?.modules ?? []} rowId={(m) => m.provider} columns={[
      { id: "provider", header: "Provider", cell: ({ row }) => <button onClick={() => { change.reset(); runtimeChange.reset(); setRuntimeEditing(false); setSelected(row.original); }}>{row.original.provider}</button> },
      { id: "enabled", header: "Organization state", cell: ({ row }) => row.original.enabled ? "Enabled" : "Disabled" },
      { id: "connections", header: "Connections", cell: ({ row }) => String(row.original.connections) },
      { id: "active", header: "Submitted / unresolved", cell: ({ row }) => String(row.original.activeOperations) },
      { id: "runtime", header: "Runtime", cell: ({ row }) => row.original.runtimes.find((r) => r.image === row.original.runtimeId)?.version ?? "Unavailable" },
      { id:"admission",header:"Admission",cell:({row})=>admission(row.original) },
      { id: "contract", header: "Worker contract", cell: ({ row }) => `v${row.original.contractVersion}` },
    ]} />{q.isPending && <p className="loading">Loading modules…</p>}</section>
    <p className="notice">These controls apply to bundled providers in this organization. You can select health-checked runtimes approved by your deployment administrator. Signed registry installation and third-party UI extensions are still being built.</p>
    <Modal open={!!current} onOpenChange={(open) => { if (!open) setSelected(null); }} title={current ? `${current.provider} module` : "Provider module"} description="Module controls do not create, stop, or delete cloud resources.">{current && <>
      <dl className="resource-details"><div><dt>State</dt><dd>{current.enabled ? "Enabled" : "Disabled"}</dd></div><div><dt>Revision</dt><dd>{String(current.revision)}</dd></div><div><dt>Selected image</dt><dd>{current.runtimeId || "No launcher catalog"}</dd></div><div><dt>Admission</dt><dd>{admission(current)}</dd></div>{current.publisherKeyId&&<><div><dt>Publisher key</dt><dd>{current.publisherKeyId}</dd></div><div><dt>Approval expiry</dt><dd>{new Date(current.approvalExpiresAt).toLocaleString()}</dd></div></>}<div><dt>Capabilities</dt><dd>{current.capabilities.join(", ")}</dd></div><div><dt>Active work</dt><dd>{String(current.activeOperations)} submitted or unresolved operations</dd></div></dl>
      <p className="notice">{admission(current)==="Unavailable"?"The selected image is not in the current launcher catalog.":current.publisherKeyId?"Publisher signature was verified when this image was admitted. Expiry and trust changes take effect on launcher/core restart; they do not stop already admitted work.":"This image was approved by deployment configuration. No publisher signature was required at admission."}</p>
      <p className="notice">Disabling blocks new discovery and mutations. Pending work from the previous revision is canceled or skipped when processed. Inventory and credentials remain stored; already-submitted operations can continue being observed. Re-enabling requires fresh requests and schedule review, with no replay of missed work.</p>
      {org.permissions.includes("modules.manage") && !runtimeEditing && <ActionForm key={`${org.id}:${current.provider}:${current.revision}`} fields={[{ name: "confirmation", label: `Type ${current.provider} to confirm`, schema: z.literal(current.provider) }]} onSubmit={(v) => change.mutateAsync(v)} submitLabel={current.enabled ? "Disable provider module" : "Enable provider module"} pending={change.isPending} error={change.error} />}
      {org.permissions.includes("modules.manage") && current.runtimes.length > 0 && <>
        {!runtimeEditing ? <button className="secondary" onClick={() => setRuntimeEditing(true)}>Change runtime / rollback</button> : <>
          <p className="notice">Select an approved immutable image. Changing runtime invalidates queued work and schedule approvals. Submitted operations keep their original runtime. Rollback does not undo cloud changes.</p>
          <ActionForm key={`runtime:${selected!.revision}`} fields={[
            { name: "runtime", label: "Approved provider runtime", type: "select", defaultValue: current.runtimeId, options: current.runtimes.map((r) => ({ value: r.image, label: `${r.version} · SDK ${r.sdkVersion} · ${admission(r)}${r.publisherKeyId?" · "+r.publisherKeyId:""} · ${r.image.slice(0, 19)}` })) },
            { name: "confirmation", label: `Type ${current.provider} to confirm runtime selection`, schema: z.literal(current.provider) },
          ]} onSubmit={(v) => runtimeChange.mutateAsync(v)} submitLabel="Select provider runtime" pending={runtimeChange.isPending} error={runtimeChange.error} />
        </>}
      </>}
    </>}</Modal>
  </>;
}
