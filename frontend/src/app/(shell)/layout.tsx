import { AppShell } from "@/components/AppShell";
import { VersionSkewBanner } from "@/components/VersionSkewBanner";

export default function ShellLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <>
      <VersionSkewBanner />
      <AppShell>{children}</AppShell>
    </>
  );
}

