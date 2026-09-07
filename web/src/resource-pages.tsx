import {useState} from "react";
import {Link,useOutletContext,useSearchParams} from "react-router";
import {useQuery} from "@tanstack/react-query";
import {api} from "./api";
import {CreateResourceButton} from "./creation";
import {ErrorNote,PageHeader} from "./ui";
import type {Organization} from "./gen/providah/v1/console_pb";

export const resourceKinds: Record<string,string> = { "storage.bucket":"Object storage buckets", "network.route_table":"Route tables", "network.internet_gateway":"Internet gateways", "network.nat_gateway":"NAT gateways", "organization.project":"Cloud projects", "application.app":"Applications", "dns.zone":"DNS zones", "dns.record":"DNS records / sets", "database.cluster":"Database clusters", "database.instance":"Database instances", "database.snapshot":"Database snapshots", "database.cluster_snapshot":"Database cluster snapshots", "kubernetes.node_group":"Kubernetes node groups", "kubernetes.cluster":"Kubernetes clusters", "network.certificate":"Certificates", "network.subnet":"Subnets", "network.ip":"Elastic / reserved IPv4", "network.reserved_ipv6":"Reserved IPv6", "network.primary_ip":"Primary IPs", "network.floating_ip":"Floating IPs", "compute.placement_group":"Placement groups", "compute.image":"Owned images", "access.ssh_key":"SSH keys", "compute.server":"Servers", "network.network":"Networks / VPCs", "network.firewall":"Firewalls / security groups", "storage.volume":"Volumes", "storage.snapshot":"Snapshots", "storage.backup":"Backups", "network.load_balancer":"Load balancers" };
export const resourceGroups:Record<string,string[]>={
  Compute:["compute.server","compute.placement_group"],
  Networking:["network.network","network.subnet","network.firewall","network.load_balancer","network.ip","network.primary_ip","network.floating_ip","network.reserved_ipv6","network.route_table","network.internet_gateway","network.nat_gateway","network.certificate"],
  Storage:["storage.volume","storage.snapshot","compute.image","storage.backup","storage.bucket"],
  Access:["access.ssh_key"],
  Databases:["database.instance","database.cluster","database.snapshot","database.cluster_snapshot"],
  Containers:["kubernetes.cluster","kubernetes.node_group","application.app"],
  DNS:["dns.zone","dns.record"],
  Projects:["organization.project"],
};
export const resourcePath=(kind:string)=>"/app/resources/"+kind.replace(".","/");
export function ResourceOverview(){
  const {org}=useOutletContext<{org:Organization}>();
  const [params,setParams]=useSearchParams();
  const provider=params.get("provider")??"";
  const [showEmpty,setShowEmpty]=useState(false);
  const q=useQuery({queryKey:["resource-summary",org.id,provider,"kind"],queryFn:()=>api.getResourceSummary({organizationId:org.id,filters:{provider},groupBy:"kind"})});
  const counts=new Map(q.data?.groups.map(g=>[g.label,Number(g.total)]));
  const scope=new URLSearchParams({org:org.id,...(provider?{provider}:{})}).toString();
  return <><PageHeader eyebrow="YOUR CLOUD INFRASTRUCTURE" title="Resources" description="Browse what you own by resource type.">
    <Link className="button secondary" to={"/app/resources/all?"+scope}>View all resources</Link><CreateResourceButton org={org}/>
  </PageHeader>
  <div className="filters"><label>Cloud provider <select aria-label="Filter provider" value={provider} onChange={e=>setParams(p=>{p.set("org",org.id);if(e.target.value)p.set("provider",e.target.value);else p.delete("provider");return p;})}>
    <option value="">All clouds</option><option value="aws">Amazon Web Services</option><option value="digitalocean">DigitalOcean</option><option value="hetzner">Hetzner Cloud</option>
  </select></label><label><input type="checkbox" checked={showEmpty} onChange={e=>setShowEmpty(e.target.checked)}/> Show empty types</label></div>
  <ErrorNote error={q.error}/>
  {q.isPending?<p role="status">Loading resource counts…</p>:q.data&&<>
    <p className="muted">{Number(q.data.total).toLocaleString()} resources across {provider?"this provider":"your clouds"}.</p>
    {Object.entries(resourceGroups).map(([group,kinds])=>{
      const visible=kinds.filter(kind=>showEmpty||(counts.get(kind)??0)>0);
      return visible.length>0&&<section className="resource-section" key={group} aria-label={group}><h2>{group}</h2><div className="resource-type-grid">
        {visible.map(kind=><Link className="resource-type-card" key={kind} to={resourcePath(kind)+"?"+scope}><span>{resourceKinds[kind]}</span><strong>{(counts.get(kind)??0).toLocaleString()}</strong><span className="muted">View {resourceKinds[kind].toLowerCase()} →</span></Link>)}
      </div></section>;
    })}
    {!showEmpty&&q.data.total===0n&&<p>No resources discovered. Enable a cloud connection or show empty types to browse the available pages.</p>}
  </>}
  </>;
}
