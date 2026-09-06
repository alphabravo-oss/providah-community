import { useState } from "react";
import { useOutletContext } from "react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { z } from "zod";
import { api, queries } from "./api";
import { ActionForm, DataTable, ErrorNote, Modal, PageHeader } from "./ui";
import type { MaintenancePolicy, Organization } from "./gen/providah/v1/console_pb";

function PolicyEditor({ org, current, done }: { org: Organization; current?: MaintenancePolicy; done: () => void }) {
  const connections = useQuery({ queryKey: ["connections", org.id], queryFn: () => api.listConnections({ organizationId: org.id }), enabled: org.permissions.includes("connections.read") });
  const save = useMutation({ mutationFn: (v: Record<string, string>) => api.saveMaintenancePolicy({
    organizationId: org.id, id: current?.id, name: v.name, timezone: v.timezone, cron: v.cron, durationMinutes: Number(v.duration),
    exceptions: v.exceptions.split(/[\s,]+/).filter(Boolean), connectionIds: v.scope === "connections" ? v.connections.split(",").filter(Boolean) : [],
  }), onSuccess: () => { void queries.invalidateQueries(); done(); } });
  return <>
    <p className="notice">Every applicable policy must be open. Saving or removing a policy requires fresh review of previously approved work across this organization. Separate cron start times within one policy are alternatives; separate policies intersect.</p>
    <ErrorNote error={connections.error} />
    <ActionForm fields={[
      { name: "name", label: "Policy name", defaultValue: current?.name, schema: z.string().min(1).max(120) },
      { name: "scope", label: "Policy scope", type: "select", defaultValue: current?.connectionIds.length ? "connections" : "organization", options: [{ value: "organization", label: "Entire organization" }, { value: "connections", label: "Selected connections" }] },
      { name: "connections", label: "Connections covered", type: "checks", defaultValue: current?.connectionIds.join(","), options: connections.data?.connections.map((c) => ({ value: c.id, label: c.name + " · " + c.provider })), when: { name: "scope", is: ["connections"] } },
      { name: "timezone", label: "IANA timezone", defaultValue: current?.spec?.timezone ?? Intl.DateTimeFormat().resolvedOptions().timeZone },
      { name: "cron", label: "Window start rule", defaultValue: current?.spec?.cron ?? "0 22 * * 1-5", description: "Five cron fields: minute, hour, day, month, weekday. This example opens at 22:00 on weekdays." },
      { name: "duration", label: "Window duration in minutes", type: "number", defaultValue: String(current?.durationMinutes ?? 120), schema: z.string().regex(/^\d+$/).refine((v) => Number(v) >= 1 && Number(v) <= 1440, "Use 1–1,440 minutes."), description: "Elapsed time from each start. Overnight windows are supported; the end instant is excluded." },
      { name: "exceptions", label: "Closed local dates", type: "textarea", defaultValue: current?.spec?.exceptions.join("\n"), schema: z.string(), description: "Optional YYYY-MM-DD dates separated by spaces or newlines. The entire local date is closed, including overnight windows." },
    ]} onSubmit={(v) => save.mutateAsync(v)} submitLabel="Save maintenance policy" error={save.error} pending={save.isPending} />
  </>;
}

export function MaintenancePage() {
  const { org } = useOutletContext<{ org: Organization }>();
  const [editing, setEditing] = useState<MaintenancePolicy | "new" | null>(null);
  const [selected, setSelected] = useState<MaintenancePolicy | null>(null);
  const q = useQuery({ queryKey: ["maintenance", org.id], queryFn: () => api.listMaintenancePolicies({ organizationId: org.id }) });
  const current = q.data?.policies.find((p) => p.id === selected?.id) ?? selected;
  const manage = org.permissions.includes("maintenance.manage");
  const remove = useMutation({ mutationFn: (v: Record<string, string>) => api.deleteMaintenancePolicy({ organizationId: org.id, id: current!.id, confirmation: v.confirmation }), onSuccess: () => { setSelected(null); void queries.invalidateQueries(); } });
  return <>
    <PageHeader eyebrow="SHARED OPERATING RULES" title="Maintenance windows" description="Manual and scheduled actions must satisfy every applicable policy at dispatch.">{manage && <button className="primary" onClick={() => setEditing("new")}>Add maintenance policy</button>}</PageHeader>
    <ErrorNote error={q.error} />
    <section className="panel"><DataTable label="Maintenance policies" data={q.data?.policies ?? []} rowId={(p) => p.id} columns={[
      { id: "name", header: "Policy", cell: ({ row }) => <button onClick={() => { setSelected(row.original); remove.reset(); }}>{row.original.name}</button> },
      { id: "scope", header: "Scope", cell: ({ row }) => row.original.connectionIds.length ? row.original.connectionIds.length + " connections" : "Organization" },
      { id: "state", header: "When checked", cell: ({ row }) => row.original.openNow ? "Open" : "Closed" },
      { accessorKey: "nextStart", header: "Next start / offset" }, { accessorKey: "nextEnd", header: "Next end / offset" }, { accessorKey: "nextSkip", header: "Exception" },
    ]} />{!q.data?.policies.length && <p className="loading">{q.isPending ? "Loading policies…" : "No maintenance restrictions are configured."}</p>}</section>
    <p className="muted">The displayed state is a preview. Execution always checks the current clock and policy revision. Out-of-window exceptions require separate authority, a reason, and independent approval.</p>
    <Modal open={!!editing} onOpenChange={(v) => { if (!v) setEditing(null); }} title={editing === "new" ? "Add maintenance policy" : "Edit maintenance policy"} description="These rules restrict actions; they do not schedule work themselves.">{editing && <PolicyEditor org={org} current={editing === "new" ? undefined : editing} done={() => setEditing(null)} />}</Modal>
    <Modal open={!!current} onOpenChange={(v) => { if (!v) setSelected(null); }} title={current?.name ?? "Maintenance policy"} description="All applicable organization and connection policies must be open.">{current && <>
      <dl className="resource-details">{Object.entries({ Timezone: current.spec?.timezone, "Start rule": current.spec?.cron, Duration: current.durationMinutes + " elapsed minutes", Scope: current.connectionIds.join(", ") || "Entire organization", "Closed dates": current.spec?.exceptions.join(", "), "Next start": current.nextStart, "Next end": current.nextEnd }).map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value || "—"}</dd></div>)}</dl>
      {manage && <><button className="secondary" onClick={() => { setEditing(current); setSelected(null); }}>Edit policy</button><ActionForm fields={[{ name: "confirmation", label: "Type the policy name to remove it", schema: z.literal(current.name) }]} onSubmit={(v) => remove.mutateAsync(v)} submitLabel="Remove policy" pending={remove.isPending} error={remove.error} /></>}
    </>}</Modal>
  </>;
}
