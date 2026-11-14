"use client";
// Admin > Users: every account, add one, set roles, reset a password, disable or enable. The root admin's row is
// locked: its roles, password and state come from ADMIN_EMAIL / ADMIN_PASSWORD in auth_service's .env.
import { useCallback, useEffect, useState } from "react";
import { ErrorNote, Info, Modal } from "@/components/kit";
import { useUser } from "@/components/shell";
import { api, message, ok, type S } from "@/lib/api";

type User = S<"User">;
type Role = S<"GrantableRole">;

/** The roles the admin can give, with what each adds. */
const ROLES: { id: Role; label: string }[] = [
  { id: "user", label: "User · create, gallery, pipelines, timeline" },
  { id: "dev", label: "Dev · also sees jobs and logs" },
];

/** A random 20-character password (URL-safe base64 of 15 random bytes). */
function randomPassword(): string {
  const b = crypto.getRandomValues(new Uint8Array(15));
  return btoa(String.fromCharCode(...b))
    .replace(/\+/g, "-")
    .replace(/\//g, "_");
}

/** The path parameter of a user route. */
const uid = (id: string) => ({ params: { path: { id } } });

/** The Admin screen with its Users table and the Add a user panel. */
export function UsersAdmin() {
  const me = useUser();
  const [users, setUsers] = useState<User[] | null>(null);
  const [error, setError] = useState("");
  const load = useCallback(() => {
    ok(api.GET("/auth/admin/users"))
      .then((r) => {
        setUsers(r.users);
        setError("");
      })
      .catch((e) => setError(message(e)));
  }, []);
  useEffect(load, [load]);
  const replace = (u: User) =>
    setUsers((list) => list?.map((x) => (x.id === u.id ? u : x)) ?? null);

  if (me && !me.roles.includes("admin"))
    return (
      <main id="main" className="flex flex-1 items-center justify-center p-6 text-t2">
        Admins only.
      </main>
    );
  return (
    <>
      <main id="main" className="flex min-w-0 flex-[999_1_560px] flex-col gap-4 px-5 pt-4 pb-6">
        <div className="flex flex-wrap items-center gap-3">
          <h1 className="font-display text-[22px] font-bold tracking-[0.01em]">Admin</h1>
          <div className="grow" />
          <span className="tag tag-accent">Signed in as root admin</span>
        </div>
        {/* the mock's Hosts and API keys tabs are left out until their backends exist */}
        <div role="tablist" aria-label="Admin sections" className="flex flex-wrap gap-2">
          <button
            type="button"
            role="tab"
            aria-selected="true"
            className="chip bg-t1 font-semibold text-panel"
          >
            Users
          </button>
        </div>
        {error && <ErrorNote onRetry={load}>{error}</ErrorNote>}
        <div className="panel min-h-0 overflow-auto px-2 py-1.5" aria-busy={!users && !error}>
          <table className="w-full min-w-[760px] border-collapse text-[13px]">
            <thead>
              <tr className="text-left text-[11px] tracking-[0.04em] text-t2">
                {["Name", "Email", "Roles", "Status", "Added", "Actions"].map((h) => (
                  <th
                    key={h}
                    scope="col"
                    className="sticky -top-1.5 z-[1] border-b border-s3 bg-s1 p-2.5 font-semibold"
                  >
                    {h}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {/* the root admin first, as in the mock; the rest keep the API's order */}
              {users
                ?.toSorted((a, b) => +b.roles.includes("admin") - +a.roles.includes("admin"))
                .map((u) => (
                  <UserRow key={u.id} u={u} onChange={replace} onError={setError} />
                ))}
              {users?.length === 0 && (
                <tr>
                  <td colSpan={6} className="p-2.5 text-t2">
                    No accounts.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </main>
      <AddUser onAdded={(u) => setUsers((list) => [u, ...(list ?? [])])} />
    </>
  );
}

/** One account: the root admin's row is read-only. */
function UserRow({
  u,
  onChange,
  onError,
}: {
  u: User;
  onChange: (u: User) => void;
  onError: (e: string) => void;
}) {
  const [dialog, setDialog] = useState<"roles" | "password" | null>(null);
  const root = u.roles.includes("admin");
  const cell = "border-b border-s3 p-2.5 whitespace-nowrap";
  async function toggle() {
    try {
      onChange(
        await ok(
          api.PUT("/auth/admin/users/{id}/disabled", {
            ...uid(u.id),
            body: { disabled: !u.disabled },
          }),
        ),
      );
    } catch (e) {
      onError(message(e));
    }
  }
  return (
    <tr className="[&:last-child>td]:border-b-0">
      <td className={`${cell} font-semibold`}>{u.name}</td>
      <td className={`${cell} mono max-w-[220px] truncate text-[12px] text-t2`} title={u.email}>
        {u.email}
      </td>
      <td className={cell}>
        <span className="flex gap-1 leading-[13px]">
          {u.roles.map((r) => (
            <span key={r} className={`tag ${r === "admin" ? "tag-accent" : ""}`}>
              {r}
            </span>
          ))}
        </span>
      </td>
      <td className={`${cell} ${u.disabled ? "text-t2" : ""}`}>
        {u.disabled ? "Disabled" : "Active"}
      </td>
      <td className={`${cell} mono text-[12px] text-t2`}>
        {/* the mock's "Last sign-in" column: the API has only created_at, so this is the date added */}
        {new Date(u.created_at).toLocaleDateString(undefined, { dateStyle: "medium" })}
      </td>
      <td className={cell}>
        {root ? (
          <span className="text-[12px] text-t2">Locked · set in the server&apos;s .env</span>
        ) : (
          <span className="flex gap-1.5">
            <button type="button" className="chip h-[30px]" onClick={() => setDialog("roles")}>
              Roles
            </button>
            <button type="button" className="chip h-[30px]" onClick={() => setDialog("password")}>
              Reset password
            </button>
            <button type="button" className="chip h-[30px]" onClick={() => void toggle()}>
              {u.disabled ? "Enable" : "Disable"}
            </button>
          </span>
        )}
        <Modal
          open={dialog === "roles"}
          onClose={() => setDialog(null)}
          title={`Roles of ${u.name}`}
        >
          <RolesForm
            u={u}
            onDone={(x) => (onChange(x), setDialog(null))}
            onCancel={() => setDialog(null)}
          />
        </Modal>
        <Modal
          open={dialog === "password"}
          onClose={() => setDialog(null)}
          title={`New password for ${u.name}`}
        >
          <PasswordForm u={u} onDone={() => setDialog(null)} />
        </Modal>
      </td>
    </tr>
  );
}

/** Role checkboxes; at least one must stay checked. */
function RoleBoxes({ value, onChange }: { value: Role[]; onChange: (r: Role[]) => void }) {
  return (
    <fieldset className="m-0 flex flex-col gap-1.5 border-0 p-0">
      <legend className="label pb-1.5">Roles</legend>
      {ROLES.map((r) => (
        <label key={r.id} className="flex min-h-8 items-center gap-2.5 text-[13px]">
          <input
            type="checkbox"
            className="accent-[var(--a1)]"
            checked={value.includes(r.id)}
            onChange={(e) =>
              onChange(e.target.checked ? [...value, r.id] : value.filter((x) => x !== r.id))
            }
          />
          {r.label}
        </label>
      ))}
    </fieldset>
  );
}

/** Changes an account's roles (its sessions end, so new tokens carry them). */
function RolesForm({
  u,
  onDone,
  onCancel,
}: {
  u: User;
  onDone: (u: User) => void;
  onCancel: () => void;
}) {
  const [roles, setRoles] = useState<Role[]>(u.roles.filter((r): r is Role => r !== "admin"));
  const [error, setError] = useState("");
  async function save(e: React.FormEvent) {
    e.preventDefault();
    try {
      onDone(await ok(api.PUT("/auth/admin/users/{id}/roles", { ...uid(u.id), body: { roles } })));
    } catch (err) {
      setError(message(err));
    }
  }
  return (
    <form onSubmit={save} className="flex flex-col gap-4">
      <RoleBoxes value={roles} onChange={setRoles} />
      {error && <ErrorNote>{error}</ErrorNote>}
      <div className="flex justify-end gap-2">
        <button type="button" className="chip" onClick={onCancel}>
          Cancel
        </button>
        <button type="submit" className="key h-10 px-5 text-[13px]" disabled={!roles.length}>
          Save
        </button>
      </div>
    </form>
  );
}

/** A password field with a Generate button, shown as text so the admin can pass it on. */
function PasswordField({
  id,
  value,
  onChange,
}: {
  id: string;
  value: string;
  onChange: (v: string) => void;
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <label htmlFor={id} className="label">
        Temporary password
      </label>
      <div className="flex gap-2">
        <input
          id={id}
          className="in"
          type="text"
          autoComplete="off"
          spellCheck={false}
          required
          minLength={10}
          maxLength={256}
          placeholder="At least 10 characters"
          value={value}
          onChange={(e) => onChange(e.target.value)}
        />
        <button
          type="button"
          className="chip h-[38px] shrink-0"
          onClick={() => onChange(randomPassword())}
        >
          Generate
        </button>
      </div>
    </div>
  );
}

/** Sets a new password (all the account's sessions end). */
function PasswordForm({ u, onDone }: { u: User; onDone: () => void }) {
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  async function save(e: React.FormEvent) {
    e.preventDefault();
    try {
      await ok(api.PUT("/auth/admin/users/{id}/password", { ...uid(u.id), body: { password } }));
      onDone();
    } catch (err) {
      setError(message(err));
    }
  }
  return (
    <form onSubmit={save} className="flex flex-col gap-4">
      <PasswordField id={`pw-${u.id}`} value={password} onChange={setPassword} />
      {error && <ErrorNote>{error}</ErrorNote>}
      <div className="flex justify-end gap-2">
        <button type="button" className="chip" onClick={onDone}>
          Cancel
        </button>
        <button type="submit" className="key h-10 px-5 text-[13px]">
          Set password
        </button>
      </div>
    </form>
  );
}

/** The Add a user panel. The name defaults to the part of the email before @ (auth_service decides). */
function AddUser({ onAdded }: { onAdded: (u: User) => void }) {
  const [form, setForm] = useState({ name: "", email: "", password: "" });
  const [roles, setRoles] = useState<Role[]>(["user"]);
  const [error, setError] = useState("");
  const [added, setAdded] = useState("");
  const [busy, setBusy] = useState(false);
  const set = (k: keyof typeof form) => (v: string) => setForm((f) => ({ ...f, [k]: v }));
  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    setAdded("");
    try {
      const u = await ok(api.POST("/auth/admin/users", { body: { ...form, roles } }));
      onAdded(u);
      setAdded(u.email);
      setForm({ name: "", email: "", password: "" });
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }
  return (
    <aside
      aria-label="Add a user"
      className="aside m-3.5 flex flex-[1_1_340px] flex-col px-[18px] pt-4 pb-6 md:ml-0"
    >
      <form onSubmit={submit} className="flex flex-col gap-3.5">
        <h2 className="flex items-center font-display text-base leading-5 font-bold">
          Add a user
          <Info>
            There is no public sign-up. Only the root admin creates accounts. The admin role belongs
            to the root account alone and cannot be granted here.
          </Info>
        </h2>
        <div className="flex flex-col gap-1.5">
          <label htmlFor="u-name" className="label">
            Name
          </label>
          <input
            id="u-name"
            className="in"
            maxLength={200}
            autoComplete="off"
            placeholder="Full name"
            value={form.name}
            onChange={(e) => set("name")(e.target.value)}
          />
        </div>
        <div className="flex flex-col gap-1.5">
          <label htmlFor="u-email" className="label">
            Email
          </label>
          <input
            id="u-email"
            className="in"
            type="email"
            required
            maxLength={254}
            autoComplete="off"
            placeholder="name@company.com"
            value={form.email}
            onChange={(e) => set("email")(e.target.value)}
          />
        </div>
        <PasswordField id="u-pass" value={form.password} onChange={set("password")} />
        <RoleBoxes value={roles} onChange={setRoles} />
        {error && <ErrorNote>{error}</ErrorNote>}
        {added && (
          <p role="status" className="text-[13px] text-t2">
            Added {added}. Share the email and password with them.
          </p>
        )}
        <button type="submit" className="key h-12 text-sm" disabled={busy || !roles.length}>
          Create user
        </button>
      </form>
    </aside>
  );
}
