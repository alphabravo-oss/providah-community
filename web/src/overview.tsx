import {Link} from "react-router";
import {useQuery} from "@tanstack/react-query";
import {api} from "./api";
import {DataTable,ErrorNote,PageHeader} from "./ui";
import {actionLabel,statuses} from "./operations";
import {ScanStatus,type Operation,type Organization} from "./gen/providah/v1/console_pb";

function OperationList({org,rows,label}:{org:string;rows:Operation[];label:string}){
 return rows.length?<DataTable label={label} data={rows} rowId={o=>o.id} columns={[
 {id:"operation",header:"Operation",cell:({row:{original:o}})=><Link to={`/app/operations?org=${org}&operation=${o.id}`}>{actionLabel(o.action,o.resourceKind)??o.action} · {o.resourceName}</Link>},
 {id:"status",header:"State",cell:({row:{original:o}})=>statuses[o.status]},
 {id:"updated",header:"Updated",cell:({row:{original:o}})=>new Date(o.updatedAt).toLocaleString()}
 ]}/>:<p className="notice">No matching operations.</p>;
}
export function OperationsOverview({org}:{org:Organization}){
 const allowed=(permission:string)=>org.permissions.includes(permission);
 const c=useQuery({queryKey:["connections",org.id],queryFn:()=>api.listConnections({organizationId:org.id}),enabled:allowed("connections.read")});
 const o=useQuery({queryKey:["operations-overview",org.id],queryFn:()=>api.getOperationsOverview({organizationId:org.id}),enabled:allowed("operations.read"),refetchInterval:30000});
 const s=useQuery({queryKey:["schedules",org.id],queryFn:()=>api.listSchedules({organizationId:org.id}),enabled:allowed("schedules.read"),refetchInterval:30000});
 const a=useQuery({queryKey:["audit",org.id,"recent"],queryFn:()=>api.listAudit({organizationId:org.id}),enabled:allowed("audit.read")});
 const connections=c.isError?[]:c.data?.connections??[];
 const unhealthy=connections.filter(c=>c.enabled&&(c.scanStatus===ScanStatus.FAILED||!c.lastScanAt));
 const upcoming=s.isError?[]:(s.data?.schedules??[]).filter(s=>s.enabled&&s.nextDue).sort((a,b)=>a.nextDue.localeCompare(b.nextDue)).slice(0,5);
 const admin=allowed("admin.access");
 return <>
 <PageHeader eyebrow="Your organization" title="Operations overview" description="Approvals, recent problems and upcoming work within your current access.">
 {admin&&allowed("connections.manage")&&<Link className="primary" to={`/admin/connections?org=${org.id}`}>Add connection</Link>}
 </PageHeader>
 <div className="row-actions">
 {allowed("resources.read")&&<><Link to={`/app/resources?org=${org.id}`}>Open inventory</Link><Link to={`/app/dashboards?org=${org.id}`}>Open dashboards</Link></>}
 {allowed("operations.read")&&<Link to={`/app/operations?org=${org.id}`}>Review operations</Link>}
 </div>
 {allowed("operations.read")&&<>
 <ErrorNote error={o.error}/>{o.isPending?<p>Loading operation overview…</p>:o.isError?null:o.data&&<>
 <div className="stats">{[["Awaiting approval",o.data.pending],["Active operations",o.data.active],["Failed operations",o.data.failed],["Uncertain outcomes",o.data.uncertain]].map(([label,total])=><div className="stat" key={String(label)}><span>{String(label)}</span><strong>{String(total)}</strong><small>All recorded operations in this organization</small></div>)}</div>
 <div className="dashboard-grid"><section className="panel"><div className="panel-heading"><h2>Pending approvals</h2><p>Ten most recently updated requests</p></div><OperationList org={org.id} rows={o.data.approvals} label="Pending approvals"/></section>
 <section className="panel"><div className="panel-heading"><h2>Recent failures and uncertainty</h2><p>Review uncertain outcomes before retrying</p></div><OperationList org={org.id} rows={o.data.problems} label="Recent operation problems"/></section></div>
 </>}
 </>}
 <div className="dashboard-grid">
 {allowed("schedules.read")&&<section className="panel"><div className="panel-heading"><h2>Upcoming schedules</h2><Link to={`/app/schedules?org=${org.id}`}>View schedules</Link></div><ErrorNote error={s.error}/>
 {s.isPending?<p className="notice">Loading schedules…</p>:s.isError?null:upcoming.length?<DataTable label="Upcoming schedules" data={upcoming} rowId={s=>s.id} columns={[
 {id:"name",header:"Schedule",cell:({row:{original:s}})=><Link to={`/app/schedules?org=${org.id}&schedule=${s.id}`}>{s.name}</Link>},
 {accessorKey:"nextLocal",header:"Next local time / offset"},{accessorKey:"nextAction",header:"Action"},
 {id:"readiness",header:"Readiness",cell:({row:{original:s}})=>!s.identityEnabled?"Identity disabled":!s.approved?"Awaiting approval":s.nextSkip||"Approved"}
 ]}/>:<p className="notice">No upcoming enabled schedules.</p>}</section>}
 {allowed("connections.read")&&<section className="panel"><div className="panel-heading"><h2>Connection health</h2>{admin&&<Link to={`/admin/connections?org=${org.id}`}>Manage connections</Link>}</div><ErrorNote error={c.error}/>
 {c.isPending?<p className="notice">Loading connections…</p>:c.isError?null:<><p className="notice">{connections.length} connections · {connections.filter(c=>c.enabled).length} enabled · {unhealthy.length} need a successful refresh</p>
 {unhealthy.length?<DataTable label="Connections needing attention" data={unhealthy.slice(0,5)} rowId={c=>c.id} columns={[
 {accessorKey:"name",header:"Connection"},{accessorKey:"provider",header:"Provider"},
 {id:"health",header:"Discovery",cell:({row:{original:c}})=>c.scanStatus===ScanStatus.FAILED?"Last refresh failed":"No successful refresh"},
 {id:"last",header:"Last success",cell:({row:{original:c}})=>c.lastScanAt?new Date(c.lastScanAt).toLocaleString():"Never"}
 ]}/>:<p className="notice">No enabled connections with failed or missing discovery. This does not verify live cloud health.</p>}</>}
 </section>}
 </div>
 {allowed("audit.read")&&<section className="panel"><div className="panel-heading"><h2>Recent activity</h2>{admin&&<Link to={`/admin/audit?org=${org.id}`}>View audit log</Link>}</div><ErrorNote error={a.error}/>
 {a.isPending?<p className="notice">Loading activity…</p>:a.isError?null:a.data?.events.length?<DataTable label="Recent activity" data={a.data.events.slice(0,5)} rowId={e=>e.id} columns={[
 {accessorKey:"action",header:"Event"},{accessorKey:"actor",header:"Actor"},{id:"when",header:"Time",cell:({row:{original:e}})=>new Date(e.occurredAt).toLocaleString()}
 ]}/>:<p className="notice">No recorded activity.</p>}</section>}
 {!org.permissions.some(p=>["connections.read","operations.read","schedules.read","audit.read"].includes(p))&&<p className="notice">Use the available navigation to open your workspace tools.</p>}
 </>;
}
