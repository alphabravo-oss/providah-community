import assert from "node:assert/strict";
import {readFileSync} from "node:fs";
import {gzipSync} from "node:zlib";

const manifest=JSON.parse(readFileSync(new URL("../dist/.vite/manifest.json",import.meta.url),"utf8"));
const entry=Object.keys(manifest).find(key=>manifest[key].isEntry);
assert(entry,"Missing production entry");
const initial=new Set();
function visit(key){
 assert(manifest[key],`Missing chunk ${key}`);
 if(initial.has(key))return;
 initial.add(key);
 for(const dependency of manifest[key].imports??[])visit(dependency);
}
visit(entry);
const pages=["templates","access","schedules","notifications","modules","maintenance","audit_export","identity-policy","operations-page"];
for(const page of pages){
 const key=`src/${page}.tsx`;
 assert(manifest[key]?.isDynamicEntry,`Page must be deferred: ${page}`);
 assert(!initial.has(key),`Page loaded eagerly: ${page}`);
}
const files=[...initial].map(key=>readFileSync(new URL("../dist/"+manifest[key].file,import.meta.url)));
console.log(JSON.stringify({initialJsBytes:files.reduce((sum,b)=>sum+b.length,0),initialJsGzipBytes:files.reduce((sum,b)=>sum+gzipSync(b).length,0),deferredPageChunks:pages.length}));
