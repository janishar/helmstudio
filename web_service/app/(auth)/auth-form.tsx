"use client";
import Link from "next/link";
import { useActionState, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Field } from "@/components/kit";
import { signin, signup, type AuthState } from "./actions";

/** 0-4: length plus character variety. A hint only: auth_service decides. */
export function strength(pw: string): number {
  if (pw.length < 10) return pw ? 1 : 0;
  const kinds = [/[a-z]/, /[A-Z]/, /\d/, /[^A-Za-z0-9]/].filter((r) => r.test(pw)).length;
  return Math.min(4, 1 + (pw.length >= 14 ? 1 : 0) + (kinds >= 3 ? 2 : kinds >= 2 ? 1 : 0));
}
/** Hint text per strength() score. */
const STRENGTH = ["", "too short", "fair", "good", "strong"];

/** The sign-in or sign-up form; sign-up adds a name, a confirmation and a strength meter. `next` is where sign-in
 * lands. */
export function AuthForm({ mode, next }: { mode: "signin" | "signup"; next?: string }) {
  const [state, action, pending] = useActionState<AuthState, FormData>(
    mode === "signin" ? signin : signup,
    undefined,
  );
  const [pw, setPw] = useState("");
  const [confirm, setConfirm] = useState("");
  const s = strength(pw);
  const mismatch = mode === "signup" && confirm !== "" && confirm !== pw;
  const err = state?.error;
  return (
    <form action={action} className="grid gap-4" noValidate={false}>
      <h1 className="text-xl font-semibold">
        {mode === "signin" ? "Sign in" : "Create your account"}
      </h1>
      {err && (
        <p
          role="alert"
          id="form-error"
          className="rounded-lg bg-danger/10 px-3 py-2 text-sm text-danger"
        >
          {err}
        </p>
      )}
      <input type="hidden" name="next" value={next ?? ""} />
      {mode === "signup" && (
        <Field label="Name" htmlFor="name">
          <Input
            id="name"
            name="name"
            autoComplete="name"
            required
            maxLength={200}
            defaultValue={state?.name}
          />
        </Field>
      )}
      <Field label="Email" htmlFor="email">
        <Input
          id="email"
          name="email"
          type="email"
          autoComplete="email"
          required
          defaultValue={state?.email}
          aria-describedby={err ? "form-error" : undefined}
        />
      </Field>
      <Field
        label="Password"
        htmlFor="password"
        hint={
          mode === "signup" ? (
            <span aria-live="polite">
              At least 10 characters{s ? ` · strength: ${STRENGTH[s]}` : ""}
            </span>
          ) : undefined
        }
      >
        <Input
          id="password"
          name="password"
          type="password"
          required
          minLength={mode === "signup" ? 10 : undefined}
          autoComplete={mode === "signin" ? "current-password" : "new-password"}
          value={pw}
          onChange={(e) => setPw(e.target.value)}
          aria-describedby={mode === "signup" ? "password-hint" : undefined}
        />
      </Field>
      {mode === "signup" && (
        <>
          <div aria-hidden className="-mt-2 grid grid-cols-4 gap-1">
            {[1, 2, 3, 4].map((i) => (
              <span
                key={i}
                className={`h-1 rounded-full ${i <= s ? (s < 3 ? "bg-warning" : "bg-success") : "bg-muted"}`}
              />
            ))}
          </div>
          <Field
            label="Confirm password"
            htmlFor="confirm"
            error={mismatch ? "The passwords don't match." : undefined}
          >
            <Input
              id="confirm"
              name="confirm"
              type="password"
              required
              autoComplete="new-password"
              value={confirm}
              onChange={(e) => setConfirm(e.target.value)}
              aria-invalid={mismatch || undefined}
              aria-describedby={mismatch ? "confirm-error" : undefined}
            />
          </Field>
        </>
      )}
      <Button type="submit" size="lg" disabled={pending || mismatch}>
        {pending ? "Please wait…" : mode === "signin" ? "Sign in" : "Create account"}
      </Button>
      <p className="text-center text-sm text-muted-foreground">
        {mode === "signin" ? (
          // sign-up is closed for the beta: accounts come from the admin
          <>Access is by invitation during the beta. Ask your admin for an account.</>
        ) : (
          <>
            Have an account?{" "}
            <Link
              className="font-medium text-primary underline-offset-4 hover:underline"
              href="/signin"
            >
              Sign in
            </Link>
          </>
        )}
      </p>
    </form>
  );
}
