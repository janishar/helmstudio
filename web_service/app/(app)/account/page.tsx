"use client";
import { useEffect, useState } from "react";
import { useUser } from "@/components/app-shell";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Card, Field, PageHeader, StatCard } from "@/components/kit";
import { api, message, ok, type S } from "@/lib/api";
import { signout } from "@/app/(auth)/actions";
import { catName, cr, type Library } from "@/lib/catalog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

/** "—" for a metric with no data yet, else the value formatted. */
const or = (v: number | null, f: (n: number) => string) => (v == null ? "—" : f(v));

/** Route /account: who you are, your Catalog Agent totals, render quality by product, and change your password (success revokes every session,
 * so the user signs in again). */
export default function AccountPage() {
  const user = useUser();
  const [stats, setStats] = useState<S<"Stats"> | null>(null);
  const [statsError, setStatsError] = useState("");
  useEffect(() => {
    ok(api.GET("/v1/catalog/stats"))
      .then(setStats)
      .catch((e) => setStatsError(message(e)));
  }, []);
  // Quality by product: metrics plus the library for product names (ids show if the library isn't there).
  const [metrics, setMetrics] = useState<S<"ProductMetrics">[] | null>(null);
  const [metricsError, setMetricsError] = useState("");
  const [lib, setLib] = useState<Library | null>(null);
  useEffect(() => {
    ok(api.GET("/v1/catalog/metrics"))
      .then((m) => setMetrics(m.products))
      .catch((e) => setMetricsError(message(e)));
    ok(api.GET("/v1/catalog/library")).then(setLib, () => {});
  }, []);
  const [cur, setCur] = useState("");
  const [pw, setPw] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState<string>();
  const [busy, setBusy] = useState(false);
  /** Checks the new password here, then changes it and signs out. */
  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (pw.length < 10) return setError("Use at least 10 characters for the new password.");
    if (pw !== confirm) return setError("The new passwords don't match.");
    setBusy(true);
    setError(undefined);
    try {
      await ok(api.PUT("/auth/password", { body: { current_password: cur, new_password: pw } }));
      await signout(); // every session was revoked: sign in again with the new password
    } catch (err) {
      setError(message(err));
      setBusy(false);
    }
  }
  return (
    <>
      <PageHeader title="Account" description={user ? `${user.name} · ${user.email}` : undefined} />
      <h2 className="mb-3 text-sm font-semibold">Catalog agent</h2>
      {statsError ? (
        <p className="mb-8 text-sm text-muted-foreground">
          Totals aren’t available right now ({statsError}).
        </p>
      ) : (
        <div className="mb-8 grid grid-cols-2 gap-3 md:grid-cols-4">
          <StatCard label="Sessions" value={stats?.sessions ?? "…"} />
          <StatCard label="SKUs" value={stats?.skus ?? "…"} />
          <StatCard label="Images approved" value={stats?.images_approved ?? "…"} />
          <StatCard
            label="Credits quoted"
            value={stats ? cr(stats.credits_quoted) : "…"}
            hint="Estimates only; nothing is charged."
          />
        </div>
      )}
      <h2 className="mb-3 text-sm font-semibold">Quality by product</h2>
      {!metrics?.length ? (
        <p className="mb-8 text-sm text-muted-foreground">{metricsError ? `Quality isn’t available right now (${metricsError}).` : metrics ? "No finished images yet." : "…"}</p>
      ) : (
        <Card className="mb-8 p-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Product</TableHead>
                <TableHead className="text-right">Images</TableHead>
                <TableHead className="text-right">First-try pass</TableHead>
                <TableHead className="text-right">Tries per image</TableHead>
                <TableHead className="text-right">Credits per approved</TableHead>
                <TableHead className="text-right">Rejected</TableHead>
                <TableHead className="text-right">Feedback</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {metrics.map((m) => (
                <TableRow key={m.product}>
                  <TableCell>{catName(lib, m.product)}</TableCell>
                  <TableCell className="text-right">{m.images}</TableCell>
                  <TableCell className="text-right">{or(m.first_pass_rate, (v) => `${Math.round(v * 100)}%`)}</TableCell>
                  <TableCell className="text-right">{or(m.avg_tries, (v) => v.toFixed(1))}</TableCell>
                  <TableCell className="text-right">{or(m.credits_per_approved, cr)}</TableCell>
                  <TableCell className="text-right">{m.rejected}</TableCell>
                  <TableCell className="text-right">{m.feedback_count}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </Card>
      )}
      <h2 className="mb-1 text-sm font-semibold">Password</h2>
      <p className="mb-3 text-sm text-muted-foreground">
        You’ll be signed out everywhere and sign in again.
      </p>
      <Card className="max-w-md">
        <form onSubmit={submit} className="grid gap-4">
          <Field label="Current password" htmlFor="current">
            <Input
              id="current"
              type="password"
              autoComplete="current-password"
              required
              value={cur}
              onChange={(e) => setCur(e.target.value)}
            />
          </Field>
          <Field label="New password" htmlFor="new" hint="At least 10 characters.">
            <Input
              id="new"
              type="password"
              autoComplete="new-password"
              required
              minLength={10}
              value={pw}
              onChange={(e) => setPw(e.target.value)}
            />
          </Field>
          <Field label="Confirm new password" htmlFor="confirm-new">
            <Input
              id="confirm-new"
              type="password"
              autoComplete="new-password"
              required
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
            />
          </Field>
          {error && (
            <p role="alert" className="text-sm text-danger">
              {error}
            </p>
          )}
          <Button type="submit" disabled={busy} className="w-fit">
            Change password
          </Button>
        </form>
      </Card>
    </>
  );
}
