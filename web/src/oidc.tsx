import { useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { z } from "zod";
import { api, queries } from "./api";
import { ActionForm, ErrorNote, Modal } from "./ui";

export function OIDCLogin() {
  const status=useQuery({queryKey:["oidc"],queryFn:()=>api.getOIDCStatus({})});
  const start=useMutation({mutationFn:()=>api.beginOIDCLogin({returnTo:window.location.pathname+window.location.search+window.location.hash}),onSuccess:r=>window.location.assign(r.url)});
  const complete=useMutation({gcTime:0,mutationFn:(v:Record<string,string>)=>api.completeOIDCLogin({code:v.code??""}),onSuccess:r=>{queries.clear();window.location.assign(r.returnTo||"/");}});
  const state=new URLSearchParams(window.location.search).get("oidc");
  if(status.isPending)return state==="verify"?<p>Checking sign-in…</p>:null;
  if (!status.data?.enabled) return state === "verify" ? <p className="notice">Organization sign-in is unavailable. <a href="/">Use local sign-in</a>.</p> : null;
  return <>
    {state==="failed" && <p className="notice">Organization sign-in could not be completed. Use a previously linked identity, or sign in locally to link it.</p>}
    {state==="verify" && status.isPending ? <p>Checking sign-in…</p> : state==="verify" && status.data.loginVerified ? <><p className="notice">{status.data.loginMfaEnabled?"Identity verified. Enter your Providah authenticator code to finish signing in.":"Identity verified. Continue to finish signing in."}</p><ActionForm fields={status.data.loginMfaEnabled?[{name:"code",label:"Providah authenticator code",autoComplete:"one-time-code",schema:z.string().regex(/^\d{6}$/)}]:[]} onSubmit={v=>complete.mutateAsync(v)} submitLabel="Complete organization sign-in" pending={complete.isPending} error={complete.error}/><button className="text-button" onClick={()=>window.location.assign(status.data.loginReturnTo||"/")}>Use local sign-in instead</button></> : <>{state==="verify"&&<p className="notice">This sign-in has expired or is unavailable. Start again.</p>}<button className="secondary full" disabled={start.isPending} onClick={()=>start.mutate()}>Sign in with your organization</button></>}

    <ErrorNote error={start.error}/>
  </>;
}
export function OIDCAccountButton() {
  const [open,setOpen]=useState(false);
  const status=useQuery({queryKey:["oidc"],queryFn:()=>api.getOIDCStatus({})});
  const change=useMutation({gcTime:0,mutationFn:async(v:Record<string,string>)=> {if(status.data?.linked){await api.unlinkOIDC({password:v.password,code:v.code});return "/";}return (await api.beginOIDCLink({password:v.password,code:v.code})).url;},onSuccess:url=>{change.reset();queries.clear();window.location.assign(url);}});
  if (!status.data?.enabled) return null;
  return <><button className="text-button" onClick={()=>{change.reset();setOpen(true);}}>Organization sign-in</button><Modal open={open} onOpenChange={value=>{setOpen(value);if(!value)change.reset();}} title="Organization sign-in" description="Manage the external identity linked to your existing account.">
    <p className="notice">{status.data.issuer} · {status.data.linked ? "Linked. Unlinking signs out every session." : "Not linked. Confirm your local account, then authenticate with the identity provider."} Your existing organization memberships and permissions stay in effect. Your account’s MFA settings still apply.</p>
    {open && <ActionForm fields={[{name:"password",label:"Current password",type:"password",autoComplete:"current-password",schema:z.string().min(12).max(72)},{name:"code",label:"Fresh authenticator code",autoComplete:"one-time-code",schema:z.string().regex(/^(|\d{6})$/),description:"Leave blank when MFA is disabled."}]} onSubmit={v=>change.mutateAsync(v)} submitLabel={status.data.linked ? "Unlink and sign out" : "Verify and link identity"} pending={change.isPending} error={change.error}/>}<ErrorNote error={status.error}/>
  </Modal></>;
}

export function SSORequired() {return <section className="panel"><h2>Organization sign-in required</h2><p className="muted">This organization requires its linked identity provider. Link your identity through your account’s Organization sign-in settings if needed, then sign in below. Your local session can still access other organizations where permitted.</p><OIDCLogin/></section>;}
