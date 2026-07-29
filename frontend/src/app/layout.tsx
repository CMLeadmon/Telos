import type { Metadata } from "next";
import "@/styles/styles.css";
import "@/styles/app.css";
import "@/styles/chat.css";
import "@/styles/landing.css";
import "@/styles/stream.css";
import "@/styles/files.css";
import "@/styles/settings.css";
import "@/styles/library.css";
import "@/styles/commentary.css";
import "@/styles/mobile.css";
import { ThemeSync } from "@/components/ThemeSync";

export const metadata: Metadata = {
  title: "Telos",
  description: "Read, watch and discuss — on a server that's yours.",
};

export default function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en" data-theme="synthwave" suppressHydrationWarning>
      <body>
        <ThemeSync />
        {children}
      </body>
    </html>
  );
}
