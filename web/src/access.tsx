import {Teams} from "./teams";
import {MfaButton} from "./recovery";
import { useEffect, useState } from "react";
import { useOutletContext } from "react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Plus, Users } from "lucide-react";
import { z } from "zod";
import { api, queries } from "./api";
import {
  ActionForm,
  DataTable,
  ErrorNote,
  Field,
  Modal,
  PageHeader,
} from "./ui";
import type { Organization, Member, Role } from "./gen/providah/v1/console_pb";

export function AccessPage() {
  const { org } = useOutletContext<{ org: Organization }>();
  const [invite, setInvite] = useState(false),
    [link, setLink] = useState(""),
    [member, setMember] = useState<Member | null>(null),
    [role, setRole] = useState<Role | null | undefined>(undefined);
  const q = useQuery({
    queryKey: ["access", org.id],
    queryFn: () => api.listAccess({ organizationId: org.id }),
  });
  const canManage = org.permissions.includes("members.manage"),
    canRoles = org.permissions.includes("roles.manage");
  const refresh = () => queries.invalidateQueries();
  const saveRole = useMutation({
    mutationFn: (v: Record<string, string>) =>
      api.saveRole({
        organizationId: org.id,
        id: role?.id ?? "",
        expectedRevision: role?.revision ?? 0n,
        name: v.name,
        permissions: v.permissions.split(",").filter(Boolean),
      }),
    onError: () => { void refresh(); },
    onSuccess: () => {
      setRole(undefined);
      void refresh();
    },
  });
  const update = useMutation({
    mutationFn: (v: Record<string, string>) =>
      api.updateMember({
        organizationId: org.id,
        userId: member!.userId,
        expectedRevision: member!.revision,
        roleId: v.role,
        active: v.active === "true",
      }),
    onError: () => { void refresh(); },
    onSuccess: () => {
      setMember(null);
      void refresh();
    },
  });
  const create = useMutation({
    mutationFn: (v: Record<string, string>) =>
      api.createInvitation({
        organizationId: org.id,
        email: v.email,
        roleId: v.role,
      }),
    onSuccess: (r) => {
      setLink(r.link);
      void refresh();
    },
  });
  const revoke = useMutation({
    mutationFn: (id: string) =>
      api.revokeInvitation({ organizationId: org.id, id }),
    onSuccess: refresh,
  });
  const roleOptions =
    q.data?.roles
      .filter((r) => r.permissions.every((p) => org.permissions.includes(p)))
      .map((r) => ({ value: r.id, label: r.name })) ?? [];
  return (
    <>
      <PageHeader
        eyebrow="PEOPLE & PERMISSIONS"
        title="Team & access"
        description="Give people the access they need without sharing cloud credentials."
      >
        {canManage && (
          <button
            className="primary"
            onClick={() => {
              setLink("");
              setInvite(true);
            }}
          >
            <Plus size={16} />
            Invite member
          </button>
        )}
      </PageHeader>
      <ErrorNote error={q.error || revoke.error} />
      {q.data&&!q.isError&&<Teams org={org} roles={q.data.roles} members={q.data.members}/>}
      <section className="panel">
        <div className="panel-heading">
          <h2>
            <Users size={17} /> Members
          </h2>
          <span className="muted">Organization access</span>
        </div>
        {q.isPending ? (
          <p className="loading">Loading team…</p>
        ) : (
          q.data && (
            <DataTable
              label="Members"
              rowId={(r) => r.userId}
              data={q.data.members}
              columns={[
                { accessorKey: "email", header: "Person" },
                { accessorKey: "roleName", header: "Role" },
                {
                  id: "status",
                  header: "Status",
                  cell: ({ row }) =>
                    row.original.active ? "Active" : "Disabled",
                },
                {
                  id: "actions",
                  header: "Actions",
                  cell: ({ row }) =>
                    canManage ? (
                      <button onClick={() => setMember(row.original)}>
                        Edit access
                      </button>
                    ) : null,
                },
              ]}
            />
          )
        )}
      </section>
      <section className="panel">
        <div className="panel-heading">
          <h2>Roles</h2>
          {canRoles && (
            <button onClick={() => setRole(null)}>
              <Plus size={14} />
              Create role
            </button>
          )}
        </div>
        {q.data && (
          <DataTable
            label="Roles"
            rowId={(r) => r.id}
            data={q.data.roles}
            columns={[
              { accessorKey: "name", header: "Role" },
              {
                id: "permissions",
                header: "Permissions",
                cell: ({ row }) => (
                  <div className="permission-tags">
                    {row.original.permissions.map((p) => (
                      <span className="badge" key={p}>
                        {p}
                      </span>
                    ))}
                  </div>
                ),
              },
              {
                id: "edit",
                header: "Actions",
                cell: ({ row }) =>
                  row.original.builtin ? (
                    <span className="muted">Built-in</span>
                  ) : canRoles ? (
                    <button onClick={() => setRole(row.original)}>
                      Edit role
                    </button>
                  ) : null,
              },
            ]}
          />
        )}
      </section>
      <section className="panel">
        <div className="panel-heading">
          <h2>Pending invitations</h2>
          <span className="muted">Expire after 48 hours</span>
        </div>
        {q.data?.invitations.length ? (
          <DataTable
            label="Invitations"
            rowId={(r) => r.id}
            data={q.data.invitations}
            columns={[
              { accessorKey: "email", header: "Email" },
              { accessorKey: "roleName", header: "Role" },
              {
                id: "expires",
                header: "Expires",
                cell: ({ row }) =>
                  new Date(row.original.expiresAt).toLocaleString(),
              },
              {
                id: "revoke",
                header: "Actions",
                cell: ({ row }) =>
                  canManage ? (
                    <button
                      disabled={revoke.isPending}
                      onClick={() => revoke.mutate(row.original.id)}
                    >
                      Revoke invitation
                    </button>
                  ) : null,
              },
            ]}
          />
        ) : (
          <p className="loading">No pending invitations.</p>
        )}
      </section>
      <Modal
        open={invite}
        onOpenChange={setInvite}
        title="Invite a team member"
        description="Invitations are single-use and expire after 48 hours. Share the link privately with the intended person."
      >
        {link ? (
          <>
            <div className="notice">
              Invitation created. Copy this link now; it cannot be shown again.
            </div>
            <Field label="Invitation link">
              <input value={link} readOnly onFocus={(e) => e.target.select()} />
            </Field>
            <button className="primary full" onClick={() => setInvite(false)}>
              Done
            </button>
          </>
        ) : (
          <ActionForm
            fields={[
              {
                name: "email",
                label: "Email address",
                type: "email",
                schema: z.email(),
              },
              {
                name: "role",
                label: "Role",
                type: "select",
                defaultValue: "viewer",
                options: roleOptions,
              },
            ]}
            onSubmit={(v) => create.mutateAsync(v)}
            submitLabel="Create invitation"
            error={create.error}
            pending={create.isPending}
          />
        )}
      </Modal>
      <Modal
        open={!!member}
        onOpenChange={(v) => {
          if (!v) setMember(null);
        }}
        title="Edit member access"
        description={member?.email ?? ""}
      >
        {member && (
          <ActionForm
            key={member.userId}
            fields={[
              {
                name: "role",
                label: "Role",
                type: "select",
                defaultValue: member.roleId,
                options: roleOptions,
              },
              {
                name: "active",
                label: "Membership status",
                type: "select",
                defaultValue: String(member.active),
                options: [
                  { value: "true", label: "Active" },
                  { value: "false", label: "Disabled" },
                ],
              },
            ]}
            onSubmit={(v) => update.mutateAsync(v)}
            submitLabel="Save access"
            pending={update.isPending}
            error={update.error}
          />
        )}
      </Modal>
      <Modal
        open={role !== undefined}
        onOpenChange={(v) => {
          if (!v) setRole(undefined);
        }}
        title={role ? "Edit role" : "Create role"}
        description="Choose explicit permissions. You cannot grant permissions you do not hold."
      >
        {role !== undefined && (
          <ActionForm
            key={role?.id ?? "new"}
            fields={[
              {
                name: "name",
                label: "Role name",
                defaultValue: role?.name,
                schema: z.string().min(1).max(80),
              },
              {
                name: "permissions",
                label: "Permissions",
                type: "checks",
                defaultValue: role?.permissions.join(","),
                options: q.data?.permissionCatalog
                  .filter((p) => org.permissions.includes(p))
                  .map((value) => ({ value, label: value })),
              },
            ]}
            onSubmit={(v) => saveRole.mutateAsync(v)}
            submitLabel="Save role"
            pending={saveRole.isPending}
            error={saveRole.error}
          />
        )}
      </Modal>
    </>
  );
}
export function InvitationPage() {
  const [token] = useState(() => window.location.hash.slice(1));
  useEffect(() => {
    window.history.replaceState(null, "", window.location.pathname);
  }, []);
  const enrollment = useQuery({
    queryKey: ["invitation", token],
    queryFn: () => api.beginInvitation({ token }),
    enabled: !!token,
    staleTime: Infinity,
    refetchOnWindowFocus: false,
  });
  const accept = useMutation({
    mutationFn: (v: Record<string, string>) =>
      api.acceptInvitation({
        token,
        password: v.password ?? "",
        code: v.code ?? "",
      }),
    onSuccess: () => {
      queries.clear();
      window.location.assign("/");
    },
  });
  return (
    <main className="invite-page">
      <section className="panel invite-card">
        <div className="eyebrow">WELCOME TO PROVIDAH</div>
        <h1>Join your team</h1>
        <ErrorNote error={enrollment.error} />
        {!token ? (
          <p>Open the complete invitation link from your administrator.</p>
        ) : enrollment.isPending ? (
          <p>Checking invitation…</p>
        ) : (
          enrollment.data && (
            <>
              <p>
                Invitation for <strong>{enrollment.data.email}</strong>
              </p>
              {enrollment.data.requiresSignIn ? (
                <>
                  <p>
                    Sign in with this email in another tab, then return here to
                    accept.
                  </p>
                  <a
                    className="primary"
                    href="/"
                    target="_blank"
                    rel="noopener"
                  >
                    Open sign in
                  </a>
                  <MfaButton />
                  <ErrorNote error={accept.error} />
                  <button
                    className="primary full"
                    disabled={accept.isPending}
                    onClick={() => accept.mutate({})}
                  >
                    Accept invitation
                  </button>
                </>
              ) : (
                <>
                  <div className="enrollment">
                    <strong>Add to your authenticator</strong>
                    <p>Save this key in your authenticator app.</p>
                    <code>{enrollment.data.secret}</code>
                  </div>
                  <ActionForm
                    fields={[
                      {
                        name: "password",
                        label: "Password",
                        type: "password",
                        autoComplete: "new-password",
                        schema: z.string().min(12).max(72),
                      },
                      {
                        name: "code",
                        label: "Authenticator code",
                        autoComplete: "one-time-code",
                        schema: z.string().regex(/^\d{6}$/),
                      },
                    ]}
                    onSubmit={(v) => accept.mutateAsync(v)}
                    submitLabel="Join organization"
                    pending={accept.isPending}
                    error={accept.error}
                  />
                </>
              )}
            </>
          )
        )}
      </section>
    </main>
  );
}
