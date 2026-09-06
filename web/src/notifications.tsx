import { useEffect, useState } from "react";
import { Link, useSearchParams, useOutletContext } from "react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { z } from "zod";
import { api, queries } from "./api";
import { ActionForm, DataTable, ErrorNote, Modal, PageHeader } from "./ui";
import type { NotificationDestination, NotificationGrouping, OrganizationSmtp, Organization } from "./gen/providah/v1/console_pb";

function DestinationEditor({ org, current, platform, profile, capture, done }: { org: Organization; current?: NotificationDestination; platform: boolean; profile?:OrganizationSmtp; capture: boolean; done: () => void }) {
  const [profileRevision]=useState(profile?.revision);
  const [profileAvailable]=useState(!!profile?.configured);
  const save = useMutation({ mutationFn: (v: Record<string, string>) => api.saveNotificationDestination({
    organizationId: org.id, id: current?.id, expectedRevision:current?.revision ?? 0n, name: v.name, kind: v.mode === "webhook" ? "webhook" : "email", endpoint: v.endpoint,
    signingSecret: v.mode === "webhook" ? v.secret : "", usePlatformSmtp: v.mode === "email-platform", useOrganizationSmtp:v.mode==="email-organization", organizationSmtpRevision:v.mode==="email-organization"?profileRevision:undefined, includeSuccess: v.success === "yes",
    smtp: v.mode === "email-custom" ? { host: v.host, port: Number(v.port), sender: v.sender, username: v.username, password: v.password } : undefined,
  }), onError:()=>{void queries.invalidateQueries();}, onSuccess: () => { void queries.invalidateQueries(); done(); } });
  const modes = [...(profileAvailable?[{value:"email-organization",label:"Email with organization SMTP"}]:[]),{ value: "webhook", label: "Signed HTTPS webhook" }, { value: "email-custom", label: "Email with custom SMTP" }, ...(platform ? [{ value: "email-platform", label: "Email with installation SMTP" }] : [])];
  return <>
    <p className="notice">Secrets are write-only. Saving changes requires fresh destination verification and cancels deliveries using the previous configuration. Email also requires verification of the sender mailbox.</p>
    <ActionForm fields={[
      { name: "name", label: "Destination name", defaultValue: current?.name, schema: z.string().min(1).max(120) },
      { name: "mode", label: "Delivery method", type: "select", defaultValue: current?.kind === "email" ? (platform ? "email-platform" : "email-custom") : "webhook", options: current ? modes.filter((m) => (m.value === "webhook") === (current.kind === "webhook")) : modes },
      { name: "endpoint", label: capture ? "Endpoint or recipient email" : "HTTPS endpoint or recipient email", defaultValue: current?.endpoint, description: capture ? "Local webhook: http://webhookie:8080/hooks/generic/default. Installation SMTP delivers to Mailpit." : "Public destinations only. Webhooks use port 443 without query parameters. SMTP requires TLS on port 465 or 587." },
      { name: "secret", label: "Webhook signing secret", type: "password", autoComplete: "new-password", description: "Provide 32–64 base64-encoded random bytes, optionally prefixed with whsec_. Configure the same secret in your receiver.", when: { name: "mode", is: ["webhook"] } },
      { name: "host", label: "SMTP hostname", when: { name: "mode", is: ["email-custom"] } },
      { name: "port", label: "SMTP port", type: "select", defaultValue: "587", options: [{ value: "587", label: "587 · Required STARTTLS" }, { value: "465", label: "465 · Implicit TLS" }], when: { name: "mode", is: ["email-custom"] } },
      { name: "sender", label: "Sender email", type: "email", when: { name: "mode", is: ["email-custom"] } },
      { name: "username", label: "SMTP username", schema: z.string(), autoComplete: "off", when: { name: "mode", is: ["email-custom"] } },
      { name: "password", label: "SMTP password", type: "password", schema: z.string(), autoComplete: "new-password", when: { name: "mode", is: ["email-custom"] } },
      { name: "success", label: "Successful operation and validation notices", type: "select", defaultValue: current?.includeSuccess ? "yes" : "no", options: [{ value: "no", label: "Off · actionable events only" }, { value: "yes", label: "Include successes" }] },
    ]} onSubmit={(v) => save.mutateAsync(v)} submitLabel="Save destination" error={save.error} pending={save.isPending} />
  </>;
}

function OrganizationSmtpControl({org}:{org:Organization}) {
 const q=useQuery({queryKey:["organization-smtp",org.id],queryFn:()=>api.getOrganizationSmtp({organizationId:org.id})});
 const [editing,setEditing]=useState<OrganizationSmtp>();
 const save=useMutation({gcTime:0,retry:false,mutationFn:(v:Record<string,string>)=>api.setOrganizationSmtp({organizationId:org.id,expectedRevision:editing!.revision,remove:v.mode==="remove",smtp:v.mode==="remove"?undefined:{host:v.host,port:Number(v.port),sender:v.sender,username:v.username,password:v.password}}),onSuccess:()=>{setEditing(undefined);save.reset();void queries.invalidateQueries();}});
 return <><ErrorNote error={q.error}/><button disabled={!q.data||q.isError} onClick={()=>{save.reset();setEditing(q.data);}}>Organization SMTP</button>
 <Modal open={!!editing} onOpenChange={open=>{if(!open){setEditing(undefined);save.reset();}}} title="Organization SMTP" description="Shared, encrypted settings for newly saved email destinations.">{editing&&<>
 <p className="notice">{editing.configured?"Configured":"Not configured"}. Secrets are write-only. Replace settings by entering the complete configuration. Existing destinations keep their saved settings; replace each destination configuration and verify its sender and recipient to rotate it. Saving this profile sends no message.</p>
 <ActionForm fields={[
 {name:"mode",label:"Profile action",type:"select",defaultValue:"save",options:[{value:"save",label:"Save replacement settings"},...(editing.configured?[{value:"remove",label:"Remove shared profile"}]:[])]},
 {name:"host",label:"SMTP hostname",when:{name:"mode",is:["save"]}},
 {name:"port",label:"SMTP port",type:"select",defaultValue:"587",options:[{value:"587",label:"587 · Required STARTTLS"},{value:"465",label:"465 · Implicit TLS"}],when:{name:"mode",is:["save"]}},
 {name:"sender",label:"Sender email",when:{name:"mode",is:["save"]}},
 {name:"username",label:"SMTP username",schema:z.string(),when:{name:"mode",is:["save"]}},
 {name:"password",label:"SMTP password",type:"password",autoComplete:"new-password",schema:z.string(),when:{name:"mode",is:["save"]}},
 {name:"confirm",label:"Confirm organization name",schema:z.literal(org.name),when:{name:"mode",is:["remove"]}}
 ]} onSubmit={v=>save.mutateAsync(v)} submitLabel="Save organization SMTP" pending={save.isPending} error={save.error}/>
 </>}</Modal></>;
}

function SubscriptionsEditor({org,current,events,done}:{org:Organization;current:NotificationDestination;events:string[];done:()=>void}) {
 const save=useMutation({mutationFn:(v:Record<string,string>)=>api.setNotificationSubscriptions({organizationId:org.id,id:current.id,expectedRevision:current.revision,eventTypes:v.mode==="custom"?v.events.split(",").filter(Boolean):[]}),onSuccess:()=>{void queries.invalidateQueries();done();}});
 return <><p className="notice">Choose events for this destination. Custom selections replace the default event set, including the success preference. Changes cancel pending deliveries from the previous revision; sent messages cannot be recalled. Verification and secrets are preserved.</p>
 <ActionForm fields={[
 {name:"mode",label:"Event selection",type:"select",defaultValue:current.eventTypes.length?"custom":"default",options:[{value:"default",label:"Default actionable events"},{value:"custom",label:"Selected events"}]},
 {name:"events",label:"Subscribed events",type:"checks",defaultValue:current.eventTypes.join(","),options:events.map(value=>({value,label:value})),schema:z.string().min(1,"Select at least one event, or use defaults."),when:{name:"mode",is:["custom"]}}
 ]} onSubmit={v=>save.mutateAsync(v)} submitLabel="Save subscriptions" error={save.error} pending={save.isPending}/></>;
}

function NotificationGroupingControl({org}:{org:Organization}) {
 const q=useQuery({queryKey:["notification-grouping",org.id],queryFn:()=>api.getNotificationGrouping({organizationId:org.id})});
 const [editing,setEditing]=useState<NotificationGrouping|null>(null);
 const save=useMutation({mutationFn:(v:Record<string,string>)=>api.setNotificationGrouping({organizationId:org.id,seconds:Number(v.seconds),expectedRevision:editing!.revision}),onSuccess:()=>{setEditing(null);void queries.invalidateQueries({queryKey:["notification-grouping",org.id]});}});
 return <><ErrorNote error={q.error}/><button disabled={!q.data||q.isError} onClick={()=>{setEditing(q.data!);save.reset();}}>Alert grouping</button>
 <Modal open={!!editing} onOpenChange={open=>{if(!open)setEditing(null);}} title="Alert grouping" description="Controls repeated alerts across this organization's destinations.">
 <p>Send the first event for each event type and target within a fixed time window. Further matches are suppressed. Window boundaries can produce alerts close together. Every event can increase delivery volume. Existing deliveries, subscriptions, tests and verification are unchanged; saving starts fresh grouping.</p>
 {editing&&<ActionForm fields={[{name:"seconds",label:"Grouping window",type:"select",defaultValue:String(editing.seconds),options:[{value:"0",label:"Every event"},{value:"60",label:"1 minute"},{value:"300",label:"5 minutes"},{value:"900",label:"15 minutes (default)"},{value:"3600",label:"60 minutes"}]}]} onSubmit={v=>save.mutateAsync(v)} submitLabel="Save grouping" pending={save.isPending} error={save.error}/>}
 </Modal></>;
}

export function NotificationsPage() {
  const { org } = useOutletContext<{ org: Organization }>();
  const manage = org.permissions.includes("notifications.manage"), read = org.permissions.includes("notifications.read");
  const [removing,setRemoving]=useState<NotificationDestination|null>(null);
  const [subscriptions,setSubscriptions]=useState<NotificationDestination|null>(null);
  const [editing, setEditing] = useState<NotificationDestination | "new" | null>(null);
  const [params,setParams]=useSearchParams();
  const deliveryId=params.get("delivery")??"",destinationId=deliveryId?"":params.get("destination")??"";
  const closeRecord=()=>setParams(previous=>{const next=new URLSearchParams(previous);next.delete("destination");next.delete("delivery");return next;});
  const destinationDetail=useQuery({queryKey:["notification-destination",org.id,destinationId],queryFn:()=>api.getNotificationDestination({organizationId:org.id,id:destinationId}),enabled:manage&&!!destinationId});
  const deliveryDetail=useQuery({queryKey:["notification-delivery",org.id,deliveryId],queryFn:()=>api.getNotificationDelivery({organizationId:org.id,id:deliveryId}),enabled:read&&!!deliveryId});
  const [page, setPage] = useState("");
  const q = useQuery({ queryKey: ["notification-destinations", org.id], queryFn: () => api.listNotificationDestinations({ organizationId: org.id }), enabled: manage });
  const history = useQuery({ queryKey: ["notification-deliveries", org.id, page], queryFn: () => api.listNotificationDeliveries({ organizationId: org.id, pageToken: page }), enabled: read });
  const attempts = useQuery({ queryKey: ["notification-attempts", org.id, deliveryId], queryFn: () => api.listNotificationAttempts({ organizationId: org.id, id: deliveryId }), enabled: read && !!deliveryId });
  const current=manage&&!destinationDetail.isError?destinationDetail.data:undefined;
  const currentDelivery=read&&!deliveryDetail.isError?deliveryDetail.data:undefined;
  const refresh = () => queries.invalidateQueries();
  useEffect(()=>{test.reset();verify.reset();toggle.reset();redeliver.reset();setEditing(null);setSubscriptions(null);setRemoving(null);},[destinationId,deliveryId,org.id]);
  const remove=useMutation({mutationFn:(v:Record<string,string>)=>api.deleteNotificationDestination({organizationId:org.id,id:removing!.id,expectedRevision:removing!.revision,confirmation:v.confirmation}),onSuccess:()=>{setRemoving(null);closeRecord();void refresh();}});
  const test = useMutation({ mutationFn: (verification: boolean) => api.sendNotificationTest({ organizationId: org.id, id: current!.id, verification }), onSuccess: refresh });
  const verify = useMutation({ mutationFn: (v: Record<string, string>) => api.verifyNotificationDestination({ organizationId: org.id, id: current!.id, code: v.code, senderCode: v.senderCode }), onSuccess: refresh });
  const toggle = useMutation({ mutationFn: () => api.setNotificationDestinationEnabled({ organizationId: org.id, id: current!.id, enabled: !current!.enabled, expectedRevision:current!.revision }), onError:refresh, onSuccess: refresh });
  const redeliver = useMutation({ mutationFn: () => api.redeliverNotification({ organizationId: org.id, id: currentDelivery!.id }), onSuccess: refresh });
  return <>
    <PageHeader eyebrow="EMAIL AND SIGNED WEBHOOKS" title="Notifications" description="Verified destinations, durable delivery attempts, and visible failures.">{manage && <button className="primary" onClick={() => setEditing("new")}>Add destination</button>}</PageHeader>
    <ErrorNote error={q.error || history.error} />
    {manage&&<><OrganizationSmtpControl key={"smtp"+org.id} org={org}/><NotificationGroupingControl key={org.id} org={org}/></>}
    {manage && <section className="panel"><DataTable label="Notification destinations" data={q.isError?[]:q.data?.destinations ?? []} rowId={(d) => d.id} columns={[
      { id: "name", header: "Destination", cell: ({ row }) => <Link to={`/admin/notifications?org=${org.id}&destination=${row.original.id}`}>{row.original.name}</Link> },
      { id:"events",header:"Events",cell:({row})=>row.original.eventTypes.length?`${row.original.eventTypes.length} selected`:row.original.includeSuccess?"Defaults + successes":"Defaults" },
      { accessorKey: "kind", header: "Method" }, { accessorKey: "endpoint", header: "Address" },
      { id: "status", header: "Status", cell: ({ row }) => !row.original.enabled ? "Disabled" : row.original.verified ? "Verified" : "Verification required" },
    ]} />{!q.data?.destinations.length && <p className="loading">{q.isPending ? "Loading destinations…" : "Add a destination to receive actionable event notices."}</p>}</section>}
    {read && <><h2>Delivery history</h2><section className="panel"><DataTable label="Notification deliveries" data={history.isError?[]:history.data?.deliveries ?? []} rowId={(d) => d.id} columns={[
      { id: "destination", header: "Destination", cell: ({ row }) => <Link to={`/admin/notifications?org=${org.id}&delivery=${row.original.id}`}>{row.original.destinationName}</Link> },
      { accessorKey: "eventType", header: "Event" }, { accessorKey: "status", header: "Status" }, { accessorKey: "attempts", header: "Attempts" }, { accessorKey: "detail", header: "Result" },
    ]} /></section><div className="pagination">{page && <button onClick={() => setPage("")}>Most recent</button>}{history.data?.nextPageToken && <button onClick={() => setPage(history.data!.nextPageToken)}>Older deliveries</button>}</div></>}
    {q.data?.developmentCapture&&<p className="notice">Local testing is on. <a href="http://localhost:18025" target="_blank" rel="noopener noreferrer">Open Mailpit</a> · <a href="http://localhost:18080" target="_blank" rel="noopener noreferrer">Open Webhookie</a>. Installation email goes to Mailpit; use http://webhookie:8080/hooks/generic/default for captured webhooks.</p>}
    <Modal open={!!removing} onOpenChange={v=>{if(!v)setRemoving(null);}} title="Remove notification destination" description="Delivery history is retained. This destination cannot be restored.">{removing&&<>
      <p className="notice">Removes {removing.name}, its address, stored credentials and verification codes. Pending deliveries are canceled. Messages already in flight may still arrive. Backups have their own retention.</p>
      <ActionForm fields={[{name:"confirmation",label:"Confirm destination name",schema:z.literal(removing.name)}]} onSubmit={v=>remove.mutateAsync(v)} submitLabel="Remove destination permanently" error={remove.error} pending={remove.isPending}/>
    </>}</Modal>
    <Modal open={!!subscriptions} onOpenChange={v=>{if(!v)setSubscriptions(null);}} title="Event subscriptions" description="Select which operational events reach this destination.">{subscriptions&&<SubscriptionsEditor org={org} current={subscriptions} events={q.data?.availableEventTypes??[]} done={()=>{setSubscriptions(null);closeRecord();}}/>}</Modal>
    <Modal open={!!editing} onOpenChange={(v) => { if (!v) setEditing(null); }} title={editing === "new" ? "Add notification destination" : "Replace destination configuration"} description="Use an existing SMTP service or a Standard Webhooks receiver.">{editing && <DestinationEditor org={org} current={editing === "new" ? undefined : editing} capture={q.data?.developmentCapture??false} platform={q.data?.platformSmtpAvailable ?? false} profile={q.data?.organizationSmtp} done={() => {setEditing(null);closeRecord();}} />}</Modal>
    <Modal open={!!destinationId&&!editing&&!subscriptions&&!removing} onOpenChange={(v) => { if (!v) closeRecord(); }} title={current?.name ?? "Destination"} description="Only verified, enabled destinations receive operational events.">
      <nav className="breadcrumbs" aria-label="Destination breadcrumb"><Link to={`/admin/notifications?org=${org.id}`}>Notifications</Link><span aria-hidden="true">›</span><span aria-current="page">{current?.name??"Destination"}</span></nav>
      <ErrorNote error={destinationDetail.error}/>{!manage?<p className="notice">Destination unavailable.</p>:destinationDetail.isPending&&<p role="status">Loading destination…</p>}
      {current && <>
      <p>{current.endpoint}</p><p className="notice">{current.verified ? "Verified" : "Verification required"} · {current.enabled ? "Enabled" : "Disabled"}. Changes invalidate pending deliveries. A test sends a real message to the configured address.</p>
      <ErrorNote error={test.error || toggle.error} />
      {test.isSuccess && <p role="status">Test queued. Check delivery history and the receiver for the result.</p>}
      <div className="resource-actions"><button onClick={()=>{setRemoving(current);remove.reset();}}>Remove destination</button><button onClick={()=>{setSubscriptions(current);}}>Event subscriptions</button><button disabled={test.isPending || !current.enabled} onClick={() => test.mutate(!current.verified)}>{current.verified ? "Send test notification" : "Send verification"}</button><button disabled={toggle.isPending} onClick={() => toggle.mutate()}>{current.enabled ? "Disable destination" : "Enable destination"}</button><button onClick={() => { setEditing(current); }}>Replace configuration / rotate secret</button></div>
      {!current.verified && <ActionForm fields={[
        { name: "code", label: "Destination verification code", description: "Copy the code received by the webhook or recipient mailbox.", autoComplete: "off" },
        ...(current.kind === "email" ? [{ name: "senderCode", label: "Sender verification code", description: "Copy the separate code received by the sender mailbox.", autoComplete: "off" }] : []),
      ]} onSubmit={(v) => verify.mutateAsync(v)} submitLabel="Verify destination" error={verify.error} pending={verify.isPending} />}
    </>}</Modal>
    <Modal open={!!deliveryId} onOpenChange={(v) => { if (!v) closeRecord(); }} title="Delivery attempts" description="Delivery is at least once and unordered. Receiver acceptance does not prove downstream work completed.">
      <nav className="breadcrumbs" aria-label="Delivery breadcrumb"><Link to={`/admin/notifications?org=${org.id}`}>Notifications</Link><span aria-hidden="true">›</span><span aria-current="page">{currentDelivery?.destinationName??"Delivery"}</span></nav>
      <ErrorNote error={deliveryDetail.error}/>{!read?<p className="notice">Delivery unavailable.</p>:deliveryDetail.isPending&&<p role="status">Loading delivery…</p>}
      {currentDelivery && <>
      <p>Event: {currentDelivery.eventId}</p><p>Status: {currentDelivery.status}</p>
      <ErrorNote error={attempts.error || redeliver.error} />
      <DataTable label="Delivery attempts" data={attempts.isError?[]:attempts.data?.attempts ?? []} rowId={(a) => a.id} columns={[{ accessorKey: "number", header: "Attempt" }, { accessorKey: "status", header: "Status" }, { accessorKey: "responseCode", header: "Response code" }, { accessorKey: "detail", header: "Result" }]} />
      {manage && ["delivered", "dead", "canceled"].includes(currentDelivery.status) && currentDelivery.eventType !== "notification.verification" && <button className="primary" disabled={redeliver.isPending} onClick={() => redeliver.mutate()}>Redeliver same event</button>}
    </>}</Modal>
  </>;
}
