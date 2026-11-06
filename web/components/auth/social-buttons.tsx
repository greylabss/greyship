"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { icons } from "@/components/ui/icons";
import { apiEnabled, oauthStartURL } from "@/lib/greyship-api";

function GoogleMark() {
  return (
    <svg width={16} height={16} viewBox="0 0 24 24" aria-hidden="true" fill="currentColor">
      <path d="M21.6 12.23c0-.71-.06-1.39-.18-2.05H12v3.88h5.39a4.6 4.6 0 0 1-2 3.02v2.5h3.23c1.9-1.75 2.98-4.32 2.98-7.35ZM12 22c2.7 0 4.96-.9 6.62-2.42l-3.23-2.5c-.9.6-2.04.95-3.39.95-2.6 0-4.81-1.76-5.6-4.12H3.07v2.59A10 10 0 0 0 12 22ZM6.4 13.91a6 6 0 0 1 0-3.82V7.5H3.07a10 10 0 0 0 0 9l3.33-2.59ZM12 5.97c1.47 0 2.79.5 3.83 1.5l2.86-2.87A9.6 9.6 0 0 0 12 2a10 10 0 0 0-8.93 5.5l3.33 2.59C7.19 7.73 9.4 5.97 12 5.97Z" />
    </svg>
  );
}

// With the API configured these go to the real OAuth start; in mock mode they jump straight to `to`.
function ProviderButton({ provider, to, children, primary }: { provider: "google" | "github"; to: string; children: string; primary?: boolean }) {
  const router = useRouter();
  const [busy, setBusy] = useState(false);
  return (
    <Button
      variant={primary ? "primary" : "secondary"}
      className="h-12 w-full text-14"
      disabled={busy}
      onClick={() => {
        setBusy(true);
        if (apiEnabled) window.location.href = oauthStartURL(provider);
        else setTimeout(() => router.push(to), 500);
      }}
    >
      {provider === "github" ? <icons.github /> : <GoogleMark />}
      {busy ? `Redirecting to ${provider === "github" ? "GitHub" : "Google"}…` : children}
    </Button>
  );
}

export function SocialButtons({ to, verb }: { to: string; verb: "Continue" | "Sign up" }) {
  return (
    <div className="flex flex-col gap-2">
      <ProviderButton provider="github" to={to} primary>
        {`${verb} with GitHub`}
      </ProviderButton>
      <ProviderButton provider="google" to={to}>
        {`${verb} with Google`}
      </ProviderButton>
    </div>
  );
}
