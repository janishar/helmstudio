"use client";
import { useActionState } from "react";
import { Info, Logo } from "@/components/kit";
import { signin, type AuthState } from "./actions";

/** The sign-in card. `next` is where to land afterwards. */
export function SigninForm({ next }: { next?: string }) {
  const [state, action, pending] = useActionState<AuthState, FormData>(signin, undefined);
  const err = state?.error;
  return (
    <form
      action={action}
      aria-label="Sign in"
      className="relative z-[1] flex w-[400px] max-w-full flex-col gap-4 bg-s1 px-8 pt-8 pb-7"
      style={{
        borderRadius: "36px 36px 36px 14px",
        backgroundImage: "linear-gradient(180deg, rgba(255,255,255,.05), rgba(255,255,255,0) 40%)",
        boxShadow:
          "inset 0 1px 0 rgba(255,255,255,.08), 0 40px 70px -24px color-mix(in srgb, var(--shd) 92%, transparent), 0 12px 24px -12px color-mix(in srgb, var(--shd) 70%, transparent)",
      }}
    >
      <div className="mb-2 flex items-center gap-3">
        <Logo />
        <h1 className="m-0 font-display text-[26px] font-bold tracking-[-0.02em]">helmstudio</h1>
      </div>
      {err && (
        <p role="alert" id="form-error" className="rounded-xl bg-s2 px-3 py-2 text-[13px] text-bad">
          {err}
        </p>
      )}
      <input type="hidden" name="next" value={next ?? ""} />
      <div className="flex flex-col gap-1.5">
        <label htmlFor="si-email" className="label">
          Email
        </label>
        <input
          id="si-email"
          name="email"
          className="in h-11 text-sm"
          type="email"
          autoComplete="username"
          required
          defaultValue={state?.email}
          aria-describedby={err ? "form-error" : undefined}
        />
      </div>
      <div className="flex flex-col gap-1.5">
        <label htmlFor="si-pass" className="label">
          Password
        </label>
        <input
          id="si-pass"
          name="password"
          className="in h-11 text-sm"
          type="password"
          autoComplete="current-password"
          required
        />
      </div>
      <button type="submit" className="key mt-1 h-[50px] text-[15px]" disabled={pending}>
        {pending ? "Signing in…" : "Sign in"}
      </button>
      <p className="mt-1 flex items-center justify-center text-[13px] text-t2">
        No account? Ask your admin.
        <Info>
          There is no public sign-up. The admin creates every account and resets forgotten
          passwords.
        </Info>
      </p>
    </form>
  );
}
