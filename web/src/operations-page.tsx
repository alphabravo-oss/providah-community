import { useState, useEffect } from "react";
import { Link, useOutletContext, useSearchParams } from "react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { z } from "zod";
import { api, queries } from "./api";
import { ActionForm, DataTable, ErrorNote, Modal, PageHeader } from "./ui";
import {
  OperationStatus,
  type Organization,
  type SessionResponse,
} from "./gen/providah/v1/console_pb";
import {actionLabel,statuses,TagReview} from "./operations";
export function OperationsPage() {
  const { org } = useOutletContext<{ org: Organization }>();
  const [page, setPage] = useState(""),
    [resolving, setResolving] = useState(false);
  const q = useQuery({
    queryKey: ["operations", org.id, page],
    queryFn: () =>
      api.listOperations({ organizationId: org.id, pageToken: page }),
  });
  const [params,setParams]=useSearchParams();
  const selected=params.get("operation")??"";
  const setSelected=(id:string)=>setParams(previous=>{const next=new URLSearchParams(previous);if(id)next.set("operation",id);else next.delete("operation");return next;});
  const detail=useQuery({queryKey:["operation",org.id,selected],queryFn:()=>api.getOperation({organizationId:org.id,id:selected}),enabled:!!selected});
  const current=detail.error?undefined:detail.data?.operation;
  useEffect(()=>setResolving(false),[selected]);
  const refresh = () => queries.invalidateQueries();
  const review = useMutation({
    mutationFn: (v: Record<string, string>) =>
      api.reviewOperation({
        organizationId: org.id,
        id: current!.id,
        approve: v.decision === "approve",
        reason: v.reason,
      }),
    onSuccess: refresh,
  });
  const cancel = useMutation({
    mutationFn: () =>
      api.cancelOperation({ organizationId: org.id, id: current!.id }),
    onSuccess: refresh,
  });
  const reconcile = useMutation({
    mutationFn: () =>
      api.reconcileOperation({ organizationId: org.id, id: current!.id }),
    onSuccess: refresh,
  });
  const resolve = useMutation({
    mutationFn: (v: Record<string, string>) =>
      api.resolveOperation({
        organizationId: org.id,
        id: current!.id,
        reason: v.reason,
      }),
    onSuccess: () => {
      setResolving(false);
      void refresh();
    },
  });
  const email = queries.getQueryData<SessionResponse>(["session"])?.email;
  return (
    <>
      <PageHeader
        eyebrow="REVIEW, EXECUTE, VERIFY"
        title="Operations"
        description="Track resource actions from request to observed outcome."
      />
      <ErrorNote error={q.error} />
      <section className="panel">
        {q.isPending ? (
          <p className="loading">Loading operations…</p>
        ) : q.data?.operations.length ? (
          <DataTable
            label="Operations"
            data={q.data.operations}
            rowId={(o) => o.id}
            columns={[
              {
                id: "target",
                header: "Resource",
                cell: ({ row: { original: o } }) => (
                  <button
                    onClick={() => {
                      setSelected(o.id);
                      setResolving(false);
                    }}
                  >
                    {o.resourceName || o.nativeId}
                    <small>
                      {o.provider} · {o.region}
                    </small>
                  </button>
                ),
              },
              {
                id: "action",
                header: "Action",
                cell: ({ row }) => actionLabel(row.original.action,row.original.resourceKind),
              },
              {
                id: "status",
                header: "Status",
                cell: ({ row }) => (
                  <span className="badge">{statuses[row.original.status]}</span>
                ),
              },
              { accessorKey: "requester", header: "Requested by" },
              {
                id: "time",
                header: "Requested",
                cell: ({ row }) =>
                  new Date(row.original.createdAt).toLocaleString(),
              },
            ]}
          />
        ) : (
          <p className="loading">
            No operations yet. Open a server in Inventory to request an action.
          </p>
        )}
      </section>
      <div className="pagination">
        {page && <button onClick={() => setPage("")}>Most recent</button>}
        {q.data?.nextPageToken && (
          <button onClick={() => setPage(q.data!.nextPageToken)}>
            Older operations
          </button>
        )}
      </div>
      <Modal
        open={!!selected}
        onOpenChange={(v) => {
          if (!v) setSelected("");
        }}
        title={
          current
            ? `${actionLabel(current.action,current.resourceKind)} · ${current.resourceName || current.nativeId}`
            : "Operation"
        }
        description="Provider acceptance and observed completion are recorded separately."
      >
        <nav className="breadcrumbs" aria-label="Operation breadcrumb"><Link to={"/app/operations?org="+org.id}>Operations</Link><span aria-hidden="true">›</span><span aria-current="page">{current?.resourceName||current?.nativeId||"Operation details"}</span></nav>
        <ErrorNote error={detail.error}/>
        {detail.isPending&&<p role="status">Loading operation…</p>}
        {current && (
          <>
            <dl className="resource-details">
              {Object.entries({
                Status: statuses[current.status],
                Resource: current.nativeId,
                Region: current.region,
                Requester: current.requester,
                Reason: current.reason,
                "Maintenance exception": current.maintenanceExceptionReason,
                "Original window ends": current.maintenanceExceptionReason ? "" : current.maintenanceWindowEndsAt,
                "Provider action": current.providerActionId,
                "Observed state": current.observedStatus,
                "Last update": new Date(current.updatedAt).toLocaleString(),
              }).map(([label, value]) => (
                <div key={label}>
                  <dt>{label}</dt>
                  <dd>{value || "—"}</dd>
                </div>
              ))}
            </dl>
            {current.detail && <p className="notice">{current.detail}</p>}
            <ErrorNote
              error={
                cancel.error || reconcile.error || review.error || resolve.error
              }
            />
            {current.action==="snapshot" && <p className="notice">Creates a billed server disk image named providah-{current.id}. Requires a stopped server. AWS creates an EBS-backed AMI; instance-store disks are excluded. DigitalOcean/Hetzner exclude attached volumes and scratch disks. No automatic restart or retry after an ambiguous submission.</p>}
            {current.action==="tags" && <section className="notice"><h3>Reviewed tags before change</h3><TagReview tags={current.expectedTags}/><h3>Complete requested tag set</h3><TagReview tags={current.targetTags}/><p>Independent approval required. Concurrent edits or partial writes may require reconciliation; no automatic rollback.</p></section>}
            {current.action==="resize" && <p className="notice">Server type: <strong>{current.expectedSize} → {current.targetSize}</strong>. Disk size is preserved. Provider compatibility/capacity rules apply and the provider may restart the server.</p>}
            {current.keyCreation && <section><h3>Reviewed public key</h3><dl className="resource-details"><div><dt>Imported name</dt><dd>{current.keyCreation.name}-{current.id}</dd></div></dl><pre className="notice multiline">{current.keyCreation.publicKey}</pre><p className="notice">Adds an account public key. Existing servers are unchanged; completion requires read-only confirmation.</p></section>}
            {current.creation && <dl className="resource-details">{Object.entries({"Template version ID":current.templateId,Name:current.creation.name,Image:current.creation.image,Size:current.creation.size,"SSH key":current.creation.sshKey,Subnet:current.creation.subnet,"Private network":current.creation.network,"Security group":current.creation.securityGroup}).filter(([,v])=>v).map(([k,v])=><div key={k}><dt>{k}</dt><dd>{v}</dd></div>)}</dl>}
            {current.deletionImpact && <div className="notice multiline">{current.deletionImpact}</div>}
            {current.canReview && (!["create","delete","snapshot"].includes(current.action) || org.permissions.includes("operations."+(current.action==="snapshot"?"create":current.action))) &&
              org.permissions.includes("operations.approve") && (!current.maintenanceExceptionReason || org.permissions.includes("maintenance.override")) && (
                <ActionForm key={current.id}
                  fields={[
                    {
                      name: "decision",
                      label: "Decision",
                      type: "select",
                      defaultValue: "approve",
                      options: [
                        { value: "approve", label: "Approve for up to one hour" },
                        { value: "reject", label: "Reject" },
                      ],
                    },
                    {
                      name: "reason",
                      label: "Review reason",
                      type: "textarea",
                      schema: z.string().min(3).max(500),
                    },
                  ]}
                  onSubmit={(v) => review.mutateAsync(v)}
                  submitLabel="Submit review"
                  pending={review.isPending}
                  error={review.error}
                />
              )}
            {current.requester === email &&
              [
                OperationStatus.AWAITING_APPROVAL,
                OperationStatus.QUEUED,
              ].includes(current.status) && (
                <button
                  className="secondary full"
                  disabled={cancel.isPending}
                  onClick={() => cancel.mutate()}
                >
                  Cancel before dispatch
                </button>
              )}
            {current.status === OperationStatus.UNCERTAIN && (
              <>
                <p className="muted">
                  Do not submit this request again. After the
                  worker lease expires, check the provider state; checking never
                  resubmits the action.
                </p>
                <div className="resource-actions">
                  {org.permissions.includes("operations.request") && (
                    <button
                      className="secondary"
                      disabled={reconcile.isPending}
                      onClick={() => reconcile.mutate()}
                    >
                      Check provider state
                    </button>
                  )}
                  {org.permissions.includes("operations.approve") && (!["create","delete","snapshot"].includes(current.action) || org.permissions.includes("operations."+(current.action==="snapshot"?"create":current.action))) &&
                    current.requester !== email && (
                      <button
                        className="secondary"
                        onClick={() => setResolving(true)}
                      >
                        Resolve after verification
                      </button>
                    )}
                </div>
                {resolving && (
                  <ActionForm key={current.id}
                    fields={[
                      {
                        name: "reason",
                        label: "How did you verify the provider state?",
                        type: "textarea",
                        schema: z.string().min(10).max(500),
                      },
                    ]}
                    onSubmit={(v) => resolve.mutateAsync(v)}
                    submitLabel="Record manual resolution"
                    error={resolve.error}
                    pending={resolve.isPending}
                  />
                )}
              </>
            )}
          </>
        )}
      </Modal>
    </>
  );
}
