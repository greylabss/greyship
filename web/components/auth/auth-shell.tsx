import Link from "next/link";
import type { ReactNode } from "react";
import { Logo } from "@/components/brand/logo";

export function AuthShell({ aside, children }: { aside: ReactNode; children: ReactNode }) {
  return (
    <div className="flex min-h-dvh flex-col">
      <header className="flex items-center justify-between px-5 py-4 md:px-8">
        <Logo href="/" height={26} />
        {aside}
      </header>
      <main className="flex flex-1 items-center justify-center px-4 py-12">{children}</main>
      <footer className="px-5 py-6 text-center text-12 text-dim md:px-8">
        <Link href="/" className="hover:text-muted">
          greyship.tech
        </Link>{" "}
        ·{" "}
        <a href="https://x.com/greylabss" className="hover:text-muted">
          Updates
        </a>
      </footer>
    </div>
  );
}
