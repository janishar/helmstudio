"use client";
import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { useUser } from "@/components/app-shell";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Card,
  EmptyState,
  ErrorNote,
  Field,
  PageHeader,
  SkeletonRows,
  StatusPill,
} from "@/components/kit";
import { api, message, ok, type S } from "@/lib/api";
import { when } from "@/lib/format";

/** The roles the admin can give, as the select shows them; admin is only the ADMIN_EMAIL account. */
const ROLES = [
  { id: "user", label: "Beta tester", roles: ["user"] },
  { id: "dev", label: "Developer (+ Jobs view)", roles: ["user", "dev"] },
] as const;
/** A user's row in the role select. */
const roleOf = (u: S<"User">) => (u.roles.includes("dev") ? "dev" : "user");
/** The roles for a select value. */
const rolesFor = (id: string) => [...(ROLES.find((r) => r.id === id) ?? ROLES[0]).roles];
/** Shorthand for a user path parameter. */
const uid = (id: string) => ({ params: { path: { id } } });

/** Route /admin/users: add an account (email, name, password, role) and manage each one's role, access and
 * password. Sign-up is closed, so this is the only way in; the ADMIN_EMAIL account is set by auth's .env. */
export function UsersAdmin() {
  const me = useUser();
  const [users, setUsers] = useState<S<"User">[] | null>(null);
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

  if (me && !me.roles.includes("admin"))
    return <EmptyState title="Admins only">Ask the admin to add or change accounts.</EmptyState>;
  return (
    <>
      <PageHeader
        title="Users"
        description="Beta access is by invitation: add an account here and share its email and password with the tester."
      />
      <AddUser onAdded={load} />
      <h2 className="mt-8 mb-3 text-sm font-semibold">Accounts</h2>
      {error ? (
        <ErrorNote onRetry={load}>Couldn’t load the accounts: {error}</ErrorNote>
      ) : !users ? (
        <SkeletonRows />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Email</TableHead>
              <TableHead>Name</TableHead>
              <TableHead>Role</TableHead>
              <TableHead>Access</TableHead>
              <TableHead>Added</TableHead>
              <TableHead className="text-right">Password</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {users.map((u) => (
              <UserRow key={u.id} u={u} onChange={load} />
            ))}
          </TableBody>
        </Table>
      )}
    </>
  );
}

/** The add-account form: the name defaults to the part of the email before @. */
function AddUser({ onAdded }: { onAdded: () => void }) {
  const [form, setForm] = useState({ email: "", name: "", password: "", role: "user" });
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);
  const set =
    (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
      setForm({ ...form, [k]: e.target.value });
  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (form.password.length < 10) return setError("Use at least 10 characters for the password.");
    setBusy(true);
    setError(undefined);
    try {
      const u = await ok(
        api.POST("/auth/admin/users", {
          body: {
            email: form.email,
            name: form.name,
            password: form.password,
            roles: rolesFor(form.role),
          },
        }),
      );
      toast(`Added ${u.email}. Share the email and password with them.`);
      setForm({ email: "", name: "", password: "", role: form.role });
      onAdded();
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Card>
      <form onSubmit={submit} className="grid gap-4 sm:grid-cols-2">
        <Field label="Email" htmlFor="new-email">
          <Input
            id="new-email"
            type="email"
            required
            autoComplete="off"
            value={form.email}
            onChange={set("email")}
          />
        </Field>
        <Field label="Name" htmlFor="new-name" hint="Optional: the part of the email before @">
          <Input
            id="new-name"
            maxLength={200}
            autoComplete="off"
            value={form.name}
            onChange={set("name")}
          />
        </Field>
        <Field
          label="Password"
          htmlFor="new-password"
          hint="At least 10 characters, not a common one"
        >
          <Input
            id="new-password"
            type="text"
            required
            minLength={10}
            autoComplete="new-password"
            value={form.password}
            onChange={set("password")}
          />
        </Field>
        <Field label="Role" htmlFor="new-role">
          <select
            id="new-role"
            value={form.role}
            onChange={set("role")}
            className="h-9 rounded-md border bg-background px-2 text-sm"
          >
            {ROLES.map((r) => (
              <option key={r.id} value={r.id}>
                {r.label}
              </option>
            ))}
          </select>
        </Field>
        {error && (
          <p
            role="alert"
            className="rounded-lg bg-danger/10 px-3 py-2 text-sm text-danger sm:col-span-2"
          >
            {error}
          </p>
        )}
        <div className="sm:col-span-2">
          <Button type="submit" disabled={busy}>
            {busy ? "Adding…" : "Add account"}
          </Button>
        </div>
      </form>
    </Card>
  );
}

/** One account: its role, access (disable signs it out everywhere) and a new password. The admin's row is read-only:
 * ADMIN_EMAIL / ADMIN_PASSWORD in auth's .env set it. */
function UserRow({ u, onChange }: { u: S<"User">; onChange: () => void }) {
  const admin = u.roles.includes("admin");
  const [pw, setPw] = useState<string | null>(null); // null: the password box is closed
  const [busy, setBusy] = useState(false);
  /** Runs one change, then reloads the list; a refusal shows as a toast. */
  async function act(fn: () => Promise<unknown>, done: string) {
    setBusy(true);
    try {
      await fn();
      toast(done);
      onChange();
    } catch (err) {
      toast.error(message(err));
    } finally {
      setBusy(false);
    }
  }
  return (
    <TableRow>
      <TableCell className="font-medium">{u.email}</TableCell>
      <TableCell className="max-w-48 truncate">{u.name}</TableCell>
      <TableCell>
        {admin ? (
          <span className="text-sm">Admin (.env)</span>
        ) : (
          <select
            aria-label={`Role of ${u.email}`}
            value={roleOf(u)}
            disabled={busy}
            onChange={(e) =>
              void act(
                () =>
                  ok(
                    api.PUT("/auth/admin/users/{id}/roles", {
                      ...uid(u.id),
                      body: { roles: rolesFor(e.target.value) },
                    }),
                  ),
                `${u.email} is now a ${ROLES.find((r) => r.id === e.target.value)?.label}; they sign in again.`,
              )
            }
            className="h-8 rounded-md border bg-background px-2 text-sm"
          >
            {ROLES.map((r) => (
              <option key={r.id} value={r.id}>
                {r.label}
              </option>
            ))}
          </select>
        )}
      </TableCell>
      <TableCell>
        <div className="flex items-center gap-2">
          <StatusPill status={u.disabled ? "failed" : "succeeded"}>
            {u.disabled ? "Disabled" : "Active"}
          </StatusPill>
          {!admin && (
            <Button
              variant="ghost"
              size="sm"
              disabled={busy}
              onClick={() =>
                void act(
                  () =>
                    ok(
                      api.PUT("/auth/admin/users/{id}/disabled", {
                        ...uid(u.id),
                        body: { disabled: !u.disabled },
                      }),
                    ),
                  u.disabled
                    ? `${u.email} can sign in again.`
                    : `${u.email} is disabled and signed out.`,
                )
              }
            >
              {u.disabled ? "Enable" : "Disable"}
            </Button>
          )}
        </div>
      </TableCell>
      <TableCell className="whitespace-nowrap text-muted-foreground">
        {when(u.created_at)}
      </TableCell>
      <TableCell className="text-right">
        {admin ? (
          <span className="text-xs text-muted-foreground">ADMIN_PASSWORD</span>
        ) : pw === null ? (
          <Button variant="outline" size="sm" onClick={() => setPw("")}>
            Set password
          </Button>
        ) : (
          <form
            className="flex items-center justify-end gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              void act(
                () =>
                  ok(
                    api.PUT("/auth/admin/users/{id}/password", {
                      ...uid(u.id),
                      body: { password: pw },
                    }),
                  ),
                `New password set for ${u.email}; they're signed out.`,
              ).then(() => setPw(null));
            }}
          >
            <Input
              aria-label={`New password for ${u.email}`}
              autoFocus
              minLength={10}
              required
              value={pw}
              onChange={(e) => setPw(e.target.value)}
              className="h-8 w-44"
            />
            <Button type="submit" size="sm" disabled={busy || pw.length < 10}>
              Save
            </Button>
            <Button type="button" variant="ghost" size="sm" onClick={() => setPw(null)}>
              Cancel
            </Button>
          </form>
        )}
      </TableCell>
    </TableRow>
  );
}
