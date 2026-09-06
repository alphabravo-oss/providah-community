import {useState} from "react";
import {create} from "@bufbuild/protobuf";
import {z} from "zod";
import {ActionForm,Modal} from "./ui";
import {TagConditionSchema,type TagCondition} from "./gen/providah/v1/console_pb";
export const emptyTags={tagKey:"",tagValue:"",tagName:"",tagExists:false,tagConditions:[] as TagCondition[],tagMatchAny:false};
export function TagConditions({value,onChange}:{value:typeof emptyTags;onChange:(v:typeof emptyTags)=>void}){
 const [open,setOpen]=useState(false),[conditions,setConditions]=useState<TagCondition[]>([]),[any,setAny]=useState(false);
 return <><button className="secondary" onClick={()=>{setConditions(value.tagConditions.length?value.tagConditions:value.tagKey||value.tagName?[create(TagConditionSchema,{key:value.tagKey,value:value.tagValue,name:value.tagName,exists:value.tagExists})]:[]);setAny(value.tagMatchAny);setOpen(true);}}>Combine tags</button>
 {!!value.tagConditions.length&&<p className="notice">Match {value.tagMatchAny?"any":"all"} of {value.tagConditions.length} tag conditions <button onClick={()=>onChange(emptyTags)}>Clear tag conditions</button></p>}
 <Modal open={open} onOpenChange={setOpen} title="Combine tag conditions" description="Match exact labels or named tags. These conditions replace the single-tag filter and do not change permissions.">
 <label>Match<select aria-label="Match tag conditions" value={any?"any":"all"} onChange={e=>setAny(e.target.value==="any")}><option value="all">All conditions</option><option value="any">Any condition</option></select></label>
 <ol>{conditions.map((c,i)=><li key={i}>{c.name?`Tag: ${c.name}`:`${c.key}${c.exists?" exists":` = ${c.value}`}`} <button aria-label={`Remove condition ${i+1}`} onClick={()=>setConditions(v=>v.filter((_,n)=>n!==i))}>Remove</button></li>)}</ol>
 {conditions.length<8&&<ActionForm fields={[
 {name:"mode",label:"Condition type",type:"select",defaultValue:"equals",options:[{value:"equals",label:"Label equals"},{value:"exists",label:"Label key exists"},{value:"name",label:"Named tag present"}]},
 {name:"key",label:"Condition label key",when:{name:"mode",is:["equals","exists"]},schema:z.string().min(1).max(256)},
 {name:"value",label:"Condition label value",when:{name:"mode",is:["equals"]},schema:z.string().max(2048)},
 {name:"name",label:"Condition named tag",when:{name:"mode",is:["name"]},schema:z.string().min(1).max(256)}
 ]} submitLabel="Add condition" onSubmit={v=>setConditions(items=>[...items,create(TagConditionSchema,{key:v.mode==="name"?"":v.key,value:v.mode==="equals"?v.value:"",name:v.mode==="name"?v.name:"",exists:v.mode==="exists"})])}/>}
 <button className="primary" onClick={()=>{onChange({...emptyTags,tagConditions:conditions,tagMatchAny:conditions.length>0&&any});setOpen(false);}}>Apply conditions</button>
 </Modal></>;
}
