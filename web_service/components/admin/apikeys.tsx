"use client";
// Admin › API keys (docs/mock/ApiKeys.dc.html): every application key with its prefix, dates and state, Revoke with
// confirm; the New key panel gives the server command that makes one (it prints the full key once).
import { useCallback, useEffect, useState } from "react";
import { ErrorNote, Info, Modal } from "@/components/kit";
import { AdminTabs } from "@/components/admin/users";
import { api, message, ok, type S } from "@/lib/api";
import { CLIENT_NOTE, CREATE_COMMAND, keyCounts, keyOrder, lastUsed } from "@/lib/apikeys";
import { when } from "@/lib/format";
import { useHydrated } from "@/lib/hydrated";

type Key = S<"ApiKey">;

export function ApiKeysAdmin() {
  const [keys, setKeys] = useState<Key[] | null>(null);
  const [now, setNow] = useState(0);
  const [error, setError] = useState("");
  const [revoking, setRevoking] = useState<Key | null>(null);
  const hydrated = useHydrated();
  const load = useCallback(() => {
    ok(api.GET("/auth/admin/apikeys"))
      .then((l) => {
        setKeys(l.api_keys);
        setNow(Date.now());
        setError("");
      })
      .catch((e) => setError(message(e)));
  }, []);
  useEffect(() => {
    load();
  }, [load]);
  async function revoke(k: Key) {
    try {
      await ok(api.DELETE("/auth/admin/apikeys/{id}", { params: { path: { id: k.id } } }));
      setRevoking(null);
      load();
    } catch (e) {
      setRevoking(null);
      setError(message(e));
    }
  }
  const counts = keyCounts(keys ?? []);
  const tile = "panel flex min-w-0 flex-col gap-1 px-4 py-3.5";
  const cell = "border-b border-s3 p-2.5";
  return (
    <>
      <main id="main" className="flex min-w-0 flex-[999_1_560px] flex-col gap-4 px-5 pt-4 pb-6">
        <div className="flex flex-wrap items-center gap-3">
          <h1 className="font-display text-[22px] font-bold tracking-[0.01em]">Admin</h1>
          <div className="grow" />
          <span className="tag tag-accent">Signed in as root admin</span>
        </div>
        <AdminTabs current="keys" />
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
          <div className={tile}>
            <span className="heading">Active keys</span>
            <span className="mono text-[24px] leading-[30px] font-medium">
              {keys ? counts.active : "–"}
            </span>
          </div>
          <div className={tile}>
            <span className="heading">Revoked</span>
            <span className="mono text-[24px] leading-[30px] font-medium">
              {keys ? counts.revoked : "–"}
            </span>
          </div>
          <div className={tile}>
            <span className="heading flex items-center">
              Checked by
              <Info>
                Kong checks the API key on every request. It identifies the calling application; the
                user is identified separately by their sign-in token.
              </Info>
            </span>
            <span className="mono text-[18px] leading-[30px] font-medium">Kong gateway</span>
          </div>
        </div>
        {error && <ErrorNote onRetry={load}>{error}</ErrorNote>}
        <div className="panel min-h-0 overflow-auto px-2 py-1.5" aria-busy={!keys && !error}>
          <table className="w-full min-w-[760px] border-collapse text-[13px]">
            <thead>
              <tr className="text-left text-[11px] tracking-[0.04em] text-t2">
                {["Client", "Key prefix", "Created", "Last used", "Status", "Actions"].map((h) => (
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
            <tbody className="[&>tr:last-child>td]:border-b-0">
              {keyOrder(keys ?? []).map((k) => (
                <tr key={k.id} className="whitespace-nowrap">
                  <td className={`${cell} font-semibold ${k.revoked_at ? "text-t2" : ""}`}>
                    {k.client}
                    {CLIENT_NOTE[k.client] && <Info>{CLIENT_NOTE[k.client]}</Info>}
                  </td>
                  <td className={`${cell} mono text-[12px] text-t2`}>{k.prefix}…</td>
                  <td className={`${cell} mono text-[12px] text-t2`}>
                    {hydrated ? when(k.created_at) : ""}
                  </td>
                  <td className={`${cell} mono text-[12px] text-t2`}>
                    {hydrated ? lastUsed(k.last_used_at, now, when) : ""}
                  </td>
                  <td className={cell}>
                    {k.revoked_at ? (
                      <span className="tag">Revoked</span>
                    ) : (
                      <span className="tag bg-[color-mix(in_srgb,var(--a1)_22%,var(--s4))]">
                        Active
                      </span>
                    )}
                  </td>
                  <td className={cell}>
                    {k.revoked_at ? (
                      <span className="text-[12px] text-t2">
                        Revoked {hydrated ? when(k.revoked_at) : ""}
                      </span>
                    ) : (
                      <button
                        type="button"
                        className="chip bevel h-[30px]"
                        onClick={() => setRevoking(k)}
                      >
                        Revoke
                      </button>
                    )}
                  </td>
                </tr>
              ))}
              {keys?.length === 0 && (
                <tr>
                  <td colSpan={6} className="p-2.5 text-t2">
                    No keys yet.
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </main>
      <NewKeyPanel />
      <Modal open={!!revoking} onClose={() => setRevoking(null)} title="Revoke this key?">
        <p className="mb-4 text-[13px] text-t2">
          Every request with the {revoking?.client} key ({revoking?.prefix}…) is refused within a
          minute. This cannot be undone: make a new key with the command if it is still needed.
        </p>
        <div className="flex justify-end gap-2">
          <button type="button" className="chip bevel" onClick={() => setRevoking(null)}>
            Keep it
          </button>
          <button
            type="button"
            className="chip bevel bg-a1 font-semibold text-on1"
            onClick={() => revoking && void revoke(revoking)}
          >
            Revoke
          </button>
        </div>
      </Modal>
    </>
  );
}

/** The board's New key panel: keys are made on the server, never in the browser. */
function NewKeyPanel() {
  const [copied, setCopied] = useState(false);
  const box = "flex flex-col gap-2.5 rounded-[18px_18px_18px_8px] bg-s2 p-3.5";
  const code =
    "mono m-0 rounded-[10px] bg-inset px-3 py-2.5 text-[12px] leading-[18px] font-medium break-all whitespace-pre-wrap text-a1";
  return (
    <aside
      aria-label="New key"
      className="aside m-3.5 flex flex-[1_1_340px] flex-col gap-3.5 px-[18px] pt-4 pb-6 md:ml-0"
    >
      <h2 className="flex items-center font-display text-base leading-5 font-bold">
        New key
        <Info>
          Keys are made on the server, not in the browser. Run the command where the auth service
          runs; the new key appears in this list.
        </Info>
      </h2>
      <div className={box}>
        <span className="heading">Run where the auth service runs</span>
        <pre className={code} aria-label="Command to create a key">
          <code>{CREATE_COMMAND}</code>
        </pre>
        <pre className={code} aria-label="Example">
          <code>docker compose -f deploy/docker-compose.yml run --rm -T auth apikey helm</code>
        </pre>
        <button
          type="button"
          className="chip bevel self-start"
          onClick={() =>
            void navigator.clipboard
              ?.writeText(CREATE_COMMAND)
              .then(() => setCopied(true))
              .catch(() => undefined)
          }
        >
          {copied ? "Copied" : "Copy command"}
        </button>
      </div>
      <div className={`${box} flex-row items-center`}>
        <span className="tag tag-accent shrink-0">Shown once</span>
        <span className="min-w-0 text-[13px]">
          Copy the full key when it prints
          <Info>
            Only the prefix is stored for display. A lost key cannot be shown again; revoke it and
            make a new one.
          </Info>
        </span>
      </div>
      <div className={box}>
        <span className="heading">Who holds which key</span>
        {[
          ["Browser", "none"],
          ["Web backend", "web"],
          ["helm CLI", "helm"],
        ].map(([who, key]) => (
          <div key={who} className="flex justify-between gap-2.5 text-[13px]">
            <span>{who}</span>
            <span className="mono text-[12px] text-t2">{key}</span>
          </div>
        ))}
      </div>
    </aside>
  );
}
