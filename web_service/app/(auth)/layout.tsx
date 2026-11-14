import { BetaBadge } from "@/components/kit";

/** Centered card with the brand mark, around the sign-in and sign-up pages. */
export default function AuthLayout({ children }: { children: React.ReactNode }) {
  return (
    <main className="flex min-h-dvh items-center justify-center bg-frame px-4 py-10">
      <div className="w-full max-w-sm">
        <div className="mb-8 flex flex-col items-center gap-3 text-center">
          <img src="/brand/mark.png" alt="" className="size-14" />
          <p className="flex items-center gap-2 text-xs font-semibold tracking-[0.2em] text-muted-foreground uppercase">
            ORA Catalog Agent <BetaBadge className="tracking-wider" />
          </p>
          <p className="text-2xl font-bold tracking-tight">
            Create for <span className="text-brand">your brand</span>
          </p>
        </div>
        <div className="rounded-2xl border bg-background p-6 shadow-card">{children}</div>
      </div>
    </main>
  );
}
