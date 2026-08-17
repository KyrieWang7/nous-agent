import "@/styles/globals.css";
import "katex/dist/katex.min.css";

import { type Metadata } from "next";

import { ThemeProvider } from "@/components/theme-provider";
import { I18nProvider } from "@/core/i18n/context";
import { detectLocaleServer } from "@/core/i18n/server";

export const metadata: Metadata = {
  title: "Nous Agent",
  description:
    "An open-source AI agent platform powered by Nous Agent Runtime.",
  icons: {
    icon: "/icon.png?v=nous-logo-1",
    apple: "/icon.png?v=nous-logo-1",
  },
};

export default async function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  const locale = await detectLocaleServer();
  return (
    <html
      lang={locale}
      style={
        {
          "--font-geist-sans":
            '"SF Pro Text", "SF Pro Display", "PingFang SC", "Hiragino Sans GB", "Helvetica Neue", sans-serif',
        } as React.CSSProperties
      }
      suppressContentEditableWarning
      suppressHydrationWarning
    >
      <body>
        <ThemeProvider
          attribute="class"
          defaultTheme="light"
          enableSystem={false}
          disableTransitionOnChange
        >
          <I18nProvider initialLocale={locale}>{children}</I18nProvider>
        </ThemeProvider>
      </body>
    </html>
  );
}
