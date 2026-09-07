import {test,expect} from "@playwright/test";
test("organization approval modes and confirmation-only operation",async({page})=>{
 const org={id:"a".repeat(64),name:"Solo development",permissions:["resources.read","operations.read","operations.request","roles.manage","admin.access"]};
 let policy={creationEnabled:true,revision:"1",approvalActions:["create","shutdown","restart","resize","snapshot","tags","delete"]};
 let requests=0;
 await page.route("**/api/**",route=>{
  const method=route.request().url().split("/").pop();
  if(method==="GetSession")return route.fulfill({json:{email:"solo@example.test",organizations:[org]}});
  if(method==="GetResourcePolicy")return route.fulfill({json:policy});
  if(method==="SaveResourcePolicy"){
   const body=route.request().postDataJSON();expect(body.expectedRevision).toBe(policy.revision);
   policy={creationEnabled:body.creationEnabled,revision:String(Number(policy.revision)+1),approvalActions:body.approvalActions??[]};return route.fulfill({json:policy});
  }
  if(method==="GetResource")return route.fulfill({json:{resource:{id:"b".repeat(64),nativeId:"123",name:"Solo server",kind:"compute.server",provider:"hetzner",status:"running"},providerEnabled:true,connectionEnabled:true,availableActions:["restart"]}});
  if(method==="RequestOperation"){requests++;expect(route.request().postDataJSON().action).toBe("restart");return route.fulfill({json:{operation:{status:"OPERATION_STATUS_QUEUED"}}});}
  return route.fulfill({json:{}});
 });
 await page.goto(`/admin/resource-policy?org=${org.id}`);
 await expect(page.getByLabel("Approval mode")).toHaveValue("selected");
 for(const mode of ["all","none","selected","none"]){
  await page.getByLabel("Approval mode").selectOption(mode);
  if(mode==="selected")await page.getByLabel("Delete resources",{exact:true}).check();
  await page.getByLabel("Change reason").fill("Configure solo development approval mode");
  await page.getByRole("button",{name:"Save resource policy"}).click();
  await expect.poll(()=>policy.approvalActions.length).toBe(mode==="all"?8:mode==="selected"?1:0);
  await page.reload();await expect(page.getByLabel("Approval mode")).toHaveValue(mode);
 }
 await page.goto(`/app/resources/detail/${"b".repeat(64)}?org=${org.id}`);
 await page.getByRole("button",{name:"Restart",exact:true}).click();
 await expect(page.getByRole("dialog")).toContainText("Confirmation is the final gate");
 expect(requests).toBe(0);
 await page.getByLabel("Reason for this action").fill("Restart solo test server");
 await page.getByRole("button",{name:"Request restart",exact:true}).click();
 await expect(page).toHaveURL(new RegExp(`/app/operations\\?org=${org.id}`));
 expect(requests).toBe(1);
 await expect(page.getByRole("link",{name:"Approval settings"})).toHaveAttribute("href",`/admin/resource-policy?org=${org.id}`);
});
