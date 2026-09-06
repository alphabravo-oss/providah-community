import { useState } from "react";
import { Link, useSearchParams, useOutletContext } from "react-router";
import { useInfiniteQuery, useMutation, useQuery } from "@tanstack/react-query";
import { z } from "zod";
import { api, queries } from "./api";
import { ActionForm, DataTable, ErrorNote, Field, Modal, PageHeader, type FormField } from "./ui";
import type { Organization, Schedule } from "./gen/providah/v1/console_pb";

function ScheduleEditor({ org, schedule, done }: { org: Organization; schedule?: Schedule; done: () => void }) {
  const [search, setSearch] = useState("");
  const [values, setValues] = useState<Record<string, string>>({});
  const [draft, setDraft] = useState<Record<string, string> | null>(null);
  const resources = useInfiniteQuery({
    queryKey: ["schedule-targets", org.id, search], initialPageParam: "",
    queryFn: ({ pageParam }) => api.listResources({ kind: "compute.server", organizationId: org.id, search, pageToken: pageParam, pageSize: 100 }),
    getNextPageParam: (p) => p.nextPageToken || undefined,
  });
  const spec = (v: Record<string, string>) => ({
    timezone: v.timezone, action: v.mode === "window" ? "window" : v.action,
    cron: v.mode === "once" ? "" : v.cron, stopCron: v.mode === "window" ? v.stopCron : "",
    runAt: v.mode === "once" ? v.runAt : "", exceptions: v.exceptions.split(/[\s,]+/).filter(Boolean),
  });
  const preview = useMutation({ mutationFn: (v: Record<string, string>) => api.previewSchedule({ organizationId: org.id, spec: spec(v), resourceIds: v.targets.split(",").filter(Boolean) }), onSuccess: (_, v) => { setValues(v); setDraft(v); } });
  const save = useMutation({ mutationFn: () => api.saveSchedule({ organizationId: org.id, id: schedule?.id, expectedRevision:schedule?.revision??0n, name: draft!.name, resourceIds: draft!.targets.split(",").filter(Boolean), spec: spec(draft!) }), onSuccess: () => { void queries.invalidateQueries(); done(); } });
  const options = new Map((resources.data?.pages.flatMap((p) => p.resources) ?? []).filter((r) => r.kind === "compute.server").map((r) => [r.id, { value: r.id, label: `${r.name} · ${r.provider} / ${r.region} · ${r.nativeId}` }]));
  for (const id of schedule?.resourceIds ?? []) if (!options.has(id)) options.set(id, { value: id, label: `Selected resource · ${id}` });
  return <>
    <p className="notice">Each schedule uses a scoped automation identity and requires another person's standing approval. Missed runs are skipped. Editing requires fresh approval.</p>
    {draft ? <>
      <p className="notice">For resources managed by Terraform/OpenTofu, a later apply may undo scheduled power changes.</p>
      <p>{draft.name} · {draft.targets.split(",").filter(Boolean).length} exact targets · {draft.timezone}</p>
      <DataTable label="Schedule preview" data={preview.data?.occurrences ?? []} rowId={(o) => o.due + o.local + o.action} columns={[
        { accessorKey: "local", header: "Local time / offset" }, { accessorKey: "action", header: "Action" }, { accessorKey: "skippedReason", header: "Skip reason" }, { accessorKey: "maintenanceRestrictions", header: "Maintenance restrictions" },
      ]} />
      <ErrorNote error={save.error} />
      <div className="resource-actions"><button disabled={save.isPending} onClick={() => setDraft(null)}>Back to edit</button><button className="primary" disabled={save.isPending} onClick={() => save.mutate()}>Save for approval</button></div>
    </> : <>
      <Field label="Find server targets"><input value={search} onChange={(e) => setSearch(e.target.value)} placeholder="Search inventory" /></Field>
      <ErrorNote error={resources.error} />
      {resources.hasNextPage && <button disabled={resources.isFetchingNextPage} onClick={() => void resources.fetchNextPage()}>Load more targets</button>}
      <ActionForm fields={([
        { name: "name", label: "Schedule name", defaultValue: schedule?.name, schema: z.string().min(1).max(120) },
        { name: "targets", label: "Exact server targets", type: "checks", defaultValue: schedule?.resourceIds.join(","), options: [...options.values()], schema: z.string().min(1, "Choose at least one server.").refine((v) => v.split(",").length <= 50, "Choose at most 50 servers.") },
        { name: "mode", label: "Schedule type", type: "select", defaultValue: schedule?.spec?.runAt ? "once" : schedule?.spec?.action === "window" ? "window" : "recurring", options: [{ value: "recurring", label: "Recurring action" }, { value: "once", label: "One-time action" }, { value: "window", label: "Startup / shutdown window" }] },
        { name: "timezone", label: "IANA timezone", defaultValue: schedule?.spec?.timezone ?? Intl.DateTimeFormat().resolvedOptions().timeZone, description: "For example, America/New_York or UTC." },
        { name: "action", label: "Action", type: "select", defaultValue: schedule?.spec?.action === "window" ? "start" : schedule?.spec?.action ?? "start", options: [{ value: "start", label: "Start" }, { value: "shutdown", label: "Graceful shutdown" }, { value: "restart", label: "Restart" }], when: { name: "mode", is: ["once", "recurring"] } },
        { name: "cron", label: "Recurring action / startup rule", defaultValue: schedule?.spec?.cron ?? "0 8 * * 1-5", description: "Five cron fields: minute, hour, day, month, weekday. This example runs weekdays at 08:00 in your timezone.", when: { name: "mode", is: ["recurring", "window"] } },
        { name: "stopCron", label: "Shutdown rule", defaultValue: schedule?.spec?.stopCron ?? "0 18 * * 1-5", when: { name: "mode", is: ["window"] } },
        { name: "runAt", label: "Local date and time", type: "datetime-local", defaultValue: schedule?.spec?.runAt, when: { name: "mode", is: ["once"] } },
        { name: "exceptions", label: "Exception dates", type: "textarea", defaultValue: schedule?.spec?.exceptions.join("\n"), schema: z.string(), description: "Optional YYYY-MM-DD dates, separated by spaces or newlines; runs on these local dates are skipped." },
      ] satisfies FormField[]).map((f) => ({ ...f, defaultValue: values[f.name] ?? f.defaultValue }))} onSubmit={(v) => preview.mutateAsync(v)} submitLabel="Preview upcoming runs" pending={preview.isPending} error={preview.error} />
    </>}
  </>;
}

export function SchedulesPage() {
  const { org } = useOutletContext<{ org: Organization }>();
  const [editing, setEditing] = useState<Schedule | "new" | null>(null);
  const [params,setParams]=useSearchParams();
  const selected=params.get("schedule")??"";
  const setSelected=(id:string)=>setParams(previous=>{const next=new URLSearchParams(previous);if(id)next.set("schedule",id);else next.delete("schedule");return next;});
  const detail=useQuery({queryKey:["schedule",org.id,selected],queryFn:()=>api.getSchedule({organizationId:org.id,id:selected}),enabled:!!selected});
  const page=params.get("occurrence_page")??"", historySchedule=params.get("history_schedule")??"", outcome=params.get("outcome")??"";
 const historyParam=(key:string,value:string)=>setParams(p=>{if(value)p.set(key,value);else p.delete(key);if(key!=="occurrence_page")p.delete("occurrence_page");return p;});
 const setPage=(value:string)=>historyParam("occurrence_page",value);
  const q = useQuery({ queryKey: ["schedules", org.id], queryFn: () => api.listSchedules({ organizationId: org.id }) });
  const history = useQuery({ queryKey: ["schedule-occurrences", org.id, historySchedule, outcome, page], queryFn: () => api.listScheduleOccurrences({ organizationId: org.id, pageToken: page, scheduleId:historySchedule, outcome }) });
  const current = detail.error?undefined:detail.data?.schedule;
  const manage = org.permissions.includes("schedules.manage");
  const refresh = () => queries.invalidateQueries();
  const approve = useMutation({ mutationFn: () => api.approveSchedule({expectedRevision:current!.revision, organizationId: org.id, id: current!.id }), onSuccess: refresh });
  const toggle = useMutation({ mutationFn: (identity: boolean) => api.setScheduleEnabled({expectedRevision:current!.revision, organizationId: org.id, id: current!.id, enabled: identity ? current!.enabled : !current!.enabled, identityEnabled: identity ? !current!.identityEnabled : current!.identityEnabled }), onSuccess: refresh });
  const remove = useMutation({ mutationFn: (v: Record<string, string>) => api.deleteSchedule({expectedRevision:current!.revision, organizationId: org.id, id: current!.id, confirmation: v.confirmation }), onSuccess: () => { setSelected(""); void refresh(); } });
  return <>
    <PageHeader eyebrow="TIME, TARGETS, APPROVAL" title="Schedules" description="Plan server actions with explicit targets and visible run history.">{manage && <button className="primary" onClick={() => setEditing("new")}>Create schedule</button>}</PageHeader>
    <ErrorNote error={q.error || history.error} />
    <section className="panel"><DataTable label="Schedules" data={q.data?.schedules ?? []} rowId={(s) => s.id} columns={[
      { id: "name", header: "Schedule", cell: ({ row }) => <button onClick={() => setSelected(row.original.id)}>{row.original.name}</button> },
      { id: "state", header: "State", cell: ({ row: { original: s } }) => !s.enabled ? "Paused" : !s.identityEnabled ? "Identity disabled" : !s.approved ? "Awaiting approval" : "Enabled" },
      { accessorKey: "nextLocal", header: "Next local time / offset" }, { accessorKey: "nextAction", header: "Action" }, { accessorKey: "lastDetail", header: "Last dispatch" },
    ]} />{!q.data?.schedules.length && <p className="loading">{q.isPending ? "Loading schedules…" : "No schedules yet."}</p>}</section>
    <h2>Occurrence history</h2>
    <div className="filters"><select aria-label="History schedule" value={historySchedule} onChange={e=>historyParam("history_schedule",e.target.value)}><option value="">All schedules</option>{historySchedule&&!q.data?.schedules.some(s=>s.id===historySchedule)&&<option value={historySchedule}>Schedule {historySchedule.slice(0,8)}</option>}{q.data?.schedules.map(s=><option key={s.id} value={s.id}>{s.name}</option>)}</select><select aria-label="Dispatch outcome" value={outcome} onChange={e=>historyParam("outcome",e.target.value)}><option value="">All dispatch outcomes</option><option value="queued">Queued</option><option value="skipped">Skipped</option></select></div>
    <section className="panel"><DataTable label="Schedule occurrences" data={history.error?[]:history.data?.occurrences ?? []} rowId={(o) => o.id} columns={[
      {id:"schedule",header:"Schedule",cell:({row})=><button onClick={()=>historyParam("history_schedule",row.original.scheduleId)}>{q.data?.schedules.find(s=>s.id===row.original.scheduleId)?.name??row.original.scheduleId.slice(0,8)}</button>},
 { accessorKey: "resourceName", header: "Resource" }, { id: "localTime", header: "Local occurrence", cell: ({ row }) => row.original.localTime || "Legacy occurrence" }, { accessorKey: "scheduledFor", header: "Scheduled time" }, { accessorKey: "action", header: "Action" },
      {accessorKey:"outcome",header:"Dispatch"}, {id:"operation",header:"Operation",cell:({row})=>row.original.operationId&&org.permissions.includes("operations.read")?<Link to={`/app/operations?org=${org.id}&operation=${row.original.operationId}`}>{row.original.operationStatus||"View operation"}</Link>:row.original.operationStatus||"—"}, { accessorKey: "detail", header: "Detail" },
    ]} /></section>
    <div className="pagination">{page && <button onClick={() => setPage("")}>Most recent</button>}{!history.error && history.data?.nextPageToken && <button onClick={() => setPage(history.data!.nextPageToken)}>Older occurrences</button>}</div>
    <Modal open={!!editing} onOpenChange={(v) => { if (!v) setEditing(null); }} title={editing === "new" ? "Create schedule" : "Edit schedule"} description="Preview timezone-aware run times before saving.">{editing && <ScheduleEditor org={org} schedule={editing === "new" ? undefined : editing} done={() => setEditing(null)} />}</Modal>
    <Modal open={!!selected} onOpenChange={(v) => { if (!v) setSelected(""); }} title={current?.name ?? "Schedule"} description="Standing approval covers this exact revision, target set, and automation identity.">
      <nav className="breadcrumbs" aria-label="Schedule breadcrumb"><Link to={"/app/schedules?org="+org.id}>Schedules</Link><span aria-hidden="true">›</span><span aria-current="page">{current?.name||"Schedule details"}</span></nav>
      <ErrorNote error={detail.error}/>{detail.isPending&&<p role="status">Loading schedule…</p>}
      {current && <>
      <dl className="resource-details">{Object.entries({ Timezone: current.spec?.timezone, Action: current.spec?.action, Rule: current.spec?.cron || current.spec?.runAt, "Shutdown rule": current.spec?.stopCron, Exceptions: current.spec?.exceptions.join(", "), Targets: current.resourceIds.join(", "), "Automation identity": current.identityId, Editor: current.editor, "Next occurrence": current.nextLocal, "Next skip reason": current.nextSkip, "Last dispatch": current.lastDetail }).map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value || "—"}</dd></div>)}</dl>
      <ErrorNote error={approve.error || toggle.error} />
      {current.canApprove && !current.approved && org.permissions.includes("operations.approve") && <button className="primary" disabled={approve.isPending} onClick={() => approve.mutate()}>Approve this schedule revision</button>}
      {manage && <><div className="resource-actions"><button onClick={() => { setEditing(current); setSelected(""); }}>Edit schedule</button><button disabled={toggle.isPending} onClick={() => toggle.mutate(false)}>{current.enabled ? "Pause schedule" : "Resume schedule"}</button><button disabled={toggle.isPending} onClick={() => toggle.mutate(true)}>{current.identityEnabled ? "Disable automation identity" : "Enable automation identity"}</button></div>
      <ActionForm key={current.id} fields={[{ name: "confirmation", label: "Type the schedule name to remove it", schema: z.literal(current.name) }]} onSubmit={(v) => remove.mutateAsync(v)} submitLabel="Remove schedule" pending={remove.isPending} error={remove.error} /></>}
    </>}</Modal>
  </>;
}
