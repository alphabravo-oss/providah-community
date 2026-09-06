import {useQuery} from "@tanstack/react-query";
import {api} from "./api";
import {DataTable,ErrorNote,PageHeader} from "./ui";
const size=(bytes:bigint)=>`${(Number(bytes)/1024**3).toFixed(2)} GiB`;
const names:Record<string,string>={operation:"Cloud operations",scan:"Discovery",delivery:"Notifications",validation:"Automation validation",audit_export:"Audit export"};
export function InstallationHealth(){
 const q=useQuery({queryKey:["installation-health"],queryFn:()=>api.getInstallationHealth({}),refetchInterval:30000});
 return <><PageHeader title="Global health" eyebrow="Installation" description="Current console workload across all organizations, including disabled scopes."><button onClick={()=>void q.refetch()}>Refresh health</button></PageHeader>
 <ErrorNote error={q.error}/>{q.isPending?<p>Loading health…</p>:q.isError?null:q.data&&<>
 <p className="muted">Observed {new Date(q.data.observedAt).toLocaleString()} · Refreshes every 30 seconds.</p>
 <section className="panel"><div className="panel-heading"><h2>Console services</h2></div><p>Database reachable · {q.data.databaseConnections} of {q.data.databaseCapacity} pool connections</p><p>Provider execution {q.data.providerExecutionConfigured?"configured":"not configured"}</p></section>
 {q.data.storage&&<section className="panel" aria-label="Database storage"><div className="panel-heading"><h2>Database storage</h2></div>
 <p>{size(q.data.storage.usedBytes)} allocated · {size(q.data.storage.auditBytes)} in audit history (included)</p>
 {q.data.storage.budgetBytes>0n?<p>{size(q.data.storage.budgetBytes)} budget · {(Number(q.data.storage.usedBytes)/Number(q.data.storage.budgetBytes)*100).toFixed(1)}% used</p>:<p className="notice">No database storage budget configured. Capacity warnings are unavailable.</p>}
 {["warning","critical"].includes(q.data.storage.status)&&<p className="notice" role="alert">{q.data.storage.status==="critical"?"Critical":"Warning"}: database allocation is at or above {q.data.storage.status==="critical"?"90":"80"}% of its budget. Review capacity and audit export backlog.</p>}
 <p className="muted">The budget is an operator-set warning threshold, not a disk quota. These sizes exclude external object storage, backups and PostgreSQL WAL. Monitor actual disk free space separately.</p>
 </section>}
 <section className="panel"><div className="panel-heading"><h2>Work queues</h2></div><p className="muted">Age is measured since creation, not time in the current state. Dead notifications are retained failures. This view does not probe cloud resources or external service health.</p>
 <DataTable label="Installation workload" data={q.data.work} rowId={r=>r.kind+":"+r.status} columns={[
 {id:"kind",header:"Work",cell:({row:{original:r}})=>names[r.kind]??r.kind},
 {accessorKey:"status",header:"State"},{id:"count",header:"Count",cell:({row:{original:r}})=>r.count.toString()},
 {id:"age",header:"Oldest age",cell:({row:{original:r}})=>`${Math.floor(r.oldestSeconds/3600)}h ${Math.floor(r.oldestSeconds%3600/60)}m`}
 ]}/>{!q.data.work.length&&<p>No queued or attention-required work.</p>}</section>
 </>}</>;
}
