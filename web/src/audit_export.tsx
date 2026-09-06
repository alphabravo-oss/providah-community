import { useState } from "react";
import { useOutletContext } from "react-router";
import { useInfiniteQuery, useMutation, useQuery } from "@tanstack/react-query";
import { z } from "zod";
import { api, queries } from "./api";
import { ActionForm, DataTable, ErrorNote, Modal, PageHeader } from "./ui";
import type { AuditExportBatch, Organization } from "./gen/providah/v1/console_pb";

export function AuditExportPage() {
  const { org } = useOutletContext<{ org: Organization }>();
  const [editing, setEditing] = useState(false);
  const [selected, setSelected] = useState<AuditExportBatch | null>(null);
  const request = { organizationId: org.id };
  const q = useQuery({ queryKey: ["audit-export", org.id], queryFn: () => api.getAuditExport(request) });
  const history = useInfiniteQuery({ queryKey: ["audit-export-batches", org.id], initialPageParam: "", queryFn: ({ pageParam }) => api.listAuditExportBatches({ ...request, pageToken: pageParam }), getNextPageParam: (page) => page.nextPageToken || undefined });
  const refresh = () => { void queries.invalidateQueries(); };
  const save = useMutation({ mutationFn: (v: Record<string, string>) => api.saveAuditExport({ ...request, endpoint: v.endpoint, region: v.region, bucket: v.bucket, accessKey: v.accessKey, secretKey: v.secretKey, sessionToken: v.sessionToken }), onSuccess: () => { setEditing(false); refresh(); } });
  const test = useMutation({ mutationFn: () => api.testAuditExport(request), onSuccess: refresh });
  const toggle = useMutation({ mutationFn: (enabled: boolean) => api.setAuditExportEnabled({ ...request, enabled }), onSuccess: refresh });
  const retry = useMutation({ mutationFn: () => api.retryAuditExport(request), onSuccess: refresh });
  const config = q.data;
  const manage = org.permissions.includes("audit.export.manage");
  const batches = history.data?.pages.flatMap((p) => p.batches) ?? [];
  const active = batches.some((b) => b.status === "pending" || b.status === "running");
  const current = batches.find((b) => b.id === selected?.id) ?? selected;
  return <>
    <PageHeader eyebrow="DURABLE AUDIT HISTORY" title="Audit export" description="Continuously copy audit events to your S3-compatible storage, with verified checksums and automatic retries.">{manage && <button className="primary" onClick={() => { save.reset(); setEditing(true); }}>Configure storage</button>}</PageHeader>
    <ErrorNote error={q.error || history.error || test.error || toggle.error || retry.error} />
    <section className="panel">
      <div className="panel-heading"><h2>{q.isPending ? "Loading export status…" : !config?.configured ? "Storage not configured" : config.enabled ? "Continuous export enabled" : "Export paused"}</h2></div>
      <div className="panel-body">
      {config && <>
        <dl className="resource-details">{Object.entries({ Storage: config.configured ? `${config.endpoint} / ${config.bucket}` : "Not configured", Verification: config.verified ? "Storage probe passed" : "Storage probe required", "Pending events": String(config.pendingEvents), "Oldest pending event": config.oldestPendingAt || "None", "Verified through event": config.cursor }).map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}</dl>
        {config.detail && <p className="notice">{config.detail}</p>}
        {(config.pendingEvents > 10000n || (config.oldestPendingAt && Date.now() - Date.parse(config.oldestPendingAt) > 86400000)) && <p role="alert" className="notice">The backlog exceeds 10,000 events or 24 hours. Check storage access and local disk capacity.</p>}
        {manage && config.configured && <div className="resource-actions">
          <button className="secondary" disabled={config.enabled || active || test.isPending} onClick={() => test.mutate()}>Test storage</button>
          <button className="secondary" disabled={toggle.isPending || (!config.enabled && !config.verified)} onClick={() => toggle.mutate(!config.enabled)}>{config.enabled ? "Pause export" : "Enable export"}</button>
          <button className="secondary" disabled={!batches.some((b) => b.status === "pending") || retry.isPending} onClick={() => retry.mutate()}>Retry now</button>
        </div>}
      </>}
      <p className="muted">Testing writes and reads a small probe object. Pausing prevents further queued work; an upload already in flight may finish. Local audit events are retained during outages.</p></div>
    </section>
    <section className="panel"><div className="panel-heading"><h2>Export history</h2></div><DataTable label="Audit export batches" data={batches} rowId={(b) => b.id} columns={[
      { id: "batch", header: "Batch", cell: ({ row }) => <button onClick={() => setSelected(row.original)}>{row.original.kind === "test" ? "Storage probe" : `${row.original.firstId}–${row.original.lastId}`}</button> },
      { accessorKey: "eventCount", header: "Events" }, { accessorKey: "status", header: "Status" }, { accessorKey: "attempts", header: "Attempts" }, { accessorKey: "updatedAt", header: "Updated" },
    ]} />{!batches.length && <p className="loading">{history.isPending ? "Loading batches…" : "No export batches yet."}</p>}{history.hasNextPage && <button disabled={history.isFetchingNextPage} onClick={() => void history.fetchNextPage()}>Load more batches</button>}</section>
    <Modal open={editing} onOpenChange={setEditing} title="Configure audit storage" description="Credentials are encrypted on the server and never returned to the browser.">
      {editing && <><p className="notice">Saving pauses export, requires a fresh storage test, and starts again from retained history. Use a dedicated bucket or restrict credentials to this organization’s providah-audit prefix.</p><ActionForm fields={[
        { name: "endpoint", label: "S3 endpoint", defaultValue: config?.endpoint, placeholder: "https://s3.us-east-1.amazonaws.com", schema: z.url(), description: "Public HTTPS service root on port 443." },
        { name: "region", label: "Storage region", defaultValue: config?.region || "us-east-1" },
        { name: "bucket", label: "Bucket name", defaultValue: config?.bucket, schema: z.string().min(3).max(63) },
        { name: "accessKey", label: "Access key", type: "password", autoComplete: "off", schema: z.string().min(3).max(256) },
        { name: "secretKey", label: "Secret key", type: "password", autoComplete: "new-password", schema: z.string().min(3).max(4096) },
        { name: "sessionToken", label: "Session token (optional)", type: "password", autoComplete: "off", schema: z.string().max(8192) },
      ]} onSubmit={(v) => save.mutateAsync(v)} submitLabel="Save storage" pending={save.isPending} error={save.error} /></>}
    </Modal>
    <Modal open={!!current} onOpenChange={(open) => { if (!open) setSelected(null); }} title="Export batch" description="Stable object identity and checksum are retained after delivery.">{current && <dl className="resource-details">{Object.entries({ Status: current.status, Detail: current.detail, "Object key": current.objectKey, "SHA-256": current.checksum, "First event": current.firstId, "Last event": current.lastId, Attempts: current.attempts }).map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}</dl>}</Modal>
  </>;
}
