import {ResourceOverview,resourceKinds,resourcePath} from "./resource-pages";
import {OperationsOverview} from "./overview";
import {InventoryLink,readInventoryLink} from "./inventory-links";
import {InstallationHealth} from "./installation-health";
import {InstallationDirectory} from "./installation-directory";
import type {InventoryView,InventoryViewSpec} from "./gen/providah/v1/console_pb";
import {ResourceScopes,emptyScope} from "./resource-scopes";
import {TagConditions,emptyTags} from "./tag-conditions";
import {version} from "../package.json";
import { MFAPolicyButton } from "./mfa-policy";
import { InventoryViews } from "./inventory-views";
import type { SortingState } from "@tanstack/react-table";
import React, { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import {
  Link,
  createBrowserRouter,
  RouterProvider,
  NavLink as RouterNavLink,
  Outlet,
  useLoaderData,
  useOutletContext,
  useRouteError,
  useNavigation,
  useLocation,
  useNavigate,
  Navigate,
  useSearchParams,
  useParams,
} from "react-router";
import {
  QueryClientProvider,
  useMutation,
  useQuery,
} from "@tanstack/react-query";
import {
  Cloud,
  LayoutDashboard,
  Server,
  Plug,
  ShieldCheck,
  ArrowUpRight,
  Plus,
  Search,
  X,
  LogOut,
  ChevronRight,
  PanelLeftClose,
  PanelLeftOpen,
  KeyRound,
  Check,
  Moon,
  Sun,
  Activity,
  Layers,
  RefreshCw,
} from "lucide-react";
import { ActionForm, DataTable, ErrorNote, Modal, PageHeader, type FormField } from "./ui";
import { useLiveUpdates } from "./live";
import { RecoveryButton, MfaButton } from "./recovery";
import { MetricsButton } from "./metrics";
import { BulkPowerButton } from "./bulk-power";
import { OIDCLogin, SSORequired } from "./oidc";
import { CreateResourceButton } from "./creation";
import { PowerActions } from "./operations";
import { z } from "zod";
import { api, queries, message } from "./api";
import type {
  SessionResponse,
  Organization,
  Connection,
} from "./gen/providah/v1/console_pb";
import { ScanStatus } from "./gen/providah/v1/console_pb";
import "./style.css";

function NavLink(props:React.ComponentProps<typeof RouterNavLink>) {
 const location=useLocation();
 const org=new URLSearchParams(location.search).get("org");
 let to=props.to;
 if(typeof to==="string"&&org) {const url=new URL(to,window.location.origin);if(!url.searchParams.has("org"))url.searchParams.set("org",org);to=url.pathname+url.search+url.hash;}
 return <RouterNavLink {...props} to={to}/>;
}
type Context = { org: Organization };
const providers: Record<
  string,
  { name: string; short: string; color: string }
> = {
  aws: { name: "Amazon Web Services", short: "AWS", color: "#d98a1b" },
  digitalocean: { name: "DigitalOcean", short: "DO", color: "#2671ed" },
  hetzner: { name: "Hetzner Cloud", short: "HZ", color: "#dd4252" },
};
function Mark({ provider }: { provider: string }) {
  const p = providers[provider];
  return (
    <span
      className="provider-mark"
      style={{ color: p?.color, background: `${p?.color}12` }}
    >
      {p?.short ?? provider}
    </span>
  );
}
function Empty({
  icon: Icon = Cloud,
  title,
  children,
}: {
  icon?: typeof Cloud;
  title: string;
  children: React.ReactNode;
}) {
  return (
    <div className="empty">
      <div className="empty-icon">
        <Icon size={28} />
      </div>
      <h3>{title}</h3>
      <p>{children}</p>
    </div>
  );
}
function Auth() {
  const status = useQuery({
    queryKey: ["setup"],
    queryFn: () => api.setupStatus({}),
  });
  const [token, setToken] = useState(""),
    [secret, setSecret] = useState("");
  const setup = status.data?.required;
  const verifyingOIDC = !setup && new URLSearchParams(window.location.search).get("oidc") === "verify";
  const [recovery, setRecovery] = useState(false);
  const [credentials, setCredentials] = useState<{email: string; password: string} | null>(null);
  const mfaStep = credentials !== null;
  const begin = useMutation({
    mutationFn: (value: string) => api.beginSetup({ token: value }),
    onSuccess: (r, value) => {
      setToken(value);
      setSecret(r.secret);
    },
  });
  const finish = useMutation({
    mutationFn: (v: Record<string, string>) =>
      setup
        ? api.finishSetup({
            token,
            email: v.email,
            password: v.password,
            code: v.code,
            organizationName: v.organizationName,
          })
        : api.login({ email: credentials?.email ?? v.email, password: credentials?.password ?? v.password, code: recovery ? "" : (v.code ?? ""), recoveryCode: recovery ? v.code : "" }),
    onSuccess: (r, v) => {
      if (r.mfaRequired) {
        setCredentials({ email: v.email, password: v.password });
        return;
      }
      setCredentials(null);
      queries.clear();
      window.location.reload();
    },
  });
  return (
    <main className="auth">
      <section className="auth-story">
        <div className="brand">
          <Layers size={27} />
          <span>
            providah<span className="brand-dot">.</span>
            <small>{import.meta.env.VITE_PROVIDAH_EDITION || "Community"}</small>
          </span>
        </div>
        <div>
          <div className="eyebrow">ONE PLACE. EVERY CLOUD.</div>
          <h1>
            Your infrastructure.
            <br />A clearer perspective.
          </h1>
          <p>
            A workspace for the people who keep things running. Bring your cloud
            connections together, with access that stays under your control.
          </p>
          <div className="auth-providers">
            {Object.keys(providers).map((p) => (
              <Mark key={p} provider={p} />
            ))}
          </div>
        </div>
        <small>SELF-HOSTED · BUILT FOR OPERATORS</small>
      </section>
      <section className="auth-form">
        <div className="eyebrow">WELCOME TO PROVIDAH</div>
        <h2>{setup ? "Set up your workspace" : mfaStep ? "Verify your sign-in" : "Good to see you again"}</h2>
        <p className="muted">
          {setup
            ? "Create the first administrator and enroll your authenticator."
            : mfaStep ? "Enter your authenticator code to finish signing in." : "Sign in securely to your cloud operations workspace."}
        </p>
        <ErrorNote error={status.error} />
        {status.isPending ? (
          <p>Checking installation…</p>
        ) : setup && !secret ? (
          <ActionForm
            fields={[
              {
                name: "token",
                label: "Installation setup token",
                type: "password" as const,
                autoComplete: "off",
                description: "Use the setup token from your installation.",
              },
            ]}
            onSubmit={(v) => begin.mutateAsync(v.token)}
            submitLabel="Begin secure setup"
            pending={begin.isPending}
            error={begin.error}
          />
        ) : verifyingOIDC ? null : (
          <>
            {setup && (
              <div className="enrollment">
                <strong>Add to your authenticator</strong>
                <p>
                  Enter this setup key in your authenticator app, then provide
                  the current code below. Enrollment expires in 10 minutes.
                </p>
                <code>{secret}</code>
              </div>
            )}
            <ActionForm
              key={setup ? "setup" : mfaStep ? (recovery ? "recovery-login" : "mfa-login") : "login"}
              fields={[
                ...(setup
                  ? [
                      {
                        name: "organizationName",
                        label: "Organization name",
                        schema: z.string().min(1).max(120),
                      },
                    ]
                  : []),
                ...(!mfaStep ? [{
                  name: "email",
                  label: "Email address",
                  type: "email" as const,
                  schema: z.email(),
                  autoComplete: "username",
                },
                {
                  name: "password",
                  label: "Password",
                  type: "password" as const,
                  schema: z.string().min(12).max(72),
                  autoComplete: setup ? "new-password" : "current-password",
                }] : []),
                ...(setup || mfaStep ? [{
                  name: "code",
                  label: !setup && recovery ? "Recovery code" : "Authenticator code",
                  type: !setup && recovery ? "password" as const : "text" as const,
                  schema: !setup && recovery ? z.string().min(32).max(64) : z
                    .string()
                    .regex(/^\d{6}$/, "Enter a six-digit code."),
                  autoComplete: "one-time-code",
                  placeholder: !setup && recovery ? "xxxxxxxx-xxxxxxxx-xxxxxxxx-xxxxxxxx" : "000000",
                }] : []),
              ]}
              onSubmit={(v) => finish.mutateAsync(v)}
              submitLabel={setup ? "Create workspace" : mfaStep ? "Verify and sign in" : "Sign in"}
              pending={finish.isPending}
              error={finish.error}
            />
          </>
        )}
        {!setup && mfaStep && !verifyingOIDC && !status.isPending && <><button className="text-button" onClick={() => { setRecovery(!recovery); finish.reset(); }}>{recovery ? "Use authenticator instead" : "Use a recovery code"}</button>{recovery && <p className="muted">Use one unused recovery code. Older sessions will be signed out. Sensitive actions still require authenticator verification.</p>}</>}
        {mfaStep && <button className="text-button" onClick={() => { setCredentials(null); setRecovery(false); finish.reset(); }}>Back to sign in</button>}
        {!setup && !mfaStep && !status.isPending && <OIDCLogin/>}
        <div className="auth-footer">
          <ShieldCheck size={16} /> Credentials stay in your installation.
        </div>
      </section>
    </main>
  );
}
function CreateOrganizationButton({ mfaEnabled, onCreated }: { mfaEnabled: boolean; onCreated: (id: string) => void }) {
  const [open, setOpen] = useState(false);
  const create = useMutation({
    gcTime: 0,
    mutationFn: (v: Record<string, string>) => api.createOrganization({name: v.name, password: v.password, code: v.code ?? ""}),
    onSuccess: async (org) => {
      await queries.invalidateQueries({queryKey: ["session"]});
      setOpen(false);
      onCreated(org.id);
    },
  });
  return <>
    <button className="nav-action" aria-label="Create organization" title="Create organization" onClick={() => { create.reset(); setOpen(true); }}><Layers size={17}/><span className="nav-text">Create organization</span></button>
    <Modal open={open} onOpenChange={setOpen} title="Create organization" description="Create a separate organization for cloud connections, resources, and access. Invite its team after creation.">
      {open && <ActionForm fields={[
        {name: "name", label: "Organization name", schema: z.string().trim().min(1).max(120)},
        {name: "password", label: "Current password", type: "password", autoComplete: "current-password", schema: z.string().min(12).max(72)},
        ...(mfaEnabled ? [{name: "code", label: "Fresh authenticator code", autoComplete: "one-time-code", schema: z.string().regex(/^\d{6}$/, "Enter a six-digit code.")}] : []),
      ]} onSubmit={v => create.mutateAsync(v)} submitLabel="Create organization" pending={create.isPending} error={create.error}/>}
    </Modal>
  </>;
}
const clampSidebarWidth=(width:number)=>Math.max(224,Math.min(360,Number.isFinite(width) ? width : 244));
function Shell() {
  const navigation = useNavigation();
  const location=useLocation();
  const navigate=useNavigate();
  const admin=location.pathname.startsWith("/admin");
  const initialSession = useLoaderData() as SessionResponse | null;
  const { data: session } = useQuery({
    queryKey: ["session"],
    queryFn: () => api.getSession({}),
    initialData: initialSession ?? undefined,
    enabled: !!initialSession,
  });
  const [params,setParams]=useSearchParams();
  const selected=params.get("org")??"";
  const [collapsed,setCollapsed]=useState(()=>localStorage.getItem("sidebar-collapsed")==="true");
  const [sidebarWidth,setSidebarWidth]=useState(()=>clampSidebarWidth(Number(localStorage.getItem("sidebar-width") ?? 244)));
  const resizeSidebar=(width:number,remember=false)=>{const value=clampSidebarWidth(width);setSidebarWidth(value);if(remember)localStorage.setItem("sidebar-width",String(value));};
  useEffect(()=>{
    const close=(event:PointerEvent)=>{const menu=document.querySelector<HTMLDetailsElement>(".account-menu");if(menu && !menu.contains(event.target as Node))menu.open=false;};
    document.addEventListener("pointerdown",close);
    return ()=>document.removeEventListener("pointerdown",close);
  },[]);

  const [dark, setDark] = useState(
    () => localStorage.getItem("theme") === "dark",
  );
  useEffect(() => {
    document.documentElement.dataset.theme = dark ? "dark" : "light";
  }, [dark]);
  const availableOrgs=session?.organizations.filter(o=>!admin||session.globalAdmin||o.permissions.includes("admin.access")) ?? [];
  const org=selected?availableOrgs.find(o=>o.id===selected):availableOrgs[0];
  useEffect(()=>{if(!selected&&org){setParams(previous=>{const next=new URLSearchParams(previous);next.set("org",org.id);return next;},{replace:true});}},[selected,org?.id,setParams]);
  const identityOrg=session?.organizations.find(o=>o.id===selected) ?? session?.organizations[0];
  const canAdmin=!!session && (session.globalAdmin||session.organizations.some(o=>o.permissions.includes("admin.access")));
  const changeOrg=(id:string)=>{void queries.cancelQueries({predicate:q=>q.queryKey[0]!=="session"});queries.removeQueries({predicate:q=>q.queryKey[0]!=="session"});setParams({org:id});};
  const live = useLiveUpdates(org?.ssoRequired || org?.mfaRequired ? undefined : org?.id);
  const logout = useMutation({
    mutationFn: () => api.logout({}),
    onSuccess: () => {
      queries.clear();
      window.location.assign("/");
    },
  });
  if (!session) return <Auth />;
  const nav = admin ? [
    ["/admin", "Administration", LayoutDashboard, ""],
    ["/admin/installation-audit", "Global audit", ShieldCheck, "global-admin"],
    ["/admin/health", "Global health", Activity, "global-admin"],
    ["/admin/connections", "Connections", Plug, "connections.read"],
    ["/admin/modules", "Provider modules", Layers, "modules.read"],
    ["/admin/notifications", "Notifications", Activity, "notifications.read"],
    ["/admin/maintenance", "Maintenance", Activity, "maintenance.read"],
    ["/admin/audit", "Audit log", ShieldCheck, "audit.read"],
    ["/admin/audit-export", "Audit export", ShieldCheck, "audit.read"],
    ["/admin/access", "Team & access", KeyRound, "members.read"],
    ["/admin/identity", "Sign-in policy", ShieldCheck, "identity.manage"],
    ["/admin/resource-policy", "Resource management", Server, "roles.manage"],
  ] as const : [
    ["/app", "Overview", LayoutDashboard, ""],
    ["/app/resources", "Inventory", Server, "resources.read"],
    ["/app/dashboards", "Dashboards", Layers, "resources.read"],
    ["/app/operations", "Operations", Activity, "operations.read"],
    ["/app/templates", "Templates", Layers, "templates.read"],
    ["/app/schedules", "Schedules", Activity, "schedules.read"],
  ] as const;
  const routePath=location.pathname.replace(/\/+$/,"");
  const routePermission=routePath.startsWith("/app/resources/")?"resources.read":nav.find(([path])=>path===routePath)?.[3];
  const directory=admin&&routePath==="/admin";
  return (
    <div className={`app ${dark ? "dark" : ""} ${collapsed ? "sidebar-collapsed" : ""}`} style={{"--sidebar-width":`${sidebarWidth}px`} as React.CSSProperties}>
      <aside className="sidebar">
        <div className="brand">
          <Layers size={26} />
          <span>
            providah<span className="brand-dot">.</span>
            <small>{import.meta.env.VITE_PROVIDAH_EDITION || "Community"}</small>
          </span>

        </div>
        <div className="sidebar-scroll" id="sidebar-navigation">
        <section className="nav-group" aria-label={admin?"Administration navigation":"Workspace"}>
          <div className="nav-label">{admin?"Administration":"Workspace"}</div>
          <nav aria-label={admin?"Administration navigation":"Workspace"}>
            {(!admin||canAdmin)&&nav.filter(([, , ,permission])=>!permission||(permission==="global-admin"?session.globalAdmin:org&&navigationAllowed(org.permissions,permission))).map(([href,label,Icon])=><NavLink key={href} to={href} end aria-label={label} title={collapsed?label:undefined}><Icon size={17}/><span className="nav-text">{label}</span></NavLink>)}
          </nav>
        </section>
        <section className="nav-group">
          <nav aria-label="Console switcher">
            {admin?<NavLink to="/app" aria-label="User console" title={collapsed?"User console":undefined}><Cloud size={17}/><span className="nav-text">User console</span></NavLink>:canAdmin&&<NavLink to={"/admin?org="+(session.organizations.find(o=>o.id===org?.id&&(session.globalAdmin||o.permissions.includes("admin.access")))?.id??session.organizations.find(o=>session.globalAdmin||o.permissions.includes("admin.access"))?.id??"")} aria-label="Admin console" title={collapsed?"Admin console":undefined}><ShieldCheck size={17}/><span className="nav-text">Admin console</span></NavLink>}
          </nav>
        </section>
        </div>
          <button className="icon-button sidebar-toggle" aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"} title={collapsed ? "Expand sidebar" : "Collapse sidebar"} aria-expanded={!collapsed} aria-controls="sidebar-navigation" onClick={()=>{setCollapsed(!collapsed);localStorage.setItem("sidebar-collapsed",String(!collapsed));}}>{collapsed ? <PanelLeftOpen size={20}/> : <PanelLeftClose size={20}/>}<span className="nav-text">{collapsed ? "Expand sidebar" : "Collapse sidebar"}</span></button>
        <footer className="sidebar-bottom">
          <span>v{version}</span>
          <span>by <a href="https://alphabravo.io" target="_blank" rel="noopener noreferrer">AlphaBravo</a></span>
        </footer>
        {!collapsed && <div className="sidebar-resize" role="separator" tabIndex={0} aria-label="Resize sidebar" aria-orientation="vertical" aria-valuemin={224} aria-valuemax={360} aria-valuenow={sidebarWidth} title="Drag to resize · arrow keys adjust width"
          onPointerDown={e=>{if(e.button===0){e.preventDefault();e.currentTarget.setPointerCapture(e.pointerId);}}}
          onPointerMove={e=>{if(e.currentTarget.hasPointerCapture(e.pointerId))resizeSidebar(e.clientX);}}
          onPointerUp={e=>{if(e.currentTarget.hasPointerCapture(e.pointerId)){resizeSidebar(e.clientX,true);e.currentTarget.releasePointerCapture(e.pointerId);}}}
          onKeyDown={e=>{if(["ArrowLeft","ArrowRight","Home","End"].includes(e.key)){e.preventDefault();resizeSidebar(e.key==="Home"?224:e.key==="End"?360:sidebarWidth+(e.key==="ArrowRight"?16:-16),true);}}}
        />}
      </aside>
      <div className="main-wrap">
        <header className="topbar">
        <div className="workspace topbar-workspace">
          <span className="workspace-icon">{org?.name.slice(0, 1) ?? "P"}</span>
          <div>
            <select
              aria-label="Organization"
              value={org?.id ?? ""}
              onChange={(e) => {
                changeOrg(e.target.value);
              }}
            >
              {availableOrgs.map((o) => (
                <option value={o.id} key={o.id}>
                  {o.name}
                </option>
              ))}
            </select>
          </div>
        </div>

          <div className="topbar-actions">
            <span className="connection-status" title={`Connection: ${live}`}><span className="dot" /> <span>{live}</span></span>
            <button className="icon-button" aria-label={dark ? "Light appearance" : "Dark appearance"} title={dark ? "Light appearance" : "Dark appearance"} onClick={() => {setDark(!dark);localStorage.setItem("theme",dark ? "light" : "dark");}}>
              {dark ? <Sun size={19}/> : <Moon size={19}/>}
            </button>
            <details className="account-menu" onKeyDown={e=>{if(e.key==="Escape")e.currentTarget.open=false;}}>
              <summary aria-label="Account menu" title="Account menu"><span className="avatar">{session.email.slice(0,1).toUpperCase()}</span></summary>
          <div className="profile account-panel" onClick={e=>{if((e.target as Element).closest("button")) e.currentTarget.closest("details")?.removeAttribute("open");}}>
            <span className="avatar">
              {session.email.slice(0, 1).toUpperCase()}
            </span>
            <div>
              <strong>Signed in</strong>
              {session.mfaEnabled && <MfaButton />}
              <RecoveryButton />
              <small>{session.email}</small><small>{session.globalAdmin ? "Global administrator" : "Organization account"} · MFA {session.mfaEnabled ? "enabled" : "disabled"}</small>
            </div>
            <button
              className="icon-button"
              aria-label="Sign out"
              onClick={() => logout.mutate()}
            >
              <LogOut size={17} />
            </button>
          </div>

            </details>
          </div>
        </header>
        <main className="content" aria-busy={navigation.state !== "idle"}>
          <nav className="breadcrumbs" aria-label="Breadcrumb">
            <NavLink aria-label={admin?"Admin console home":"Console home"} to={admin?"/admin":"/app"}>{admin?"Administration":"Console"}</NavLink>
            {org&&<><ChevronRight size={13}/><span>{org.name}</span></>}
            {routePath!==(admin?"/admin":"/app")&&<><ChevronRight size={13}/><span aria-current="page">{nav.find(([path])=>path===routePath)?.[1]??Object.entries(resourceKinds).find(([kind])=>resourcePath(kind)===routePath)?.[1]??(routePath==="/app/resources/all"?"All resources":routePath.startsWith("/app/resources/detail/")?"Resource details":"Page")}</span></>}
          </nav>
          {navigation.state !== "idle" && <p role="status">Loading page…</p>}
          {admin&&!canAdmin&&identityOrg?.ssoRequired ? <SSORequired/> : admin&&!canAdmin&&identityOrg?.mfaRequired ? <Empty title="MFA required">Enable MFA from your account menu to continue.</Empty> : admin&&!canAdmin ? <Empty title="Administration access required">Your account has no administrative grants. Use the user console or ask an administrator for access.</Empty> : routePath==="/admin/health" ? (session.globalAdmin?<InstallationHealth/>:<Empty title="Global administrator required">This view requires installation access.</Empty>) : routePath==="/admin/installation-audit" ? (session.globalAdmin?<Audit installation/>:<Empty title="Global administrator required">This view requires installation access.</Empty>) : directory ? <>
            <PageHeader eyebrow={session.globalAdmin?"Global administration":"Delegated administration"} title="Administration" description={session.globalAdmin?"Manage your installation and its organizations.":"Manage only the organizations and controls granted to your account."}>
              {session.globalAdmin&&<CreateOrganizationButton mfaEnabled={session.mfaEnabled} onCreated={changeOrg}/>}
            </PageHeader>
            {session.globalAdmin&&<section className="panel"><div className="panel-heading"><h2>Installation security</h2><MFAPolicyButton/></div></section>}
            {session.globalAdmin?<InstallationDirectory email={session.email} mfaEnabled={session.mfaEnabled}/>:<section className="panel">
              <div className="panel-heading"><h2>Organizations</h2><span>{availableOrgs.length} available</span></div>
              <DataTable label="Organizations" data={availableOrgs} rowId={o=>o.id} columns={[
                {id:"organization",header:"Organization",cell:({row:{original:o}})=><button className="text-button" onClick={()=>{changeOrg(o.id);const target=nav.find(([, , ,p])=>p&&navigationAllowed(o.permissions,p));navigate((target?.[0]??"/admin")+"?org="+o.id);}}>{o.name}</button>},
                {id:"scope",header:"Access",cell:({row:{original:o}})=><span>{o.mfaRequired?"MFA required":o.ssoRequired?"SSO required":session.globalAdmin?"Global administrator":"Delegated permissions"}</span>},
                {id:"permissions",header:"Permissions",cell:({row:{original:o}})=><span>{o.permissions.length ? String(o.permissions.filter(p=>p!=="admin.access").length)+" permissions" : "Identity verification required"}</span>},
              ]}/>
            </section>}
          </> : org ? (
            org.mfaRequired ? <Empty title="MFA required">An administrator requires MFA for this organization. Open the account menu and enable MFA to continue.</Empty> : org.ssoRequired ? <SSORequired/> : routePermission&&!navigationAllowed(org.permissions,routePermission)?<Empty title="Permission required">This page is outside your granted permissions.</Empty> : <Outlet key={org.id+String(admin)} context={{ org } satisfies Context} />
          ) : (
            <Empty title={selected?"Organization unavailable":"No organization access"}>
              Ask an organization administrator to grant access.
            </Empty>
          )}
        </main>
        <footer className="footer">
          PROVIDAH <span>Server discovery · AWS, DigitalOcean, Hetzner</span>
        </footer>
      </div>
    </div>
  );
}
function Overview() {
 const {org}=useOutletContext<Context>();
 return <OperationsOverview org={org}/>;
}
const awsCredentialFields: FormField[] = [
  { name: "access_key_id", label: "AWS access key ID", type: "password", autoComplete: "off", schema: z.string().min(1).max(256) },
  { name: "secret_access_key", label: "AWS secret access key", type: "password", autoComplete: "new-password", schema: z.string().min(1).max(4096) },
  { name: "session_token", label: "AWS session token (optional)", type: "password", autoComplete: "off", schema: z.string().max(8192) },
  { name: "role_arn", label: "Role ARN (optional)", schema: z.string().max(2048), description: "Assume this role before accessing resources. Leave empty to use the supplied keys directly." },
  { name: "external_id", label: "External ID (optional)", type: "password", autoComplete: "off", schema: z.string().max(1224), description: "Must match the role trust policy. Requires a role ARN." },
  { name: "account_id", label: "Expected AWS account ID (optional)", schema: z.string().regex(/^(?:[0-9]{12})?$/, "Enter a 12-digit AWS account ID."), description: "Verifies the account before running a job. A role ARN already pins its target account." },
];
function credentialFields(externalEnabled: boolean, provider?: string): FormField[] {
  const builtin = { name: "credential_source", is: ["builtin"] };
  const external = { name: "credential_source", is: ["vault_kv2"] };
  return [
    { name: "credential_source", label: "Credential source", type: "select", defaultValue: "builtin", options: [
      { value: "builtin", label: "Encrypted credential" },
      ...(externalEnabled ? [{ value: "vault_kv2", label: "External OpenBao / Vault KV v2" }] : []),
    ] },
    { name: "vault_path", label: "Secret path", when: external, schema: z.string().min(1).max(200).regex(/^[A-Za-z0-9][A-Za-z0-9_.-]*(\/[A-Za-z0-9][A-Za-z0-9_.-]*)*$/), description: "Relative to providah/<organization ID>/<provider>/ in the configured KV v2 mount." },
    { name: "vault_key", label: "Secret field", when: external, defaultValue: "credential", schema: z.string().max(80).regex(/^[A-Za-z0-9][A-Za-z0-9_.-]*$/), description: "A string containing the provider API token or AWS credential JSON." },
    { name: "vault_version", label: "Secret version", type: "number", when: external, schema: z.string().regex(/^[1-9][0-9]*$/).refine(v => Number(v) <= 2147483647, "Enter a positive version number."), description: "An exact existing version is required. Rotating a secret requires replacing this reference." },
    ...(provider && provider !== "aws" ? [] : awsCredentialFields.map(f => ({ ...f, when: provider ? builtin : [builtin, { name: "provider", is: ["aws"] }] }))),
    ...(provider === "aws" ? [] : [{ name: "credential", label: provider ? "New credential" : "API token", type: "password" as const, autoComplete: "new-password", when: provider ? builtin : [builtin, { name: "provider", is: ["digitalocean", "hetzner"] }], schema: z.string().min(8).max(16384) }]),
  ];
}
function connectionCredential(provider: string, values: Record<string, string>) {
  if (values.credential_source === "vault_kv2") return "vault-kv2:" + JSON.stringify({ path: values.vault_path, key: values.vault_key, version: Number(values.vault_version) });
  return provider === "aws"
    ? JSON.stringify(Object.fromEntries(awsCredentialFields.filter(f => values[f.name]).map(f => [f.name, values[f.name]])))
    : values.credential;
}
function Connections() {
  const { org } = useOutletContext<Context>();
  const [open, setOpen] = useState(false),
    [rotating, setRotating] = useState<Connection | null>(null);
  const [params,setParams]=useSearchParams();
  const selected=params.get("connection")??"";
  const closeDetail=()=>setParams(previous=>{const next=new URLSearchParams(previous);next.delete("connection");return next;});
  const detail=useQuery({queryKey:["connection",org.id,selected],queryFn:()=>api.getConnection({organizationId:org.id,id:selected}),enabled:!!selected});
  const current=detail.isError?undefined:detail.data?.connection;
  const allowed = org.permissions.includes("connections.manage");
  const q = useQuery({
    queryKey: ["connections", org.id],
    queryFn: () => api.listConnections({ organizationId: org.id }),
  });
  const refresh = () => {
    queries.invalidateQueries({ queryKey: ["connections", org.id] });
    queries.invalidateQueries({ queryKey: ["connection", org.id] });
    queries.invalidateQueries({ queryKey: ["audit", org.id] });
  };
  const add = useMutation({
    gcTime: 0,
    mutationFn: (v: Record<string, string>) =>
      api.createConnection({
        organizationId: org.id,
        name: v.name,
        provider: v.provider,
        region: v.region,
        credential: connectionCredential(v.provider, v),
      }),
    onSuccess: () => {
      setOpen(false);
      add.reset();
      refresh();
    },
  });
  const toggle = useMutation({
    mutationFn: (c: Connection) =>
      api.setConnectionEnabled({
        organizationId: org.id,
        id: c.id,
        enabled: !c.enabled,
      }),
    onSuccess: refresh,
  });
  const scan = useMutation({
    mutationFn: (id: string) =>
      api.refreshConnection({ organizationId: org.id, id }),
    onSuccess: refresh,
  });
  const rotate = useMutation({
    gcTime: 0,
    mutationFn: (credential: string) =>
      api.rotateCredential({
        organizationId: org.id,
        id: rotating!.id,
        credential,
      }),
    onSuccess: () => {
      setRotating(null);
      rotate.reset();
      refresh();
    },
  });
  useEffect(()=>{setRotating(null);rotate.reset();toggle.reset();scan.reset();},[selected]);
  return (
    <>
      <PageHeader
        eyebrow="CONNECT YOUR INFRASTRUCTURE"
        title="Cloud connections"
        description="One secure connection. Shared access without shared secrets."
      >
        {allowed && (
          <button className="primary" onClick={() => setOpen(true)}>
            <Plus size={16} /> Add connection
          </button>
        )}
      </PageHeader>
      <ErrorNote error={q.error || toggle.error || scan.error} />
      <div className="notice">
        <ShieldCheck size={18} />
        <span>
          Credentials are encrypted at rest and never returned by the API.
          Enabled connections refresh server inventory every five minutes when
          the provider launcher is running. A successful refresh confirms server
          read access.
        </span>
      </div>
      <section className="panel">
        {q.isPending ? (
          <p className="loading">Loading connections…</p>
        ) : q.isError ? <ErrorNote error={q.error}/> : q.data?.connections.length ? (
          <DataTable
            label="Connections"
            data={q.data.connections}
            rowId={(r) => r.id}
            columns={[
              { id: "credentialSource", header: "Credential source", cell: ({ row }) => row.original.credentialSource === "vault_kv2" ? "External KV v2" : "Encrypted credential" },
              {
                id: "col0",
                header: 'Connection',
                cell: ({ row: { original: c } }) => (
                  <>
                    <div className="connection-name">
                      <Mark provider={c.provider} />
                      <div>
                        <Link to={`/admin/connections?org=${org.id}&connection=${c.id}`}>{c.name}</Link>
                        <small>
                          Added {new Date(c.createdAt).toLocaleDateString()}
                        </small>
                      </div>
                    </div>
                  </>
                ),
              },
              {
                id: "col1",
                header: 'Provider',
                cell: ({ row: { original: c } }) => (
                  <>{providers[c.provider]?.name}</>
                ),
              },
              {
                id: "col2",
                header: 'Region',
                cell: ({ row: { original: c } }) => (
                  <>{c.region || "All / provider default"}</>
                ),
              },
              {
                id: "col3",
                header: 'Status',
                cell: ({ row: { original: c } }) => (
                  <>
                    <span className={`status ${c.enabled ? "enabled" : ""}`}>
                      <span className="dot" />
                      {c.enabled ? "Enabled" : "Disabled"}
                    </span>
                  </>
                ),
              },
              {
                id: "col4",
                header: 'Discovery',
                cell: ({ row: { original: c } }) => (
                  <>
                    <span>
                      {{
                        [ScanStatus.UNSPECIFIED]: "Not refreshed",
                        [ScanStatus.NEVER]: "Not refreshed",
                        [ScanStatus.QUEUED]: "Queued",
                        [ScanStatus.RUNNING]: "Refreshing…",
                        [ScanStatus.SUCCEEDED]: "Succeeded",
                        [ScanStatus.FAILED]: "Failed",
                      }[c.scanStatus] || "Not refreshed"}
                    </span>
                    <small>
                      {c.lastScanAt
                        ? `Last success ${new Date(c.lastScanAt).toLocaleString()}`
                        : "No successful refresh"}
                    </small>
                    {c.scanError && (
                      <small className="error">{c.scanError}</small>
                    )}
                  </>
                ),
              },
              {
                id: "col5",
                header: "Actions",
                cell: ({ row: { original: c } }) => (
                  <>
                    {allowed && (
                      <div className="row-actions">
                        <button
                          disabled={
                            !c.enabled ||
                            scan.isPending ||
                            c.scanStatus === ScanStatus.QUEUED ||
                            c.scanStatus === ScanStatus.RUNNING
                          }
                          onClick={() => scan.mutate(c.id)}
                        >
                          <RefreshCw size={14} /> Refresh
                        </button>
                        <button
                          onClick={() => {
                            setRotating(c);
                          }}
                        >
                          Rotate credential
                        </button>
                        <button
                          disabled={toggle.isPending}
                          onClick={() => toggle.mutate(c)}
                        >
                          {c.enabled ? "Disable" : "Enable"}
                        </button>
                      </div>
                    )}
                  </>
                ),
              },
            ]}
          />
        ) : (
          <Empty icon={Plug} title="Bring your first cloud into view">
            Connect AWS, DigitalOcean, or Hetzner to securely store account
            credentials.
          </Empty>
        )}
      </section>
      <Modal open={!!selected&&!rotating} onOpenChange={value=>{if(!value)closeDetail();}} title={current?.name??"Connection"} description="Connection metadata and discovery status. Credentials are never returned.">
        <nav className="breadcrumbs" aria-label="Connection breadcrumb"><Link to={`/admin/connections?org=${org.id}`}>Connections</Link><span aria-hidden="true">›</span><span aria-current="page">{current?.name??"Connection"}</span></nav>
        <ErrorNote error={detail.error||toggle.error||scan.error}/>
        {detail.isPending?<p>Loading connection…</p>:current?<>
          <dl className="resource-details"><div><dt>Provider</dt><dd>{providers[current.provider]?.name??current.provider}</dd></div><div><dt>Region</dt><dd>{current.region||"All / provider default"}</dd></div><div><dt>Status</dt><dd>{current.enabled?"Enabled":"Disabled"}</dd></div><div><dt>Credential source</dt><dd>{current.credentialSource==="vault_kv2"?"External KV v2":"Encrypted credential"}</dd></div><div><dt>Last successful refresh</dt><dd>{current.lastScanAt?new Date(current.lastScanAt).toLocaleString():"Never"}</dd></div></dl>
          {current.scanError&&<p className="error">{current.scanError}</p>}
          {allowed&&<div className="row-actions"><button disabled={!current.enabled||scan.isPending||current.scanStatus===ScanStatus.QUEUED||current.scanStatus===ScanStatus.RUNNING} onClick={()=>scan.mutate(current.id)}>Refresh</button><button disabled={toggle.isPending} onClick={()=>toggle.mutate(current)}>{current.enabled?"Disable":"Enable"}</button><button onClick={()=>setRotating(current)}>Rotate credential</button></div>}
        </>:null}
      </Modal>
      <Modal
        open={open}
        onOpenChange={(v) => {
          setOpen(v);
          if (!v) add.reset();
        }}
        title={<>Add a cloud connection</>}
        description={
          <>
            Credentials are write-only. You can replace them later, but cannot
            reveal them.
          </>
        }
      >
        <ErrorNote error={add.error} />
        <ActionForm
          fields={[
            {
              name: "name",
              label: "Connection name",
              schema: z.string().min(1).max(120),
            },
            {
              name: "provider",
              label: "Cloud provider",
              type: "select",
              defaultValue: "hetzner",
              options: Object.entries(providers).map(([value, p]) => ({
                value,
                label: p.name,
              })),
            },
            {
              name: "region",
              label: "Region",
              schema: z.string().max(64),
              description:
                "AWS defaults to us-east-1. Leave empty for all DigitalOcean or Hetzner regions.",
            },
            ...credentialFields(!!q.data?.externalSecretsEnabled),
          ]}
          onSubmit={(v) => add.mutateAsync(v)}
          submitLabel="Save connection"
          pending={add.isPending}
          error={add.error}
        />
      </Modal>
      <Modal
        open={!!rotating}
        onOpenChange={(v) => {
          if (!v) {
            setRotating(null);
            rotate.reset();
          }
        }}
        title={<>Replace credential</>}
        description={
          <>
            Replace the stored credential for {rotating?.name}. The previous
            value cannot be revealed.
          </>
        }
      >
        <ErrorNote error={rotate.error} />
        <ActionForm
          key={rotating?.id}
          fields={credentialFields(!!q.data?.externalSecretsEnabled, rotating?.provider)}
          onSubmit={(v) => rotate.mutateAsync(connectionCredential(rotating!.provider, v))}
          submitLabel="Replace securely"
          pending={rotate.isPending}
          error={rotate.error}
        />
      </Modal>
    </>
  );
}

function ResourceHome(){
 const [params]=useSearchParams();
 return params.has("filters")||params.has("view")?<Inventory/>:<ResourceOverview/>;
}
function Inventory({resourceKind=""}:{resourceKind?:string}) {
 const {org}=useOutletContext<Context>();
 const [params,setParams]=useSearchParams();const id=params.get("view")??"";
 let linked:InventoryViewSpec|undefined,linkError:unknown;
 try{linked=readInventoryLink(params);}catch(e){linkError=e;}
 const views=useQuery({queryKey:["inventory-views",org.id],queryFn:()=>api.listInventoryViews({organizationId:org.id}),enabled:!!id&&!params.has("filters")});
 const view=views.error?undefined:views.data?.views.find(v=>v.id===id);
 if(linkError)return <><PageHeader eyebrow="Resource inventory" title="Inventory link unavailable" description="The filter link is malformed, ambiguous or too large."/><button onClick={()=>setParams(p=>{p.delete("filters");p.delete("view");p.delete("resource");return p;})}>Back to inventory</button></>;

 if(id&&(views.isPending||!view))return <><PageHeader eyebrow="Resource inventory" title={views.isPending?"Loading saved view…":"Saved view unavailable"} description="Saved views are private and require current organization access."/><ErrorNote error={views.error}/><button onClick={()=>setParams(p=>{p.delete("view");p.delete("resource");return p;})}>Back to inventory</button></>;
 return <InventoryContent key={org.id+":"+resourceKind+":"+id+":"+(params.get("filters")??"")} resourceKind={resourceKind} initialProvider={params.get("provider")??""} initialView={view} linked={linked}/>;
}
function InventoryContent({initialView,linked,resourceKind="",initialProvider=""}:{initialView?:InventoryView;linked?:InventoryViewSpec;resourceKind?:string;initialProvider?:string}) {
  const { org } = useOutletContext<Context>();
  const initial=linked??initialView?.spec;
  const navigate=useNavigate();
  const [sorting,setSorting] = useState<SortingState>(initial?.sortBy?[{id:initial.sortBy,desc:initial.descending}]:[]);
  const [tags,setTags]=useState(initial?{tagKey:initial.tagKey,tagValue:initial.tagValue,tagName:initial.tagName,tagExists:initial.tagExists,tagConditions:initial.tagConditions,tagMatchAny:initial.tagMatchAny}:emptyTags);
  const [scope,setScope]=useState(initial?{connectionId:initial.connectionId,region:initial.region,status:initial.status}:emptyScope);
  const [tagEditor,setTagEditor]=useState(false);
  const [visibility,setVisibility] = useState<Record<string,boolean>>(Object.fromEntries((initial?.hiddenColumns??(resourceKind?["kind"]:[])).map(id=>[id,false])));
  const [search, setSearch] = useState(initial?.search??""),
    [provider, setProvider] = useState(initial?.provider??initialProvider),
    [kind, setKind] = useState(resourceKind||initial?.kind||""),
    [page, setPage] = useState("");
  const [params,setParams]=useSearchParams();
  const q = useQuery({
    queryKey: ["resources", org.id, search, provider, kind, page, sorting,tags,scope],
    queryFn: () =>
      api.listResources({
        organizationId: org.id,
        ...tags,...scope,search,
        kind,
        provider,
        pageToken: page,
        sortBy:sorting[0]?.id ?? "",
        descending:sorting[0]?.desc ?? false,
      }),
  });
  const settings={...tags,...scope,search,provider,kind,sortBy:sorting[0]?.id??"",descending:sorting[0]?.desc??false,hiddenColumns:Object.keys(visibility).filter(id=>visibility[id]===false)};
  return (
    <>
      <PageHeader
        eyebrow="ACROSS YOUR CLOUDS"
        title={resourceKind?resourceKinds[resourceKind]:"All resources"}
        description={resourceKind?`Manage your ${resourceKinds[resourceKind].toLowerCase()} across cloud connections.`:"Search all infrastructure your organization can access."}
      ><div className="row-actions"><InventoryViews org={org.id} spec={settings} onApply={(spec,id)=>{
        setScope({connectionId:spec.connectionId,region:spec.region,status:spec.status});
        setTags({tagConditions:spec.tagConditions,tagMatchAny:spec.tagMatchAny,tagKey:spec.tagKey,tagValue:spec.tagValue,tagName:spec.tagName,tagExists:spec.tagExists});setSearch(spec.search);setProvider(spec.provider);setKind(resourceKind||spec.kind);
        setSorting(spec.sortBy ? [{id:spec.sortBy,desc:spec.descending}] : []);
        setVisibility(Object.fromEntries(spec.hiddenColumns.map(id=>[id,false])));
        setPage("");setParams(p=>{p.set("org",org.id);p.set("view",id);p.delete("filters");p.delete("resource");return p;});
      }}/><InventoryLink org={org.id} spec={settings}/><BulkPowerButton org={org} resources={q.data?.resources??[]}/><CreateResourceButton org={org}/></div></PageHeader>
      <nav className="breadcrumbs" aria-label="Resource type navigation"><Link to={`/app/resources?org=${org.id}${provider?"&provider="+provider:""}`}>Resources</Link><ChevronRight size={13}/><span aria-current="page">{resourceKind?resourceKinds[resourceKind]:"All resources"}</span></nav>
      {linked&&<p className="notice">This link restores its filter snapshot. After editing, use Link to current filters to create an updated link. Access still follows your current permissions.</p>}
      {initialView&&<><nav className="breadcrumbs" aria-label="Saved view breadcrumb"><NavLink to="/app/resources">Inventory</NavLink><ChevronRight size={13}/><span aria-current="page">{initialView.name}</span></nav><p className="notice">This link restores the saved settings. Use Saved views to replace them after making changes.</p></>}
      <div className="filters">
        <label className="search">
          <Search size={17} />
          <input
            aria-label="Search resources"
            placeholder="Search by name or resource ID…"
            value={search}
            onChange={(e) => {
              setSearch(e.target.value);
              setPage("");
            }}
          />
        </label>
        <select
          aria-label="Filter provider"
          value={provider}
          onChange={(e) => {
            setProvider(e.target.value);
            setPage("");
          }}
        >
          <option value="">All providers</option>
          {Object.entries(providers).map(([id, p]) => (
            <option key={id} value={id}>
              {p.name}
            </option>
          ))}
        </select>
        {resourceKind&&<select aria-label="Resource page" value={resourceKind} onChange={e=>navigate(resourcePath(e.target.value)+`?org=${org.id}${provider?"&provider="+provider:""}`)}>{Object.entries(resourceKinds).map(([value,label])=><option key={value} value={value}>{label}</option>)}</select>}
        {!resourceKind&&<select aria-label="Filter resource type" value={kind} onChange={e => {setKind(e.target.value);setPage("");}}>
          <option value="">All resource types</option>
          {Object.entries(resourceKinds).map(([value,label]) => <option key={value} value={value}>{label}</option>)}
        </select>}
      </div>
      <ResourceScopes org={org.id} value={scope} onChange={v=>{setScope(v);setPage("");}}/>
      <TagConditions value={tags} onChange={v=>{setTags(v);setPage("");}}/>
      <button className="secondary" onClick={()=>setTagEditor(true)}>{tags.tagKey||tags.tagName?"Edit tag filter":"Filter tags"}</button>
      {(tags.tagKey||tags.tagName)&&<p className="notice">{tags.tagName?"Tag: "+tags.tagName:"Label: "+tags.tagKey+(tags.tagExists?" (exists)":" = "+tags.tagValue)} <button onClick={()=>{setTags(emptyTags);setPage("");}}>Clear tag filter</button></p>}
      <Modal open={tagEditor} onOpenChange={setTagEditor} title="Filter tags" description="Exact, case-sensitive matching against the last observation. Uncollected tags never match. This filter does not change permissions.">
        <ActionForm fields={[
          {name:"mode",label:"Tag filter type",type:"select",defaultValue:tags.tagName?"name":tags.tagExists?"exists":"equals",options:[{value:"equals",label:"Label equals"},{value:"exists",label:"Label key exists"},{value:"name",label:"Named tag present"}]},
          {name:"key",label:"Label key",defaultValue:tags.tagKey,when:{name:"mode",is:["equals","exists"]},schema:z.string().min(1).max(256)},
          {name:"value",label:"Label value",defaultValue:tags.tagValue,when:{name:"mode",is:["equals"]},schema:z.string().max(2048)},
          {name:"name",label:"Named tag",defaultValue:tags.tagName,when:{name:"mode",is:["name"]},schema:z.string().min(1).max(256)}
        ]} submitLabel="Apply tag filter" onSubmit={v=>{setTags({...emptyTags,tagKey:v.mode==="name"?"":v.key,tagValue:v.mode==="equals"?v.value:"",tagName:v.mode==="name"?v.name:"",tagExists:v.mode==="exists"});setPage("");setTagEditor(false);}}/>
      </Modal>
      <ErrorNote error={q.error} />
      <section className="panel">
        {q.isPending ? (
          <p className="loading">Loading inventory…</p>
        ) : q.data?.resources.length ? (
          <DataTable
            label="Resources"
            columnVisibility={visibility}
            onColumnVisibilityChange={setVisibility}
            sorting={sorting}
            onSortingChange={updater => {setSorting(previous => typeof updater === "function" ? updater(previous) : updater);setPage("");}}
            data={q.data.resources}
            rowId={(r) => r.id}
            columns={[
              {
                accessorKey: "name",
                header: 'Resource',
                cell: ({ row: { original: r } }) => (
                  <>
                    <Link to={`/app/resources/detail/${encodeURIComponent(r.id)}?org=${org.id}`}>
                      {r.name || r.nativeId}
                    </Link>
                    <small>{r.nativeId}</small>
                  </>
                ),
              },
              {
                accessorKey: "provider",
                header: 'Provider',
                cell: ({ row: { original: r } }) => (
                  <>
                    <Mark provider={r.provider} />
                  </>
                ),
              },
              {
                accessorKey: "kind",
                header: 'Type',
                cell: ({ row: { original: r } }) => <>{resourceKinds[r.kind] ?? r.kind}</>,
              },
              {
                accessorKey: "region",
                header: 'Region',
                cell: ({ row: { original: r } }) => <>{r.region}</>,
              },
              {
                accessorKey: "status",
                header: 'Status',
                cell: ({ row: { original: r } }) => <>{r.status}</>,
              },
              {
                id: "observedAt",
                header: 'Last observed',
                cell: ({ row: { original: r } }) => (
                  <>{new Date(r.observedAt).toLocaleString()}</>
                ),
              },
            ]}
          />
        ) : (
          <Empty
            icon={Server}
            title={
              search ? "No matching resources" : "No resources discovered yet"
            }
          >
            Enable a cloud connection and refresh it to discover resources. Check
            the connection’s discovery status if resources are missing.
          </Empty>
        )}
      </section>
      <div className="pagination">
        {page && <button onClick={() => setPage("")}>First page</button>}
        {q.data?.nextPageToken && (
          <button onClick={() => setPage(q.data!.nextPageToken)}>
            Next page <ChevronRight size={15} />
          </button>
        )}
      </div>

    </>
  );
}
function ResourceDetailPage(){
  const {org}=useOutletContext<Context>();
  const {resourceId=""}=useParams();
  const detail=useQuery({queryKey:["resource",org.id,resourceId],queryFn:()=>api.getResource({organizationId:org.id,id:resourceId}),refetchInterval:15000});
  const resource=detail.data?.resource;
  return <>
    <PageHeader eyebrow={resource?resourceKinds[resource.kind]??resource.kind:"RESOURCE"} title={resource?.name||resource?.nativeId||"Resource details"} description="Inventory reflects the last successful observation. Refresh the connection for the latest state."/>
    <nav className="breadcrumbs" aria-label="Resource breadcrumb"><Link to={`/app/resources?org=${org.id}`}>Resources</Link>{resource&&<><ChevronRight size={13}/><Link to={resourcePath(resource.kind)+`?org=${org.id}`}>{resourceKinds[resource.kind]??resource.kind}</Link></>}<ChevronRight size={13}/><span aria-current="page">{resource?.name||"Resource details"}</span></nav>
        <ErrorNote error={detail.error} />
        {detail.isPending && <p>Loading resource…</p>}
        {detail.data?.resource && (
          <>
            {!detail.data.providerEnabled && <p className="notice">This provider module is disabled. Inventory is retained; new discovery and actions are paused.</p>}
            {!detail.data.connectionEnabled && (
              <p className="notice">
                Connection disabled. Inventory is retained; refresh is paused.
              </p>
            )}
            {!!detail.data.ownershipProjectIds.length && <div className="notice" role="note"><strong>Protected by managed IaC state</strong><p>Direct edits and deletion are blocked. Power actions and server-image creation remain available. A later Terraform/OpenTofu apply may undo power changes. State references are matched within the project’s configured connection; they do not verify provider aliases or cloud account ownership.</p>{org.permissions.includes("templates.read") && detail.data.ownershipProjectIds.map(id=><p key={id}><Link to={`/app/templates?org=${org.id}&project=${id}`}>View referencing project · {id.slice(0,8)}</Link></p>)}</div>}
            {!detail.data.ownershipProjectIds.length && <p className="notice">No supported IaC state reference found. Detection covers supported resource types in stored project state; unsupported types, external state and provider aliases may be unrecognized.</p>}
            <section aria-label="Resource tags"><h3>Tags and labels</h3>{detail.data.resource.tags ? <>
              {Object.entries(detail.data.resource.tags.labels).map(([key,value])=><span className="badge" key={key}>{key}={value}</span>)}
              {detail.data.resource.tags.names.map(name=><span className="badge" key={name}>{name}</span>)}
              {!Object.keys(detail.data.resource.tags.labels).length&&!detail.data.resource.tags.names.length&&<p>No tags on the last observation.</p>}
            </> : <p>Tag metadata has not been collected for this resource.</p>}</section>
            <MetricsButton key={detail.data.resource.id} org={org} resource={detail.data.resource} supported={detail.data.metricsSupported} enabled={detail.data.connectionEnabled && detail.data.providerEnabled}/>
            <PowerActions key={detail.data.resource.id}
              org={org}
              resource={detail.data.resource}
              availableActions={detail.data.availableActions}
              managedByIaC={!!detail.data.ownershipProjectIds.length}
              enabled={detail.data.connectionEnabled && detail.data.providerEnabled}
            />
            <dl className="resource-details">
              {Object.entries({
                Connection: detail.data.connectionName,
                Provider: providers[detail.data.resource.provider]?.name,
                "Resource ID": detail.data.resource.nativeId,
                Type: resourceKinds[detail.data.resource.kind] ?? detail.data.resource.kind,
                Region: detail.data.resource.region,
                Status: detail.data.resource.status,
                Specification: detail.data.resource.size,
                "Public IP": detail.data.resource.publicIp,
                "Private IP": detail.data.resource.privateIp,
                "Last observed": new Date(
                  detail.data.resource.observedAt,
                ).toLocaleString(),
              }).map(([label, value]) => (
                <div key={label}>
                  <dt>{label}</dt>
                  <dd>{value || "—"}</dd>
                </div>
              ))}
            </dl>
          </>
        )}
    {!detail.isPending&&!detail.error&&!resource&&<Empty title="Resource unavailable">This resource is no longer available in this organization.</Empty>}
  </>;
}
function Audit({installation=false}:{installation?:boolean}={}) {
  const context = useOutletContext<Context|undefined>();
  const orgId=installation?"":context!.org.id;
  const [source,setSource]=useState("installation");
  const [page, setPage] = useState("");
  const empty = {actor:"",action:"",target:"",occurredFrom:"",occurredBefore:""};
  const [filters, setFilters] = useState(empty);
  const [editing, setEditing] = useState(false);
  const [filterError, setFilterError] = useState<unknown>();
  const filtered = Object.values(filters).some(Boolean);
  const q = useQuery({
    queryKey: [installation?"installation-audit":"audit", orgId, source, page, filters],
    queryFn: () => (installation?api.listInstallationAudit:api.listAudit)({ organizationId: orgId,source,pageToken: page, ...filters }),
  });
  return (
    <>
      <PageHeader
        eyebrow="ACCOUNTABILITY, BUILT IN"
        title={installation?"Global audit":"Audit log"}
        description={installation?"Review installation changes and activity across organizations.":"A record of who changed what in your organization."}
      >
        <span className="badge">
          <ShieldCheck size={14} /> Secrets excluded
        </span>
      </PageHeader>
      <div className="table-controls">
        {installation&&<select aria-label="Audit source" value={source} onChange={e=>{setSource(e.target.value);setPage("");}}><option value="installation">Installation events</option><option value="organizations">All organizations</option></select>}
        <button onClick={() => {setFilterError(undefined);setEditing(true);}}>Filter events</button>
        {filtered && <><span role="status">Filters applied</span><button onClick={() => {setFilters(empty);setPage("");}}>Clear filters</button></>}
      </div>
      <Modal open={editing} onOpenChange={setEditing} title="Filter audit events" description="Actor, action and target match exactly. Times include a timezone; start is inclusive and end is exclusive.">
        <ActionForm fields={[
          {name:"actor",label:"Actor",defaultValue:filters.actor,schema:z.string().max(320)},
          {name:"action",label:"Action",defaultValue:filters.action,placeholder:"connection.credential_rotated",schema:z.string().max(200)},
          {name:"target",label:"Target",defaultValue:filters.target,schema:z.string().max(512)},
          {name:"occurredFrom",label:"From (RFC3339)",defaultValue:filters.occurredFrom,placeholder:"2026-09-05T00:00:00Z",schema:z.string().max(40)},
          {name:"occurredBefore",label:"Before (RFC3339)",defaultValue:filters.occurredBefore,placeholder:"2026-09-06T00:00:00Z",schema:z.string().max(40)},
        ]} error={filterError} submitLabel="Apply filters" onSubmit={async values => {
          const next = {...empty,...values};
          const timestamp = z.iso.datetime({offset:true});
          if ([next.occurredFrom,next.occurredBefore].some(v => v && !timestamp.safeParse(v).success)) {setFilterError(new Error("Use RFC3339 timestamps with a timezone, or leave times empty."));return;}
          if(next.occurredFrom && next.occurredBefore && Date.parse(next.occurredFrom)>=Date.parse(next.occurredBefore)){setFilterError(new Error("The end must be after the start."));return;}
          setFilters(next);setPage("");setEditing(false);
        }}/>
      </Modal>
      <ErrorNote error={q.error} />
      <section className="panel">
        {q.isPending ? (
          <p className="loading">Loading events…</p>
        ) : q.isError ? null : q.data?.events.length ? (
          <DataTable
            label="Events"
            data={q.data.events}
            rowId={(r) => r.id}
            columns={[
              {id:"scope",header:"Organization",cell:({row:{original:e}})=>e.organizationName|| (installation?"Installation":"Current organization")},
              {
                id: "col0",
                header: 'Event',
                cell: ({ row: { original: e } }) => (
                  <>
                    <strong>{e.action}</strong>
                    <small title={e.target}>{e.target.slice(0, 16)}</small>
                  </>
                ),
              },
              {
                id: "col1",
                header: 'Actor',
                cell: ({ row: { original: e } }) => <>{e.actor}</>,
              },
              {
                id: "col2",
                header: 'Details',
                cell: ({ row: { original: e } }) => (
                  <>
                    <code className="details">{e.details}</code>
                  </>
                ),
              },
              {
                id: "col3",
                header: 'Time',
                cell: ({ row: { original: e } }) => (
                  <>{new Date(e.occurredAt).toLocaleString()}</>
                ),
              },
            ]}
          />
        ) : (
          <Empty icon={ShieldCheck} title={filtered ? "No matching events" : "No events yet"}>
            {filtered ? "Adjust or clear your filters to see other events." : "Recorded changes will appear here."}
          </Empty>
        )}
      </section>
      <div className="pagination">
        {page && <button onClick={() => setPage("")}>Most recent</button>}
        {q.data?.nextPageToken && (
          <button onClick={() => setPage(q.data!.nextPageToken)}>
            Older events <ChevronRight size={15} />
          </button>
        )}
      </div>
    </>
  );
}
function RouteLoading() {return <div className="content" role="status">Loading Providah…</div>;}
function RouteError() {
  const error = useRouteError();
  return (
    <main className="content">
      <h1>Unable to load the console</h1>
      <ErrorNote error={error} />
      <a href="/">Try again</a>
    </main>
  );
}
function navigationAllowed(permissions:readonly string[],required:string) {
 return permissions.includes(required)||(required==="notifications.read"&&permissions.includes("notifications.manage"));
}
const legacyRoutes:Record<string,string>={connections:"/admin/connections",modules:"/admin/modules",audit:"/admin/audit","audit-export":"/admin/audit-export",access:"/admin/access",identity:"/admin/identity",notifications:"/admin/notifications",maintenance:"/admin/maintenance",templates:"/app/templates",resources:"/app/resources",operations:"/app/operations",schedules:"/app/schedules"};
function LegacyRedirect({target}:{target:string}) {const location=useLocation();return <Navigate to={target+location.search+location.hash} replace/>;}
const router = createBrowserRouter([
  { path: "/invite", errorElement:<RouteError/>, HydrateFallback:RouteLoading, lazy: async () => ({Component:(await import("./access")).InvitationPage}) },
  {
    path: "/",
    Component: Shell,
    HydrateFallback: RouteLoading,
    errorElement: <RouteError />,
    loader: async () => {
      try {
        return await api.getSession({});
      } catch (e) {
        const err = message(e);
        if (err.includes("Sign in")) return null;
        throw e;
      }
    },
    children: [
      { index: true, element:<LegacyRedirect target="/app"/> },
      { path:"app", Component:Overview },
      { path:"admin", element:null },
      ...Object.entries(legacyRoutes).map(([path,target])=>({path,element:<LegacyRedirect target={target}/>})),
      { path: "admin/connections", Component: Connections },
      { path: "app/templates", lazy: async () => ({Component:(await import("./templates")).TemplatesPage}) },
      { path: "admin/modules", lazy: async () => ({Component:(await import("./modules")).ModulesPage}) },
      { path: "app/resources", Component: ResourceHome },
      { path: "app/resources/all", Component: Inventory },
      { path: "app/resources/detail/:resourceId", Component: ResourceDetailPage },
      ...Object.keys(resourceKinds).map(kind=>({path:resourcePath(kind).slice(1),element:<Inventory resourceKind={kind}/>})),
      { path: "app/dashboards", lazy: async () => ({Component:(await import("./dashboards")).DashboardsPage}) },
      { path: "admin/audit", Component: Audit },
      { path: "admin/installation-audit", Component: Audit },
      { path: "admin/health", Component: InstallationHealth },
      { path: "admin/audit-export", lazy: async () => ({Component:(await import("./audit_export")).AuditExportPage}) },
      { path: "admin/access", lazy: async () => ({Component:(await import("./access")).AccessPage}) },
      { path: "admin/resource-policy", lazy: async () => ({Component:(await import("./resource-policy")).ResourcePolicyPage}) },
      { path: "admin/identity", lazy: async () => ({Component:(await import("./identity-policy")).IdentityPolicyPage}) },
      { path: "app/operations", lazy: async () => ({Component:(await import("./operations-page")).OperationsPage}) },
      { path: "app/schedules", lazy: async () => ({Component:(await import("./schedules")).SchedulesPage}) },
      { path: "admin/notifications", lazy: async () => ({Component:(await import("./notifications")).NotificationsPage}) },
      { path: "admin/maintenance", lazy: async () => ({Component:(await import("./maintenance")).MaintenancePage}) },
    ],
  },
]);
createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <QueryClientProvider client={queries}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </React.StrictMode>,
);
