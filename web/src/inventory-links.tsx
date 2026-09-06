import {create,fromJsonString,toJsonString} from "@bufbuild/protobuf";
import {Link} from "react-router";
import {InventoryViewSpecSchema} from "./gen/providah/v1/console_pb";
import type {InventorySettings} from "./inventory-views";
const columns=["name","provider","kind","region","status","observedAt"];
export function readInventoryLink(params:URLSearchParams){
 const raw=params.get("filters");if(raw===null)return undefined;
 if(params.getAll("filters").length!==1||params.has("view")||("/app/resources?"+params).length>2048)throw new Error("Invalid or oversized inventory link.");
 const spec=fromJsonString(InventoryViewSpecSchema,raw);
 if(spec.hiddenColumns.length>=columns.length||new Set(spec.hiddenColumns).size!==spec.hiddenColumns.length||spec.hiddenColumns.some(c=>!columns.includes(c)))throw new Error("Invalid inventory columns.");
 return spec;
}
export function InventoryLink({org,spec}:{org:string;spec:InventorySettings}){
 const params=new URLSearchParams({org,filters:toJsonString(InventoryViewSpecSchema,create(InventoryViewSpecSchema,spec))});
 const href="/app/resources?"+params;
 return href.length<=2048?<Link className="secondary" to={href}>Link to current filters</Link>:<span className="muted">These filters are too large for a link. Use a saved view.</span>;
}
