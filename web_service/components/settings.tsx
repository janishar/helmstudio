"use client";
// Settings (docs/mock/Settings.dc.html): Account, Appearance, Password and Sign out; one column on a phone.
import { useActionState, useEffect, useState } from "react";
import { useTheme } from "next-themes";
import { Check, CircleAlert, LogOut } from "lucide-react";
import { changePassword, signout, type PasswordState } from "@/app/(auth)/actions";
import { useUser } from "@/components/shell";
import { THEMES } from "@/lib/themes";

const PANEL =
  "panel flex min-w-0 flex-col gap-3.5 px-[18px] pt-4 pb-[18px] max-sm:gap-3 max-sm:rounded-[22px_22px_22px_10px] max-sm:p-3.5";
const H2 = "m-0 font-display text-base font-bold";

export function SettingsScreen() {
  return (
    <main id="main" className="flex min-w-0 flex-[999_1_560px] flex-col gap-4 px-5 pt-4 pb-6">
      <h1 className="font-display text-[22px] font-bold tracking-[0.01em] max-sm:text-xl">
        Settings
      </h1>
      <div className="grid max-w-[1240px] items-start gap-4 lg:grid-cols-[420px_minmax(0,1fr)] lg:grid-rows-[auto_auto_1fr] lg:[grid-template-areas:'acct_look'_'pass_look'_'pass_out']">
        <Account />
        <Appearance />
        <Password />
        <section aria-labelledby="set-out" className={`${PANEL} lg:[grid-area:out]`}>
          <h2 id="set-out" className={H2}>
            Sign out
          </h2>
          <form action={signout}>
            <button
              type="submit"
              className="bevel inline-flex h-10 items-center gap-2 rounded-xl border-0 bg-s4 px-4 text-[13px] font-medium"
            >
              <LogOut size={16} strokeWidth={1.75} aria-hidden /> Sign out
            </button>
          </form>
        </section>
      </div>
    </main>
  );
}

/** Name, email and roles from /auth/me (the shell's user). */
function Account() {
  const user = useUser();
  return (
    <section aria-labelledby="set-acct" className={`${PANEL} lg:[grid-area:acct]`}>
      <h2 id="set-acct" className={H2}>
        Account
      </h2>
      {user ? (
        <dl className="m-0 grid grid-cols-[72px_minmax(0,1fr)] items-center gap-x-3 gap-y-2.5 text-[13px]">
          <dt className="label">Name</dt>
          <dd className="m-0 truncate font-semibold">{user.name}</dd>
          <dt className="label">Email</dt>
          <dd className="mono m-0 truncate text-[12px]">{user.email}</dd>
          <dt className="label">Roles</dt>
          <dd className="m-0 flex flex-wrap gap-1.5">
            {user.roles.map((r) => (
              <span key={r} className={r === "admin" ? "tag tag-accent" : "tag"}>
                {r}
              </span>
            ))}
          </dd>
        </dl>
      ) : (
        <p className="m-0 text-[13px] text-t2">Your account can&apos;t be loaded right now.</p>
      )}
    </section>
  );
}

/** The seven themes as a radio group; each card is painted in its own theme. Applies at once (next-themes keeps it in
 * localStorage). */
function Appearance() {
  const { theme, setTheme } = useTheme();
  const [mounted, setMounted] = useState(false);
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- the stored theme is only known in the browser
    setMounted(true);
  }, []);
  const current = mounted ? theme : "forest";
  return (
    <section aria-labelledby="set-look" className={`${PANEL} lg:[grid-area:look]`}>
      <h2 id="set-look" className={H2}>
        Appearance
      </h2>
      <fieldset className="m-0 min-w-0 border-0 p-0">
        <legend className="label pb-2.5">Theme · applies at once, saved on this device</legend>
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-4 sm:gap-3">
          {THEMES.map((t) => {
            const name = t.label.replace(/ \(light\)$/, "");
            return (
              <label
                key={t.id}
                className="group relative flex cursor-pointer flex-col gap-2 rounded-[16px_16px_16px_6px] bg-s2 px-2 pt-2 pb-2.5 has-checked:shadow-[inset_0_0_0_2px_var(--a1)] has-focus-visible:outline-2 has-focus-visible:outline-offset-3 has-focus-visible:outline-t1"
              >
                <input
                  type="radio"
                  name="theme"
                  value={t.id}
                  checked={current === t.id}
                  onChange={() => setTheme(t.id)}
                  className="absolute m-0 h-px w-px opacity-0"
                />
                <span
                  data-theme={t.id}
                  aria-hidden
                  className="flex aspect-[16/8] gap-1.5 overflow-hidden rounded-[10px_10px_10px_4px] bg-ground p-2 shadow-[inset_0_0_0_1px_rgba(255,255,255,.06)] sm:aspect-[16/10]"
                >
                  <span className="w-3 shrink-0 rounded-md bg-s1" />
                  <span className="flex min-w-0 flex-auto flex-col gap-[5px] rounded-[8px_8px_8px_3px] bg-s1 px-2 py-[7px]">
                    <b className="font-display text-sm leading-4 text-t1">Aa</b>
                    <i className="block h-1 w-4/5 rounded-sm bg-t2 opacity-80" />
                    <i className="block h-1 w-[55%] rounded-sm bg-t2 opacity-80" />
                    <span className="mt-auto flex gap-1 text-[8px] leading-[14px] font-semibold">
                      <span className="h-3.5 rounded-full bg-a1 px-1.5 text-on1">Go</span>
                      <span className="h-3.5 rounded-full bg-a2 px-1.5 text-on2">Pick</span>
                    </span>
                  </span>
                </span>
                <span className="flex items-center gap-1.5 px-0.5 text-[13px] font-medium">
                  {name}
                  {name !== t.label && (
                    <small className="text-[11px] font-normal text-t2">light</small>
                  )}
                  <span
                    aria-hidden
                    className="ml-auto hidden size-[18px] place-items-center rounded-full bg-a1 text-on1 group-has-checked:grid"
                  >
                    <Check size={12} strokeWidth={3} />
                  </span>
                </span>
              </label>
            );
          })}
        </div>
      </fieldset>
    </section>
  );
}

/** Rule bullet: grey, met (accent) or broken (red). */
function Rule({ state, children }: { state?: "ok" | "bad"; children: React.ReactNode }) {
  const tone = state === "ok" ? "text-[var(--t1b)]" : state === "bad" ? "text-bad" : "";
  const dot = state === "ok" ? "bg-a1" : state === "bad" ? "bg-bad" : "bg-line";
  return (
    <li className={`flex items-center gap-1.5 ${tone}`}>
      <span aria-hidden className={`size-1.5 rounded-full ${dot}`} />
      {children}
    </li>
  );
}

const Err = ({ id, children }: { id: string; children: React.ReactNode }) => (
  <p id={id} className="m-0 text-[12px] leading-[17px] text-bad">
    {children}
  </p>
);

/** Current / new / confirm; the rules are checked here first, then by auth_service. */
function Password() {
  const [state, action, pending] = useActionState<PasswordState, FormData>(
    changePassword,
    undefined,
  );
  const [cur, setCur] = useState("");
  const [next, setNext] = useState("");
  const [cfm, setCfm] = useState("");
  const [tried, setTried] = useState(false);
  // what the server answered, and for which values (typing again clears its marks)
  const [seen, setSeen] = useState<{ s: PasswordState; cur: string; next: string }>();
  if (state !== seen?.s) {
    setSeen({ s: state, cur, next });
    if (state?.done) {
      setCur("");
      setNext("");
      setCfm("");
      setTried(false);
    }
  }
  const server = seen?.s;
  const wrong = server?.wrong && seen?.cur === cur;
  const common = server?.rule === "common" && seen?.next === next;
  const short = next.length < 10;
  const lengthBad = (tried && short) || (server?.rule === "length" && seen?.next === next);
  const mismatch = tried && cfm !== next;
  const ruleBad = lengthBad || common;
  // under Confirm; right under New once one fails (as the board)
  const rules = (
    <ul id="pw-rules" className="m-0 flex list-none flex-col gap-1 p-0 text-[12px] text-t2">
      <Rule state={lengthBad ? "bad" : next && !short ? "ok" : undefined}>
        At least 10 characters
      </Rule>
      <Rule state={common ? "bad" : tried ? "ok" : undefined}>Not a common password</Rule>
    </ul>
  );
  const failed = tried && (!cur || short || mismatch || wrong || common || !!server?.error);
  return (
    <section aria-labelledby="set-pass" className={`${PANEL} lg:[grid-area:pass]`}>
      <h2 id="set-pass" className={H2}>
        Password
      </h2>
      <form
        action={action}
        className="flex flex-col gap-3.5"
        onSubmit={(e) => {
          setTried(true);
          if (!cur || short || cfm !== next) e.preventDefault();
        }}
      >
        {failed && (
          <div
            role="alert"
            className="flex items-start gap-2.5 rounded-[12px_12px_12px_6px] bg-[color-mix(in_srgb,var(--bad)_14%,transparent)] px-3 py-2.5 text-[13px] leading-[18px] shadow-[inset_0_0_0_1px_color-mix(in_srgb,var(--bad)_45%,transparent)]"
          >
            <CircleAlert size={14} strokeWidth={2} aria-hidden className="mt-0.5 shrink-0" />
            <span>
              {server?.error && !wrong && !common ? server.error : "Password not changed."}
            </span>
          </div>
        )}
        {server?.done && !tried && (
          <p role="status" className="m-0 flex items-start gap-2 text-[13px] text-[var(--t1b)]">
            <Check size={14} strokeWidth={2.5} aria-hidden className="mt-0.5 shrink-0 text-a1" />
            Password changed. Your other sessions are signed out.
          </p>
        )}
        <div className="flex flex-col gap-1.5">
          <label htmlFor="pw-cur" className="label">
            Current password
          </label>
          <input
            id="pw-cur"
            name="current"
            className="in aria-invalid:shadow-[inset_0_0_0_2px_var(--bad)]"
            type="password"
            autoComplete="current-password"
            value={cur}
            onChange={(e) => setCur(e.target.value)}
            aria-invalid={(tried && !cur) || wrong || undefined}
            aria-describedby={wrong ? "pw-cur-e" : tried && !cur ? "pw-cur-m" : undefined}
          />
          {tried && !cur && <Err id="pw-cur-m">Enter your current password.</Err>}
          {wrong && (
            <>
              <Err id="pw-cur-e">Current password is incorrect.</Err>
              <p className="m-0 text-[12px] leading-[17px] text-t2">
                5 wrong tries in 15 minutes sign you out here too.
              </p>
            </>
          )}
        </div>
        <div className="flex flex-col gap-1.5">
          <label htmlFor="pw-new" className="label">
            New password
          </label>
          <input
            id="pw-new"
            name="new"
            className="in aria-invalid:shadow-[inset_0_0_0_2px_var(--bad)]"
            type="password"
            autoComplete="new-password"
            maxLength={256}
            value={next}
            onChange={(e) => setNext(e.target.value)}
            aria-invalid={lengthBad || common || undefined}
            aria-describedby="pw-rules"
          />
        </div>
        {ruleBad && rules}
        <div className="flex flex-col gap-1.5">
          <label htmlFor="pw-cfm" className="label">
            Confirm
          </label>
          <input
            id="pw-cfm"
            name="confirm"
            className="in aria-invalid:shadow-[inset_0_0_0_2px_var(--bad)]"
            type="password"
            autoComplete="new-password"
            value={cfm}
            onChange={(e) => setCfm(e.target.value)}
            aria-invalid={mismatch || undefined}
            aria-describedby={mismatch ? "pw-cfm-e" : undefined}
          />
          {mismatch && <Err id="pw-cfm-e">Does not match.</Err>}
        </div>
        {!ruleBad && rules}
        <p className="m-0 flex items-start gap-2 rounded-[12px_12px_12px_6px] bg-s2 px-3 py-2.5 text-[12px] leading-[17px] text-[var(--t1b)]">
          <CircleAlert size={14} strokeWidth={2} aria-hidden className="mt-0.5 shrink-0" />
          <span>Changing it signs you out everywhere else.</span>
        </p>
        <button
          type="submit"
          className="key h-11 self-start rounded-[14px] px-[18px] text-sm"
          disabled={pending}
        >
          Change password
        </button>
      </form>
    </section>
  );
}
