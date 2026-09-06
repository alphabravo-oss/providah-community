import {KeyRound} from "lucide-react";
import { OIDCAccountButton } from "./oidc";
import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { z } from "zod";
import { api, queries } from "./api";
import { ActionForm, DataTable, ErrorNote, Modal } from "./ui";

function RecoverySettings() {
  const [codes, setCodes] = useState<string[]>([]);
  const status = useQuery({ queryKey: ["account-security"], queryFn: () => api.getAccountSecurity({}) });
  const generate = useMutation({
    gcTime: 0,
    mutationFn: async (v: Record<string, string>) => {
      const result = await api.generateRecoveryCodes({ password: v.password, code: v.code });
      // Plaintext codes stay only in this mounted dialog, never in Query data.
      setCodes(result.codes);
    },
    onSuccess: () => { generate.reset(); void queries.invalidateQueries({ queryKey: ["account-security"] }); },
  });
  return <>
    <ErrorNote error={status.error} />
    {codes.length ? <>
      <p className="notice">Save these ten codes in your password manager or offline. Each works once with your password. This is the only time they will be shown; closing this dialog clears them.</p>
      <div className="enrollment"><pre aria-label="New recovery codes">{codes.join("\n")}</pre></div>
      <button className="secondary" onClick={() => setCodes([])}>I saved my recovery codes</button>
    </> : <>
      <p className="notice">{status.isPending ? "Checking recovery codes…" : `${status.data?.remainingCodes ?? 0} unused recovery codes.`} Generating a new set immediately invalidates every previous code. Recovery login revokes older sessions; sensitive actions still require authenticator verification.</p>
      <ActionForm fields={[
        { name: "password", label: "Current password", type: "password", autoComplete: "current-password", schema: z.string().min(12).max(72) },
        { name: "code", label: "Fresh authenticator code", autoComplete: "one-time-code", schema: z.string().regex(/^(|\d{6})$/, "Enter a six-digit code; leave blank if MFA is disabled.") },
      ]} onSubmit={(v) => generate.mutateAsync(v)} submitLabel="Generate replacement codes" pending={generate.isPending} error={generate.error} />
    </>}
    <p className="muted">Keep your authenticator enrolled. Recovery codes do not reset it or bypass independent approvals. Lost-authenticator replacement and offline administrator recovery are not yet available.</p>
    <DataTable label="Account security history" data={status.data?.events ?? []} rowId={(e) => e.id} columns={[
      { id: "action", header: "Event", cell: ({ row }) => ({ "account.recovery_code_used": "Recovery code used", "account.recovery_codes_replaced": "Recovery codes replaced", "account.password_changed": "Password changed", "account.oidc_linked":"Organization identity linked", "account.oidc_unlinked":"Organization identity unlinked", "account.oidc_signed_in":"Organization sign-in", "account.session_revoked":"Session revoked", "account.mfa_enabled":"MFA enabled", "account.mfa_disabled":"MFA disabled", "account.global_admin_seeded":"Global administrator seeded" }[row.original.action] ?? row.original.action) },
      { accessorKey: "occurredAt", header: "Time" },
    ]} />
  </>;
}

export function RecoveryButton() {
  const session=useQuery({queryKey:["session"],queryFn:()=>api.getSession({})});
  const [mfaOpen,setMfaOpen]=useState(false);
  const [mfaSeed,setMfaSeed]=useState("");
  const enrollment=useMutation({gcTime:0,mutationFn:async(v:Record<string,string>)=>{const r=await api.beginMFAEnrollment({password:v.password});setMfaSeed(r.secret);}});

  const mfa=useMutation({gcTime:0,mutationFn:(v:Record<string,string>)=>api.setAccountMFA({enabled:!session.data?.mfaEnabled,password:v.password,code:v.code}),onSuccess:()=>{queries.clear();window.location.assign("/");}});
  const [open, setOpen] = useState(false);
  const [passwordOpen, setPasswordOpen] = useState(false);
  const [sessionsOpen,setSessionsOpen]=useState(false);
  return <>
    <OIDCAccountButton/>
    <button className="text-button" onClick={()=>{setMfaSeed("");enrollment.reset();mfa.reset();setMfaOpen(true);}}>{session.data?.mfaEnabled ? "Disable MFA" : "Enable MFA"}</button>
    <Modal open={mfaOpen} onOpenChange={open=>{setMfaOpen(open);if(!open)setMfaSeed("");}} title={session.data?.mfaEnabled ? "Disable MFA" : "Enable MFA"} description="Confirm your account password. Enabling MFA enrolls your authenticator; changing the setting signs out all sessions.">
      {mfaOpen && !session.data?.mfaEnabled && !mfaSeed ? <ActionForm fields={[{name:"password",label:"Current password",type:"password",schema:z.string().min(12).max(72)}]} onSubmit={v=>enrollment.mutateAsync(v)} pending={enrollment.isPending} error={enrollment.error} submitLabel="Set up authenticator"/> : mfaOpen && <>
        {mfaSeed && <div className="enrollment"><strong>Add this key to your authenticator</strong><code>{mfaSeed}</code><p>Enter its current code below to enable MFA.</p></div>}
        <ActionForm key={mfaSeed || "disable"} fields={[{name:"password",label:"Current password",type:"password",schema:z.string().min(12).max(72)},{name:"code",label:"Authenticator code",schema:z.string().regex(/^\d{6}$/)}]} onSubmit={v=>mfa.mutateAsync(v)} pending={mfa.isPending} error={mfa.error} submitLabel="Save MFA setting and sign out"/>
      </>}
    </Modal>
    <button className="text-button" onClick={()=>setSessionsOpen(true)}>Active sessions</button>
    <Modal open={sessionsOpen} onOpenChange={setSessionsOpen} title="Active sign-in sessions" description="Inspect and revoke your own sessions.">{sessionsOpen && <AccountSessions/>}</Modal>
    <button className="text-button" onClick={() => setOpen(true)}>Recovery codes</button>
    <button className="text-button" onClick={() => setPasswordOpen(true)}>Change password</button>
    <Modal open={passwordOpen} onOpenChange={setPasswordOpen} title="Change password" description="Your current password and a fresh authenticator code are required. Every session, including this one, will be signed out.">{passwordOpen && <PasswordSettings />}</Modal>
    <Modal open={open} onOpenChange={setOpen} title="Account recovery codes" description="Manage your own single-use backup sign-in codes.">{open && <RecoverySettings />}</Modal>
  </>;
}

function PasswordSettings() {
  const change = useMutation({
    gcTime: 0,
    mutationFn: (v: Record<string, string>) => api.changePassword({ password: v.password, code: v.code, newPassword: v.newPassword }),
    onSuccess: () => { change.reset(); queries.clear(); window.location.assign("/"); },
  });
  return <ActionForm fields={[
    { name: "password", label: "Current password", type: "password", autoComplete: "current-password", schema: z.string().min(12).max(72) },
    { name: "newPassword", label: "New password", type: "password", autoComplete: "new-password", schema: z.string().min(12).max(72), description: "Use a different password, 12–72 bytes long." },
    { name: "code", label: "Fresh authenticator code", autoComplete: "one-time-code", schema: z.string().regex(/^(|\d{6})$/, "Enter a six-digit code; leave blank if MFA is disabled.") },
  ]} onSubmit={v => change.mutateAsync(v)} submitLabel="Change password and sign out" pending={change.isPending} error={change.error} />;
}

export function MfaButton() {
  const [open, setOpen] = useState(false);
  const verify = useMutation({
    mutationFn: (code: string) => api.verifyMfa({ code }),
    onSuccess: () => setOpen(false),
  });
  return (
    <>
      <button className="text-button" onClick={() => setOpen(true)}>
        <KeyRound size={13} />
        Verify MFA
      </button>
      <Modal
        open={open}
        onOpenChange={setOpen}
        title="Verify your identity"
        description="Use a new authenticator code to authorize sensitive actions for five minutes."
      >
        <ActionForm
          fields={[
            {
              name: "code",
              label: "Authenticator code",
              autoComplete: "one-time-code",
              schema: z.string().regex(/^\d{6}$/, "Enter a six-digit code."),
            },
          ]}
          onSubmit={(v) => verify.mutateAsync(v.code)}
          submitLabel="Verify MFA"
          pending={verify.isPending}
          error={verify.error}
        />
      </Modal>
    </>
  );
}

function AccountSessions() {
  const [page,setPage]=useState("");
  const [selected,setSelected]=useState<{id:string;current:boolean}|null>(null);
  const sessions=useQuery({queryKey:["account-sessions",page],queryFn:()=>api.listAccountSessions({pageToken:page})});
  const revoke=useMutation({gcTime:0,mutationFn:(v:Record<string,string>)=>api.revokeAccountSession({id:selected!.id,password:v.password,code:v.code}),onSuccess:result=>{
    revoke.reset();setSelected(null);
    if(result.signedOut){queries.clear();window.location.assign("/");return;}
    void queries.invalidateQueries({queryKey:["account-sessions"]});void queries.invalidateQueries({queryKey:["account-security"]});
  }});
  return <>
    <p className="notice">These are your active sign-in sessions across the installation. Revoking one blocks its next authenticated request. Already-submitted cloud work is not canceled.</p>
    <ErrorNote error={sessions.error}/>
    {sessions.isPending ? <p role="status">Loading sessions…</p> : <DataTable label="Active sessions" data={sessions.data?.sessions??[]} rowId={s=>s.id} columns={[
      {id:"session",header:"Session",cell:({row})=><><strong>{row.original.current?"This session":"Other session"}</strong><small>{row.original.id.slice(0,12)}</small></>},
      {id:"identity",header:"Sign-in",cell:({row})=>row.original.oidc?"Organization identity":"Local account"},
      {id:"verified",header:"MFA verified",cell:({row})=>row.original.mfaAt?new Date(row.original.mfaAt).toLocaleString():"Not verified"},
      {id:"expires",header:"Expires",cell:({row})=>new Date(row.original.expiresAt).toLocaleString()},
      {id:"actions",header:"Actions",cell:({row})=><button onClick={()=>{revoke.reset();setSelected(row.original);}}>{row.original.current?"Sign out this session":`Revoke session ${row.original.id.slice(0,12)}`}</button>},
    ]}/>}
    <div className="pagination">{page && <button onClick={()=>setPage("")}>First sessions</button>}{sessions.data?.nextPageToken && <button onClick={()=>setPage(sessions.data!.nextPageToken)}>More sessions</button>}</div>
    <p className="muted">Session references distinguish sign-ins. Device names, network addresses and session creation times are not recorded.</p>
    <Modal open={!!selected} onOpenChange={open=>{if(!open)setSelected(null);}} title="Revoke sign-in session" description={selected?.current?"This signs you out here. Your password and a new authenticator code are required.":"Only the selected session will be signed out. Your password and a new authenticator code are required."}>
      {selected && <><p>Session reference: <code>{selected.id.slice(0,12)}</code></p><ActionForm key={selected.id} fields={[
        {name:"password",label:"Current password",type:"password",autoComplete:"current-password",schema:z.string().min(12).max(72)},
        {name:"code",label:"Fresh authenticator code",autoComplete:"one-time-code",schema:z.string().regex(/^(|\d{6})$/,"Enter a six-digit code; leave blank if MFA is disabled.")},
      ]} onSubmit={v=>revoke.mutateAsync(v)} submitLabel={selected.current?"Confirm sign out":"Revoke selected session"} pending={revoke.isPending} error={revoke.error}/></>}
    </Modal>
  </>;
}
