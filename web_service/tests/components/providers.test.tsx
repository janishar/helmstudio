// @vitest-environment jsdom
// Admin > Model providers (components/admin/providers.tsx; docs/mock/Providers.dc.html): a remote provider says it is
// off while the deployment's remote switch is, and the form explains what Remote asks for.
import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { ProvidersAdmin } from "@/components/admin/providers";
import remote from "../../../contracts/fixtures/http/Provider.valid.2.json";
import { answer } from "./helpers";

vi.mock("next/link", () => ({
  default: ({ href, children, ...rest }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...rest}>
      {children}
    </a>
  ),
}));
vi.mock("@/components/shell", () => ({ useUser: () => ({ roles: ["admin"] }) }));

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const serve = (remote_enabled: boolean) =>
  answer("GET", {
    "/v1/studio/admin/providers": () => ({ data: { providers: [remote], remote_enabled } }),
  });

describe("ProvidersAdmin", () => {
  it("notes a remote provider is off while remote models are off", async () => {
    serve(false);
    render(<ProvidersAdmin />);
    expect(await screen.findByText("off · remote models are off")).toBeTruthy();
    expect(
      screen.getByText(/^Remote: the key's secret file name \(in deploy\/secrets\)/),
    ).toBeTruthy();
  });

  it("no note while they are on", async () => {
    serve(true);
    render(<ProvidersAdmin />);
    expect(await screen.findByText("Enabled")).toBeTruthy();
    expect(screen.queryByText("off · remote models are off")).toBeNull();
  });
});
