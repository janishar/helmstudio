import type { Metadata } from "next";
import { Bricolage_Grotesque, Inter, JetBrains_Mono } from "next/font/google";
import { ThemeProvider } from "next-themes";
import { THEMES } from "@/lib/themes";
import "./globals.css";

const inter = Inter({ subsets: ["latin"], variable: "--font-inter" });
const bricolage = Bricolage_Grotesque({
  subsets: ["latin"],
  weight: ["600", "700"],
  variable: "--font-bricolage",
});
const jetbrains = JetBrains_Mono({ subsets: ["latin"], variable: "--font-jetbrains" });

/** Tab title: "helmstudio", or "<page> · helmstudio" where a page sets its own. */
export const metadata: Metadata = { title: { default: "helmstudio", template: "%s · helmstudio" } };

/** Root layout: fonts and the theme (data-theme on <html>, kept in localStorage by next-themes). */
export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <html
      lang="en"
      className={`${inter.variable} ${bricolage.variable} ${jetbrains.variable}`}
      suppressHydrationWarning
    >
      <body>
        <ThemeProvider
          attribute="data-theme"
          themes={THEMES.map((t) => t.id)}
          defaultTheme="forest"
          enableSystem={false}
          storageKey="helm-theme"
          disableTransitionOnChange
        >
          {children}
        </ThemeProvider>
      </body>
    </html>
  );
}
