import { notFound } from "next/navigation";

/** Route /signup: closed for the beta (no public accounts; the admin creates them on /admin/users). To reopen it,
 * render `<AuthForm mode="signup" />` again and put "/signup" back in proxy.ts's PUBLIC, with auth's route. */
export default function SignupPage() {
  notFound();
}
