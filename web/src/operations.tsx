import { useCatalog } from "./creation";
import { useState } from "react";
import { useNavigate } from "react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { z } from "zod";
import { Power, RotateCw } from "lucide-react";
import { api, queries } from "./api";
import { ActionForm, ErrorNote, Modal } from "./ui";
import {
  type OperationStatus,
  type Organization,
  type Resource,
} from "./gen/providah/v1/console_pb";
export const statuses: Record<OperationStatus, string> = {
  0: "Unknown",
  1: "Awaiting approval",
  2: "Queued",
  3: "Dispatching",
  4: "Observing",
  5: "Succeeded",
  6: "Failed",
  7: "Uncertain",
  8: "Rejected",
  9: "Canceled",
  10: "Expired",
  11: "Resolved manually",
};
export const actions: Record<string, string> = {
  create: "Create server",
  snapshot: "Create server image",
  resize: "Resize server",
  tags: "Edit tags",
  start: "Start",
  shutdown: "Graceful shutdown",
  restart: "Restart",
  delete: "Delete resource",
};
export function actionLabel(action:string,kind:string) {
if(kind==="access.ssh_key" && action==="create") return "Import SSH key";
if(kind==="access.ssh_key" && action==="delete") return "Delete SSH key";
  if (["database.instance","database.cluster"].includes(kind)) return action==="shutdown"?"Stop database":action==="start"?"Start database":actions[action];
  return actions[action];
}
export function PowerActions({
  org,
  resource,
  enabled,
  availableActions,
  managedByIaC = false,
}: {
  org: Organization;
  resource: Resource;
  enabled: boolean;
  availableActions: string[];
  managedByIaC?: boolean;
}) {
  const [request, setRequest] = useState<{
    resource: Resource;
    action: string;
    key: string;
  } | null>(null);
  const navigate = useNavigate();
  const policy=useQuery({queryKey:["resource-policy",org.id],queryFn:()=>api.getResourcePolicy({organizationId:org.id}),enabled:!!request});
  const sizes = useCatalog(org.id, resource.connectionId, "compute.type", request?.action==="resize");
  const sizeOptions = [...new Map(sizes.data?.pages.flatMap(p=>p.resources).filter(r=>r.status!=="unavailable" && (r.region==="global" || r.region===resource.region)).map(r=>{const value=r.provider==="hetzner" ? r.name:r.nativeId;return [value,{value,label:`${r.name} · ${r.size}`}];})??[]).values()].filter(o=>o.value!==resource.size);
  const preview = useMutation({ gcTime: 0, mutationFn: async (target: {resource: Resource; key: string}) => ({ key: target.key, ...await api.previewDeletion({organizationId:org.id,resourceId:target.resource.id}) }) });
  const deletion = preview.data?.key === request?.key ? preview.data : undefined;
  const submit = useMutation({
    mutationFn: (v: Record<string, string>) =>
      api.requestOperation({
        organizationId: org.id,
        resourceId: request!.resource.id,
        action: request!.action,
        expectedStatus: request!.resource.status,
 expectedSize:request!.action==="resize" ? request!.resource.size:undefined,
 expectedTags:request!.action==="tags" ? request!.resource.tags:undefined,
 targetTags:request!.action==="tags" ? tagTarget(v.tags,request!.resource.provider):undefined,
 targetSize:request!.action==="resize" ? v.targetSize:undefined,
        confirmation: v.confirmation,
        deletionDigest: request!.action === "delete" ? deletion?.digest : undefined,
        reason: v.reason,
        maintenanceExceptionReason: v.maintenanceExceptionReason,
        idempotencyKey: request!.key,
      }),
    onSuccess: () => {
      void queries.invalidateQueries();
      navigate(`/app/operations?org=${org.id}`);
    },
  });
  if (
    !org.permissions.includes("operations.request") ||
    !["compute.placement_group","organization.project","compute.image","compute.server","network.load_balancer","database.snapshot","database.cluster_snapshot","storage.snapshot","storage.volume","network.network","network.firewall","access.ssh_key","database.instance","database.cluster"].includes(resource.kind)
  )
    return null;
  const database=["database.instance","database.cluster"].includes(resource.kind);
  const running = (database?["available"]:["running", "active"]).includes(resource.status),
    off = ["off", "stopped"].includes(resource.status);
  return (
    <>
      <div className="resource-actions">
        {Object.entries(actions).filter(([action]) => action!=="create" && availableActions.includes(action) && (action!=="delete" || org.permissions.includes("operations.delete")) && (action!=="snapshot" || org.permissions.includes("operations.create"))).map(([action]) => (
          <button
            key={action}
            className="secondary"
            disabled={!enabled || (action === "tags" ? (managedByIaC || !resource.tags || !(off || running)) : action === "delete" ? !(off || running || (["storage.snapshot","database.snapshot","database.cluster_snapshot"].includes(resource.kind) && ["available","completed","present"].includes(resource.status)) || (resource.kind==="network.load_balancer" && ["active","errored","present"].includes(resource.status)) || (["compute.placement_group","storage.volume","compute.image"].includes(resource.kind) && resource.status==="available") || (["compute.placement_group","organization.project","network.network","network.firewall","access.ssh_key"].includes(resource.kind) && ["present","succeeded"].includes(resource.status))) : (action === "start" || action==="resize" || action==="snapshot") ? !off : !running)}
            onClick={() =>
              setRequest({ resource, action, key: crypto.randomUUID() })
            }
          >
            {action === "restart" ? (
              <RotateCw size={14} />
            ) : (
              <Power size={14} />
            )}{" "}
            {actionLabel(action,resource.kind)}
          </button>
        ))}
      </div>
      <Modal
        open={!!request}
        onOpenChange={(v) => {
          if (!v) setRequest(null);
        }}
        title={request ? actionLabel(request.action,request.resource.kind) : "Resource action"}
        description="Review the exact target before requesting this operation."
      >
        {request && (
          <>
            <ErrorNote error={policy.error}/><p className="notice">{!policy.data?"Loading approval policy…":policy.data.approvalActions.includes(request.action)?"Requires approval from a different authorized person after confirmation.":"Confirmation is the final gate: this action queues directly after you submit."}</p>
            {managedByIaC && ["start","shutdown","restart"].includes(request.action) && <p className="notice" role="note">This resource is referenced by managed IaC state. A later Terraform/OpenTofu apply may undo this power change.</p>}
            {request.action==="snapshot" && <p className="notice">Creates a billed disk image from this stopped server. AWS creates an EBS-backed AMI; instance-store disks are excluded. DigitalOcean and Hetzner snapshot the server disk, excluding attached volumes and scratch disks. The image is named automatically from the operation ID. This does not replace application-consistent backup procedures.</p>}
            <dl className="resource-details">
              <div>
                <dt>Resource</dt>
                <dd>
                  {request.resource.name} · {request.resource.nativeId}
                </dd>
              </div>
              <div>
                <dt>Cloud / region</dt>
                <dd>
                  {request.resource.provider} / {request.resource.region}
                </dd>
              </div>
              <div>
                <dt>Current state</dt>
                <dd>{request.resource.status}</dd>
              </div>
            </dl>
            <p className="notice">
              {request.action === "tags" ? "Replaces the complete tag set. Current tags must still match the reviewed set. Cloud edits can race this check and writes may partially apply; no automatic rollback is performed. AWS reserved tags must remain unchanged." : database ? (request.action==="start" ? "Starting this database resumes compute charges.  Startup may take minutes to hours." : "Stopping interrupts database connections. A cluster stop affects its member instances. AWS automatically starts stopped databases after seven days; storage and backup charges continue. No extra snapshot is created. Provider compatibility rules apply.") : request.action === "snapshot" ? "The source server is not started or rebooted by this request. Approval follows organization policy." : request.action === "resize" ? "Resizing changes cloud charges. The source type must still match at dispatch. Disk size is preserved; provider compatibility and capacity rules apply. The provider may restart the server during the change. No backup or separate restart is performed automatically." : request.action === "delete" ? "Deletion is permanent. Review the exact impact below. No backup is created automatically." : request.action === "start"
                ? "Starting this server may incur cloud charges. "
                : "This interrupts workloads. Graceful shutdown never falls back to force power-off."}
            </p>
            {request.action==="resize" && <>
              <p>Current server type: <strong>{request.resource.size}</strong></p>
              <ErrorNote error={sizes.error}/>
              {sizes.hasNextPage && <button disabled={sizes.isFetchingNextPage} onClick={()=>void sizes.fetchNextPage()}>Load more server types</button>}
              {!sizes.isPending && !sizeOptions.length && <p>No alternative types discovered. Refresh the connection inventory.</p>}
            </>}
            {request.action==="delete" && <>
              <button className="secondary" disabled={preview.isPending} onClick={() => preview.mutate(request)}>{preview.isPending ? "Inspecting dependencies…" : "Read deletion impact"}</button>
              <ErrorNote error={preview.error} />
              {deletion && <div className="notice multiline">{deletion.impact}</div>}
            </>}
            {(request.action!=="delete" || deletion) && <ActionForm key={request.key + (deletion?.digest ?? "")}
              fields={[
                ...(request.action==="tags" ? [{name:"tags",label:"Target tags",type:request.resource.provider==="digitalocean" ? "tag-names" as const : "tag-labels" as const,defaultValue:JSON.stringify(request.resource.provider==="digitalocean" ? request.resource.tags!.names.map(name=>[name,""]) : Object.entries(request.resource.tags!.labels)),schema:z.string().refine(value=>{const rows:string[][]=JSON.parse(value);const keys=rows.map(row=>request.resource.provider==="digitalocean" ? row[0].toLowerCase():row[0]);return keys.every(Boolean) && new Set(keys).size===keys.length;},"Use nonempty, unique tag keys or names."),description:"This is the complete replacement set. Remove rows to remove tags; add rows to add tags."}]:[]),
                ...(request.action==="resize" ? [{name:"targetSize",label:"Target server type",type:"select" as const,options:sizeOptions,schema:z.string().min(1),description:"Choose a compatible type. Existing disks will not be expanded."}]:[]),
                ...(request.action==="delete" ? [{name:"confirmation",label:`Type ${request.resource.nativeId} to confirm deletion and the listed data loss`,schema:z.literal(request.resource.nativeId)}] : []),
                {
                  name: "reason",
                  label: "Reason for this action",
                  type: "textarea",
                  schema: z.string().min(3).max(500),
                },
                ...(org.permissions.includes("maintenance.override") ? [{ name: "maintenanceExceptionReason", label: "Maintenance exception reason (optional)", description: "An exception requires maintenance override permission and independent approval unless the organization uses confirmation only. Any approver must also have override permission. Leave empty to follow normal windows.", schema: z.string().max(500).refine((v) => !v.trim() || v.trim().length >= 10, "Use at least 10 characters.") }] : []),
              ]}
              onSubmit={(v) => submit.mutateAsync(v)}
              submitLabel={`Request ${actionLabel(request.action,request.resource.kind).toLowerCase()}`}
              pending={submit.isPending||policy.isPending||policy.isError}
              error={submit.error}
            />}
          </>
        )}
      </Modal>
    </>
  );
}

function tagTarget(value:string,cloud:string) {
  const rows:string[][]=JSON.parse(value);
  return cloud==="digitalocean" ? {names:rows.map(row=>row[0])} : {labels:Object.fromEntries(rows)};
}
export function TagReview({tags}:{tags?:{labels:Record<string,string>;names:string[]}}) {
  if(!tags) return <p>Tag metadata unavailable</p>;
  const rows=[...Object.entries(tags.labels),...tags.names.map(name=>[name,""])];
  return rows.length ? <dl className="resource-details">{rows.map(([key,value])=><div key={key}><dt className="multiline">{key}</dt><dd className="multiline">{value || "(empty)"}</dd></div>)}</dl> : <p>No tags</p>;
}
