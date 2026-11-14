import type { Metadata } from "next";
import { Nunito } from "next/font/google";
import { ThemeProvider } from "next-themes";
import { Toaster } from "@/components/ui/sonner";
import { TooltipProvider } from "@/components/ui/tooltip";
import "./globals.css";

/** The app font, exposed as the --font-nunito CSS variable. */
const nunito = Nunito({ subsets: ["latin"], variable: "--font-nunito" });

/** Tab title: "ORA Catalog Agent", or "<page> · ORA Catalog Agent" where a page sets its own. */
export const metadata: Metadata = { title: { default: "ORA Catalog Agent", template: "%s · ORA Catalog Agent" } };

/** Root layout of every route: the html shell, Nunito font, theme, tooltips and the toast outlet. */
export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en" className={nunito.variable} suppressHydrationWarning>
      <body>
        <ThemeProvider
          attribute="class"
          defaultTheme="system"
          enableSystem
          disableTransitionOnChange
        >
          <TooltipProvider>{children}</TooltipProvider>
          <Toaster position="bottom-right" />
        </ThemeProvider>
      </body>
    </html>
  );
}
