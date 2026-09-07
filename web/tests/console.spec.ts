import { test, expect } from "@playwright/test";
import { readFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { createHmac } from "node:crypto";
async function totp(secret: string) {
  const remaining=30000-Date.now()%30000;
  if(remaining<3000)await new Promise(resolve=>setTimeout(resolve,remaining+10));
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  const bits = [...secret]
    .map((c) => alphabet.indexOf(c).toString(2).padStart(5, "0"))
    .join("");
  const key = Buffer.from(bits.match(/.{8}/g)!.map((b) => parseInt(b, 2)));
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(Date.now() / 30000)));
  const mac = createHmac("sha1", key).update(counter).digest();
  const offset = mac[mac.length - 1] & 15;
  return ((mac.readUInt32BE(offset) & 0x7fffffff) % 1000000)
    .toString()
    .padStart(6, "0");
}

async function accountAction(page: import("@playwright/test").Page, name: string) {
  const button=page.getByRole("button",{name,exact:true});
  if(!await button.isVisible()) await page.getByLabel("Account menu",{exact:true}).click();
  await button.click();
}
async function consolePage(page: import("@playwright/test").Page, name:string) {
 const admin=["Connections","Provider modules","Notifications","Maintenance","Audit log","Audit export","Team & access","Sign-in policy","Administration"].includes(name);
 if(admin&&!new URL(page.url()).pathname.startsWith("/admin")) await page.getByRole("link",{name:"Admin console",exact:true}).click();
 if(!admin&&new URL(page.url()).pathname.startsWith("/admin")) await page.getByRole("link",{name:"User console",exact:true}).click();
 await page.getByRole("link",{name,exact:true}).click();
 if(name==="Inventory")await page.getByRole("link",{name:"View all resources",exact:true}).click();
}
test("secure setup, connection lifecycle, audit, and responsive navigation", async ({
  page,
  browser,
}) => {
  test.setTimeout(60_000);
  const modules = new Set<string>();
  page.on("request",r=>modules.add(new URL(r.url()).pathname));
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  page.on("console",m=>{if(m.type()==="error" && m.text().includes("same key")) errors.push(m.text());});
  const env = readFileSync("../.env", "utf8");
  const token = env.match(/^BOOTSTRAP_TOKEN=(.+)$/m)![1];
  await page.goto("/");
  await page.getByLabel("Installation setup token").fill(token);
  for (const module of ["access","schedules","notifications","modules","maintenance","audit_export","identity-policy","operations-page"]) expect(modules.has(`/src/${module}.tsx`)).toBe(false);
  await page.getByRole("button", { name: "Begin secure setup" }).click();
  const secret = await page.locator(".enrollment code").textContent();
  await page.getByLabel("Organization name").fill("Acme Operations");
  await page.getByLabel("Email address").fill("browser@example.com");
  await page
    .getByLabel("Password", { exact: true })
    .fill("browser-test-password");
  await page.getByLabel("Authenticator code").fill(await totp(secret!));
  await page.getByRole("button", { name: "Create workspace" }).click();
  await expect(
    page.getByRole("heading", { name: "Operations overview" }),
  ).toBeVisible();

  const headerGeometry=await page.evaluate(()=>({
    brand:document.querySelector(".sidebar > .brand")!.getBoundingClientRect().height,
    topbar:document.querySelector(".topbar")!.getBoundingClientRect().height,
    theme:document.querySelector(".topbar-actions > button")!.getBoundingClientRect().x,
    account:document.querySelector(".account-menu")!.getBoundingClientRect().x,
  }));
  expect(headerGeometry.brand).toBe(headerGeometry.topbar);
  await page.setViewportSize({width:927,height:882});
  const aligned=await page.evaluate(()=>{const b=document.querySelector(".sidebar > .brand")!.getBoundingClientRect(),h=document.querySelector(".topbar")!.getBoundingClientRect();return b.top===h.top && b.bottom===h.bottom;});
  expect(aligned).toBe(true);
  await page.setViewportSize({width:1440,height:1000});
  expect(headerGeometry.theme).toBeLessThan(headerGeometry.account);
  await page.getByRole("button",{name:"Collapse sidebar",exact:true}).click();
  await expect(page.locator(".sidebar")).toHaveCSS("width","88px");
  await expect(page.locator(".sidebar-bottom")).toContainText("v0.1.0");
  await expect(page.getByRole("link",{name:"AlphaBravo",exact:true})).toHaveAttribute("href","https://alphabravo.io");
  await page.reload();
  await expect(page.getByRole("button",{name:"Expand sidebar",exact:true})).toBeVisible();
  await consolePage(page,"Templates");
  await expect(page.getByRole("heading",{name:"Server templates",exact:true})).toBeVisible();
  const pinned=await page.locator(".sidebar-bottom").evaluate(el=>el.getBoundingClientRect().bottom<=window.innerHeight);
  expect(pinned).toBe(true);
  await page.getByRole("button",{name:"Expand sidebar",exact:true}).click();
  await expect(page.locator(".sidebar")).toHaveCSS("width","244px");
  await expect(page.locator(".sidebar").getByRole("button",{name:"Collapse sidebar",exact:true})).toBeVisible();
  await expect(page.locator(".topbar").getByLabel("Organization",{exact:true})).toBeVisible();
  expect(await page.locator(".sidebar-toggle").evaluate(el=>el.nextElementSibling?.classList.contains("sidebar-bottom"))).toBe(true);

  const resize=page.getByRole("separator",{name:"Resize sidebar",exact:true});
  await resize.focus();await page.keyboard.press("ArrowRight");
  await expect(page.locator(".sidebar")).toHaveCSS("width","260px");
  const edge=await resize.boundingBox();
  await page.mouse.move(edge!.x+4,edge!.y+60);await page.mouse.down();await page.mouse.move(320,edge!.y+60);await page.mouse.up();
  await expect(page.locator(".sidebar")).toHaveCSS("width","320px");
  await page.reload();await expect(page.locator(".sidebar")).toHaveCSS("width","320px");
  await resize.focus();await page.keyboard.press("Home");
  await expect(page.locator(".sidebar")).toHaveCSS("width","224px");
  const resetEdge=await resize.boundingBox();
  await page.mouse.move(resetEdge!.x+4,resetEdge!.y+60);await page.mouse.down();await page.mouse.move(244,resetEdge!.y+60);await page.mouse.up();


  await page.getByRole("button",{name:"Dark appearance",exact:true}).click();
  expect(await page.locator(".app").evaluate(el=>getComputedStyle(el).backgroundColor)).toBe("rgb(16, 16, 18)");
  await consolePage(page,"Connections");
  await page
    .getByRole("button", { name: "Add connection", exact: true })
    .click();
  await page.getByLabel("Connection name").fill("Hetzner production");
  await page
    .getByLabel("API token")
    .fill("browser-test-credential-secret");
  await page.getByRole("button", { name: "Save connection" }).click();
  await expect(
    page.getByText("Hetzner production", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText("browser-test-credential-secret")).toHaveCount(0);
  await page.getByRole("button", { name: "Refresh", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText(
    "Provider discovery is not configured",
  );
  execFileSync("docker", [
    "compose",
    "exec",
    "-T",
    "db",
    "psql",
    "-U",
    "providah",
    "-d",
    "providah_browser",
    "-v",
    "ON_ERROR_STOP=1",
    "-c",
    "INSERT INTO resources(id,org_id,connection_id,native_id,name,provider,kind,region,status,public_ip,size,observed_at) SELECT repeat('a',64),org_id,id,'123','Browser test server','hetzner','compute.server','fsn1','running','192.0.2.1','cx23',now() FROM connections WHERE name='Hetzner production'; UPDATE resources SET tag_metadata=jsonb_build_object('labels',jsonb_build_object('env','production')) WHERE id=repeat('a',64)",
  ]);
  await page.getByRole("button", { name: "Disable", exact: true }).click();
  await expect(page.getByText("Disabled", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Rotate credential" }).click();
  await page
    .getByLabel("New credential")
    .fill("replacement-browser-test-secret");
  await page.getByRole("button", { name: "Replace securely" }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await consolePage(page,"Audit log");
  await expect(
    page.getByText("connection.credential_rotated", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText("replacement-browser-test-secret")).toHaveCount(
    0,
  );
  await page.getByRole("button", {name:"Filter events",exact:true}).click();
  await page.getByLabel("Action",{exact:true}).fill("connection.credential_rotated");
  await page.getByLabel("From (RFC3339)").fill("invalid time");
  await page.getByRole("button",{name:"Apply filters",exact:true}).click();
  await expect(page.getByRole("alert")).toContainText("RFC3339");
  await page.getByLabel("From (RFC3339)").fill("");
  await page.getByRole("button",{name:"Apply filters",exact:true}).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByRole("table",{name:"Events",exact:true}).getByRole("row")).toHaveCount(2);
  await expect(page.getByText("connection.credential_rotated",{exact:true})).toBeVisible();
  await page.getByRole("button",{name:"Filter events",exact:true}).click();
  await expect(page.getByLabel("Action",{exact:true})).toHaveValue("connection.credential_rotated");
  await page.getByLabel("Target",{exact:true}).fill("nonexistent-target");
  await page.getByRole("button",{name:"Apply filters",exact:true}).click();
  await expect(page.getByRole("heading",{name:"No matching events"})).toBeVisible();
  await page.getByRole("button",{name:"Clear filters",exact:true}).click();
  await expect(page.getByText("connection.credential_rotated",{exact:true})).toBeVisible();
  await consolePage(page,"Overview");
  await page.screenshot({ path: "test-results/overview.png", fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await consolePage(page,"Inventory");
  await expect(
    page.getByRole("heading", { name: "All resources" }),
  ).toBeVisible();
  const resourceHeader = page.getByRole("table",{name:"Resources",exact:true}).getByRole("columnheader",{name:/Resource/});
  await resourceHeader.getByRole("button").click();
  await expect(resourceHeader).toHaveAttribute("aria-sort","ascending");
  await resourceHeader.getByRole("button").click();
  await expect(resourceHeader).toHaveAttribute("aria-sort","descending");
  await resourceHeader.getByRole("button").click();
  await expect(resourceHeader).toHaveAttribute("aria-sort","none");
  await page.getByRole("button",{name:"Choose columns for Resources",exact:true}).click();
  await page.getByRole("checkbox",{name:"Region",exact:true}).uncheck();
  await page.getByRole("button",{name:"Apply columns",exact:true}).click();
  await expect(page.getByRole("table",{name:"Resources",exact:true}).getByRole("columnheader",{name:"Region",exact:true})).toHaveCount(0);
  await expect(page.getByRole("table",{name:"Resources",exact:true}).getByText("fsn1",{exact:true})).toHaveCount(0);
  await page.getByLabel("Search resources",{exact:true}).fill("Browser");
  await resourceHeader.getByRole("button").click();
  await expect(resourceHeader).toHaveAttribute("aria-sort","ascending");
  await page.getByRole("button",{name:"Filter tags",exact:true}).click();
  await page.getByLabel("Label key",{exact:true}).fill("env");
  await page.getByLabel("Label value",{exact:true}).fill("Production");
  await page.getByRole("button",{name:"Apply tag filter",exact:true}).click();
  await expect(page.getByRole("link",{name:"Browser test server",exact:true})).toHaveCount(0);
  await page.getByRole("button",{name:"Edit tag filter",exact:true}).click();
  await page.getByLabel("Label value",{exact:true}).fill("production");
  await page.getByRole("button",{name:"Apply tag filter",exact:true}).click();
  await expect(page.getByRole("link",{name:"Browser test server",exact:true})).toBeVisible();
  await page.screenshot({path:"test-results/tag-filter.png",fullPage:true});
  await page.getByRole("button",{name:"Combine tags",exact:true}).click();
  await page.getByLabel("Condition type",{exact:true}).selectOption("exists");
  await page.getByLabel("Condition label key",{exact:true}).fill("missing");
  await page.getByRole("button",{name:"Add condition",exact:true}).click();
  await page.getByRole("button",{name:"Apply conditions",exact:true}).click();
  await expect(page.getByRole("link",{name:"Browser test server",exact:true})).toHaveCount(0);
  await page.getByRole("button",{name:"Combine tags",exact:true}).click();
  await page.getByLabel("Match tag conditions",{exact:true}).selectOption("any");
  await page.getByRole("button",{name:"Apply conditions",exact:true}).click();
  await expect(page.getByRole("link",{name:"Browser test server",exact:true})).toBeVisible();
  await page.screenshot({path:"test-results/combined-tags.png",fullPage:true});
  await page.getByRole("button",{name:"Clear tag conditions",exact:true}).click();
  await page.getByRole("button",{name:"Filter tags",exact:true}).click();
  await page.getByLabel("Label key",{exact:true}).fill("env");
  await page.getByLabel("Label value",{exact:true}).fill("production");
  await page.getByRole("button",{name:"Apply tag filter",exact:true}).click();

  await page.getByRole("button",{name:"Scope resources",exact:true}).click();
  await page.getByRole("button",{name:"Apply resource scope",exact:true}).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await page.getByRole("button",{name:"Scope resources",exact:true}).click();
  await page.getByLabel("Account connection",{exact:true}).selectOption({label:"Hetzner production"});
  await page.getByLabel("Resource region",{exact:true}).selectOption("fsn1");
  await page.getByLabel("Resource status",{exact:true}).selectOption("running");
  await page.getByRole("button",{name:"Apply resource scope",exact:true}).click();
  await expect(page.getByRole("link",{name:"Browser test server",exact:true})).toBeVisible();
  await page.getByRole("button",{name:"Saved views",exact:true}).click();
  await page.getByRole("button",{name:"Save current view",exact:true}).click();
  await page.getByLabel("View name",{exact:true}).fill("Production inventory");
  await page.getByRole("button",{name:"Save view",exact:true}).click();
  await expect(page.getByRole("button",{name:"Production inventory",exact:true})).toBeVisible();
  await page.getByRole("button",{name:"Close",exact:true}).click();
  await page.reload();
  await expect(page.getByLabel("Search resources",{exact:true})).toHaveValue("");
  await expect(page.getByRole("table",{name:"Resources",exact:true}).getByRole("columnheader",{name:"Region",exact:true})).toBeVisible();
  await page.getByRole("button",{name:"Saved views",exact:true}).click();
  await page.getByRole("button",{name:"Production inventory",exact:true}).click();
  await expect(page.getByLabel("Search resources",{exact:true})).toHaveValue("Browser");
  await expect.poll(()=>new URL(page.url()).searchParams.get("view")).toBeTruthy();
  const savedViewURL=page.url();
  await page.reload();
  await expect(page.getByLabel("Search resources",{exact:true})).toHaveValue("Browser");
  await expect(page.getByRole("navigation",{name:"Saved view breadcrumb",exact:true})).toContainText("Production inventory");
  await expect(page.getByText("Resource scope active",{exact:false})).toBeVisible();

  await expect(page.getByText("Label: env = production",{exact:false})).toBeVisible();
  await consolePage(page,"Dashboards");
  await page.getByRole("button",{name:"Create dashboard",exact:true}).click();
  await page.getByLabel("Add saved view",{exact:true}).selectOption({label:"Production inventory"});
  await page.getByLabel("Dashboard name",{exact:true}).fill("Production dashboard");
  await page.getByRole("button",{name:"Save dashboard",exact:true}).click();
  await expect(page.getByRole("heading",{name:"Production dashboard",exact:true})).toBeVisible();
  await expect(page.getByRole("table",{name:"Production inventory resources",exact:true})).toContainText("Browser test server");
  await page.getByRole("button",{name:"Show all columns for Production inventory resources",exact:true}).click();
  await expect(page.getByRole("table",{name:"Production inventory resources",exact:true}).getByRole("columnheader",{name:"Region",exact:true})).toBeVisible();
  const dashboardURL=page.url();
  await page.reload();
  await expect(page.getByRole("table",{name:"Production inventory resources",exact:true})).toContainText("Browser test server");
  await page.getByRole("button",{name:"Edit dashboard",exact:true}).click();
  await page.getByLabel("Dashboard visibility",{exact:true}).selectOption("shared");
  await page.getByLabel("Add saved view",{exact:true}).selectOption({label:"Production inventory"});
  await page.getByRole("button",{name:"Save dashboard",exact:true}).click();
  await expect(page.getByRole("table",{name:"Production inventory resources",exact:true})).toHaveCount(2);
  await expect(page.getByText("Shared with this organization",{exact:true})).toBeVisible();
  await page.getByRole("button",{name:"Edit dashboard",exact:true}).click();
  await page.getByLabel("Widget 2 display",{exact:true}).selectOption("status");
  await page.getByRole("button",{name:"Save dashboard",exact:true}).click();
  await expect(page.getByRole("table",{name:"Production inventory summary",exact:true})).toContainText("running");
  await expect(page.getByText("1 matching resources",{exact:true})).toBeVisible();
  await page.reload();
  await expect(page.getByRole("table",{name:"Production inventory summary",exact:true})).toContainText("running");

  await page.getByRole("button",{name:"Edit dashboard",exact:true}).click();
  await page.getByLabel("Add saved view",{exact:true}).selectOption({label:"Production inventory"});
  await page.getByLabel("Widget 3 display",{exact:true}).selectOption("activity");
  await page.getByRole("button",{name:"Save dashboard",exact:true}).click();
  await expect(page.getByText("No matching operations.",{exact:true})).toBeVisible();
  await page.reload();
  await expect(page.getByText("No matching operations.",{exact:true})).toBeVisible();

  execFileSync("docker",["compose","exec","-T","db","psql","-U","providah","-d","providah_browser","-v","ON_ERROR_STOP=1","-c","INSERT INTO teams(org_id,id,name,role_id) SELECT org_id,repeat('95',32),'Dashboard operations','viewer' FROM connections WHERE name='Hetzner production'"]);
  await page.getByRole("button",{name:"Edit dashboard",exact:true}).click();
  await page.getByLabel("Dashboard visibility",{exact:true}).selectOption("teams");
  await page.getByRole("button",{name:"Save dashboard",exact:true}).click();
  await expect(page.getByText("Choose at least one team.",{exact:true})).toBeVisible();
  await page.getByRole("checkbox",{name:"Dashboard operations",exact:true}).check();
  await page.getByRole("button",{name:"Save dashboard",exact:true}).click();
  await expect(page.getByText("Shared with selected teams",{exact:true})).toBeVisible();
  await page.reload();
  await expect(page.getByText("Shared with selected teams",{exact:true})).toBeVisible();
  await page.getByRole("button",{name:"Edit dashboard",exact:true}).click();
  await expect(page.getByRole("checkbox",{name:"Dashboard operations",exact:true})).toBeChecked();
  await page.screenshot({path:"test-results/dashboard-teams.png",fullPage:true});
  await page.getByRole("button",{name:"Close",exact:true}).click();
  execFileSync("docker",["compose","exec","-T","db","psql","-U","providah","-d","providah_browser","-v","ON_ERROR_STOP=1","-c","DELETE FROM teams WHERE id=repeat('95',32)"]);
  await page.reload();
  await expect(page.getByText("Shared with selected teams",{exact:true})).toBeVisible();
  await page.getByRole("button",{name:"Edit dashboard",exact:true}).click();
  await expect(page.getByRole("checkbox",{name:"Unavailable team · 95959595",exact:true})).toBeChecked();
  await page.getByLabel("Dashboard visibility",{exact:true}).selectOption("private");
  await page.getByRole("button",{name:"Save dashboard",exact:true}).click();
  await expect(page.getByText("Private to you",{exact:true})).toBeVisible();
  await page.screenshot({path:"test-results/dashboard.png",fullPage:true});
  await page.getByRole("button",{name:"Delete dashboard",exact:true}).click();
  await expect(page.getByText("No dashboards yet.",{exact:false})).toBeVisible();
  await page.goto(dashboardURL);
  await expect(page.getByRole("alert")).toContainText("Dashboard is unavailable");
  await consolePage(page,"Inventory");
  await page.getByRole("button",{name:"Saved views",exact:true}).click();
  await page.getByRole("button",{name:"Production inventory",exact:true}).click();
  await expect(page.getByText("Resource scope active",{exact:false})).toBeVisible();
  await page.getByRole("button",{name:"Clear resource scope",exact:true}).click();
  await page.getByRole("button",{name:"Clear tag filter",exact:true}).click();
  await expect(resourceHeader).toHaveAttribute("aria-sort","ascending");
  await expect(page.getByRole("table",{name:"Resources",exact:true}).getByRole("columnheader",{name:"Region",exact:true})).toHaveCount(0);
  await page.getByRole("button",{name:"Saved views",exact:true}).click();
  await page.getByRole("button",{name:"Replace with current settings",exact:true}).click();
  await page.getByLabel("View name",{exact:true}).fill("Renamed inventory");
  await page.getByRole("button",{name:"Save view",exact:true}).click();
  await expect(page.getByRole("button",{name:"Renamed inventory",exact:true})).toBeVisible();
  await page.getByRole("button",{name:"Delete view",exact:true}).click();
  await expect(page.getByRole("heading",{name:"Saved view unavailable",exact:true})).toBeVisible();
  await expect(page.getByRole("table",{name:"Resources",exact:true})).toHaveCount(0);
  await page.goto(savedViewURL);
  await expect(page.getByRole("heading",{name:"Saved view unavailable",exact:true})).toBeVisible();
  await page.getByRole("button",{name:"Back to inventory",exact:true}).click();

  await resourceHeader.getByRole("button").click();
  await resourceHeader.getByRole("button").click();
  await page.getByLabel("Search resources",{exact:true}).fill("");

  await page.getByRole("button",{name:"Choose columns for Resources",exact:true}).click();
  await expect(page.getByRole("checkbox",{name:"Region",exact:true})).toBeChecked();
  await page.getByRole("checkbox",{name:"Region",exact:true}).uncheck();
  await page.getByRole("button",{name:"Apply columns",exact:true}).click();
  await page.getByRole("button",{name:"Choose columns for Resources",exact:true}).click();
  await expect(page.getByRole("checkbox",{name:"Region",exact:true})).not.toBeChecked();

  for (const checkbox of await page.getByRole("dialog").getByRole("checkbox").all()) await checkbox.uncheck();
  await expect(page.getByText("Keep at least one column visible.",{exact:true})).toBeVisible();
  await page.getByRole("button",{name:"Close",exact:true}).click();
  await page.getByRole("button",{name:"Show all columns for Resources",exact:true}).click();
  await expect(page.getByRole("table",{name:"Resources",exact:true}).getByRole("columnheader",{name:"Region",exact:true})).toBeVisible();
  await page
    .getByRole("link", { name: "Browser test server", exact: true })
    .click();
  await expect(page.locator("main")).toContainText("192.0.2.1");
  await expect(page.locator("main")).toContainText("Connection disabled");
  await page.screenshot({
    path: "test-results/resource-mobile.png",
    fullPage: true,
  });
  await page.goBack();
  await page.setViewportSize({ width: 1440, height: 1000 });
  execFileSync("docker", ["compose", "exec", "-T", "db", "psql", "-U", "providah", "-d", "providah_browser", "-v", "ON_ERROR_STOP=1", "-c", "INSERT INTO resources(id,org_id,connection_id,native_id,name,provider,kind,region,status,size,observed_at) SELECT repeat('2',64),org_id,id,'123/www/A','Browser DNS record','hetzner','dns.record','global','present','A · TTL 300 seconds · 2 values',now() FROM connections WHERE name='Hetzner production'"]);
  await page.reload();
  await page.getByLabel("Filter resource type").selectOption("dns.record");
  await page.getByRole("link",{name:"Browser DNS record",exact:true}).click();
  await expect(page.locator("main")).toContainText("DNS records / sets");
  await expect(page.locator("main")).toContainText("TTL 300 seconds");
  await expect(page.locator("main").getByRole("button",{name:"Delete resource",exact:true})).toHaveCount(0);
  await page.goBack();
  execFileSync("docker", ["compose", "exec", "-T", "db", "psql", "-U", "providah", "-d", "providah_browser", "-v", "ON_ERROR_STOP=1", "-c", "INSERT INTO resources(id,org_id,connection_id,native_id,name,provider,kind,region,status,size,observed_at) SELECT repeat('3',64),org_id,id,'321','Browser TLS certificate','hetzner','network.certificate','global','completed','managed · 1 domains · expires 2030-01-01T12:00:00Z',now() FROM connections WHERE name='Hetzner production'"]);
  await page.reload();
  await page.getByLabel("Filter resource type").selectOption("network.certificate");
  await page.getByRole("link",{name:"Browser TLS certificate",exact:true}).click();
  await expect(page.locator("main")).toContainText("Certificates");
  await expect(page.locator("main")).toContainText("2030-01-01T12:00:00Z");
  await expect(page.locator("main").getByRole("button",{name:"Delete resource",exact:true})).toHaveCount(0);
  await page.goBack();
  await page.evaluate(async()=>{
    const rpc=async(name:string,body:unknown)=>{
      const response=await fetch(`/api/providah.v1.ConsoleService/${name}`,{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(body)});
      if(!response.ok) throw new Error(`Database fixture setup failed: ${name}`);
      return response.json();
    };
    const session=await rpc("GetSession",{});
    const organizationId=session.organizations[0].id;
    const result=await rpc("CreateConnection",{organizationId,name:"AWS database fixture",provider:"aws",region:"us-east-1",credential:JSON.stringify({access_key_id:"test",secret_access_key:"browser-fixture"})});
    await rpc("SetConnectionEnabled",{organizationId,id:result.connection.id,enabled:false});
    const ipv6=await rpc("CreateConnection",{organizationId,name:"DO IPv6 fixture",provider:"digitalocean",region:"nyc3",credential:"fake-browser-token"});
    await rpc("SetConnectionEnabled",{organizationId,id:ipv6.connection.id,enabled:false});
  });
  execFileSync("docker",["compose","exec","-T","db","psql","-U","providah","-d","providah_browser","-v","ON_ERROR_STOP=1","-c","INSERT INTO resources(id,org_id,connection_id,native_id,name,provider,kind,region,status,size,observed_at) SELECT md5(kind)||md5(kind),org_id,id,'rds-fixture','Browser '||kind,'aws',kind,'us-east-1','available','postgres 17.4 · 40 GiB allocated',now() FROM connections CROSS JOIN unnest(ARRAY['database.instance','database.cluster','database.snapshot','database.cluster_snapshot']) kind WHERE name='AWS database fixture'"]);
  await page.reload();
  await page.route("**/providah.v1.ConsoleService/GetResource",async route=>{
    const response=await route.fetch();const body=await response.json();
    await route.fulfill({response,json:{...body,connectionEnabled:true}});
  });
  for(const [kind,label] of [["database.instance","Database instances"],["database.cluster","Database clusters"],["database.snapshot","Database snapshots"],["database.cluster_snapshot","Database cluster snapshots"]]){
    await page.getByLabel("Filter resource type").selectOption(kind);
    await page.getByRole("link",{name:`Browser ${kind}`,exact:true}).click();
    await expect(page.locator("main")).toContainText(label);
    await expect(page.locator("main")).toContainText("postgres 17.4");
    await expect(page.locator("main").getByRole("button",{name:"Start",exact:true})).toHaveCount(0);
    await expect(page.locator("main").getByRole("button",{name:"Delete resource",exact:true})).toHaveCount(kind.includes("snapshot")?1:0);
    await page.goBack();
  }

  execFileSync("docker",["compose","exec","-T","db","psql","-U","providah","-d","providah_browser","-v","ON_ERROR_STOP=1","-c","INSERT INTO resources(id,org_id,connection_id,native_id,name,provider,kind,region,status,public_ip,observed_at) SELECT repeat('d',64),org_id,id,'2001:db8::1','Browser reserved IPv6','digitalocean','network.reserved_ipv6','nyc3','assigned','2001:db8::1',now() FROM connections WHERE name='DO IPv6 fixture'"]);
  await page.reload();
  await page.getByLabel("Filter resource type").selectOption("network.reserved_ipv6");
  await page.getByRole("link",{name:"Browser reserved IPv6",exact:true}).click();
  await expect(page.locator("main")).toContainText("Reserved IPv6");
  await expect(page.locator("main")).toContainText("2001:db8::1");
  await expect(page.locator("main").getByRole("button",{name:"Start",exact:true})).toHaveCount(0);
  await expect(page.locator("main").getByRole("button",{name:"Delete resource",exact:true})).toHaveCount(0);
  await page.goBack();
  await page.unrouteAll({behavior:"wait"});
  execFileSync("docker", ["compose", "exec", "-T", "db", "psql", "-U", "providah", "-d", "providah_browser", "-v", "ON_ERROR_STOP=1", "-c", "INSERT INTO resources(id,org_id,connection_id,native_id,name,provider,kind,region,status,public_ip,observed_at) SELECT repeat('4',64),org_id,id,'789','Browser primary IP','hetzner','network.primary_ip','fsn1','assigned','192.0.2.44',now() FROM connections WHERE name='Hetzner production'"]);
  await page.reload();
  await page.getByLabel("Filter resource type").selectOption("network.primary_ip");
  await page.getByRole("link", {name:"Browser primary IP",exact:true}).click();
  await expect(page.locator("main")).toContainText("Primary IPs");
  await expect(page.locator("main")).toContainText("192.0.2.44");
  await expect(page.locator("main").getByRole("button",{name:"Start",exact:true})).toHaveCount(0);
  await page.goBack();
  execFileSync("docker", ["compose", "exec", "-T", "db", "psql", "-U", "providah", "-d", "providah_browser", "-v", "ON_ERROR_STOP=1", "-c", "INSERT INTO resources(id,org_id,connection_id,native_id,name,provider,kind,region,status,size,observed_at) SELECT repeat('9',64),org_id,id,'456','Browser recovery snapshot','hetzner','storage.snapshot','global','available','snapshot · x86',now() FROM connections WHERE name='Hetzner production'"]);
  await page.reload();
  await page.getByLabel("Filter resource type").selectOption("storage.snapshot");
  await page.route("**/providah.v1.ConsoleService/GetResource",async route=>{
    const response=await route.fetch();const body=await response.json();
    await route.fulfill({response,json:{...body,connectionEnabled:true}});
  });
  await page.getByRole("link", { name: "Browser recovery snapshot", exact: true }).click();
  await expect(page.locator("main")).toContainText("Snapshots");
  await expect(page.locator("main").getByRole("button", { name: "Start", exact: true })).toHaveCount(0);
  await page.route("**/providah.v1.ConsoleService/PreviewDeletion",route=>route.fulfill({json:{impact:"Delete standalone snapshot 456. Recovery data is lost; the source server is retained.",digest:"b".repeat(64)}}));
  let snapshotRequested=false;
  await page.route("**/providah.v1.ConsoleService/RequestOperation",route=>{
    const body=route.request().postDataJSON();expect(body.resourceId).toBe("9".repeat(64));expect(body.confirmation).toBe("456");expect(body.deletionDigest).toBe("b".repeat(64));snapshotRequested=true;
    return route.fulfill({json:{}});
  });
  await page.getByRole("button",{name:"Delete resource",exact:true}).click();
  await page.getByRole("button",{name:"Read deletion impact",exact:true}).click();
  await page.getByLabel("Type 456 to confirm deletion and the listed data loss",{exact:true}).fill("456");
  await page.getByLabel("Reason for this action",{exact:true}).fill("Retire the reviewed snapshot");
  await page.screenshot({path:"test-results/snapshot-delete.png",fullPage:true});
  await page.getByRole("button",{name:"Request delete resource",exact:true}).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);expect(snapshotRequested).toBe(true);
  await page.unrouteAll({behavior:"wait"});
  await consolePage(page,"Team & access");
  await page.getByRole("button", { name: "Create role", exact: true }).click();
  await page.getByLabel("Role name").fill("Monitoring");
  await page
    .getByRole("checkbox", { name: "connections.read", exact: true })
    .check();
  await page
    .getByRole("checkbox", { name: "resources.read", exact: true })
    .check();
  await page.getByRole("button", { name: "Save role", exact: true }).click();
  await expect(
    page.getByRole("table", { name: "Roles", exact: true }),
  ).toContainText("Monitoring");
  await page
    .getByRole("button", { name: "Invite member", exact: true })
    .click();
  await page.getByLabel("Email address").fill("guest@example.com");
  await page
    .getByLabel("Role", { exact: true })
    .selectOption({ label: "Monitoring" });
  await page
    .getByRole("button", { name: "Create invitation", exact: true })
    .click();
  const inviteLink = await page.getByLabel("Invitation link").inputValue();
  await page.getByRole("button", { name: "Done", exact: true }).click();
  await expect(
    page.getByRole("table", { name: "Invitations", exact: true }),
  ).toContainText("guest@example.com");
  const guestContext = await browser.newContext();
  const guest = await guestContext.newPage();
  await guest.goto(inviteLink);
  const guestSecret = await guest.locator(".enrollment code").textContent();
  await guest
    .getByLabel("Password", { exact: true })
    .fill("guest-password-long");
  await guest.getByLabel("Authenticator code").fill(await totp(guestSecret!));
  await guest
    .getByRole("button", { name: "Join organization", exact: true })
    .click();
  await expect(
    guest.getByRole("heading", { name: "Operations overview" }),
  ).toBeVisible();
  await expect(
    page.getByRole("table", { name: "Members", exact: true }),
  ).toContainText("guest@example.com", { timeout: 8000 });
  await page.screenshot({
    path: "test-results/team-access.png",
    fullPage: true,
  });
  const guestRow = page
    .getByRole("table", { name: "Members", exact: true })
    .getByRole("row")
    .filter({ hasText: "guest@example.com" });
  await guestRow
    .getByRole("button", { name: "Edit access", exact: true })
    .click();
  await page.getByLabel("Membership status").selectOption("false");
  await page.getByRole("button", { name: "Save access", exact: true }).click();
  await expect(
    guest.getByRole("heading", { name: "No organization access" }),
  ).toBeVisible({ timeout: 8000 });
  // Exercise the shared action modal without enabling a live cloud executor in browser tests.
  await consolePage(page,"Connections");
  await page.getByRole("row").filter({hasText:"Hetzner production"}).getByRole("button", { name: "Enable", exact: true }).click();
  await consolePage(page,"Inventory");
  await page
    .getByRole("link", { name: "Browser test server", exact: true })
    .click();
  await page
    .getByRole("button", { name: "Graceful shutdown", exact: true })
    .click();
  await page
    .getByLabel("Reason for this action")
    .fill("Planned maintenance window");
  await page
    .getByRole("button", { name: "Request graceful shutdown", exact: true })
    .click();
  await expect(page.getByRole("dialog").last()).toContainText(
    "Provider execution is not configured",
  );
  await page.getByRole("button", { name: "Close", exact: true }).last().click();
  await page.getByRole("button", { name: "Close", exact: true }).click();
  await consolePage(page,"Team & access");
  await page
    .getByRole("table", { name: "Members", exact: true })
    .getByRole("row")
    .filter({ hasText: "guest@example.com" })
    .getByRole("button", { name: "Edit access", exact: true })
    .click();
  await page.getByLabel("Role", { exact: true }).selectOption("approver");
  await page.getByLabel("Membership status").selectOption("true");
  await page.getByRole("button", { name: "Save access", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  execFileSync("docker", [
    "compose",
    "exec",
    "-T",
    "db",
    "psql",
    "-U",
    "providah",
    "-d",
    "providah_browser",
    "-v",
    "ON_ERROR_STOP=1",
    "-c",
    "INSERT INTO operations(id,org_id,resource_id,connection_id,connection_revision,provider,native_id,region,resource_name,action,expected_status,reason,requester_id,requester_email,status,idempotency_key) SELECT repeat('d',64),r.org_id,r.id,r.connection_id,c.revision,r.provider,r.native_id,r.region,r.name,'shutdown',r.status,'Planned browser review',u.id,u.email,'awaiting_approval',repeat('e',64) FROM resources r JOIN connections c ON c.id=r.connection_id CROSS JOIN users u WHERE r.name='Browser test server' AND u.email='browser@example.com'",
  ]);
  await guest.goto("/operations");
  await guest.getByRole("button", { name: /Browser test server/ }).click();
  await expect.poll(()=>new URL(guest.url()).searchParams.get("operation")).toBe("d".repeat(64));
  await guest.route("**/providah.v1.ConsoleService/ListOperations",route=>route.fulfill({json:{operations:[]}}));
  await guest.reload();
  await expect(guest.getByRole("dialog")).toContainText("Planned browser review");
  await expect(guest.getByRole("navigation",{name:"Operation breadcrumb",exact:true})).toContainText("Browser test server");
  await guest.unroute("**/providah.v1.ConsoleService/ListOperations");
  await guest
    .getByLabel("Review reason")
    .fill("Approved maintenance for this server");
  await guest
    .getByRole("button", { name: "Submit review", exact: true })
    .click();
  await expect(guest.getByRole("dialog")).toContainText("Queued");
  await consolePage(page,"Operations");
  await page.getByRole("button", { name: /Browser test server/ }).click();
  await expect(page.getByRole("dialog")).toContainText("Queued");
  await page.screenshot({
    path: "test-results/operation-review.png",
    fullPage: true,
  });
  await page
    .getByRole("button", { name: "Cancel before dispatch", exact: true })
    .click();
  await expect(page.getByRole("dialog")).toContainText("Canceled");
  await page.getByRole("button", { name: "Close", exact: true }).click();
  await expect.poll(()=>new URL(page.url()).searchParams.get("operation")).toBeNull();
  await page.goBack();
  await expect(page.getByRole("dialog")).toContainText("Canceled");
  await page.getByRole("button", { name: "Close", exact: true }).click();
  await consolePage(page,"Schedules");
  await expect.poll(()=>modules.has("/src/schedules.tsx")).toBe(true);
  await page.getByRole("button", { name: "Create schedule", exact: true }).click();
  await page.getByLabel("Schedule name", { exact: true }).fill("Weekday servers");
  await page.getByRole("checkbox", { name: /Browser test server/ }).check();
  await page.getByLabel("Schedule type", { exact: true }).selectOption("window");
  await expect(page.getByLabel("Shutdown rule", { exact: true })).toBeVisible();
  await page.getByLabel("Schedule type", { exact: true }).selectOption("recurring");
  await expect(page.getByLabel("Shutdown rule", { exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "Preview upcoming runs" }).click();
  await expect(page.getByRole("table", { name: "Schedule preview" })).toBeVisible();
  await page.getByRole("button", { name: "Back to edit" }).click();
  await expect(page.getByLabel("Schedule name", { exact: true })).toHaveValue("Weekday servers");
  await page.getByRole("button", { name: "Preview upcoming runs" }).click();
  await page.getByRole("button", { name: "Save for approval" }).click();
  await expect(page.getByRole("table", { name: "Schedules", exact: true })).toContainText("Awaiting approval");
  await guest.goto("/schedules");
  await guest.getByRole("button", { name: "Weekday servers", exact: true }).click();
  await expect.poll(()=>new URL(guest.url()).searchParams.get("schedule")).toMatch(/^[a-f0-9]{64}$/);
  await guest.route("**/providah.v1.ConsoleService/ListSchedules",route=>route.fulfill({json:{schedules:[]}}));
  await guest.reload();
  await expect(guest.getByRole("navigation",{name:"Schedule breadcrumb",exact:true})).toContainText("Weekday servers");
  await guest.screenshot({path:"test-results/schedule-link.png",fullPage:true});
  await guest.unroute("**/providah.v1.ConsoleService/ListSchedules");
  await guest.route("**/providah.v1.ConsoleService/ApproveSchedule",async route=>{
    const body=route.request().postDataJSON();expect(Number(body.expectedRevision)).toBeGreaterThan(0);expect(body.id).toMatch(/^[a-f0-9]{64}$/);
    execFileSync("docker",["compose","exec","-T","db","psql","-U","providah","-d","providah_browser","-v","ON_ERROR_STOP=1","-c",`UPDATE schedules SET write_revision=write_revision+1 WHERE id='${body.id}'`]);
    await route.continue();
  },{times:1});
  await guest.getByRole("button", { name: "Approve this schedule revision" }).click();
  await expect(guest.getByText("Schedule changed. Reload and review it before continuing.",{exact:true})).toBeVisible();
  await guest.reload();
  await guest.getByRole("button", { name: "Approve this schedule revision" }).click();

  await expect(page.getByRole("table", { name: "Schedules", exact: true })).toContainText("Enabled", { timeout: 8000 });
  await page.getByRole("button", { name: "Weekday servers", exact: true }).click();
  await page.getByRole("button", { name: "Disable automation identity", exact: true }).click();
  await expect(page.getByRole("button", { name: "Enable automation identity", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Close", exact: true }).click();
  await expect(page.getByRole("table", { name: "Schedules", exact: true })).toContainText("Identity disabled");
  await page.screenshot({ path: "test-results/schedule-review.png", fullPage: true });
  await consolePage(page,"Notifications");
  await page.getByRole("button", { name: "Add destination", exact: true }).click();
  await page.getByLabel("Destination name", { exact: true }).fill("Operations receiver");
  await page.getByLabel("Delivery method", { exact: true }).selectOption("email-custom");
  await expect(page.getByLabel("SMTP hostname", { exact: true })).toBeVisible();
  await page.getByLabel("Delivery method", { exact: true }).selectOption("webhook");
  await expect(page.getByLabel("SMTP hostname", { exact: true })).toHaveCount(0);
  await page.getByLabel("HTTPS endpoint or recipient email", { exact: true }).fill("https://hooks.example.com/events");
  await page.getByLabel("Webhook signing secret", { exact: true }).fill("whsec_" + Buffer.alloc(32, 7).toString("base64"));
  await page.getByRole("button", { name: "Save destination", exact: true }).click();
  await expect(page.getByRole("table", { name: "Notification destinations", exact: true })).toContainText("Verification required");
  await page.getByRole("link", { name: "Operations receiver", exact: true }).click();
  await page.getByLabel("Destination verification code", { exact: true }).fill("invalid-test-code");
  await page.getByRole("button", { name: "Verify destination", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("invalid or expired");
  await page.getByRole("button", { name: "Disable destination", exact: true }).click();
  await expect(page.getByRole("button", { name: "Enable destination", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Close", exact: true }).click();
  await expect(page.getByRole("table", { name: "Notification destinations", exact: true })).toContainText("Disabled");
  await page.screenshot({ path: "test-results/notifications.png", fullPage: true });
  await consolePage(page,"Maintenance");
  await page.getByRole("button", { name: "Add maintenance policy", exact: true }).click();
  await page.getByLabel("Policy name", { exact: true }).fill("Night operations");
  await page.getByLabel("IANA timezone", { exact: true }).fill("UTC");
  await page.getByLabel("Window start rule", { exact: true }).fill("0 22 * * *");
  await page.getByLabel("Window duration in minutes", { exact: true }).fill("60");
  await page.getByRole("button", { name: "Save maintenance policy", exact: true }).click();
  await expect(page.getByRole("table", { name: "Maintenance policies", exact: true })).toContainText("Night operations");
  await page.screenshot({ path: "test-results/maintenance.png", fullPage: true });
  await guest.goto("/schedules");
  await guest.getByRole("button", { name: "Weekday servers", exact: true }).click();
  await expect(guest.getByRole("button", { name: "Approve this schedule revision", exact: true })).toBeVisible();
  await consolePage(page,"Schedules");
  await page.getByRole("button", { name: "Weekday servers", exact: true }).click();
  await page.getByRole("button", { name: "Edit schedule", exact: true }).click();
  await page.getByRole("button", { name: "Preview upcoming runs", exact: true }).click();
  await expect(page.getByRole("table", { name: "Schedule preview", exact: true })).toContainText("Maintenance window closed");
  await page.getByRole("button", { name: "Close", exact: true }).click();
  await consolePage(page,"Maintenance");
  await page.getByRole("button", { name: "Night operations", exact: true }).click();
  await page.getByLabel("Type the policy name to remove it", { exact: true }).fill("Night operations");
  await page.getByRole("button", { name: "Remove policy", exact: true }).click();
  await expect(page.getByRole("table", { name: "Maintenance policies", exact: true })).not.toContainText("Night operations");
  await consolePage(page,"Audit export");
  await expect(page.getByRole("heading", { name: "Storage not configured", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Configure storage", exact: true }).click();
  await page.getByLabel("S3 endpoint", { exact: true }).fill("https://storage.example.com");
  await page.getByLabel("Bucket name", { exact: true }).fill("browser-audit");
  await page.getByLabel("Access key", { exact: true }).fill("browser-key");
  await page.getByLabel("Secret key", { exact: true }).fill("browser-secret");
  await page.getByRole("button", { name: "Save storage", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "Export paused", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Enable export", exact: true })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Test storage", exact: true })).toBeEnabled();
  await expect(page.getByText("browser-secret", { exact: true })).toHaveCount(0);
  await page.screenshot({ path: "test-results/audit-export.png", fullPage: true });
  await page.getByRole("button", { name: "Configure storage", exact: true }).click();
  await expect(page.getByLabel("Secret key", { exact: true })).toHaveValue("");
  await page.getByRole("button", { name: "Close", exact: true }).click();
  await consolePage(page,"Provider modules");
  await expect(page.getByRole("table", { name: "Provider modules", exact: true })).toContainText("digitalocean");
  await page.getByRole("button", { name: "hetzner", exact: true }).click();
  await page.getByLabel("Type hetzner to confirm", { exact: true }).fill("hetzner");
  await page.getByRole("button", { name: "Disable provider module", exact: true }).click();
  await expect(page.getByRole("table", { name: "Provider modules", exact: true })).toContainText("Disabled");
  await page.screenshot({ path: "test-results/provider-modules.png", fullPage: true });
  await consolePage(page,"Inventory");
  await page.getByRole("link", { name: "Browser test server", exact: true }).click();
  await expect(page.locator("main")).toContainText("provider module is disabled");
  await expect(page.getByRole("button", { name: "Graceful shutdown", exact: true })).toBeDisabled();
  await page.goBack();
  await consolePage(page,"Provider modules");
  await page.getByRole("button", { name: "hetzner", exact: true }).click();
  await page.getByLabel("Type hetzner to confirm", { exact: true }).fill("hetzner");
  await page.getByRole("button", { name: "Enable provider module", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  // Runtime chooser UI fixture; real update/rollback persistence is covered in PostgreSQL tests.
  const runtimeA = "sha256:" + "a".repeat(64), runtimeB = "sha256:" + "b".repeat(64);
  let selectedRuntime = runtimeA;
  await page.route("**/providah.v1.ConsoleService/ListProviderModules", (route) => route.fulfill({ json: { modules: [{ provider: "hetzner", enabled: true, revision: "8", runtimeId: selectedRuntime, contractVersion: 1, runtimes: [{ image: runtimeA, version: "1.0.0", sdkVersion: "v2" }, { image: runtimeB, version: "1.1.0", sdkVersion: "v2" }] }] } }));
  await page.route("**/providah.v1.ConsoleService/SetProviderRuntime", (route) => {
    const body = route.request().postDataJSON();
    expect(body.runtimeId).toBe(runtimeB);
    expect(body.expectedRevision).toBe("8");
    expect(body.confirmation).toBe("hetzner");
    selectedRuntime = runtimeB;
    return route.fulfill({ json: {} });
  });
  await page.reload();
  await page.getByRole("button", { name: "hetzner", exact: true }).click();
  await page.getByRole("button", { name: "Change runtime / rollback", exact: true }).click();
  await page.getByLabel("Approved provider runtime", { exact: true }).selectOption(runtimeB);
  await page.getByLabel("Type hetzner to confirm runtime selection", { exact: true }).fill("hetzner");
  await page.screenshot({ path: "test-results/provider-runtime.png", fullPage: true });
  await page.getByRole("button", { name: "Select provider runtime", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByRole("table", { name: "Provider modules", exact: true })).toContainText("1.1.0");
  await page.unroute("**/providah.v1.ConsoleService/ListProviderModules");
  await page.unroute("**/providah.v1.ConsoleService/SetProviderRuntime");
  await consolePage(page,"Connections");
  await page.getByRole("button", { name: "Add connection", exact: true }).click();
  await page.getByLabel("Connection name").fill("AWS role account");
  await page.getByLabel("Cloud provider").selectOption("aws");
  await expect(page.getByLabel("API token", { exact: true })).toHaveCount(0);
  await page.getByLabel("AWS access key ID", { exact: true }).fill("browser-source-key");
  await page.getByLabel("AWS secret access key", { exact: true }).fill("browser-source-secret");
  await page.getByLabel("Role ARN (optional)", { exact: true }).fill("arn:aws:iam::123456789012:role/Providah");
  await page.getByLabel("External ID (optional)", { exact: true }).fill("browser-external-id");
  await page.getByLabel("Expected AWS account ID (optional)", { exact: true }).fill("123456789012");
  await page.getByRole("button", { name: "Save connection", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  const awsRow = page.getByRole("row").filter({ hasText: "AWS role account" });
  await expect(awsRow).toBeVisible();
  await awsRow.getByRole("button", { name: "Rotate credential", exact: true }).click();
  await expect(page.getByLabel("AWS access key ID", { exact: true })).toHaveValue("");
  await expect(page.getByLabel("Role ARN (optional)", { exact: true })).toHaveValue("");
  await page.getByLabel("AWS access key ID", { exact: true }).fill("replacement-key");
  await page.getByLabel("AWS secret access key", { exact: true }).fill("replacement-secret");
  await page.screenshot({ path: "test-results/aws-credentials.png", fullPage: true });
  await page.getByRole("button", { name: "Replace securely", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.getByText("replacement-secret", { exact: true })).toHaveCount(0);
  // External-store availability is a deployment setting; mock only this UI capability and write.
  await page.route("**/providah.v1.ConsoleService/ListConnections", async route => {
    const response = await route.fetch();
    await route.fulfill({ response, json: { ...(await response.json()), externalSecretsEnabled: true } });
  });
  await page.reload();
  await page.getByRole("button", { name: "Add connection", exact: true }).click();
  await page.getByLabel("Connection name").fill("External account");
  await page.getByLabel("Cloud provider").selectOption("aws");
  await page.getByLabel("Credential source", { exact: true }).selectOption("vault_kv2");
  await expect(page.getByLabel("AWS access key ID", { exact: true })).toHaveCount(0);
  await page.getByLabel("Secret path", { exact: true }).fill("production");
  await page.getByLabel("Secret version", { exact: true }).fill("2");
  await page.getByLabel("Credential source", { exact: true }).selectOption("builtin");
  await expect(page.getByLabel("Secret path", { exact: true })).toHaveCount(0);
  await expect(page.getByLabel("AWS access key ID", { exact: true })).toBeVisible();
  await page.getByLabel("Credential source", { exact: true }).selectOption("vault_kv2");
  await page.getByLabel("Secret path", { exact: true }).fill("production");
  await page.getByLabel("Secret version", { exact: true }).fill("2");
  let externalSubmitted = false;
  await page.route("**/providah.v1.ConsoleService/CreateConnection", async route => {
    const body = route.request().postDataJSON();
    expect(body.credential).toBe('vault-kv2:{"path":"production","key":"credential","version":2}');
    externalSubmitted = true;
    await route.fulfill({ json: {} });
  });
  await page.screenshot({ path: "test-results/external-credentials.png", fullPage: true });
  await page.getByRole("button", { name: "Save connection", exact: true }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  expect(externalSubmitted).toBe(true);
  await page.unrouteAll({ behavior: "wait" });
  // Test-only clock fixture: do not wait for another 30-second TOTP period.
  execFileSync("docker", ["compose", "exec", "-T", "db", "psql", "-U", "providah", "-d", "providah_browser", "-v", "ON_ERROR_STOP=1", "-c", "UPDATE users SET last_totp_step=0 WHERE email='browser@example.com'"]);
  await accountAction(page, "Recovery codes");
  await page.getByLabel("Current password", { exact: true }).fill("browser-test-password");
  await page.getByLabel("Fresh authenticator code", { exact: true }).fill(await totp(secret!));
  await page.getByRole("button", { name: "Generate replacement codes", exact: true }).click();
  const recoveryCodes = (await page.getByLabel("New recovery codes", { exact: true }).textContent())!.trim().split("\n");
  expect(recoveryCodes).toHaveLength(10);
  await page.getByRole("button", { name: "I saved my recovery codes", exact: true }).click();
  await expect(page.getByLabel("New recovery codes", { exact: true })).toHaveCount(0);
  await expect(page.getByRole("dialog")).toContainText("10 unused recovery codes");
  await page.screenshot({ path: "test-results/recovery-settings.png", fullPage: true });
  await page.getByRole("button", { name: "Close", exact: true }).click();
  await accountAction(page, "Sign out");
  await page.getByLabel("Email address", { exact: true }).fill("browser@example.com");
  await page.getByLabel("Password", { exact: true }).fill("browser-test-password");
  await expect(page.getByLabel("Authenticator code", { exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.getByRole("button", { name: "Use a recovery code", exact: true }).click();
  await page.getByLabel("Recovery code", { exact: true }).fill(recoveryCodes[0]);
  await page.getByRole("button", { name: "Verify and sign in", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Operations overview", exact: true })).toBeVisible();
  await accountAction(page, "Recovery codes");
  await expect(page.getByRole("dialog")).toContainText("9 unused recovery codes");
  await expect(page.getByRole("table", { name: "Account security history", exact: true })).toContainText("Recovery code used");
  await page.getByRole("button", { name: "Close", exact: true }).click();
  await page.route("**/providah.v1.ConsoleService/PreviewDeletion", route => route.fulfill({json:{impact:"Delete resource 123 and local disk data. Delete automatic backups. Retain volume 42.",digest:"a".repeat(64)}}));
  let deletionRequested=false;
  await page.route("**/providah.v1.ConsoleService/RequestOperation", route => {
    const body=route.request().postDataJSON();
    expect(body.action).toBe("delete");expect(body.confirmation).toBe("123");expect(body.deletionDigest).toBe("a".repeat(64));
    deletionRequested=true;
    return route.fulfill({json:{}});
  });
  await consolePage(page,"Inventory");
  await page.getByRole("link", {name:"Browser test server",exact:true}).click();
  const metricsEnd=new Date(Math.floor(Date.now()/300000)*300000).toISOString();
  const metricsStart=new Date(Date.parse(metricsEnd)-3600000).toISOString();
  await page.route("**/providah.v1.ConsoleService/GetResourceMetrics",route=>{
    const body=route.request().postDataJSON();expect(body.resourceId).toBeTruthy();
    return route.fulfill({json:{start:metricsStart,end:metricsEnd,fetchedAt:metricsEnd,series:[{id:"cpu",name:"CPU utilization",unit:"percent",aggregation:"Provider-reported samples",periodSeconds:300,points:body.hours===24?[]:[{timestamp:metricsStart,value:0},{timestamp:metricsEnd,value:12}]},{id:"network",name:"Public network received",unit:"bytes / second",aggregation:"Provider-reported samples",periodSeconds:300,points:[]}]}});
  });
  await page.getByRole("button",{name:"Provider metrics",exact:true}).click();
  await expect(page.getByRole("img",{name:/CPU utilization; 2 reported samples/})).toBeVisible();
  await expect(page.getByRole("table",{name:"Metric samples",exact:true})).toContainText("0");
  await page.screenshot({path:"test-results/provider-metrics.png",fullPage:true});
  await page.getByLabel("Measurement",{exact:true}).selectOption("network");
  await expect(page.getByText("No samples were published for this measurement. This does not indicate zero utilization.",{exact:true})).toBeVisible();
  await page.getByLabel("History period",{exact:true}).selectOption("24");
  await page.getByRole("button",{name:"Read metrics",exact:true}).click();
  await expect(page.getByRole("table",{name:"Metric samples",exact:true})).toHaveCount(0);
  await page.getByRole("button",{name:"Close",exact:true}).click();
  await page.unroute("**/providah.v1.ConsoleService/GetResourceMetrics");
  await page.getByRole("button", {name:"Delete resource",exact:true}).click();
  await expect(page.getByRole("button", {name:"Request delete resource",exact:true})).toHaveCount(0);
  await page.getByRole("button", {name:"Read deletion impact",exact:true}).click();
  await expect(page.getByText("Delete resource 123 and local disk data. Delete automatic backups. Retain volume 42.", {exact:true})).toBeVisible();
  await page.getByLabel("Type 123 to confirm deletion and the listed data loss", {exact:true}).fill("123");
  await page.getByLabel("Reason for this action", {exact:true}).fill("Retire the server after reviewing its disks");
  await page.screenshot({path:"test-results/delete-review.png",fullPage:true});
  await page.getByRole("button", {name:"Request delete resource",exact:true}).click();
  await expect(page.getByRole("heading", {name:"Operations",exact:true})).toBeVisible();
  expect(deletionRequested).toBe(true);
  await page.unroute("**/providah.v1.ConsoleService/PreviewDeletion");
  await page.unroute("**/providah.v1.ConsoleService/RequestOperation");
  execFileSync("docker", ["compose", "exec", "-T", "db", "psql", "-U", "providah", "-d", "providah_browser", "-v", "ON_ERROR_STOP=1", "-c", "INSERT INTO resources(id,org_id,connection_id,native_id,name,provider,kind,region,status,size,observed_at) SELECT repeat('8',64),org_id,id,'123','Browser data volume','hetzner','storage.volume','fsn1','available','10 GiB',now() FROM connections WHERE name='Hetzner production'"]);
  await consolePage(page,"Inventory");
  await page.getByLabel("Filter resource type", { exact: true }).selectOption("storage.volume");
  await expect(page.getByRole("table", { name: "Resources", exact: true })).toContainText("Browser data volume");
  await expect(page.getByRole("table", { name: "Resources", exact: true })).not.toContainText("Browser test server");
  await page.getByRole("link", { name: "Browser data volume", exact: true }).click();
  await expect(page.getByRole("dialog")).toContainText("10 GiB");
  await expect(page.getByRole("dialog").getByRole("button", { name: "Start", exact: true })).toHaveCount(0);
  await page.route("**/providah.v1.ConsoleService/PreviewDeletion",route=>route.fulfill({json:{impact:"Delete detached volume 123 and all stored data. Existing snapshots are retained.",digest:"c".repeat(64)}}));
  let volumeRequested=false;
  await page.route("**/providah.v1.ConsoleService/RequestOperation",route=>{
    const body=route.request().postDataJSON();expect(body.resourceId).toBe("8".repeat(64));expect(body.action).toBe("delete");expect(body.confirmation).toBe("123");expect(body.deletionDigest).toBe("c".repeat(64));volumeRequested=true;
    return route.fulfill({json:{}});
  });
  await page.getByRole("button",{name:"Delete resource",exact:true}).click();
  await page.getByRole("button",{name:"Read deletion impact",exact:true}).click();
  await page.getByLabel("Type 123 to confirm deletion and the listed data loss",{exact:true}).fill("123");
  await page.getByLabel("Reason for this action",{exact:true}).fill("Retire detached data after impact review");
  await page.screenshot({path:"test-results/volume-delete.png",fullPage:true});
  await page.getByRole("button",{name:"Request delete resource",exact:true}).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);expect(volumeRequested).toBe(true);
  await page.unrouteAll({behavior:"wait"});
  await consolePage(page,"Inventory");
  for (const [kind,part,native,name,cloud="hetzner"] of [["network.network","61","991","Unused browser network"],["network.firewall","62","992","Unused browser firewall"],["access.ssh_key","63","993","Unused browser SSH key"],["network.firewall","64","sg-12345678","Unused AWS security group","aws"]]) {
    execFileSync("docker",["compose","exec","-T","db","psql","-U","providah","-d","providah_browser","-v","ON_ERROR_STOP=1","-c",`INSERT INTO resources(id,org_id,connection_id,native_id,name,provider,kind,region,status,observed_at) SELECT repeat('${part}',32),org_id,id,'${native}','${name}','${cloud}','${kind}','${cloud==='aws'?'us-east-1':'global'}','present',now() FROM connections WHERE name='${cloud==='aws'?'AWS database fixture':'Hetzner production'}'`]);
    if (cloud==="aws") {
      // The fake AWS connection stays disabled so background discovery never calls AWS.
      await page.route("**/providah.v1.ConsoleService/GetResource",async route=>{
        const response=await route.fetch();const body=await response.json();
        await route.fulfill({response,json:{...body,connectionEnabled:true}});
      });
    }
    await page.getByLabel("Filter resource type",{exact:true}).selectOption(kind);
    await page.getByRole("button",{name,exact:true}).click();
    await expect(page.getByRole("dialog").getByRole("button",{name:"Start",exact:true})).toHaveCount(0);
    if(kind==="access.ssh_key") {
      await expect.poll(()=>new URL(page.url()).searchParams.get("resource")).toBe(part.repeat(32));
      expect(new URL(page.url()).searchParams.get("org")).toBeTruthy();
      await page.reload();
      await expect(page.getByRole("dialog")).toContainText(name);
      await expect(page.getByRole("navigation",{name:"Resource breadcrumb",exact:true})).toContainText(name);
    }
    await page.route("**/providah.v1.ConsoleService/PreviewDeletion",route=>route.fulfill({json:{impact:`Delete unused ${kind} ${native} and its reviewed configuration. No attached resources are detached.`,digest:"f".repeat(64)}}));
    let requested=false;
    await page.route("**/providah.v1.ConsoleService/RequestOperation",route=>{const body=route.request().postDataJSON();expect(body.resourceId).toBe(part.repeat(32));expect(body.action).toBe("delete");expect(body.confirmation).toBe(native);expect(body.deletionDigest).toBe("f".repeat(64));requested=true;return route.fulfill({json:{}})});
    await page.getByRole("button",{name:kind==="access.ssh_key"?"Delete SSH key":"Delete resource",exact:true}).click();
    await page.getByRole("button",{name:"Read deletion impact",exact:true}).click();
    await page.getByLabel(`Type ${native} to confirm deletion and the listed data loss`,{exact:true}).fill(native);
    await page.getByLabel("Reason for this action",{exact:true}).fill("Retire unused configuration after reviewing dependencies");
    await page.screenshot({path:`test-results/${cloud}-${kind}-delete.png`,fullPage:true});
    await page.getByRole("button",{name:kind==="access.ssh_key"?"Request delete ssh key":"Request delete resource",exact:true}).click();
    await expect(page.getByRole("dialog")).toHaveCount(0);expect(requested).toBe(true);
    await page.unrouteAll({behavior:"wait"});
    await consolePage(page,"Inventory");
  }
  execFileSync("docker", ["compose", "exec", "-T", "db", "psql", "-U", "providah", "-d", "providah_browser", "-v", "ON_ERROR_STOP=1", "-c", "INSERT INTO resources(id,org_id,connection_id,native_id,name,provider,kind,region,status,size,observed_at) SELECT repeat('7',64),org_id,id,'23','cx23','hetzner','compute.type','fsn1','catalog','2 vCPU · 4 GiB RAM',now() FROM connections WHERE name='Hetzner production'"]);
  await page.getByLabel("Filter resource type", {exact:true}).selectOption("compute.type");
  await expect(page.getByRole("table", {name:"Resources",exact:true})).toContainText("cx23");
  await page.getByRole("button", {name:"cx23",exact:true}).click();
  await expect(page.getByRole("dialog")).toContainText("2 vCPU · 4 GiB RAM");
  await expect(page.getByRole("dialog").getByRole("button", {name:"Delete resource",exact:true})).toHaveCount(0);
  await page.getByRole("button", {name:"Close",exact:true}).click();
  execFileSync("docker", ["compose", "exec", "-T", "db", "psql", "-U", "providah", "-d", "providah_browser", "-v", "ON_ERROR_STOP=1", "-c", "INSERT INTO resources(id,org_id,connection_id,native_id,name,provider,kind,region,status,size,observed_at) SELECT repeat('6',64),org_id,id,'11','Ubuntu image','hetzner','compute.image','global','available','x86',now() FROM connections WHERE name='Hetzner production'; INSERT INTO resources(id,org_id,connection_id,native_id,name,provider,kind,region,status,size,observed_at) SELECT repeat('5',64),org_id,id,'12','Deployment key','hetzner','access.ssh_key','global','present','fingerprint',now() FROM connections WHERE name='Hetzner production'"]);
  let creationRequested=false;
  await page.route("**/providah.v1.ConsoleService/RequestServerCreation", route=>{
    const body=route.request().postDataJSON();
    expect(body.region).toBe("fsn1");expect(body.creation).toMatchObject({name:"browser-created-server",image:"11",size:"cx23",sshKey:"12"});
    expect(body.idempotencyKey).toBeTruthy();creationRequested=true;
    return route.fulfill({json:{}});
  });
  await page.getByRole("button", {name:"Create server",exact:true}).click();
  await page.getByLabel("Cloud connection",{exact:true}).selectOption({label:"Hetzner production · hetzner"});
  await page.getByRole("button", {name:"Choose configuration",exact:true}).click();
  await page.getByLabel("Server name",{exact:true}).fill("browser-created-server");
  await page.getByLabel("Region / location",{exact:true}).fill("fsn1");
  await page.getByLabel("Image",{exact:true}).selectOption("11");
  await page.getByLabel("Server size",{exact:true}).selectOption("cx23");
  await page.getByLabel("SSH key",{exact:true}).selectOption("12");
  await page.getByRole("button", {name:"Review configuration",exact:true}).click();
  await expect(page.getByRole("dialog")).toContainText("Creates one billable server");
  await page.getByLabel("Creation reason",{exact:true}).fill("Provision an approved test server");
  await page.getByLabel("Confirm the reviewed server name",{exact:true}).fill("browser-created-server");
  await page.screenshot({path:"test-results/create-review.png",fullPage:true});
  await page.getByRole("button", {name:"Request independent approval",exact:true}).click();
  await expect(page.getByRole("heading", {name:"Operations",exact:true})).toBeVisible();
  expect(creationRequested).toBe(true);
  await page.unroute("**/providah.v1.ConsoleService/RequestServerCreation");
  await consolePage(page,"Templates");
  execFileSync("docker",["compose","exec","-T","db","psql","-U","providah","-d","providah_browser","-v","ON_ERROR_STOP=1","-c","UPDATE users SET last_totp_step=0 WHERE email='browser@example.com'"]);
  await accountAction(page, "Verify MFA");
  await page.getByRole("dialog").getByLabel("Authenticator code",{exact:true}).fill(await totp(secret!));
  await page.getByRole("dialog").getByRole("button",{name:"Verify MFA",exact:true}).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);


  await page.getByRole("button",{name:"Import source bundle",exact:true}).click();
  const bundle=execFileSync("python3",["-c","import io,zipfile,sys; b=io.BytesIO(); z=zipfile.ZipFile(b,'w'); z.writestr('main.tf','terraform {}'); z.writestr('providah.inputs.json',__import__('json').dumps({'version':1,'inputs':[{'name':'size','label':'Deployment size','type':'string','choices':['small','large']},{'name':'count','label':'Node count','type':'integer','min':1,'max':5}]})); z.close(); sys.stdout.buffer.write(b.getvalue())"]);
  await page.getByLabel("Source ZIP",{exact:true}).setInputFiles({name:"source.zip",mimeType:"application/zip",buffer:bundle});
  await page.getByLabel("Source name",{exact:true}).fill("Browser source");
  await page.getByLabel("Runtime",{exact:true}).selectOption("opentofu");
  await page.getByLabel("Entrypoint path inside ZIP",{exact:true}).fill("main.tf");
  await page.getByRole("button",{name:"Import immutable source",exact:true}).click();
  await expect(page.getByRole("table",{name:"Imported sources",exact:true})).toContainText("Browser source");
  await page.getByRole("link",{name:"Inspect Browser source",exact:true}).click();
  await expect.poll(()=>new URL(page.url()).searchParams.get("source")).toMatch(/^[a-f0-9]{64}$/);
  await page.route("**/providah.v1.ConsoleService/ListAutomationSources",route=>route.fulfill({json:{sources:[]}}));
  await page.reload();
  await expect(page.getByRole("navigation",{name:"Source breadcrumb",exact:true})).toContainText("Browser source");
  await expect(page.getByRole("table",{name:"Source inputs",exact:true})).toContainText("Deployment size");
  await page.getByRole("navigation",{name:"Source breadcrumb",exact:true}).getByRole("link",{name:"Templates",exact:true}).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await page.goBack();
  await expect(page.getByRole("dialog")).toContainText("main.tf");
  await page.screenshot({path:"test-results/source-link.png",fullPage:true});
  await page.unroute("**/providah.v1.ConsoleService/ListAutomationSources");
  await page.reload();

  await expect(page.getByRole("dialog")).toContainText("main.tf");
  await page.keyboard.press("Escape");


  let validationJob:Record<string,string>|undefined;
  await page.route("**/providah.v1.ConsoleService/ListAutomationValidations",route=>route.fulfill({json:{runnerConfigured:true,validations:validationJob?[validationJob]:[]}}));
  await page.route("**/providah.v1.ConsoleService/RequestAutomationValidation",route=>{const body=route.request().postDataJSON();expect(body.versionId).toBeTruthy();validationJob={id:"browser-validation",versionId:body.versionId,status:"queued",detail:"",createdAt:new Date().toISOString()};return route.fulfill({json:validationJob});});
  await page.getByRole("button",{name:"Publish automation version",exact:true}).click();
  await page.getByLabel("Imported source",{exact:true}).selectOption({index:0});
  await page.getByRole("button",{name:"Review source binding",exact:true}).click();
  await page.getByLabel("Automation name",{exact:true}).fill("Browser automation");
  await page.getByLabel("Allowed runtime image",{exact:true}).selectOption("sha256:"+"a".repeat(64));
  await page.getByLabel("Bound cloud connection",{exact:true}).selectOption({label:"Hetzner production"});
  await page.getByLabel("Bound region",{exact:true}).fill("fsn1");
  await page.getByLabel("Source and dependency review",{exact:true}).fill("Reviewed fixture source and immutable test runtime");
  await page.getByRole("button",{name:"Publish reviewed version",exact:true}).click();
  await expect(page.getByRole("dialog")).toContainText("Credential revision");
  await expect.poll(()=>new URL(page.url()).searchParams.get("version")).toMatch(/^[a-f0-9]{64}$/);
  await page.route("**/providah.v1.ConsoleService/ListAutomationVersions",route=>route.fulfill({json:{versions:[],runtimes:[]}}));
  await page.reload();
  await expect(page.getByRole("navigation",{name:"Publication breadcrumb",exact:true})).toContainText("Browser automation v1");
  await page.screenshot({path:"test-results/publication-link.png",fullPage:true});
  await page.unroute("**/providah.v1.ConsoleService/ListAutomationVersions");
  await page.reload();
  await expect(page.getByRole("dialog")).toContainText("Credential revision");
  await page.getByRole("button",{name:"Validate source",exact:true}).click();
  await expect(page.getByRole("table",{name:"Automation validations",exact:true})).toContainText("queued");
  await page.getByRole("button",{name:"Create managed project",exact:true}).click();
  await page.getByLabel("Project name",{exact:true}).fill("Browser managed project");
  await page.getByLabel("Published source version",{exact:true}).selectOption({label:"Browser automation v1 · opentofu"});
  await page.getByRole("button",{name:"Configure project inputs",exact:true}).click();
  await page.getByLabel("Deployment size",{exact:true}).selectOption("small");
  await page.getByLabel("Node count",{exact:true}).fill("2");
  await page.getByRole("button",{name:"Create project",exact:true}).click();
  await expect(page.getByRole("dialog")).toContainText("Not initialized");
  await expect(page.getByRole("table",{name:"State versions",exact:true})).toBeVisible();
  await expect.poll(()=>new URL(page.url()).searchParams.get("project")).toMatch(/^[a-f0-9]{64}$/);
  await page.route("**/providah.v1.ConsoleService/ListAutomationProjects",route=>route.fulfill({json:{projects:[]}}));
  await page.reload();
  await expect(page.getByRole("dialog")).toContainText("Browser managed project");
  await expect(page.getByRole("navigation",{name:"Project breadcrumb",exact:true})).toContainText("Browser managed project");
  await page.screenshot({path:"test-results/project-link.png",fullPage:true});
  await page.unroute("**/providah.v1.ConsoleService/ListAutomationProjects");
  await page.reload();
  await expect(page.getByRole("dialog")).toContainText("Browser managed project");
  await page.keyboard.press("Escape");
  await expect(page.getByRole("table",{name:"Managed projects",exact:true})).toContainText("Browser managed project");
  await page.getByRole("button",{name:"Inspect Browser automation v1",exact:true}).click();

  await page.getByRole("button",{name:"Retire version",exact:true}).click();
  await page.getByLabel("Confirm automation name",{exact:true}).fill("Browser automation");
  await page.getByRole("button",{name:"Retire version",exact:true}).click();
  await expect(page.getByRole("dialog")).toContainText("retired");
  await page.getByRole("button",{name:"Revoke version",exact:true}).click();
  await page.getByLabel("Confirm automation name",{exact:true}).fill("Browser automation");
  await page.getByRole("button",{name:"Revoke version",exact:true}).click();
  await expect(page.getByRole("dialog")).toContainText("revoked");
  await page.unroute("**/providah.v1.ConsoleService/ListAutomationValidations");
  await page.unroute("**/providah.v1.ConsoleService/RequestAutomationValidation");

  await page.keyboard.press("Escape");
  await page.getByRole("button",{name:"Publish template",exact:true}).click();
  await page.getByLabel("Cloud connection",{exact:true}).selectOption({label:"Hetzner production"});
  await page.getByRole("button",{name:"Choose configuration",exact:true}).click();
  await expect(page.getByLabel("Server name",{exact:true})).toHaveCount(0);
  await page.getByLabel("Region / location",{exact:true}).fill("fsn1");
  await page.getByLabel("Image",{exact:true}).selectOption("11");
  await page.getByLabel("Server size",{exact:true}).selectOption("cx23");
  await page.getByLabel("SSH key",{exact:true}).selectOption("12");
  await page.getByRole("button",{name:"Review configuration",exact:true}).click();
  await page.getByLabel("Template name",{exact:true}).fill("Browser standard");
  await page.getByRole("button",{name:"Publish immutable version",exact:true}).click();
  await page.getByRole("link",{name:"View Browser standard v1",exact:true}).click();
  await expect.poll(()=>new URL(page.url()).searchParams.get("template")).toMatch(/^[a-f0-9]{64}$/);
  await page.route("**/providah.v1.ConsoleService/ListServerTemplates",route=>route.fulfill({json:{templates:[]}}));
  await page.reload();
  await expect(page.getByRole("navigation",{name:"Template breadcrumb",exact:true})).toContainText("Browser standard v1");
  await expect(page.getByRole("button",{name:"Request deployment approval",exact:true})).toBeVisible();
  await page.route("**/providah.v1.ConsoleService/GetServerTemplate",route=>route.fulfill({status:403,json:{code:"permission_denied",message:"Template unavailable"}}));
  await page.reload();
  await expect(page.getByText("Template unavailable",{exact:true})).toBeVisible();
  await expect(page.getByRole("button",{name:"Request deployment approval",exact:true})).toHaveCount(0);
  await page.unroute("**/providah.v1.ConsoleService/GetServerTemplate");
  await page.unroute("**/providah.v1.ConsoleService/ListServerTemplates");
  await page.reload();
  await page.screenshot({path:"test-results/template-link.png",fullPage:true});

  await expect(page.getByRole("dialog")).toContainText("cx23");
  let templateRequested=false;
  await page.route("**/providah.v1.ConsoleService/RequestServerCreation",route=>{
    const body=route.request().postDataJSON();
    expect(body.templateId).toBeTruthy();
    expect(body.creation).toMatchObject({name:"template-server",image:"11",size:"cx23",sshKey:"12"});
    templateRequested=true;
    return route.fulfill({json:{}});
  });
  await page.getByLabel("New server name",{exact:true}).fill("template-server");
  await page.getByLabel("Deployment reason",{exact:true}).fill("Deploy reviewed template");
  await page.getByRole("button",{name:"Request deployment approval",exact:true}).click();
  await expect(page.getByRole("heading",{name:"Operations",exact:true})).toBeVisible();
  expect(templateRequested).toBe(true);
  await page.unroute("**/providah.v1.ConsoleService/RequestServerCreation");
  await consolePage(page,"Templates");
  await page.getByRole("link",{name:"View Browser standard v1",exact:true}).click();
  await page.getByRole("button",{name:"Retire template",exact:true}).click();
  await page.getByLabel("Confirm template name",{exact:true}).fill("Browser standard");
  await page.getByRole("button",{name:"Retire template",exact:true}).click();
  await expect(page.getByRole("table",{name:"Server templates",exact:true})).toContainText("retired");
  await page.getByRole("link",{name:"View Browser standard v1",exact:true}).click();
  await expect(page.getByRole("button",{name:"Request deployment approval",exact:true})).toHaveCount(0);
  await page.getByRole("button",{name:"Revoke template",exact:true}).click();
  await page.getByLabel("Confirm template name",{exact:true}).fill("Browser standard");
  await page.getByRole("button",{name:"Revoke template",exact:true}).click();
  await expect(page.getByRole("table",{name:"Server templates",exact:true})).toContainText("revoked");

  execFileSync("docker", ["compose","exec","-T","db","psql","-U","providah","-d","providah_browser","-v","ON_ERROR_STOP=1","-c","UPDATE resources SET status='off',size='cx13',observed_at=now() WHERE id=repeat('a',64)"]);
  await consolePage(page,"Inventory");
  await page.reload();
  await page.getByLabel("Filter resource type",{exact:true}).selectOption("compute.server");
  await page.getByRole("link",{name:"Browser test server",exact:true}).click();
  await page.getByRole("button",{name:"Resize server",exact:true}).click();
  await expect(page.getByRole("dialog").last()).toContainText("cx13");
  await expect(page.getByRole("dialog").last()).toContainText("Disk size is preserved");
  await page.getByLabel("Target server type",{exact:true}).selectOption("cx23");
  let resizeRequested=false;
  await page.route("**/providah.v1.ConsoleService/RequestOperation",route=>{
    const body=route.request().postDataJSON();expect(body).toMatchObject({resourceId:"a".repeat(64),action:"resize",expectedStatus:"off",expectedSize:"cx13",targetSize:"cx23"});resizeRequested=true;return route.fulfill({json:{}});
  });
  await page.getByLabel("Reason for this action",{exact:true}).fill("Increase CPU and RAM with existing disk");
  await page.screenshot({path:"test-results/resize-review.png",fullPage:true});
  await page.getByRole("button",{name:"Request resize server",exact:true}).click();
  await expect(page.getByRole("heading",{name:"Operations",exact:true})).toBeVisible();expect(resizeRequested).toBe(true);
  await page.unrouteAll({behavior:"wait"});
  execFileSync("docker", ["compose","exec","-T","db","psql","-U","providah","-d","providah_browser","-v","ON_ERROR_STOP=1","-c","UPDATE resources SET status='running',size='cx23',observed_at=now() WHERE id=repeat('a',64)"]);

  await consolePage(page,"Inventory");
  // Direct database fixtures do not emit the application audit/SSE invalidation.
  await page.reload();
  await page.getByLabel("Filter resource type",{exact:true}).selectOption("");
  await page.route("**/providah.v1.ConsoleService/PreviewBulkPower",route=>{
    const body=route.request().postDataJSON();expect(body.resourceIds).toHaveLength(2);expect(body.action).toBe("shutdown");
    return route.fulfill({json:{targets:[{resourceId:"f".repeat(64),name:"Browser test server",nativeId:"123",provider:"hetzner",region:"fsn1",status:"running",action:"shutdown",eligibility:"eligible",reviewToken:"browser-review-token"},{resourceId:"8".repeat(64),name:"Browser data volume",eligibility:"unsupported",detail:"Power actions require a server."}]}});
  });
  let bulkRequested=false;
  await page.route("**/providah.v1.ConsoleService/RequestBulkPower",route=>{
    const body=route.request().postDataJSON();expect(body.reviewTokens).toEqual(["browser-review-token"]);expect(body.reason).toBe("Shut down the reviewed application servers");bulkRequested=true;
    return route.fulfill({json:{results:[{resourceId:"f".repeat(64),operation:{id:"browser-bulk-operation",status:"OPERATION_STATUS_AWAITING_APPROVAL"}}]}});
  });
  await expect(page.getByRole("table",{name:"Resources",exact:true})).toContainText("Browser data volume");
  await page.getByRole("button",{name:"Bulk power actions",exact:true}).click();
  await page.getByLabel("Power action",{exact:true}).selectOption("shutdown");
  await page.getByRole("checkbox",{name:/Browser test server/}).check();
  await page.getByRole("checkbox",{name:/Browser data volume/}).check();
  await page.getByRole("button",{name:"Preview exact targets",exact:true}).click();
  await expect(page.getByRole("table",{name:"Bulk target review",exact:true})).toContainText("unsupported");
  await page.getByLabel("Reason for these operations",{exact:true}).fill("Shut down the reviewed application servers");
  await page.screenshot({path:"test-results/bulk-power.png",fullPage:true});
  await page.getByRole("button",{name:"Request 1 operation",exact:true}).click();
  await expect(page.getByRole("table",{name:"Bulk request results",exact:true})).toContainText("Recorded");expect(bulkRequested).toBe(true);
  await page.getByRole("link",{name:"Track individual operations",exact:true}).click();
  await page.unroute("**/providah.v1.ConsoleService/PreviewBulkPower");
  await page.unroute("**/providah.v1.ConsoleService/RequestBulkPower");
  let identityLinked=false;
  await page.route("**/providah.v1.ConsoleService/GetOIDCStatus", route=>route.fulfill({json:{enabled:true,issuer:"https://identity.example.com",linked:identityLinked}}));
  await page.route("**/providah.v1.ConsoleService/BeginOIDCLink", route=>{
    const body=route.request().postDataJSON();expect(body.password).toBe("browser-test-password");expect(body.code).toMatch(/^\d{6}$/);identityLinked=true;
    return route.fulfill({json:{url:"http://localhost:5174/?oidc=linked"}});
  });
  await page.reload();
  await accountAction(page, "Organization sign-in");
  await page.getByLabel("Current password",{exact:true}).fill("browser-test-password");
  await page.getByLabel("Fresh authenticator code",{exact:true}).fill(await totp(secret!));
  await page.getByRole("button",{name:"Verify and link identity",exact:true}).click();
  await expect(page).toHaveURL(/oidc=linked/);
  await accountAction(page, "Organization sign-in");
  await expect(page.getByRole("button",{name:"Unlink and sign out",exact:true})).toBeVisible();
  await page.screenshot({path:"test-results/oidc-account.png",fullPage:true});
  await page.getByRole("button",{name:"Close",exact:true}).click();
  await page.unroute("**/providah.v1.ConsoleService/GetOIDCStatus");
  await page.unroute("**/providah.v1.ConsoleService/BeginOIDCLink");
  let policySaved=false;
  await page.route("**/providah.v1.ConsoleService/GetIdentityPolicy", route=>route.fulfill({json:{enabled:policySaved,issuer:policySaved ? "https://identity.example.com" : "",configuredIssuer:"https://identity.example.com",revision:policySaved ? "1" : "0",recoveryUsers:[]}}));
  await page.route("**/providah.v1.ConsoleService/SaveIdentityPolicy", route=>{
    const body=route.request().postDataJSON();expect(body.enabled).toBe(true);expect(body.recoveryUsers).toHaveLength(1);expect(body.reason).toBe("Require our organization identity provider");policySaved=true;
    return route.fulfill({json:{enabled:true,issuer:"https://identity.example.com",revision:"1",recoveryUsers:body.recoveryUsers}});
  });
  await consolePage(page,"Sign-in policy");
  await page.getByRole("button",{name:"Edit sign-in policy",exact:true}).click();
  await page.getByLabel("Member sign-in",{exact:true}).selectOption("required");
  await page.getByRole("checkbox",{name:"browser@example.com",exact:true}).check();
  await page.getByLabel("Policy change reason",{exact:true}).fill("Require our organization identity provider");
  await page.screenshot({path:"test-results/identity-policy.png",fullPage:true});
  await page.getByRole("button",{name:"Save sign-in policy",exact:true}).click();
  await expect(page.getByRole("heading",{name:"Organization sign-in required",exact:true})).toBeVisible();expect(policySaved).toBe(true);
  await page.unroute("**/providah.v1.ConsoleService/GetIdentityPolicy");
  await page.unroute("**/providah.v1.ConsoleService/SaveIdentityPolicy");
  await page.route("**/providah.v1.ConsoleService/GetSession",async route=>{
    const response=await route.fetch();const body=await response.json();body.organizations=body.organizations.map((org:Record<string,unknown>)=>({...org,ssoRequired:true,permissions:[]}));await route.fulfill({json:body});
  });
  await page.route("**/providah.v1.ConsoleService/GetOIDCStatus",route=>route.fulfill({json:{enabled:true,issuer:"https://identity.example.com",linked:true}}));
  await page.reload();
  await expect(page.getByRole("heading",{name:"Organization sign-in required",exact:true})).toBeVisible();
  await expect(page.getByRole("link",{name:"Connections",exact:true})).toHaveCount(0);
  await expect(page.getByRole("button",{name:"Sign in with your organization",exact:true})).toBeVisible();
  await page.unroute("**/providah.v1.ConsoleService/GetSession");
  await page.unroute("**/providah.v1.ConsoleService/GetOIDCStatus");
  await page.reload();
  const oidcContext=await browser.newContext();const oidcPage=await oidcContext.newPage();
  let oidcMfa=true,oidcVerified=true,oidcCompleted=0;
  const oidcReturn="/app/resources?org=original-org&view=original-view";
  await oidcPage.route("**/providah.v1.ConsoleService/GetOIDCStatus",route=>route.fulfill({json:{enabled:true,issuer:"https://identity.example.com",loginVerified:oidcVerified,loginMfaEnabled:oidcMfa,loginReturnTo:oidcReturn}}));
  await oidcPage.route("**/providah.v1.ConsoleService/BeginOIDCLogin",route=>{expect(route.request().postDataJSON().returnTo).toBe(oidcReturn);return route.fulfill({json:{url:"http://localhost:5174/?oidc=verify"}});});
  await oidcPage.route("**/providah.v1.ConsoleService/CompleteOIDCLogin",route=>{expect(route.request().postDataJSON().code??"").toBe(oidcMfa?"123456":"");oidcCompleted++;return route.fulfill({json:{returnTo:oidcReturn}});});
  await oidcPage.goto("http://localhost:5174"+oidcReturn);
  await oidcPage.getByRole("button",{name:"Sign in with your organization",exact:true}).click();
  await oidcPage.getByLabel("Providah authenticator code",{exact:true}).fill("123456");
  await oidcPage.screenshot({path:"test-results/oidc-login.png",fullPage:true});
  await oidcPage.getByRole("button",{name:"Complete organization sign-in",exact:true}).click();
  await expect(oidcPage).toHaveURL("http://localhost:5174"+oidcReturn);expect(oidcCompleted).toBe(1);
  oidcMfa=false;
  await oidcPage.goto("http://localhost:5174/?oidc=verify");
  await expect(oidcPage.getByRole("button",{name:"Complete organization sign-in",exact:true})).toBeVisible();
  await expect(oidcPage.getByLabel("Providah authenticator code",{exact:true})).toHaveCount(0);
  await oidcPage.screenshot({path:"test-results/oidc-no-mfa.png",fullPage:true});
  await oidcPage.getByRole("button",{name:"Complete organization sign-in",exact:true}).click();
  await expect(oidcPage).toHaveURL("http://localhost:5174"+oidcReturn);expect(oidcCompleted).toBe(2);
  oidcVerified=false;
  await oidcPage.goto("http://localhost:5174/?oidc=verify");
  await expect(oidcPage.getByText("This sign-in has expired or is unavailable. Start again.",{exact:true})).toBeVisible();
  await expect(oidcPage.getByRole("button",{name:"Complete organization sign-in",exact:true})).toHaveCount(0);
  await oidcContext.close();
  await accountAction(page, "Change password");
  await page.getByLabel("Current password", { exact: true }).fill("browser-test-password");
  await page.getByLabel("New password", { exact: true }).fill("browser-replacement-password");
  // Force a consumed TOTP step so this check is independent of clock boundaries.
  execFileSync("docker", ["compose", "exec", "-T", "db", "psql", "-U", "providah", "-d", "providah_browser", "-v", "ON_ERROR_STOP=1", "-c", "UPDATE users SET last_totp_step=9999999999 WHERE email='browser@example.com'"]);
  await page.getByLabel("Fresh authenticator code", { exact: true }).fill(await totp(secret!));
  await page.getByRole("button", { name: "Change password and sign out", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("already used");
  execFileSync("docker", ["compose", "exec", "-T", "db", "psql", "-U", "providah", "-d", "providah_browser", "-v", "ON_ERROR_STOP=1", "-c", "UPDATE users SET last_totp_step=0 WHERE email='browser@example.com'; INSERT INTO sessions(id,user_id,refresh_hash,expires_at) SELECT repeat('9',64),id,decode(repeat('ab',32),'hex'),now()+interval '1 day' FROM users WHERE email='browser@example.com'"]);
  await page.getByLabel("Fresh authenticator code", { exact: true }).fill(await totp(secret!));
  await page.getByRole("button", { name: "Change password and sign out", exact: true }).click();
  await expect(page.getByRole("button", { name: "Sign in", exact: true })).toBeVisible();
  const sessions = execFileSync("docker", ["compose", "exec", "-T", "db", "psql", "-U", "providah", "-d", "providah_browser", "-Atc", "SELECT count(*) FROM sessions s JOIN users u ON u.id=s.user_id WHERE u.email='browser@example.com'"]).toString().trim();
  expect(sessions).toBe("0");
  await page.getByLabel("Email address", { exact: true }).fill("browser@example.com");
  await page.getByLabel("Password", { exact: true }).fill("browser-test-password");
  await expect(page.getByLabel("Authenticator code", { exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("alert")).toBeVisible();
  execFileSync("docker", ["compose", "exec", "-T", "db", "psql", "-U", "providah", "-d", "providah_browser", "-v", "ON_ERROR_STOP=1", "-c", "UPDATE users SET last_totp_step=0 WHERE email='browser@example.com'"]);
  await page.getByLabel("Password", { exact: true }).fill("browser-replacement-password");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByLabel("Password", { exact: true })).toHaveCount(0);
  await page.getByLabel("Authenticator code", { exact: true }).fill(await totp(secret!));
  await page.getByRole("button", { name: "Verify and sign in", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Operations overview", exact: true })).toBeVisible();
  await accountAction(page, "Recovery codes");
  await expect(page.getByRole("table", { name: "Account security history", exact: true })).toContainText("Password changed");
  await page.getByRole("button", { name: "Close", exact: true }).click();
  execFileSync("docker", ["compose","exec","-T","db","psql","-U","providah","-d","providah_browser","-v","ON_ERROR_STOP=1","-c","UPDATE users SET last_totp_step=0 WHERE email='browser@example.com'; DELETE FROM auth_limits WHERE key LIKE 'mfa:%'; INSERT INTO sessions(id,user_id,refresh_hash,expires_at) SELECT repeat('8',64),id,decode(repeat('cd',32),'hex'),now()+interval '1 day' FROM users WHERE email='browser@example.com'"]);
  await accountAction(page, "Active sessions");
  await expect(page.getByRole("table",{name:"Active sessions",exact:true})).toContainText("This session");
  await page.getByRole("button",{name:"Revoke session 888888888888",exact:true}).click();
  await page.getByLabel("Current password",{exact:true}).fill("browser-replacement-password");
  await page.getByLabel("Fresh authenticator code",{exact:true}).fill(await totp(secret!));
  await page.getByRole("button",{name:"Revoke selected session",exact:true}).click();
  await expect(page.getByRole("dialog")).toHaveCount(1);
  await expect(page.getByRole("button",{name:"Revoke session 888888888888",exact:true})).toHaveCount(0);
  await expect(page.getByRole("table",{name:"Active sessions",exact:true})).toContainText("This session");
  await page.getByRole("button",{name:"Close",exact:true}).click();
  await accountAction(page, "Recovery codes");
  await expect(page.getByRole("table",{name:"Account security history",exact:true})).toContainText("Session revoked");
  await page.getByRole("button",{name:"Close",exact:true}).click();
  execFileSync("docker", ["compose","exec","-T","db","psql","-U","providah","-d","providah_browser","-v","ON_ERROR_STOP=1","-c","INSERT INTO organizations(id,name) VALUES(repeat('c',64),'Zeta sandbox'); INSERT INTO memberships(org_id,user_id,permissions) SELECT repeat('c',64),id,ARRAY['resources.read','audit.read','admin.access'] FROM users WHERE email='browser@example.com'"]);
  await consolePage(page,"Inventory");
  await page.reload();
  await page.getByLabel("Search resources",{exact:true}).fill("Browser test server");
  await page.getByLabel("Filter resource type",{exact:true}).selectOption("compute.server");
  await page.getByLabel("Organization",{exact:true}).selectOption({label:"Zeta sandbox"});
  await expect(page.getByLabel("Search resources",{exact:true})).toHaveValue("");
  await expect(page.getByLabel("Filter resource type",{exact:true})).toHaveValue("");
  await expect(page.getByRole("link",{name:"Browser test server",exact:true})).toHaveCount(0);
  await consolePage(page,"Audit log");
  await page.getByRole("button",{name:"Filter events",exact:true}).click();
  await page.getByLabel("Action",{exact:true}).fill("operation.requested");
  await page.getByRole("button",{name:"Apply filters",exact:true}).click();
  await expect(page.getByText("Filters applied",{exact:true})).toBeVisible();
  await page.getByLabel("Organization",{exact:true}).selectOption({label:"Acme Operations"});
  await expect(page.getByText("Filters applied",{exact:true})).toHaveCount(0);
  await page.getByRole("button",{name:"Filter events",exact:true}).click();
  await expect(page.getByLabel("Action",{exact:true})).toHaveValue("");
  await page.getByRole("button",{name:"Close",exact:true}).click();
  await guestContext.close();
  expect(errors).toEqual([]);
  await accountAction(page, "Sign out");
  execFileSync("docker", ["compose", "exec", "-T", "db", "psql", "-U", "providah", "-d", "providah_browser", "-v", "ON_ERROR_STOP=1", "-c", "DELETE FROM auth_limits; UPDATE users SET mfa_enabled=false,global_admin=true WHERE email='browser@example.com'"]);
  await page.getByLabel("Email address", { exact: true }).fill("browser@example.com");
  await page.getByLabel("Password", { exact: true }).fill("browser-replacement-password");
  await expect(page.getByLabel("Authenticator code", { exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Operations overview", exact: true })).toBeVisible();

  await consolePage(page,"Administration");
  await page.getByRole("button", {name:"Create organization",exact:true}).click();
  const organizationDialog = page.getByRole("dialog");
  await organizationDialog.getByLabel("Organization name",{exact:true}).fill("New customer");
  await organizationDialog.getByLabel("Current password",{exact:true}).fill("browser-replacement-password");
  await expect(organizationDialog.getByLabel("Fresh authenticator code",{exact:true})).toHaveCount(0);
  await organizationDialog.getByRole("button",{name:"Create organization",exact:true}).click();
  await expect(organizationDialog).toHaveCount(0);
  await expect(page.getByLabel("Organization",{exact:true})).toContainText("New customer");
  await expect(page.getByLabel("Organization",{exact:true}).locator("option:checked")).toHaveText("New customer");
  await consolePage(page,"Audit log");
  await expect(page.getByRole("table")).toContainText("organization.created");
  await consolePage(page,"Team & access");
  await expect(page.getByRole("button",{name:"Invite member",exact:true})).toBeVisible();
  expect(errors).toEqual([]);

  await consolePage(page,"Administration");
  await page.getByRole("button",{name:"Global MFA policy",exact:true}).click();
  await expect(page.getByLabel("MFA requirement",{exact:true})).toHaveValue("optional");
  await page.getByLabel("Current password",{exact:true}).fill("browser-replacement-password");
  await page.getByRole("button",{name:"Save MFA policy",exact:true}).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await consolePage(page,"Sign-in policy");
  await page.getByRole("button",{name:"Organization MFA policy",exact:true}).click();
  await expect(page.getByLabel("MFA requirement",{exact:true})).toHaveValue("optional");
  await page.getByLabel("Current password",{exact:true}).fill("browser-replacement-password");
  await page.getByRole("button",{name:"Save MFA policy",exact:true}).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);

  await accountAction(page,"Enable MFA");
  await page.getByLabel("Current password",{exact:true}).fill("browser-replacement-password");
  await page.getByRole("button",{name:"Set up authenticator",exact:true}).click();
  const enrolledSeed=(await page.locator(".enrollment code").textContent())!;
  await page.getByLabel("Current password",{exact:true}).fill("browser-replacement-password");
  await page.getByLabel("Authenticator code",{exact:true}).fill(await totp(enrolledSeed));
  await page.getByRole("button",{name:"Save MFA setting and sign out",exact:true}).click();
  await page.getByLabel("Email address",{exact:true}).fill("browser@example.com");
  await page.getByLabel("Password",{exact:true}).fill("browser-replacement-password");
  await expect(page.getByLabel("Authenticator code",{exact:true})).toHaveCount(0);
  await page.getByRole("button",{name:"Sign in",exact:true}).click();
  execFileSync("docker",["compose","exec","-T","db","psql","-U","providah","-d","providah_browser","-v","ON_ERROR_STOP=1","-c","UPDATE users SET last_totp_step=0 WHERE email='browser@example.com'"]);
  await page.getByLabel("Authenticator code",{exact:true}).fill(await totp(enrolledSeed));
  await page.getByRole("button",{name:"Verify and sign in",exact:true}).click();
  await expect(page.getByRole("heading",{name:"Operations overview",exact:true})).toBeVisible();
  expect(errors).toEqual([]);

});

test("separate consoles enforce scoped navigation and close stale access", async ({page})=>{
 const alpha={id:"a".repeat(64),name:"Alpha",permissions:["admin.access","members.read","resources.read"]};
 const beta={id:"b".repeat(64),name:"Beta",permissions:["resources.read"]};
 let session={email:"delegated@example.test",globalAdmin:false,organizations:[alpha,beta]};
 const accessRequests:string[]=[];let inventoryRequests=0,directoryRequests=0;let teams:Record<string,unknown>[]=[];
 await page.route("**/api/**",route=>{
  const url=route.request().url();
  if(url.endsWith("/ListResources"))inventoryRequests++;
  if(url.endsWith("/ListInstallationAudit"))return route.fulfill({json:{events:[{id:"1",actor:"global@example.test",action:"user.access_changed",target:"u",details:'{"active":false}',occurredAt:"2026-09-06T00:00:00Z",organizationName:route.request().postDataJSON().source==="organizations"?"Beta":""}]}});
  if(url.endsWith("/ListTeams"))return route.fulfill({json:{teams}});
  if(url.endsWith("/DeleteTeam")){expect(route.request().postDataJSON().expectedRevision).toBe("1");teams=[];return route.fulfill({json:{}});}
  if(url.endsWith("/SaveTeam")){const r=route.request().postDataJSON();expect(r.userIds).toEqual(["c".repeat(64)]);teams=[{...r,id:"d".repeat(64),revision:"1"}];return route.fulfill({json:{}});}
  if(url.endsWith("/GetInstallationHealth"))return route.fulfill({json:{observedAt:"2026-09-06T00:00:00Z",databaseConnections:2,databaseCapacity:10,providerExecutionConfigured:true,work:[{kind:"operation",status:"uncertain",count:"3",oldestSeconds:7200}]}});
  if(url.endsWith("/SetInstallationOrganization")){const r=route.request().postDataJSON();expect(r.id).toBe(alpha.id);expect(r.expectedRevision).toBe("1");expect(r.active??false).toBe(false);return route.fulfill({json:{}});}
  if(url.endsWith("/SetInstallationUser")){const r=route.request().postDataJSON();expect(r.id).toBe("u");expect(r.expectedRevision).toBe("1");expect(r.password).toBe("browser-test-password");return route.fulfill({json:{}});}
  if(url.endsWith("/ListInstallationDirectory")){directoryRequests++;return route.fulfill({json:{organizations:session.organizations.map(o=>({...o,active:true,revision:"1"})),users:[{id:"u",email:"global@example.test",active:true,globalAdmin:true,mfaEnabled:false,revision:"1"}]}});}

  if(url.endsWith("/GetSession"))return route.fulfill({json:session});
  if(url.endsWith("/ListAccess")){accessRequests.push(route.request().postDataJSON().organizationId);return route.fulfill({json:{roles:[{id:"viewer",name:"Viewer",permissions:["resources.read"],builtin:true}],members:[{userId:"c".repeat(64),email:"member@example.test",roleId:"viewer",active:true}],invitations:[],permissionCatalog:[]}});}
  return route.fulfill({json:{}});
 });
 await page.goto(`/app/resources?org=${alpha.id}&view=${"f".repeat(64)}`);
 await expect(page.getByRole("heading",{name:"Saved view unavailable",exact:true})).toBeVisible();
 expect(inventoryRequests).toBe(0);
 for(const filters of ["{",JSON.stringify({surprise:true}),JSON.stringify({hiddenColumns:["name","provider","kind","region","status","observedAt"]})]){
  await page.goto("/app/resources?"+new URLSearchParams({org:alpha.id,filters}));
  await expect(page.getByRole("heading",{name:"Inventory link unavailable",exact:true})).toBeVisible();
  expect(inventoryRequests).toBe(0);
 }
 const snapshot={search:"production",provider:"hetzner",sortBy:"name",descending:true,hiddenColumns:["region"],tagKey:"env",tagValue:"prod",region:"fsn1"};
 await page.goto("/app/resources?"+new URLSearchParams({org:alpha.id,filters:JSON.stringify(snapshot)}));
 await expect(page.getByLabel("Search resources",{exact:true})).toHaveValue("production");
 await expect(page.getByLabel("Filter provider",{exact:true})).toHaveValue("hetzner");
 await expect(page.getByText("Resource scope active · fsn1",{exact:false})).toBeVisible();
 await page.getByLabel("Search resources",{exact:true}).fill("production api");
 const filterLink=page.getByRole("link",{name:"Link to current filters",exact:true});
 const filterURL=new URL((await filterLink.getAttribute("href"))!,"http://localhost:5174");
 expect(JSON.parse(filterURL.searchParams.get("filters")!)).toMatchObject({...snapshot,search:"production api"});
 expect(filterURL.searchParams.has("view")).toBe(false);
 await filterLink.click();
 await expect.poll(()=>JSON.parse(new URL(page.url()).searchParams.get("filters")!).search).toBe("production api");
 await page.reload();
 await expect(page.getByLabel("Search resources",{exact:true})).toHaveValue("production api");

 await page.goto("/app");
 await expect(page.getByRole("link",{name:"Team & access",exact:true})).toHaveCount(0);
 await page.getByRole("link",{name:"Admin console",exact:true}).click();
 await expect(page.getByRole("heading",{name:"Administration",exact:true})).toBeVisible();
 await expect(page.getByRole("table")).toContainText("Alpha");
 await expect(page.getByRole("table")).not.toContainText("Beta");
 await expect(page.getByLabel("Organization",{exact:true}).locator("option")).toHaveCount(1);
 await expect(page.getByRole("button",{name:"Create organization",exact:true})).toHaveCount(0);
 await expect(page.getByRole("button",{name:"Global MFA policy",exact:true})).toHaveCount(0);
 await page.getByRole("link",{name:"Team & access",exact:true}).click();
 await expect(page.getByRole("heading",{name:"Team & access",exact:true})).toBeVisible();
 await expect(page.getByRole("button",{name:"Invite member",exact:true})).toHaveCount(0);
 expect(accessRequests).toEqual([alpha.id]);
 await page.goto("/app/resources?org="+"f".repeat(64));
 await expect(page.getByRole("heading",{name:"Organization unavailable",exact:true})).toBeVisible();
 await page.goto("/admin/modules");
 await expect(page.getByRole("navigation",{name:"Breadcrumb",exact:true})).toContainText("Provider modules");
 await expect(page.getByRole("heading",{name:"Permission required",exact:true})).toBeVisible();
 await page.goto("/access?review=1");
 await expect.poll(()=>new URL(page.url()).pathname).toBe("/admin/access");
 await expect.poll(()=>new URL(page.url()).searchParams.get("review")).toBe("1");
 session={...session,organizations:[{...alpha,permissions:["resources.read"]},beta]};
 await page.reload();
 await expect(page.getByRole("heading",{name:"Administration access required",exact:true})).toBeVisible();
 await expect(page.getByRole("link",{name:"Team & access",exact:true})).toHaveCount(0);
 await page.getByRole("link",{name:"User console",exact:true}).click();
 await expect(page.getByRole("link",{name:"Admin console",exact:true})).toHaveCount(0);
 expect(directoryRequests).toBe(0);
 session={...session,globalAdmin:true,organizations:[{...alpha,permissions:["admin.access","members.read"]},{...beta,permissions:["admin.access","members.read"]}]};
 await page.goto("/admin");
 await expect(page.getByRole("table")).toContainText("Alpha");
 await expect(page.getByRole("table")).toContainText("Beta");
 await expect(page.getByRole("button",{name:"Create organization",exact:true})).toBeVisible();
 await expect(page.getByRole("button",{name:"Global MFA policy",exact:true})).toBeVisible();
 await page.getByRole("button",{name:"Manage organization",exact:true}).first().click();
 await expect(page.getByRole("dialog")).toContainText("cloud resources keep running");
 await expect(page.getByLabel("Installation access",{exact:true})).toHaveCount(0);
 await page.getByLabel("Account or organization status",{exact:true}).selectOption("disabled");
 await page.getByLabel("Current password",{exact:true}).fill("browser-test-password");
 await page.getByRole("button",{name:"Save organization status",exact:true}).click();
 await expect(page.getByRole("dialog")).toHaveCount(0);
 await page.getByLabel("Directory",{exact:true}).selectOption("users");
 await expect(page.getByRole("table",{name:"Installation users"})).toContainText("global@example.test");
 await expect(page.getByRole("table",{name:"Installation users"})).toContainText("Global administrator");
 await page.getByRole("button",{name:"Manage access",exact:true}).click();
 await expect(page.getByRole("dialog")).toContainText("global@example.test");
 await expect(page.getByLabel("Fresh authenticator code",{exact:true})).toHaveCount(0);
 await page.getByLabel("Account or organization status",{exact:true}).selectOption("disabled");
 await page.getByLabel("Installation access",{exact:true}).selectOption("scoped");
 await page.getByLabel("Current password",{exact:true}).fill("browser-test-password");
 await page.getByRole("button",{name:"Save access and sign user out",exact:true}).click();
 await expect(page.getByRole("dialog")).toHaveCount(0);
 expect(directoryRequests).toBeGreaterThan(0);
 await page.screenshot({path:"test-results/admin-directory.png",fullPage:true});
 await page.getByRole("link",{name:"Global audit",exact:true}).click();
 await expect(page.getByRole("heading",{name:"Global audit",exact:true})).toBeVisible();
 await expect(page.getByRole("table",{name:"Events"})).toContainText("user.access_changed");
 await page.getByLabel("Audit source",{exact:true}).selectOption("organizations");
 await expect(page.getByRole("table",{name:"Events"})).toContainText("Beta");
 await page.reload();
 await expect(page.getByRole("navigation",{name:"Breadcrumb",exact:true})).toContainText("Global audit");
 await page.getByRole("link",{name:"Global health",exact:true}).click();
 await expect(page.getByRole("heading",{name:"Global health",exact:true})).toBeVisible();
 await expect(page.getByRole("table",{name:"Installation workload"})).toContainText("uncertain");
 await expect(page.getByRole("table",{name:"Installation workload"})).toContainText("2h 0m");
 await page.reload();
 await expect(page.getByRole("navigation",{name:"Breadcrumb",exact:true})).toContainText("Global health");
 session={...session,organizations:[{...alpha,permissions:[...alpha.permissions,"members.manage"]}]};
 await page.goto("/admin/access?org="+alpha.id);
 await page.getByRole("button",{name:"Create team",exact:true}).click();
 await page.getByRole("checkbox",{name:"member@example.test",exact:true}).check();
 await page.getByLabel("Team name",{exact:true}).fill("Operations team");
 await page.getByLabel("Team role",{exact:true}).selectOption("viewer");
 await page.getByRole("button",{name:"Save team",exact:true}).click();
 await expect(page.getByRole("dialog")).toHaveCount(0);
 await page.getByRole("button",{name:"Operations team",exact:true}).click();
 await expect.poll(()=>new URL(page.url()).searchParams.get("team")).toBe("d".repeat(64));
 await page.reload();
 await expect(page.getByRole("navigation",{name:"Team breadcrumb"})).toContainText("Operations team");
 await expect(page.getByRole("checkbox",{name:"member@example.test",exact:true})).toBeChecked();
 const teamURL=page.url();
 await page.getByRole("button",{name:"Delete team",exact:true}).click();
 await page.getByLabel("Type the team name to delete",{exact:true}).fill("Operations team");
 await page.getByRole("button",{name:"Delete team permanently",exact:true}).click();
 await expect(page.getByRole("dialog")).toHaveCount(0);
 await page.goto(teamURL);
 await expect(page.getByRole("alert")).toContainText("This team is unavailable");




});

test("overview respects permissions and links attention items",async({page})=>{
 const org={id:"a".repeat(64),name:"Overview organization",permissions:["operations.read"]};
 const pending={id:"b".repeat(64),resourceName:"Production API",resourceKind:"compute.server",action:"start",status:"OPERATION_STATUS_AWAITING_APPROVAL",updatedAt:"2026-09-06T00:00:00Z",createdAt:"2026-09-06T00:00:00Z"};
 let failed=false;const calls:Record<string,number>={};
 await page.route("**/api/**",route=>{
  const method=route.request().url().split("/").pop()!;calls[method]=(calls[method]??0)+1;
  if(method==="GetSession")return route.fulfill({json:{email:"reader@example.test",organizations:[org]}});
  if(method==="GetOperationsOverview")return failed?route.fulfill({status:403,json:{code:"permission_denied",message:"Overview unavailable"}}):route.fulfill({json:{pending:"13",active:"2",failed:"3",uncertain:"1",approvals:[pending],problems:[{...pending,id:"c".repeat(64),resourceName:"Worker",status:"OPERATION_STATUS_UNCERTAIN"}]}});
  if(method==="GetOperation")return route.fulfill({json:{operation:pending}});
  if(method==="ListConnections")return route.fulfill({json:{connections:[{id:"d".repeat(64),name:"Needs discovery",provider:"hetzner",enabled:true,scanStatus:"SCAN_STATUS_FAILED"}]}});
  if(method==="ListSchedules")return route.fulfill({json:{schedules:[{id:"e".repeat(64),name:"Morning start",enabled:true,identityEnabled:true,nextDue:"2027-01-01T08:00:00Z",nextLocal:"2027-01-01 08:00 UTC",nextAction:"start",approved:false}]}});
  return route.fulfill({json:{}});
 });
 await page.goto(`/app?org=${org.id}`);
 await expect(page.getByRole("table",{name:"Pending approvals",exact:true})).toContainText("Production API");
 await expect(page.locator(".stat").filter({hasText:"Awaiting approval"}).locator("strong")).toHaveText("13");
 await expect(page.getByRole("table",{name:"Recent operation problems",exact:true})).toContainText("Uncertain");
 expect(calls.ListConnections??0).toBe(0);expect(calls.ListSchedules??0).toBe(0);expect(calls.ListAudit??0).toBe(0);
 await expect(page.getByRole("link",{name:"Add connection",exact:true})).toHaveCount(0);
 await page.getByRole("link",{name:"Start · Production API",exact:true}).click();
 await expect.poll(()=>new URL(page.url()).searchParams.get("operation")).toBe(pending.id);
 org.permissions.push("connections.read","schedules.read");
 await page.goto(`/app?org=${org.id}`);
 await expect(page.getByRole("table",{name:"Connections needing attention",exact:true})).toContainText("Needs discovery");
 await expect(page.getByRole("table",{name:"Upcoming schedules",exact:true})).toContainText("Awaiting approval");
 await page.screenshot({path:"test-results/overview-attention.png",fullPage:true});
 failed=true;await page.reload();
 await expect(page.getByText("Overview unavailable",{exact:true})).toBeVisible();
 await expect(page.getByRole("table",{name:"Pending approvals",exact:true})).toHaveCount(0);
 await expect(page.getByRole("table",{name:"Recent operation problems",exact:true})).toHaveCount(0);
});

test("notification subscriptions preserve selections and reject stale saves",async({page})=>{
 const org={id:"a".repeat(64),name:"Alerts organization",permissions:["admin.access","notifications.manage","notifications.read"]};
 const destination={id:"b".repeat(64),name:"Operations inbox",kind:"email",endpoint:"ops@example.test",verified:true,enabled:true,revision:"4",eventTypes:[] as string[]};
 let stale=false;let saves=0;
 await page.route("**/api/**",route=>{
  const method=route.request().url().split("/").pop();
  if(method==="GetSession")return route.fulfill({json:{email:"admin@example.test",organizations:[org]}});
  if(method==="GetNotificationDestination")return route.fulfill({json:destination});
  if(method==="ListNotificationDestinations")return route.fulfill({json:{destinations:[destination],availableEventTypes:["operation.failed","operation.succeeded"]}});
  if(method==="SetNotificationSubscriptions"){
   saves++;const body=route.request().postDataJSON();
   expect(body.expectedRevision).toBe(destination.revision);
   expect(body.organizationId).toBe(org.id);
   if(stale)return route.fulfill({status:409,json:{code:"failed_precondition",message:"Destination changed. Reload and review it before continuing."}});
   destination.eventTypes=body.eventTypes??[];destination.revision=String(Number(destination.revision)+1);
   return route.fulfill({json:{}});
  }
  return route.fulfill({json:{}});
 });
 const open=async()=>{
  await page.getByRole("link",{name:"Operations inbox",exact:true}).click();
  await page.getByRole("button",{name:"Event subscriptions",exact:true}).click();
 };
 await page.goto(`/admin/notifications?org=${org.id}`);await open();
 await page.getByLabel("Event selection",{exact:true}).selectOption("custom");
 await page.getByRole("button",{name:"Save subscriptions",exact:true}).click();
 await expect(page.getByText("Select at least one event, or use defaults.",{exact:true})).toBeVisible();expect(saves).toBe(0);
 await page.getByRole("checkbox",{name:"operation.failed",exact:true}).check();
 await page.getByRole("button",{name:"Save subscriptions",exact:true}).click();
 await expect(page.getByRole("dialog")).toHaveCount(0);
 await page.reload();await open();
 await expect(page.getByRole("checkbox",{name:"operation.failed",exact:true})).toBeChecked();
 await expect(page.getByRole("checkbox",{name:"operation.succeeded",exact:true})).not.toBeChecked();
 stale=true;await page.getByRole("checkbox",{name:"operation.succeeded",exact:true}).check();
 await page.getByRole("button",{name:"Save subscriptions",exact:true}).click();
 await expect(page.getByText("Destination changed. Reload and review it before continuing.",{exact:true})).toBeVisible();
 await page.screenshot({path:"test-results/notification-subscriptions.png",fullPage:true});
 stale=false;await page.getByLabel("Event selection",{exact:true}).selectOption("default");
 await page.getByRole("button",{name:"Save subscriptions",exact:true}).click();
 await expect(page.getByRole("dialog")).toHaveCount(0);expect(destination.eventTypes).toEqual([]);
});

test("notification deep links survive reload and hide denied actions",async({page})=>{
 const org={id:"a".repeat(64),name:"Notification organization",permissions:["admin.access","notifications.manage","notifications.read"]};
 const destination={id:"b".repeat(64),name:"Deep linked receiver",kind:"webhook",endpoint:"https://example.test/hooks",enabled:true,verified:true,revision:"2"};
 const delivery={id:"c".repeat(64)+":"+destination.id,eventId:"c".repeat(64),destinationName:destination.name,eventType:"operation.failed",status:"dead",attempts:8};
 let denied=false;
 await page.route("**/api/**",route=>{
  const method=route.request().url().split("/").pop();
  if(method==="GetSession")return route.fulfill({json:{email:"admin@example.test",organizations:[org]}});
  if(method==="GetNotificationDestination"||method==="GetNotificationDelivery")return denied?route.fulfill({status:403,json:{code:"permission_denied",message:"Notification unavailable"}}):route.fulfill({json:method==="GetNotificationDestination"?destination:delivery});
  return route.fulfill({json:{}});
 });
 await page.goto(`/admin/notifications?org=${org.id}&destination=${destination.id}`);
 await expect(page.getByRole("dialog",{name:destination.name,exact:true})).toBeVisible();
 await page.reload();await expect(page.getByRole("button",{name:"Event subscriptions",exact:true})).toBeVisible();
 await expect(page.getByRole("navigation",{name:"Destination breadcrumb",exact:true})).toContainText(destination.name);
 await page.getByRole("navigation",{name:"Destination breadcrumb",exact:true}).getByRole("link",{name:"Notifications",exact:true}).click();
 await expect(page.getByRole("dialog")).toHaveCount(0);
 await page.goBack();await expect(page.getByRole("dialog",{name:destination.name,exact:true})).toBeVisible();
 denied=true;await page.reload();await expect(page.getByText("Notification unavailable",{exact:true})).toBeVisible();
 await expect(page.getByRole("button",{name:"Send test notification",exact:true})).toHaveCount(0);
 denied=false;await page.goto(`/admin/notifications?org=${org.id}&delivery=${delivery.id}`);
 await expect(page.getByRole("button",{name:"Redeliver same event",exact:true})).toBeVisible();
 await page.reload();await expect(page.getByRole("navigation",{name:"Delivery breadcrumb",exact:true})).toContainText(destination.name);
 await page.screenshot({path:"test-results/notification-delivery-link.png",fullPage:true});
 denied=true;await page.reload();await expect(page.getByText("Notification unavailable",{exact:true})).toBeVisible();
 await expect(page.getByRole("button",{name:"Redeliver same event",exact:true})).toHaveCount(0);
});

test("notification removal requires confirmation and preserves history",async({page})=>{
 const org={id:"a".repeat(64),name:"Removal organization",permissions:["admin.access","notifications.manage","notifications.read"]};
 const destination={id:"b".repeat(64),name:"Retired receiver",kind:"webhook",endpoint:"https://example.test/hooks",enabled:true,verified:true,revision:"7"};
 const delivery={id:"c".repeat(64)+":"+destination.id,eventId:"c".repeat(64),destinationName:destination.name,eventType:"operation.failed",status:"canceled",attempts:0};
 let removed=false,stale=true;
 await page.route("**/api/**",route=>{
  const method=route.request().url().split("/").pop();
  if(method==="GetSession")return route.fulfill({json:{email:"admin@example.test",organizations:[org]}});
  if(method==="ListNotificationDestinations")return route.fulfill({json:{destinations:removed?[]:[destination]}});
  if(method==="ListNotificationDeliveries")return route.fulfill({json:{deliveries:[delivery]}});
  if(method==="GetNotificationDelivery")return route.fulfill({json:delivery});
  if(method==="GetNotificationDestination")return removed?route.fulfill({status:403,json:{code:"permission_denied",message:"Destination unavailable"}}):route.fulfill({json:destination});
  if(method==="DeleteNotificationDestination"){
   const body=route.request().postDataJSON();expect(body.expectedRevision).toBe("7");expect(body.confirmation).toBe(destination.name);expect(body.organizationId).toBe(org.id);
   if(stale)return route.fulfill({status:409,json:{code:"failed_precondition",message:"Destination changed. Reload and review it before removing."}});
   removed=true;return route.fulfill({json:{}});
  }
  return route.fulfill({json:{}});
 });
 await page.goto(`/admin/notifications?org=${org.id}&destination=${destination.id}`);
 await page.getByRole("button",{name:"Remove destination",exact:true}).click();
 await page.getByLabel("Confirm destination name",{exact:true}).fill(destination.name);
 await page.getByRole("button",{name:"Remove destination permanently",exact:true}).click();
 await expect(page.getByText("Destination changed. Reload and review it before removing.",{exact:true})).toBeVisible();expect(removed).toBe(false);
 await page.screenshot({path:"test-results/notification-removal.png",fullPage:true});
 stale=false;await page.getByRole("button",{name:"Remove destination permanently",exact:true}).click();
 await expect(page.getByRole("dialog")).toHaveCount(0);
 await expect(page.getByRole("table",{name:"Notification destinations",exact:true})).not.toContainText(destination.name);
 await expect(page.getByRole("table",{name:"Notification deliveries",exact:true})).toContainText(destination.name);
 await page.goto(`/admin/notifications?org=${org.id}&destination=${destination.id}`);
 await expect(page.getByText("Destination unavailable",{exact:true})).toBeVisible();
 await expect(page.getByRole("button",{name:"Send test notification",exact:true})).toHaveCount(0);
});

test("database snapshots use shared reviewed deletion",async({page})=>{
 const org={id:"a".repeat(64),name:"Database organization",permissions:["resources.read","operations.read","operations.request","operations.delete"]};
 const resource={id:"b".repeat(64),connectionId:"c".repeat(64),nativeId:"manual-recovery",name:"Database recovery",provider:"aws",kind:"database.cluster_snapshot",region:"us-east-1",status:"available"};
 let requested=false;
 await page.route("**/api/**",route=>{
  const method=route.request().url().split("/").pop();
  if(method==="GetSession")return route.fulfill({json:{email:"operator@example.test",organizations:[org]}});
  if(method==="GetResource")return route.fulfill({json:{resource,connectionName:"AWS",connectionEnabled:true,providerEnabled:true,availableActions:["delete"]}});
  if(method==="PreviewDeletion")return route.fulfill({json:{impact:"Delete manual database snapshot. Source database retained; recovery copy lost.",digest:"d".repeat(64)}});
  if(method==="RequestOperation"){
   const body=route.request().postDataJSON();expect(body.resourceId).toBe(resource.id);expect(body.confirmation).toBe(resource.nativeId);expect(body.deletionDigest).toBe("d".repeat(64));requested=true;
  }
  return route.fulfill({json:{}});
 });
 await page.goto(`/app/resources/detail/${resource.id}?org=${org.id}`);
 await page.getByRole("button",{name:"Delete resource",exact:true}).click();
 await page.getByRole("button",{name:"Read deletion impact",exact:true}).click();
 await page.getByLabel(`Type ${resource.nativeId} to confirm deletion and the listed data loss`,{exact:true}).fill(resource.nativeId);
 await page.getByLabel("Reason for this action",{exact:true}).fill("Remove obsolete manual recovery copy");
 await page.screenshot({path:"test-results/database-snapshot-delete.png",fullPage:true});
 await page.getByRole("button",{name:"Request delete resource",exact:true}).click();
 await expect(page.getByRole("dialog")).toHaveCount(0);expect(requested).toBe(true);
});

test("AWS images use shared reviewed deletion",async({page})=>{
 const org={id:"a".repeat(64),name:"Database organization",permissions:["resources.read","operations.read","operations.request","operations.delete"]};
 const resource={id:"b".repeat(64),connectionId:"c".repeat(64),nativeId:"ami-12345678",name:"Server recovery image",provider:"aws",kind:"compute.image",region:"us-east-1",status:"available"};
 let requested=false;
 await page.route("**/api/**",route=>{
  const method=route.request().url().split("/").pop();
  if(method==="GetSession")return route.fulfill({json:{email:"operator@example.test",organizations:[org]}});
  if(method==="GetResource")return route.fulfill({json:{resource,connectionName:"AWS",connectionEnabled:true,providerEnabled:true,availableActions:["delete"]}});
  if(method==="PreviewDeletion")return route.fulfill({json:{impact:"Deregister image. Associated snapshots and existing servers retained; future launches fail.",digest:"d".repeat(64)}});
  if(method==="RequestOperation"){
   const body=route.request().postDataJSON();expect(body.resourceId).toBe(resource.id);expect(body.confirmation).toBe(resource.nativeId);expect(body.deletionDigest).toBe("d".repeat(64));requested=true;
  }
  return route.fulfill({json:{}});
 });
 await page.goto(`/app/resources/detail/${resource.id}?org=${org.id}`);
 await page.getByRole("button",{name:"Delete resource",exact:true}).click();
 await page.getByRole("button",{name:"Read deletion impact",exact:true}).click();
 await page.getByLabel(`Type ${resource.nativeId} to confirm deletion and the listed data loss`,{exact:true}).fill(resource.nativeId);
 await page.getByLabel("Reason for this action",{exact:true}).fill("Remove obsolete manual recovery copy");
 await page.screenshot({path:"test-results/image-delete.png",fullPage:true});
 await page.getByRole("button",{name:"Request delete resource",exact:true}).click();
 await expect(page.getByRole("dialog")).toHaveCount(0);expect(requested).toBe(true);
});

for(const cloud of ["aws","digitalocean","hetzner"]) {
test("load balancers use shared reviewed deletion / "+cloud,async({page})=>{
 const org={id:"a".repeat(64),name:"Database organization",permissions:["resources.read","operations.read","operations.request","operations.delete"]};
 const resource={id:"b".repeat(64),connectionId:"c".repeat(64),nativeId:cloud==="aws"?"arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/edge/0123456789abcdef":cloud==="hetzner"?"4567":"12345678-1234-1234-1234-123456789abc",name:"Public routing",provider:cloud,kind:"network.load_balancer",region:cloud==="aws"?"us-east-1":cloud==="hetzner"?"fsn1":"nyc3",status:cloud==="hetzner"?"present":"active"};
 let requested=false;
 await page.route("**/api/**",route=>{
  const method=route.request().url().split("/").pop();
  if(method==="GetSession")return route.fulfill({json:{email:"operator@example.test",organizations:[org]}});
  if(method==="GetResource")return route.fulfill({json:{resource,connectionName:"AWS",connectionEnabled:true,providerEnabled:true,availableActions:["delete"]}});
  if(method==="PreviewDeletion")return route.fulfill({json:{impact:"Delete load balancer. Traffic stops; backend servers retained.",digest:"d".repeat(64)}});
  if(method==="RequestOperation"){
   const body=route.request().postDataJSON();expect(body.resourceId).toBe(resource.id);expect(body.confirmation).toBe(resource.nativeId);expect(body.deletionDigest).toBe("d".repeat(64));requested=true;
  }
  return route.fulfill({json:{}});
 });
 await page.goto(`/app/resources/detail/${resource.id}?org=${org.id}`);
 await page.getByRole("button",{name:"Delete resource",exact:true}).click();
 await page.getByRole("button",{name:"Read deletion impact",exact:true}).click();
 await page.getByLabel(`Type ${resource.nativeId} to confirm deletion and the listed data loss`,{exact:true}).fill(resource.nativeId);
 await page.getByLabel("Reason for this action",{exact:true}).fill("Retire reviewed public routing");
 await page.screenshot({path:`test-results/load-balancer-delete-${cloud}.png`,fullPage:true});
 await page.getByRole("button",{name:"Request delete resource",exact:true}).click();
 await expect(page.getByRole("dialog")).toHaveCount(0);expect(requested).toBe(true);
});

}

test("provider modules show verified admission and expiry",async({page})=>{
 const org={id:"a".repeat(64),name:"Runtime organization",permissions:["admin.access","modules.read","modules.manage"]};
 const image="sha256:"+"b".repeat(64);
 const runtime={image,version:"1.2.0",sdkVersion:"1",publisherKeyId:"approved-publisher",approvalExpiresAt:"2099-06-01T00:00:00Z"};
 const module={provider:"aws",enabled:true,revision:"1",runtimeId:image,publisherKeyId:runtime.publisherKeyId,approvalExpiresAt:runtime.approvalExpiresAt,runtimes:[runtime,{image:"sha256:"+"c".repeat(64),version:"1.1.0",sdkVersion:"1"}]};
 let denied=false;
 await page.route("**/api/**",route=>{
  const method=route.request().url().split("/").pop();
  if(method==="GetSession")return route.fulfill({json:{email:"admin@example.test",organizations:[org]}});
  if(method==="ListProviderModules")return denied?route.fulfill({status:403,json:{code:"permission_denied",message:"Module catalog unavailable"}}):route.fulfill({json:{modules:[module]}});
  return route.fulfill({json:{}});
 });
 await page.goto(`/admin/modules?org=${org.id}`);
 await expect(page.getByRole("table",{name:"Provider modules",exact:true})).toContainText("Publisher verified");
 await page.getByRole("button",{name:"aws",exact:true}).click();
 await expect(page.getByRole("dialog")).toContainText("approved-publisher");
 await expect(page.getByRole("dialog")).toContainText("2099");
 await page.getByRole("button",{name:"Change runtime / rollback",exact:true}).click();
 await expect(page.getByLabel("Approved provider runtime",{exact:true})).toContainText("Deployment approved");
 await page.screenshot({path:"test-results/runtime-publisher.png",fullPage:true});
 module.approvalExpiresAt="2000-01-01T00:00:00Z";await page.reload();
 await expect(page.getByRole("table",{name:"Provider modules",exact:true})).toContainText("Publisher approval expired");
 module.publisherKeyId="";module.approvalExpiresAt="";await page.reload();
 await expect(page.getByRole("table",{name:"Provider modules",exact:true})).toContainText("Deployment approved");
 module.runtimes=[];await page.reload();
 await expect(page.getByRole("table",{name:"Provider modules",exact:true}).getByRole("row").nth(1).getByRole("cell").nth(5)).toHaveText("Unavailable");
 denied=true;await page.reload();await expect(page.getByText("Module catalog unavailable",{exact:true})).toBeVisible();
 await expect(page.getByRole("button",{name:"aws",exact:true})).toHaveCount(0);
});

test("connection details have independent deep links and deny stale controls",async({page})=>{
 const org={id:"a".repeat(64),name:"Connection organization",permissions:["admin.access","connections.read","connections.manage"]};
 const connection={id:"b".repeat(64),organizationId:org.id,name:"Linked cloud",provider:"hetzner",region:"fsn1",enabled:true,createdAt:"2026-01-01T00:00:00Z",credentialSource:"builtin"};
 let denied=false;let toggled=false;
 await page.route("**/api/**",route=>{
  const method=route.request().url().split("/").pop();
  if(method==="GetSession")return route.fulfill({json:{email:"admin@example.test",organizations:[org]}});
  // The record is intentionally outside the directory result.
  if(method==="ListConnections")return route.fulfill({json:{connections:[]}});
  if(method==="GetConnection"){
   expect(route.request().postDataJSON()).toEqual({organizationId:org.id,id:connection.id});
   return denied?route.fulfill({status:403,json:{code:"permission_denied",message:"Connection unavailable"}}):route.fulfill({json:{connection}});
  }
  if(method==="SetConnectionEnabled") {const body=route.request().postDataJSON();expect(body).toEqual({organizationId:org.id,id:connection.id});expect(body.enabled??false).toBe(false);connection.enabled=false;toggled=true;return route.fulfill({json:{connection}});}
  return route.fulfill({json:{}});
 });
 await page.goto(`/admin/connections?org=${org.id}&connection=${connection.id}`);
 await expect(page.getByRole("dialog",{name:connection.name,exact:true})).toBeVisible();
 await expect(page.getByRole("navigation",{name:"Connection breadcrumb"})).toContainText(connection.name);
 await page.reload();await expect(page.getByRole("dialog")).toContainText("fsn1");
 await page.getByRole("dialog").getByRole("button",{name:"Disable",exact:true}).click();
 await expect(page.getByRole("dialog").getByRole("button",{name:"Enable",exact:true})).toBeVisible();expect(toggled).toBe(true);
 await page.screenshot({path:"test-results/connection-detail.png",fullPage:true});
 denied=true;await page.reload();await expect(page.getByText("Connection unavailable",{exact:true})).toBeVisible();
 await expect(page.getByRole("dialog").getByRole("button",{name:"Enable",exact:true})).toHaveCount(0);
 await page.getByRole("navigation",{name:"Connection breadcrumb"}).getByRole("link",{name:"Connections",exact:true}).click();
 await expect(page.getByRole("dialog")).toHaveCount(0);
});

test("publishers can cancel active automation validations",async({page})=>{
 const org={id:"a".repeat(64),name:"Automation organization",permissions:["templates.read","templates.publish"]};
 const check={id:"b".repeat(64),versionId:"c".repeat(64),status:"running",detail:"Checking source",createdAt:"2026-01-01T00:00:00Z"};
 let canceled=false;
 await page.route("**/api/**",route=>{
  const method=route.request().url().split("/").pop();
  if(method==="GetSession")return route.fulfill({json:{email:"publisher@example.test",organizations:[org]}});
  if(method==="ListAutomationValidations")return route.fulfill({json:{runnerConfigured:true,validations:[check]}});
  if(method==="CancelAutomationValidation"){
   expect(route.request().postDataJSON()).toEqual({organizationId:org.id,id:check.id});
   check.status="canceled";check.detail="Canceled by an authorized publisher.";canceled=true;
   return route.fulfill({json:check});
  }
  return route.fulfill({json:{}});
 });
 await page.goto(`/app/templates?org=${org.id}`);
 const table=page.getByRole("table",{name:"Automation validations",exact:true});
 await table.getByRole("button",{name:"Cancel validation",exact:true}).click();
 await expect(table).toContainText("Canceled by an authorized publisher.");
 await expect(table.getByRole("button",{name:"Cancel validation",exact:true})).toHaveCount(0);expect(canceled).toBe(true);
 check.status="queued";org.permissions=["templates.read"];await page.reload();
 await expect(table).toContainText("queued");
 await expect(table.getByRole("button",{name:"Cancel validation",exact:true})).toHaveCount(0);
});

test("notification grouping saves a reviewed organization policy",async({page})=>{
 const org={id:"a".repeat(64),name:"Grouping organization",permissions:["admin.access","notifications.manage"]};
 const policy={seconds:900,revision:"1"};let saved=false;
 await page.route("**/api/**",route=>{
  const method=route.request().url().split("/").pop();
  if(method==="GetSession")return route.fulfill({json:{email:"admin@example.test",organizations:[org]}});
  if(method==="GetNotificationGrouping")return route.fulfill({json:policy});
  if(method==="SetNotificationGrouping"){
   const body=route.request().postDataJSON();expect(body.organizationId).toBe(org.id);expect(body.expectedRevision).toBe("1");expect(body.seconds??0).toBe(0);
   policy.seconds=0;policy.revision="2";saved=true;return route.fulfill({json:policy});
  }
  return route.fulfill({json:{}});
 });
 await page.goto(`/admin/notifications?org=${org.id}`);
 await page.getByRole("button",{name:"Alert grouping",exact:true}).click();
 await expect(page.getByLabel("Grouping window",{exact:true})).toHaveValue("900");
 await page.getByLabel("Grouping window",{exact:true}).selectOption("0");
 await page.getByRole("button",{name:"Save grouping",exact:true}).click();
 await expect(page.getByRole("dialog")).toHaveCount(0);expect(saved).toBe(true);
 await page.getByRole("button",{name:"Alert grouping",exact:true}).click();
 await expect(page.getByLabel("Grouping window",{exact:true})).toHaveValue("0");
 await page.screenshot({path:"test-results/notification-grouping.png",fullPage:true});
});

test("server creation reviews and submits a private network",async({page})=>{
 const org={id:"a".repeat(64),name:"Network organization",permissions:["resources.read","connections.read","operations.read","operations.request","operations.create"]};
 const connection={id:"b".repeat(64),name:"Hetzner account",provider:"hetzner",region:"fsn1",enabled:true};let submitted=false;
 await page.route("**/api/**",route=>{
  const method=route.request().url().split("/").pop();
  if(method==="GetSession")return route.fulfill({json:{email:"operator@example.test",organizations:[org]}});
  if(method==="ListConnections")return route.fulfill({json:{connections:[connection]}});
  if(method==="ListResources"){
   const kind=route.request().postDataJSON().kind;
   const item=({"compute.image":["11","Ubuntu"],"compute.type":["1","cx23"],"access.ssh_key":["12","Operator key"],"network.network":["44","Production network"]} as Record<string,string[]>)[kind];
   return route.fulfill({json:{resources:item?[{id:"c".repeat(64),nativeId:item[0],name:item[1],provider:"hetzner",kind,region:"global",status:"available"}]:[]}});
  }
  if(method==="RequestServerCreation"){expect(route.request().postDataJSON().creation.network).toBe("44");submitted=true;return route.fulfill({json:{operation:{id:"d".repeat(64)}}});}
  return route.fulfill({json:{}});
 });
 await page.goto(`/app/resources/all?org=${org.id}`);
 await page.getByRole("button",{name:"Create server",exact:true}).click();
 await page.getByLabel("Cloud connection",{exact:true}).selectOption(connection.id);
 await page.getByRole("button",{name:"Choose configuration",exact:true}).click();
 await page.getByLabel("Server name",{exact:true}).fill("private-server");
 await page.getByLabel("Image",{exact:true}).selectOption("11");
 await page.getByLabel("Server size",{exact:true}).selectOption("cx23");
 await page.getByLabel("SSH key",{exact:true}).selectOption("12");
 await page.getByLabel("Private network",{exact:true}).selectOption("44");
 await page.getByRole("button",{name:"Review configuration",exact:true}).click();
 await expect(page.getByRole("dialog")).toContainText("Private network");
 await page.getByLabel("Creation reason",{exact:true}).fill("Use the reviewed private network");
 await page.getByLabel("Confirm the reviewed server name",{exact:true}).fill("private-server");
 await page.screenshot({path:"test-results/private-network-create.png",fullPage:true});
 await page.getByRole("button",{name:"Request independent approval",exact:true}).click();
 await expect(page.getByRole("dialog")).toHaveCount(0);expect(submitted).toBe(true);
});

test("managed IaC resources show protection and power drift warning",async({page})=>{
 const org={id:"a".repeat(64),name:"State organization",permissions:["resources.read","operations.request","templates.read"]};
 const project="d".repeat(64);
 const resource={id:"b".repeat(64),connectionId:"c".repeat(64),nativeId:"887766",name:"Managed server",provider:"hetzner",kind:"compute.server",region:"fsn1",status:"off",size:"cx23"};
 await page.route("**/api/**",route=>{
  const method=route.request().url().split("/").pop();
  if(method==="GetSession")return route.fulfill({json:{email:"operator@example.test",organizations:[org]}});
  if(method==="GetResource")return route.fulfill({json:{resource,connectionEnabled:true,providerEnabled:true,availableActions:["start"],ownershipProjectIds:[project]}});
  return route.fulfill({json:{}});
 });
 await page.goto(`/app/resources/detail/${resource.id}?org=${org.id}`);
 await expect(page.getByText("Protected by managed IaC state",{exact:true})).toBeVisible();
 await expect(page.getByRole("link",{name:/View referencing project/})).toHaveAttribute("href",`/app/templates?org=${org.id}&project=${project}`);
 await expect(page.getByRole("button",{name:"Delete",exact:true})).toHaveCount(0);
 await expect(page.getByRole("button",{name:"Resize",exact:true})).toHaveCount(0);
 await page.getByRole("button",{name:"Start",exact:true}).click();
 await expect(page.getByRole("dialog").last()).toContainText("A later Terraform/OpenTofu apply may undo this power change.");
 await page.screenshot({path:"test-results/iac-protection.png",fullPage:true});
});

test("managed projects expose partial coverage and protected references",async({page})=>{
 const org={id:"a".repeat(64),name:"State organization",permissions:["templates.read","resources.read"]};
 const project={id:"b".repeat(64),name:"Production state",versionId:"c".repeat(64),serial:"4",ownership:{status:"partial",unsupported:2,scannerVersion:2,scannedAt:"2026-09-06T00:00:00Z",protectedReferences:"3",incompleteVersions:"1"}};
 let unavailable=false,lists=0;
 await page.route("**/api/**",route=>{
  const method=route.request().url().split("/").pop();
  if(method==="GetSession")return route.fulfill({json:{email:"reader@example.test",organizations:[org]}});
  if(method==="GetAutomationProject")return unavailable?route.fulfill({status:403,json:{code:"permission_denied",message:"Project unavailable"}}):route.fulfill({json:project});
  if(method==="ListProjectOwnership"){lists++;return route.fulfill({json:{references:[{kind:"storage.volume",nativeId:"123",region:"fsn1",resourceId:"d".repeat(64),conflicting:true},{kind:"network.firewall",nativeId:"456",region:"global"}]}});}
  return route.fulfill({json:{}});
 });
 await page.goto(`/app/templates?org=${org.id}&project=${project.id}`);
 const coverage=page.getByRole("region",{name:"IaC protection coverage"});
 await expect(coverage).toContainText("Some identities unrecognized");
 await expect(coverage).toContainText("Also referenced by another project");
 await expect(coverage).toContainText("Not currently in inventory");
 await expect(coverage.getByRole("link",{name:"Open resource",exact:true})).toHaveAttribute("href",`/app/resources/detail/${"d".repeat(64)}?org=${org.id}`);
 await coverage.scrollIntoViewIfNeeded();await page.screenshot({path:"test-results/project-ownership.png",fullPage:true});
 org.permissions=["templates.read"];const before=lists;await page.reload();
 await expect(coverage).toContainText("Some identities unrecognized");
 await expect(page.getByRole("table",{name:"Protected state references",exact:true})).toHaveCount(0);expect(lists).toBe(before);
 unavailable=true;await page.reload();await expect(page.getByText("Project unavailable",{exact:true})).toBeVisible();await expect(coverage).toHaveCount(0);
});

test("server images require stopped targets and show storage review",async({page})=>{
 const org={id:"a".repeat(64),name:"Image organization",permissions:["resources.read","operations.read","operations.request","operations.create"]};
 const resource={id:"b".repeat(64),connectionId:"c".repeat(64),nativeId:"123",name:"Image source",provider:"hetzner",kind:"compute.server",region:"fsn1",status:"running",observedAt:"2026-09-06T00:00:00Z"};let submitted=false;
 await page.route("**/api/**",route=>{
  const method=route.request().url().split("/").pop();
  if(method==="GetSession")return route.fulfill({json:{email:"operator@example.test",organizations:[org]}});
  if(method==="GetResource")return route.fulfill({json:{resource,providerEnabled:true,connectionEnabled:true,availableActions:["snapshot"]}});
  if(method==="RequestOperation"){const body=route.request().postDataJSON();expect(body.action).toBe("snapshot");expect(body.expectedStatus).toBe("off");submitted=true;return route.fulfill({json:{operation:{id:"d".repeat(64)}}});}
  return route.fulfill({json:{}});
 });
 await page.goto(`/app/resources/detail/${resource.id}?org=${org.id}`);
 await expect(page.getByRole("button",{name:"Create server image",exact:true})).toBeDisabled();
 resource.status="off";await page.reload();await page.getByRole("button",{name:"Create server image",exact:true}).click();
 await expect(page.getByRole("dialog").last()).toContainText("Independent approval is required");
 await expect(page.getByRole("dialog").last()).toContainText("excluding attached volumes and scratch disks");
 await page.getByLabel("Reason for this action",{exact:true}).fill("Reviewed recovery image and storage costs");
 await page.screenshot({path:"test-results/server-image-review.png",fullPage:true});
 await page.getByRole("button",{name:"Request create server image",exact:true}).click();expect(submitted).toBe(true);
 org.permissions=org.permissions.filter(p=>p!=="operations.create");await page.goto(`/app/resources/detail/${resource.id}?org=${org.id}`);
 await expect(page.getByRole("button",{name:"Create server image",exact:true})).toHaveCount(0);
});

test("validation links open exact results and hide unavailable records",async({page})=>{
 const org={id:"a".repeat(64),name:"Automation organization",permissions:["templates.read","templates.publish"]};
 const result={id:"b".repeat(64),versionId:"c".repeat(64),projectId:"d".repeat(64),status:"running",detail:"Validation in progress",createdAt:"2026-09-06T12:00:00Z"};
 let denied=false,cancels=0;
 await page.route("**/api/**",route=>{
  const method=route.request().url().split("/").pop();
  if(method==="GetSession")return route.fulfill({json:{email:"admin@example.test",organizations:[org]}});
  if(method==="GetAutomationValidation"){
   expect(route.request().postDataJSON()).toMatchObject({organizationId:org.id,id:result.id});
   return denied?route.fulfill({status:403,json:{code:"permission_denied",message:"Validation unavailable"}}):route.fulfill({json:result});
  }
  if(method==="ListAutomationValidations")return route.fulfill({json:{validations:[],runnerConfigured:true}});
  if(method==="CancelAutomationValidation"){cancels++;result.status="canceled";result.detail="Canceled by operator";return route.fulfill({json:result});}
  return route.fulfill({json:{}});
 });
 await page.goto(`/app/templates?org=${org.id}&validation=${result.id}`);
 const modal=page.getByRole("dialog");
 await expect(modal).toContainText("Validation in progress");
 await expect(modal.getByRole("navigation",{name:"Validation breadcrumb"})).toContainText("Templates");
 await expect(modal.getByRole("link",{name:"View published version"})).toHaveAttribute("href",`/app/templates?org=${org.id}&version=${result.versionId}`);
 await page.reload();await expect(modal).toContainText("Validation in progress");
 await modal.getByRole("button",{name:"Cancel validation",exact:true}).click();
 await expect(modal).toContainText("Canceled by operator");expect(cancels).toBe(1);
 await expect(modal.getByRole("button",{name:"Cancel validation",exact:true})).toHaveCount(0);
 await page.screenshot({path:"test-results/validation-link.png",fullPage:true});
 denied=true;await page.reload();await expect(modal.getByRole("alert")).toContainText("Validation unavailable");
 await expect(modal.getByRole("link",{name:"View published version"})).toHaveCount(0);
 await expect(modal).not.toContainText("Canceled by operator");
 denied=false;result.status="running";org.permissions=["templates.read"];await page.reload();
 await expect(modal).toContainText("Canceled by operator");
 await expect(modal.getByRole("button",{name:"Cancel validation",exact:true})).toHaveCount(0);
});

test("global storage health distinguishes budgets and capacity warnings",async({page})=>{
 let failed=false;
 const storage={usedBytes:"9663676416",auditBytes:"1073741824",budgetBytes:"10737418240",status:"critical"};
 await page.route("**/api/**",route=>{
  const method=route.request().url().split("/").pop();
  if(method==="GetSession")return route.fulfill({json:{email:"admin@example.test",globalAdmin:true,organizations:[]}});
  if(method==="GetInstallationHealth")return failed?route.fulfill({status:503,json:{code:"unavailable",message:"Storage measurement unavailable"}}):route.fulfill({json:{observedAt:"2026-09-06T12:00:00Z",databaseConnections:2,databaseCapacity:10,storage,work:[]}});
  return route.fulfill({json:{}});
 });
 await page.goto("/admin/health");
 const panel=page.getByRole("region",{name:"Database storage"});
 await expect(panel).toContainText("9.00 GiB allocated");
 await expect(panel).toContainText("90.0% used");
 await expect(panel.getByRole("alert")).toContainText("Critical");
 await expect(panel).toContainText("not a disk quota");
 storage.usedBytes="8589934592";storage.status="warning";await page.getByRole("button",{name:"Refresh health",exact:true}).click();
 await expect(panel.getByRole("alert")).toContainText("80%");
 storage.budgetBytes="0";storage.status="unconfigured";await page.getByRole("button",{name:"Refresh health",exact:true}).click();
 await expect(panel).toContainText("No database storage budget configured");
 await expect(panel.getByRole("alert")).toHaveCount(0);
 await page.screenshot({path:"test-results/database-storage.png",fullPage:true});
 failed=true;await page.getByRole("button",{name:"Refresh health",exact:true}).click();
 await expect(page.getByRole("alert")).toContainText("Storage measurement unavailable");
 await expect(panel).toHaveCount(0);
});

test("organization SMTP is write-only and reusable by destinations",async({page})=>{
 const org={id:"a".repeat(64),name:"Mail organization",permissions:["admin.access","notifications.manage"]};
 const profile={configured:false,revision:"0"};let stale=false;let destination:any;
 await page.route("**/api/**",route=>{
  const method=route.request().url().split("/").pop();
  if(method==="GetSession")return route.fulfill({json:{email:"admin@example.test",organizations:[org]}});
  if(method==="GetOrganizationSmtp")return route.fulfill({json:profile});
  if(method==="ListNotificationDestinations")return route.fulfill({json:{destinations:[],organizationSmtp:profile}});
  if(method==="SetOrganizationSmtp"){
   const body=route.request().postDataJSON();expect(body.expectedRevision??"0").toBe(profile.revision);
   if(stale)return route.fulfill({status:409,json:{code:"failed_precondition",message:"Organization SMTP changed. Reload before saving."}});
   if(body.remove){profile.configured=false;}else{expect(body.smtp.host).toBe("smtp.example.com");expect(body.smtp.password).toBe("private-smtp-password");profile.configured=true;}
   profile.revision=String(Number(profile.revision)+1);return route.fulfill({json:profile});
  }
  if(method==="SaveNotificationDestination"){destination=route.request().postDataJSON();return route.fulfill({json:{}});}
  return route.fulfill({json:{}});
 });
 await page.goto(`/admin/notifications?org=${org.id}`);
 await page.getByRole("button",{name:"Organization SMTP",exact:true}).click();
 const modal=page.getByRole("dialog");
 await expect(modal).toContainText("Existing destinations keep their saved settings");
 await page.getByLabel("SMTP hostname",{exact:true}).fill("smtp.example.com");
 await page.getByLabel("Sender email",{exact:true}).fill("sender@example.com");
 await page.getByLabel("SMTP username",{exact:true}).fill("smtp-user");
 await page.getByLabel("SMTP password",{exact:true}).fill("private-smtp-password");
 await page.getByRole("button",{name:"Save organization SMTP",exact:true}).click();
 await expect(modal).toHaveCount(0);
 await page.getByRole("button",{name:"Add destination",exact:true}).click();
 await page.getByLabel("Destination name",{exact:true}).fill("Team inbox");
 await page.getByLabel("Delivery method",{exact:true}).selectOption("email-organization");
 await page.getByLabel("HTTPS endpoint or recipient email",{exact:true}).fill("team@example.com");
 await expect(page.getByLabel("SMTP password",{exact:true})).toHaveCount(0);
 await page.getByRole("button",{name:"Save destination",exact:true}).click();
 await expect(modal).toHaveCount(0);
 expect(destination.useOrganizationSmtp).toBe(true);expect(destination.organizationSmtpRevision).toBe("1");expect(destination.smtp).toBeUndefined();
 await page.getByRole("button",{name:"Organization SMTP",exact:true}).click();
 await expect(page.getByLabel("SMTP password",{exact:true})).toHaveValue("");
 await page.getByLabel("Profile action",{exact:true}).selectOption("remove");
 await page.getByLabel("Confirm organization name",{exact:true}).fill(org.name);
 stale=true;await page.getByRole("button",{name:"Save organization SMTP",exact:true}).click();
 await expect(modal.getByRole("alert")).toContainText("Organization SMTP changed");
 await page.screenshot({path:"test-results/organization-smtp.png",fullPage:true});
 stale=false;await page.getByRole("button",{name:"Save organization SMTP",exact:true}).click();await expect(modal).toHaveCount(0);
 await page.getByRole("button",{name:"Add destination",exact:true}).click();
 await expect(page.getByLabel("Delivery method",{exact:true}).locator('option[value="email-organization"]')).toHaveCount(0);
});

test("validation history pages and status filters survive reload",async({page})=>{
 const org={id:"a".repeat(64),name:"History organization",permissions:["templates.read"]};let fail=false;
 const requests:any[]=[];
 await page.route("**/api/**",route=>{
  const method=route.request().url().split("/").pop();
  if(method==="GetSession")return route.fulfill({json:{email:"reader@example.test",organizations:[org]}});
  if(method==="ListAutomationValidations"){
   const body=route.request().postDataJSON();requests.push(body);
   if(fail)return route.fulfill({status:403,json:{code:"permission_denied",message:"History unavailable"}});
   const older=!!body.pageToken;
   return route.fulfill({json:{validations:[{id:(older?"b":"c").repeat(64),versionId:"d".repeat(64),status:body.status||"succeeded",detail:older?"Older validation result":"Recent validation result",createdAt:"2026-09-06T12:00:00Z"}],nextPageToken:older?"":"older-page",runnerConfigured:true}});
  }
  return route.fulfill({json:{}});
 });
 await page.goto(`/app/templates?org=${org.id}`);
 const table=page.getByRole("table",{name:"Automation validations"});
 await expect(table).toContainText("Recent validation result");
 await page.getByRole("button",{name:"Older validations",exact:true}).click();
 await expect(table).toContainText("Older validation result");
 expect(new URL(page.url()).searchParams.get("validation_page")).toBe("older-page");
 await page.reload();await expect(table).toContainText("Older validation result");
 await page.getByLabel("Validation status",{exact:true}).selectOption("failed");
 await expect(table).toContainText("Recent validation result");
 await expect(table).toContainText("failed");
 expect(new URL(page.url()).searchParams.has("validation_page")).toBe(false);
 await page.getByRole("button",{name:"Older validations",exact:true}).click();
 await expect(table).toContainText("Older validation result");
 await page.reload();await expect(table).toContainText("Older validation result");
 await expect(page.getByLabel("Validation status",{exact:true})).toHaveValue("failed");
 expect(requests.at(-1)).toMatchObject({status:"failed",pageToken:"older-page"});
 await expect(table.getByRole("link",{name:"View validation"})).toHaveAttribute("href",`/app/templates?org=${org.id}&validation=${"b".repeat(64)}`);
 await page.screenshot({path:"test-results/validation-history.png",fullPage:true});
 await page.getByRole("button",{name:"Most recent validations",exact:true}).click();
 await expect(table).toContainText("Recent validation result");
 fail=true;await page.reload();await expect(page.getByRole("alert")).toContainText("History unavailable");
 await expect(table).not.toContainText("Recent validation result");
 await expect(page.getByRole("button",{name:"Older validations",exact:true})).toHaveCount(0);
});

for (const kind of ["organization.project","application.app"]) {
 test("DigitalOcean service inventory / "+kind,async({page})=>{
  const org={id:"a".repeat(64),name:"Cloud team",permissions:["resources.read"]};
  const resource={id:"b".repeat(64),connectionId:"c".repeat(64),nativeId:"service-id",name:"Production service",provider:"digitalocean",kind,region:kind==="organization.project"?"global":"nyc",status:"present",size:"Service metadata"};
  await page.route("**/api/**",route=>{
   const method=route.request().url().split("/").pop();
   if(method==="GetSession")return route.fulfill({json:{email:"operator@example.test",organizations:[org]}});
   if(method==="ListResources")return route.fulfill({json:{resources:[resource]}});
   if(method==="GetResource")return route.fulfill({json:{resource,connectionName:"DigitalOcean",connectionEnabled:true,providerEnabled:true,availableActions:[]}});
   return route.fulfill({json:{}});
  });
  await page.goto(`/app/resources/all?org=${org.id}`);
  await page.getByLabel("Filter resource type").selectOption(kind);
  await page.getByRole("link",{name:resource.name,exact:true}).click();
  await expect(page.locator("main")).toContainText("Service metadata");
  await expect(page.locator("main").getByRole("button",{name:"Delete resource",exact:true})).toHaveCount(0);
  await page.reload();
  await expect(page.locator("main")).toContainText("service-id");
 });
}

test("schedule history filters and pages survive reload",async({page})=>{
 const org={id:"a".repeat(64),name:"Scheduler team",permissions:["schedules.read","operations.read"]},scheduleId="b".repeat(64),operationId="c".repeat(64);
 let fail=false;
 await page.route("**/api/**",route=>{
  const method=route.request().url().split("/").pop();
  if(method==="GetSession")return route.fulfill({json:{email:"operator@example.test",organizations:[org]}});
  if(method==="ListSchedules")return route.fulfill({json:{schedules:[{id:scheduleId,name:"Nightly stop"}]}});
  if(method==="ListScheduleOccurrences"){
   if(fail)return route.fulfill({status:403,json:{code:"permission_denied",message:"Unavailable"}});
   const body=route.request().postDataJSON();
   expect(body.scheduleId).toBe(scheduleId);expect(body.outcome).toBe("queued");
   return route.fulfill({json:{occurrences:[{id:body.pageToken?"1":"2",scheduleId,resourceName:body.pageToken?"Older server":"Recent server",outcome:"queued",operationId,operationStatus:"succeeded"}],nextPageToken:body.pageToken?"":"older"}});
  }
  return route.fulfill({json:{}});
 });
 await page.goto(`/app/schedules?org=${org.id}&history_schedule=${scheduleId}&outcome=queued`);
 await expect(page.getByLabel("History schedule")).toHaveValue(scheduleId);
 await expect(page.getByLabel("Dispatch outcome")).toHaveValue("queued");
 await expect(page.getByRole("table",{name:"Schedule occurrences"})).toContainText("Recent server");
 await expect(page.getByRole("link",{name:"succeeded",exact:true})).toHaveAttribute("href",`/app/operations?org=${org.id}&operation=${operationId}`);
 await page.getByRole("button",{name:"Older occurrences",exact:true}).click();
 await expect(page.getByRole("table",{name:"Schedule occurrences"})).toContainText("Older server");
 await page.reload();
 await expect(page.getByRole("table",{name:"Schedule occurrences"})).toContainText("Older server");
 fail=true;await page.reload();
 await expect(page.getByRole("alert")).toBeVisible();
 await expect(page.getByRole("table",{name:"Schedule occurrences"})).not.toContainText("Older server");
});


test("DigitalOcean projects use shared reviewed deletion",async({page})=>{
 const org={id:"a".repeat(64),name:"Database organization",permissions:["resources.read","operations.read","operations.request","operations.delete"]};
 const resource={id:"b".repeat(64),connectionId:"c".repeat(64),nativeId:"506f78a4-e098-11e5-ad9f-000f53306ae1",name:"Empty cloud project",provider:"digitalocean",kind:"organization.project",region:"global",status:"present"};
 let requested=false;
 await page.route("**/api/**",route=>{
  const method=route.request().url().split("/").pop();
  if(method==="GetSession")return route.fulfill({json:{email:"operator@example.test",organizations:[org]}});
  if(method==="GetResource")return route.fulfill({json:{resource,connectionName:"AWS",connectionEnabled:true,providerEnabled:true,availableActions:["delete"]}});
  if(method==="PreviewDeletion")return route.fulfill({json:{impact:"Remove the empty cloud project. No cloud resource is deleted or reassigned.",digest:"d".repeat(64)}});
  if(method==="RequestOperation"){
   const body=route.request().postDataJSON();expect(body.resourceId).toBe(resource.id);expect(body.confirmation).toBe(resource.nativeId);expect(body.deletionDigest).toBe("d".repeat(64));requested=true;
  }
  return route.fulfill({json:{}});
 });
 await page.goto(`/app/resources/detail/${resource.id}?org=${org.id}`);
 await page.getByRole("button",{name:"Delete resource",exact:true}).click();
 await page.getByRole("button",{name:"Read deletion impact",exact:true}).click();
 await page.getByLabel(`Type ${resource.nativeId} to confirm deletion and the listed data loss`,{exact:true}).fill(resource.nativeId);
 await page.getByLabel("Reason for this action",{exact:true}).fill("Remove obsolete manual recovery copy");
 await page.screenshot({path:"test-results/project-delete.png",fullPage:true});
 await page.getByRole("button",{name:"Request delete resource",exact:true}).click();
 await expect(page.getByRole("dialog")).toHaveCount(0);expect(requested).toBe(true);
});

for (const kind of ["network.route_table","network.internet_gateway","network.nat_gateway","network.load_balancer","kubernetes.cluster","kubernetes.node_group"]) {
 test("AWS network inventory / "+kind,async({page})=>{
  const org={id:"a".repeat(64),name:"Cloud team",permissions:["resources.read"]};
  const resource={id:"b".repeat(64),connectionId:"c".repeat(64),nativeId:"service-id",name:"Production service",provider:"aws",kind,region:"us-east-1",status:"present",size:"Service metadata"};
  await page.route("**/api/**",route=>{
   const method=route.request().url().split("/").pop();
   if(method==="GetSession")return route.fulfill({json:{email:"operator@example.test",organizations:[org]}});
   if(method==="ListResources")return route.fulfill({json:{resources:[resource]}});
   if(method==="GetResource")return route.fulfill({json:{resource,connectionName:"DigitalOcean",connectionEnabled:true,providerEnabled:true,availableActions:[]}});
   return route.fulfill({json:{}});
  });
  await page.goto(`/app/resources/all?org=${org.id}`);
  await page.getByLabel("Filter resource type").selectOption(kind);
  await page.getByRole("link",{name:resource.name,exact:true}).click();
  await expect(page.locator("main")).toContainText("Service metadata");
  await expect(page.locator("main").getByRole("button",{name:"Delete resource",exact:true})).toHaveCount(0);
  await page.reload();
  await expect(page.locator("main")).toContainText("service-id");
 });
}

test("role editor rejects stale revisions and reloads before retry",async({page})=>{
 const org={id:"a".repeat(64),name:"Alpha",permissions:["admin.access","members.read","roles.manage","resources.read"]};
 let revision="1",saves=0;
 await page.route("**/api/**",route=>{
  const path=route.request().url();
  if(path.endsWith("/GetSession"))return route.fulfill({json:{email:"admin@example.test",organizations:[org]}});
  if(path.endsWith("/ListAccess"))return route.fulfill({json:{roles:[{id:"b".repeat(64),name:"Reader",permissions:["resources.read"],revision}],permissionCatalog:["resources.read"]}});
  if(path.endsWith("/SaveRole")){
   const body=route.request().postDataJSON();saves++;
   expect(body.expectedRevision).toBe(saves===1?"1":"2");
   if(saves===1){revision="2";return route.fulfill({status:400,json:{code:"failed_precondition",message:"Role changed. Close and reopen the editor before saving."}});}
   revision="3";return route.fulfill({json:{}});
  }
  return route.fulfill({json:{}});
 });
 await page.goto("/admin/access?org="+org.id);
 await page.getByRole("button",{name:"Edit role",exact:true}).click();
 await page.getByRole("button",{name:"Save role",exact:true}).click();
 await expect(page.getByRole("dialog")).toContainText("Role changed.");
 expect(saves).toBe(1);
 await page.keyboard.press("Escape");
 await page.getByRole("button",{name:"Edit role",exact:true}).click();
 await page.getByRole("button",{name:"Save role",exact:true}).click();
 await expect(page.getByRole("dialog")).toHaveCount(0);
 expect(saves).toBe(2);
});

test("member editor rejects stale revisions and reloads before retry",async({page})=>{
 const org={id:"a".repeat(64),name:"Alpha",permissions:["admin.access","members.read","members.manage","resources.read"]};
 let revision="1",saves=0;
 await page.route("**/api/**",route=>{
  const path=route.request().url();
  if(path.endsWith("/GetSession"))return route.fulfill({json:{email:"admin@example.test",organizations:[org]}});
  if(path.endsWith("/ListAccess"))return route.fulfill({json:{roles:[{id:"viewer",name:"Viewer",permissions:["resources.read"],revision:"1",builtin:true}],members:[{userId:"b".repeat(64),email:"member@example.test",roleId:"viewer",roleName:"Viewer",active:true,revision}]}});
  if(path.endsWith("/UpdateMember")){
   const body=route.request().postDataJSON();saves++;
   expect(body.expectedRevision).toBe(saves===1?"1":"2");
   if(saves===1){revision="2";return route.fulfill({status:400,json:{code:"failed_precondition",message:"Membership changed. Close and reopen the editor before saving."}});}
   revision="3";return route.fulfill({json:{}});
  }
  return route.fulfill({json:{}});
 });
 await page.goto("/admin/access?org="+org.id);
 await page.getByRole("button",{name:"Edit access",exact:true}).click();
 await page.getByRole("button",{name:"Save access",exact:true}).click();
 await expect(page.getByRole("dialog")).toContainText("Membership changed.");
 expect(saves).toBe(1);
 await page.keyboard.press("Escape");
 await page.getByRole("button",{name:"Edit access",exact:true}).click();
 await page.getByRole("button",{name:"Save access",exact:true}).click();
 await expect(page.getByRole("dialog")).toHaveCount(0);
 expect(saves).toBe(2);
});

for (const cloud of ["aws","hetzner","digitalocean"]) test(`server tag editor and review ${cloud}`,async({page})=>{
 const org={id:"a".repeat(64),name:"Tags organization",permissions:["resources.read","operations.read","operations.request","operations.approve"]};
 const tags=cloud==="digitalocean"?{names:["dev"]}:{labels:{env:"dev"}};
 const resource={id:"b".repeat(64),connectionId:"c".repeat(64),nativeId:"123",name:"Tagged server",provider:cloud,kind:"compute.server",region:"region",status:"running",tags};
 let submitted:any;let owned=false;
 await page.route("**/api/**",route=>{
  const method=route.request().url().split("/").pop();
  if(method==="GetSession")return route.fulfill({json:{email:"operator@example.test",organizations:[org]}});
  if(method==="GetResource")return route.fulfill({json:{resource,providerEnabled:true,connectionEnabled:true,availableActions:["tags"],ownershipProjectIds:owned?["e".repeat(64)]:[]}});
  if(method==="RequestOperation"){submitted=route.request().postDataJSON();return route.fulfill({json:{operation:{id:"d".repeat(64)}}});}
  if(method==="GetOperation")return route.fulfill({json:{operation:{id:"d".repeat(64),action:"tags",resourceKind:"compute.server",resourceName:resource.name,expectedTags:tags,targetTags:submitted?.targetTags,status:"OPERATION_STATUS_AWAITING_APPROVAL",canReview:true}}});
  return route.fulfill({json:{}});
 });
 await page.goto(`/app/resources/detail/${resource.id}?org=${org.id}`);
 await page.getByRole("button",{name:"Edit tags",exact:true}).click();
 await page.getByRole("button",{name:"Add tag",exact:true}).click();
 await page.getByLabel(cloud==="digitalocean"?"Tag 2 name":"Tag 2 key",{exact:true}).fill("team");
 if(cloud!=="digitalocean")await page.getByLabel("Tag 2 value",{exact:true}).fill("platform");
 await page.getByRole("button",{name:"Remove tag 1",exact:true}).click();
 await page.getByLabel("Reason for this action",{exact:true}).fill("Assign platform team");
 await page.getByRole("button",{name:"Request edit tags",exact:true}).click();
 await expect(page.getByRole("heading",{name:"Operations",exact:true})).toBeVisible();
 expect(submitted.expectedTags).toEqual(tags);expect(submitted.targetTags).toEqual(cloud==="digitalocean"?{names:["team"]}:{labels:{team:"platform"}});
 await page.goto(`/app/operations?org=${org.id}&operation=${"d".repeat(64)}`);
 await expect(page.getByRole("heading",{name:"Reviewed tags before change"})).toBeVisible();
 await expect(page.getByRole("heading",{name:"Complete requested tag set"})).toBeVisible();
 await expect(page.getByRole("dialog")).toContainText("dev");
 await expect(page.getByRole("dialog")).toContainText("team");
 owned=true;await page.goto(`/app/resources/detail/${resource.id}?org=${org.id}`);
 await expect(page.getByRole("button",{name:"Edit tags",exact:true})).toBeDisabled();
});

test("destination replacement rejects stale revision and requires reopening",async({page})=>{
 const org={id:"a".repeat(64),name:"Alpha",permissions:["admin.access","notifications.read","notifications.manage"]};
 let revision="1",saves=0;
 const destination=()=>({id:"b".repeat(64),name:"Receiver",kind:"webhook",endpoint:"https://hooks.example.com/events",revision,enabled:true,verified:true});
 await page.route("**/api/**",route=>{
  const method=route.request().url().split("/").pop();
  if(method==="GetSession")return route.fulfill({json:{email:"operator@example.test",organizations:[org]}});
  if(method==="ListNotificationDestinations")return route.fulfill({json:{destinations:[destination()]}});
  if(method==="GetNotificationDestination")return route.fulfill({json:destination()});
  if(method==="SaveNotificationDestination"){
   saves++;const body=route.request().postDataJSON();expect(body.expectedRevision).toBe(saves===1?"1":"2");
   if(saves===1){revision="2";return route.fulfill({status:400,json:{code:"failed_precondition",message:"Destination changed. Reload and review it before replacing its configuration."}});}
   return route.fulfill({json:{}});
  }
  return route.fulfill({json:{}});
 });
 await page.goto(`/admin/notifications?org=${org.id}&destination=${"b".repeat(64)}`);
 await page.getByRole("button",{name:"Replace configuration / rotate secret",exact:true}).click();
 await page.getByLabel("Webhook signing secret",{exact:true}).fill("whsec_"+"e".repeat(44));
 await page.getByRole("button",{name:"Save destination",exact:true}).click();
 await expect(page.getByRole("alert")).toContainText("Destination changed");
 await page.getByRole("dialog").getByRole("button",{name:"Close",exact:true}).click();
 await page.getByRole("button",{name:"Replace configuration / rotate secret",exact:true}).click();
 await page.getByLabel("Webhook signing secret",{exact:true}).fill("whsec_"+"e".repeat(44));
 await page.getByRole("button",{name:"Save destination",exact:true}).click();
 await expect(page.getByRole("dialog")).toHaveCount(0);expect(saves).toBe(2);
});

test("destination toggle refreshes after revision conflict",async({page})=>{
 const org={id:"a".repeat(64),name:"Alpha",permissions:["admin.access","notifications.read","notifications.manage"]};let revision="1",enabled=true,calls=0;
 const destination=()=>({id:"b".repeat(64),name:"Receiver",kind:"webhook",endpoint:"https://hooks.example.com/events",revision,enabled,verified:true});
 await page.route("**/api/**",route=>{
  const method=route.request().url().split("/").pop();
  if(method==="GetSession")return route.fulfill({json:{email:"operator@example.test",organizations:[org]}});
  if(method==="ListNotificationDestinations")return route.fulfill({json:{destinations:[destination()]}});
  if(method==="GetNotificationDestination")return route.fulfill({json:destination()});
  if(method==="SetNotificationDestinationEnabled"){
   calls++;const body=route.request().postDataJSON();expect(body.expectedRevision).toBe(calls===1?"1":"2");expect(body.enabled??false).toBe(false);
   if(calls===1){revision="2";return route.fulfill({status:400,json:{code:"failed_precondition",message:"Destination changed. Reload and review before enabling or disabling it."}});}
   enabled=false;revision="3";return route.fulfill({json:{}});
  }
  return route.fulfill({json:{}});
 });
 await page.goto(`/admin/notifications?org=${org.id}&destination=${"b".repeat(64)}`);
 await page.getByRole("button",{name:"Disable destination",exact:true}).click();
 await expect(page.getByRole("alert")).toContainText("Destination changed");
 await page.getByRole("button",{name:"Disable destination",exact:true}).click();
 await expect(page.getByRole("button",{name:"Enable destination",exact:true})).toBeVisible();expect(calls).toBe(2);
});

test("placement groups use shared reviewed deletion",async({page})=>{
 const org={id:"a".repeat(64),name:"Database organization",permissions:["resources.read","operations.read","operations.request","operations.delete"]};
 const resource={id:"b".repeat(64),connectionId:"c".repeat(64),nativeId:"879",name:"Empty placement group",provider:"hetzner",kind:"compute.placement_group",region:"global",status:"present"};
 let requested=false;
 await page.route("**/api/**",route=>{
  const method=route.request().url().split("/").pop();
  if(method==="GetSession")return route.fulfill({json:{email:"operator@example.test",organizations:[org]}});
  if(method==="GetResource")return route.fulfill({json:{resource,connectionName:"AWS",connectionEnabled:true,providerEnabled:true,availableActions:["delete"]}});
  if(method==="PreviewDeletion")return route.fulfill({json:{impact:"Remove the empty placement group. No server is detached or deleted.",digest:"d".repeat(64)}});
  if(method==="RequestOperation"){
   const body=route.request().postDataJSON();expect(body.resourceId).toBe(resource.id);expect(body.confirmation).toBe(resource.nativeId);expect(body.deletionDigest).toBe("d".repeat(64));requested=true;
  }
  return route.fulfill({json:{}});
 });
 await page.goto(`/app/resources/detail/${resource.id}?org=${org.id}`);
 await page.getByRole("button",{name:"Delete resource",exact:true}).click();
 await page.getByRole("button",{name:"Read deletion impact",exact:true}).click();
 await page.getByLabel(`Type ${resource.nativeId} to confirm deletion and the listed data loss`,{exact:true}).fill(resource.nativeId);
 await page.getByLabel("Reason for this action",{exact:true}).fill("Remove obsolete manual recovery copy");
 await page.screenshot({path:"test-results/placement-delete.png",fullPage:true});
 await page.getByRole("button",{name:"Request delete resource",exact:true}).click();
 await expect(page.getByRole("dialog")).toHaveCount(0);expect(requested).toBe(true);
});

for (const cloud of ["aws", "digitalocean", "hetzner"]) {
 test(`SSH public key import reviews exact ${cloud} input and independent approval`,async({page})=>{
  const org={id:"a".repeat(64),name:"Key organization",permissions:["resources.read","connections.read","operations.read","operations.request","operations.create","operations.approve"]};
  const connection={id:"b".repeat(64),name:"Cloud account",provider:cloud,region:cloud==="aws"?"us-east-1":"fsn1",enabled:true};
  const publicKey="ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAABAgMEBQYHCAkKCwwNDg8QERITFBUWFxgZGhscHR4f fixture@example.test";
  const operation={id:"d".repeat(64),resourceKind:"access.ssh_key",resourceName:"operator-key",provider:cloud,region:cloud==="aws"?"us-east-1":"global",action:"create",status:"OPERATION_STATUS_AWAITING_APPROVAL",requester:"requester@example.test",canReview:true,createdAt:"2026-09-06T12:00:00Z",updatedAt:"2026-09-06T12:00:00Z",keyCreation:{name:"operator-key",publicKey}};
  let submitted:Record<string,unknown>|undefined,calls=0,reviews=0,fail=true;
  await page.route("**/api/**",route=>{
   const method=route.request().url().split("/").pop();
   if(method==="GetSession")return route.fulfill({json:{email:"reviewer@example.test",organizations:[org]}});
   if(method==="ListConnections")return route.fulfill({json:{connections:[connection]}});
   if(method==="ListResources")return route.fulfill({json:{resources:[]}});
   if(method==="RequestSSHKeyCreation"){
    const input=route.request().postDataJSON();calls++;
    expect(input).toMatchObject({organizationId:org.id,connectionId:connection.id,region:operation.region,creation:operation.keyCreation,reason:"Import reviewed operator key"});
    expect(input.idempotencyKey).toBeTruthy();
    if(submitted)expect(input).toEqual(submitted);submitted=input;
    if(fail)return route.fulfill({status:503,json:{code:"unavailable",message:"Import response unavailable"}});
    return route.fulfill({json:{operation}});
   }
   if(method==="ListOperations")return route.fulfill({json:{operations:submitted?[operation]:[]}});
   if(method==="GetOperation")return route.fulfill({json:{operation}});
   if(method==="ReviewOperation"){
    expect(route.request().postDataJSON()).toMatchObject({organizationId:org.id,id:operation.id,approve:true,reason:"Reviewed exact public key"});reviews++;
    operation.canReview=false;operation.status="OPERATION_STATUS_QUEUED";
    return route.fulfill({json:{operation}});
   }
   return route.fulfill({json:{}});
  });
  await page.goto(`/app/resources/all?org=${org.id}`);
  await page.getByRole("button",{name:"Import SSH key",exact:true}).click();
  await page.getByLabel("Cloud connection",{exact:true}).selectOption(connection.id);
  await page.getByRole("button",{name:"Choose configuration",exact:true}).click();
  await page.getByLabel("Key name",{exact:true}).fill("operator-key");
  await expect(page.getByLabel("AWS region",{exact:true})).toHaveCount(cloud==="aws"?1:0);
  await expect(page.getByLabel("Server size",{exact:true})).toHaveCount(0);
  await page.getByLabel("OpenSSH public key",{exact:true}).fill("-----BEGIN OPENSSH PRIVATE KEY-----\n"+publicKey);
  await expect(page.getByRole("button",{name:"Review configuration",exact:true})).toBeDisabled();
  await expect(page.getByLabel("OpenSSH public key",{exact:true})).toBeVisible();
  expect(calls).toBe(0);
  await page.getByLabel("OpenSSH public key",{exact:true}).fill(publicKey);
  await page.getByRole("button",{name:"Review configuration",exact:true}).click();
  await expect(page.getByRole("dialog").locator("pre")).toHaveText(publicKey);
  await expect(page.getByRole("dialog")).toContainText(operation.region);
  await page.getByLabel("Import reason",{exact:true}).fill("Import reviewed operator key");
  await page.getByLabel("Confirm the reviewed key name",{exact:true}).fill("operator-key");
  await page.getByRole("button",{name:"Request independent approval",exact:true}).click();
  await expect(page.getByRole("dialog")).toContainText("Import response unavailable");
  expect(calls).toBe(1);fail=false;
  await page.getByRole("button",{name:"Request independent approval",exact:true}).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);expect(calls).toBe(2);
  await page.getByRole("button",{name:/operator-key/}).click();
  await expect(page.getByRole("dialog")).toContainText("Import SSH key");
  await expect(page.getByRole("dialog").locator("pre")).toHaveText(publicKey);
  await expect(page.getByRole("dialog")).toContainText("operator-key-"+operation.id);
  if(cloud==="hetzner"){
   await page.screenshot({path:"test-results/ssh-key-review.png",fullPage:true});
   await page.setViewportSize({width:390,height:844});
   await page.screenshot({path:"test-results/ssh-key-review-mobile.png",fullPage:true});
   expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true);
  }
  await page.getByLabel("Review reason",{exact:true}).fill("Reviewed exact public key");
  await page.getByRole("button",{name:"Submit review",exact:true}).click();
  await expect(page.getByRole("button",{name:"Submit review",exact:true})).toHaveCount(0);expect(reviews).toBe(1);
 });
}
