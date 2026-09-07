import {test,expect} from "@playwright/test";
import {readFileSync} from "node:fs";
const source=readFileSync(new URL("../src/resource-pages.tsx",import.meta.url),"utf8");
const kinds:Record<string,string>=JSON.parse(source.match(/export const resourceKinds[^=]*= (\{.*?\});/)![1]);
test("resource overview, every type and detail page, and confirmed server actions",async({page})=>{
  test.setTimeout(90000);
  const org={id:"a".repeat(64),name:"Test organization",permissions:["resources.read","operations.request","operations.delete","operations.read"]};
  let kind="compute.server",state="running",requests=0;
  const resource=()=>({id:"b".repeat(64),connectionId:"c".repeat(64),nativeId:"test-resource",name:"Test resource",provider:"hetzner",kind,region:"fsn1",status:state});
  await page.route("**/api/**",route=>{
    const method=route.request().url().split("/").pop();
    if(method==="GetSession")return route.fulfill({json:{email:"test@example.test",organizations:[org]}});
    if(method==="GetResourceSummary")return route.fulfill({json:{total:"1",groups:[{label:"compute.server",total:"1"}]}});
    if(method==="ListResources"){
      const body=route.request().postDataJSON();
      if(body.kind)expect(body.kind).toBe(kind);
      return route.fulfill({json:{resources:[resource()]}});
    }
    if(method==="GetResource")return route.fulfill({json:{resource:resource(),providerEnabled:true,connectionEnabled:true,availableActions:kind==="compute.server"?["start","shutdown","restart","delete"]:[]}});
    if(method==="RequestOperation")requests++;
    return route.fulfill({json:{}});
  });
  await page.goto(`/app/resources?org=${org.id}`);
  await expect(page.getByRole("heading",{name:"Resources",exact:true})).toBeVisible();
  await page.getByLabel("Show empty types").check();
  await expect(page.locator(".resource-type-card")).toHaveCount(Object.keys(kinds).length);
  await page.getByLabel("Filter provider").selectOption("hetzner");
  await page.locator('.resource-type-card[href*="compute/server"]').click();
  await expect(page.getByLabel("Filter provider")).toHaveValue("hetzner");
  for(const [value,label] of Object.entries(kinds)){
    kind=value;
    await page.goto(`/app/resources/${kind.replace(".","/")}?org=${org.id}`);
    await expect(page.getByRole("heading",{level:1})).toHaveText(label);
    await expect(page.getByLabel("Resource page")).toHaveValue(kind);
    await page.getByRole("link",{name:"Test resource",exact:true}).click();
    await expect(page).toHaveURL(new RegExp(`/app/resources/detail/${resource().id}`));
    await expect(page.getByRole("heading",{level:1})).toHaveText("Test resource");
    await expect(page.getByRole("dialog")).toHaveCount(0);
  }
  kind="compute.server";
  await page.reload();
  for(const name of ["Graceful shutdown","Restart","Delete resource"]){
    await page.getByRole("button",{name,exact:true}).click();
    await expect(page.getByRole("dialog")).toContainText("test-resource");
    await page.getByRole("button",{name:"Close",exact:true}).click();
  }
  state="off";await page.reload();
  await page.getByRole("button",{name:"Start",exact:true}).click();
  await expect(page.getByRole("dialog")).toContainText("test-resource");
  expect(requests).toBe(0);
});
