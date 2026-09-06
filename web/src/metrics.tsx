import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import { ActionForm, DataTable, ErrorNote, MetricPlot, Modal } from "./ui";
import type { Organization, Resource } from "./gen/providah/v1/console_pb";

export function MetricsButton({org,resource,supported,enabled}:{org:Organization;resource:Resource;supported:boolean;enabled:boolean}) {
 const [open,setOpen]=useState(false),[hours,setHours]=useState(1),[selected,setSelected]=useState(""),[page,setPage]=useState(0);
 const query=useQuery({queryKey:["metrics",org.id,resource.id,hours],queryFn:()=>api.getResourceMetrics({organizationId:org.id,resourceId:resource.id,hours}),enabled:open,staleTime:60_000,gcTime:0,refetchOnWindowFocus:false});
 if(resource.kind!=="compute.server")return null;
 const series=query.data?.series.find(s=>s.id===selected)??query.data?.series[0];
 const points=series?.points??[];const latest=points.at(-1);
 return <><button className="secondary" disabled={!supported||!enabled} onClick={()=>setOpen(true)}>Provider metrics</button>{!supported&&<p className="muted">Metrics are unavailable in the selected provider runtime.</p>}
 <Modal wide open={open} onOpenChange={setOpen} title={`${resource.name || resource.nativeId} metrics`} description="Read provider-reported history. Missing measurements are not zero; units and aggregation differ between providers.">
  <ActionForm key={hours} fields={[{name:"hours",label:"History period",type:"select",defaultValue:String(hours),options:[{value:"1",label:"Last hour"},{value:"6",label:"Last six hours"},{value:"24",label:"Last twenty-four hours"}]}]} onSubmit={async v=>{setPage(0);if(Number(v.hours)===hours){await query.refetch();}else setHours(Number(v.hours));}} submitLabel="Read metrics" pending={query.isFetching}/>
  <ErrorNote error={query.error}/>{query.isFetching&&<p role="status">Reading provider metrics…</p>}
  {query.data&&!query.error&&<>
   <p className="muted">UTC window: {query.data.start} – {query.data.end}. Read at {query.data.fetchedAt}. The latest five-minute boundary is used; cloud publication may lag.</p>
   {!query.data.series.length?<p>No metric series were published for this interval.</p>:<>
    <label className="field"><span>Measurement</span><select aria-label="Measurement" value={series?.id??""} onChange={e=>{setSelected(e.target.value);setPage(0);}}>{query.data.series.map(s=><option key={s.id} value={s.id}>{s.name} ({s.unit})</option>)}</select></label>
    <p>{series?.aggregation} · {series?.periodSeconds ? `${series.periodSeconds}-second period` : "Provider-defined sampling interval"}. Values are shown in {series?.unit}.</p>
    {!points.length?<p className="notice">No samples were published for this measurement. This does not indicate zero utilization.</p>:<>
     <MetricPlot points={points} start={query.data.start} end={query.data.end} label={series!.name} unit={series!.unit}/>
     <p>Latest reported: <strong>{latest!.value.toLocaleString(undefined,{maximumFractionDigits:3})} {series!.unit}</strong> at {latest!.timestamp}.</p>
     <DataTable label="Metric samples" data={points.slice(page*100,(page+1)*100)} rowId={p=>p.timestamp} columns={[{accessorKey:"timestamp",header:"Timestamp (UTC)"},{id:"value",header:series!.unit,cell:({row:{original:p}})=>p.value.toLocaleString(undefined,{maximumFractionDigits:6})}]}/>
     <div className="pagination"><button disabled={page===0} onClick={()=>setPage(page-1)}>Previous samples</button><span>{page+1} / {Math.max(1,Math.ceil(points.length/100))}</span><button disabled={(page+1)*100>=points.length} onClick={()=>setPage(page+1)}>Next samples</button></div>
    </>}
   </>}
  </>}
 </Modal></>;
}
