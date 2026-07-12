import type { Metadata } from "next";
import "@/styles/styles.css";
import "@/styles/app.css";
import "@/styles/chat.css";
import "@/styles/landing.css";
import "@/styles/files.css";
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
