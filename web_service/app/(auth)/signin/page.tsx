import type { Metadata } from "next";
import { SigninForm } from "../auth-form";

export const metadata: Metadata = { title: "Sign in" };

/** Route /signin: `?next=` is where to land after signing in. */
export default async function SigninPage({
  searchParams,
}: {
  searchParams: Promise<{ next?: string }>;
}) {
  return <SigninForm next={(await searchParams).next} />;
}
