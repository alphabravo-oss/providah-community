import {z} from "zod";
import type {AutomationInput} from "./gen/providah/v1/console_pb";
import type {FormField} from "./ui";

export function inputFields(inputs:AutomationInput[]):FormField[]{
 return inputs.map(f=>({name:f.name,label:f.label,type:f.type==="integer"?"number":"select",options:f.type==="integer"?undefined:[{value:"",label:"Choose a value"},...(f.type==="boolean"?["true","false"]:f.choices).map(value=>({value,label:value}))],description:f.type==="integer"?`Whole number from ${f.min} to ${f.max}`:"Allowed by the published source",schema:z.string().refine(v=>f.type==="integer"?v.trim()!==""&&Number.isInteger(Number(v))&&Number(v)>=Number(f.min)&&Number(v)<=Number(f.max):f.type==="boolean"?["true","false"].includes(v):f.choices.includes(v),"Choose an allowed value")}));
}
export function inputValues(inputs:AutomationInput[],values:Record<string,string>):string{
 return JSON.stringify(Object.fromEntries(inputs.map(f=>[f.name,f.type==="integer"?Number(values[f.name]):f.type==="boolean"?values[f.name]==="true":values[f.name]])));
}
